import { createHash, timingSafeEqual } from "node:crypto";
import {
  appendFile,
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from "node:fs/promises";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { isDeepStrictEqual } from "node:util";
import { downloadArchive, inspectArchive } from "./inspect-npm-archive.mjs";
export { checkArchiveEntry, inspectArchive } from "./inspect-npm-archive.mjs";
import {
  parseTrustedKeys,
  verifyRegistryPackages,
} from "./verify-npm-registry.mjs";

const pinPath = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  "npm-toolchain.json",
);
const keysPath = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  "npm-bootstrap-keys.json",
);

// The replacements are intentionally limited to this layout. Their published
// major versions fall outside npm's declared ranges; compatibility is tested
// separately, and updates to this list require a fresh dependency review.
const replacementLayout = [
  {
    name: "glob",
    path: ["node_modules", "glob"],
    replaces: true,
    dependencies: {
      minimatch: "^11.0.2",
      minipass: "^7.1.3",
      "path-scurry": "^3.0.0",
    },
  },
  {
    name: "minimatch",
    path: ["node_modules", "minimatch"],
    replaces: true,
    dependencies: {
      "@isaacs/nonrecursive-glob-match": "^1.0.3",
      "brace-expansion": "^5.0.12",
    },
  },
  {
    name: "path-scurry",
    path: ["node_modules", "glob", "node_modules", "path-scurry"],
    replaces: false,
    dependencies: { "lru-cache": "^11.0.0", minipass: "^7.1.2" },
  },
  {
    name: "@isaacs/nonrecursive-glob-match",
    path: ["node_modules", "@isaacs", "nonrecursive-glob-match"],
    replaces: false,
    dependencies: {},
  },
];

const supportPackages = [
  { name: "minipass", version: "7.1.3" },
  { name: "lru-cache", version: "11.5.2" },
  { name: "brace-expansion", version: "5.0.12" },
  { name: "balanced-match", version: "4.0.4" },
];

function validatePackagePin(pin, name) {
  const basename = name.slice(name.lastIndexOf("/") + 1);
  if (
    pin?.name !== name ||
    typeof pin.version !== "string" ||
    !/^\d+\.\d+\.\d+$/.test(pin.version) ||
    pin.resolved !==
      `https://registry.npmjs.org/${name}/-/${basename}-${pin.version}.tgz` ||
    typeof pin.integrity !== "string" ||
    !pin.integrity.startsWith("sha512-")
  )
    throw new Error(`Invalid ${name} toolchain pin`);
  const encoded = pin.integrity.slice("sha512-".length);
  const digest = Buffer.from(encoded, "base64");
  if (digest.length !== 64 || digest.toString("base64") !== encoded) {
    throw new Error(`Invalid ${name} toolchain SHA-512 digest`);
  }
  return digest;
}

export function validatePin(pin) {
  return validatePackagePin(pin, "npm");
}

export function validateReplacements(pin) {
  if (pin.replacements === undefined && pin.replacementsFor === undefined)
    return [];
  if (
    pin.replacementsFor !== pin.version ||
    !Array.isArray(pin.replacements) ||
    pin.replacements.length !== replacementLayout.length
  ) {
    throw new Error("Invalid npm replacement plan");
  }
  return pin.replacements.map((replacement, index) => {
    const layout = replacementLayout[index];
    const digest = validatePackagePin(replacement, layout.name);
    if (
      layout.replaces
        ? typeof replacement.replacesVersion !== "string" ||
          !/^\d+\.\d+\.\d+$/.test(replacement.replacesVersion)
        : replacement.replacesVersion !== undefined
    ) {
      throw new Error(`Invalid replacement target for ${layout.name}`);
    }
    return { pin: replacement, layout, digest };
  });
}

async function extractEntries(entries, destination) {
  await mkdir(destination, { recursive: true });
  for (const entry of entries) {
    const relative = entry.path.replace(/\/$/, "").split("/").slice(1);
    if (!relative.length) continue;
    const target = path.join(destination, ...relative);
    if (entry.type === "Directory") {
      await mkdir(target, { recursive: true });
    } else {
      await mkdir(path.dirname(target), { recursive: true });
      await writeFile(target, entry.data, { mode: entry.mode, flag: "wx" });
    }
  }
}

function validateReplacementArchive(entries, replacement) {
  if (
    entries.some((entry) =>
      entry.path
        .split("/")
        .slice(1)
        .some((segment) => segment.toLowerCase() === "node_modules"),
    )
  ) {
    throw new Error(
      `Bundled dependencies are not permitted in ${replacement.layout.name}`,
    );
  }
  const manifest = entries.find(
    (entry) => entry.path === "package/package.json" && entry.type === "File",
  );
  if (!manifest)
    throw new Error(`Missing ${replacement.layout.name} package metadata`);
  const metadata = JSON.parse(manifest.data.toString("utf8"));
  const noAdditionalDependencies = (value) =>
    value === undefined ||
    (value !== null &&
      typeof value === "object" &&
      !Array.isArray(value) &&
      Object.keys(value).length === 0);
  const noBundledDependencies = (value) =>
    value === undefined || (Array.isArray(value) && value.length === 0);
  const dependencies =
    metadata.dependencies === undefined ? {} : metadata.dependencies;
  if (
    metadata.name !== replacement.pin.name ||
    metadata.version !== replacement.pin.version ||
    !isDeepStrictEqual(dependencies, replacement.layout.dependencies) ||
    !noAdditionalDependencies(metadata.optionalDependencies) ||
    !noAdditionalDependencies(metadata.peerDependencies) ||
    !noBundledDependencies(metadata.bundleDependencies) ||
    !noBundledDependencies(metadata.bundledDependencies)
  ) {
    throw new Error(`Unexpected ${replacement.layout.name} package metadata`);
  }
}

async function checkInstalledIdentity(directory, name, version) {
  const info = await lstat(directory);
  if (!info.isDirectory() || info.isSymbolicLink())
    throw new Error(`Unexpected installed ${name} path`);
  const metadata = JSON.parse(
    await readFile(path.join(directory, "package.json"), "utf8"),
  );
  if (metadata.name !== name || metadata.version !== version)
    throw new Error(`Unexpected installed ${name} version`);
}

export async function prepareNpm(options = {}) {
  const pin = options.pin || JSON.parse(await readFile(pinPath, "utf8"));
  const expected = validatePin(pin);
  const replacements = validateReplacements(pin);
  const env = options.env || process.env;
  for (const key of ["RUNNER_TEMP", "GITHUB_PATH", "GITHUB_ENV"]) {
    if (!env[key] || /[\r\n]/.test(env[key]))
      throw new Error(`Missing or invalid ${key}`);
  }
  const archiveBytes = await downloadArchive(
    pin.resolved,
    options.fetcher || fetch,
  );
  const actual = createHash("sha512").update(archiveBytes).digest();
  if (!timingSafeEqual(actual, expected))
    throw new Error("npm tarball SHA-512 does not match the pin");
  const replacementArchives = [];
  for (const replacement of replacements) {
    const bytes = await downloadArchive(
      replacement.pin.resolved,
      options.fetcher || fetch,
    );
    const digest = createHash("sha512").update(bytes).digest();
    if (!timingSafeEqual(digest, replacement.digest))
      throw new Error(
        `${replacement.layout.name} tarball SHA-512 does not match the pin`,
      );
    replacementArchives.push({ ...replacement, bytes });
  }
  // The pinned signing key was authenticated against Sigstore TUF at update
  // time, so bootstrap does not execute the older npm bundled with Node.
  const trustedKeys = parseTrustedKeys(
    JSON.parse(await readFile(keysPath, "utf8")),
  );
  const packages = [pin, ...replacements.map((replacement) => replacement.pin)];
  await (options.verify || verifyRegistryPackages)(packages, { trustedKeys });
  const entries = inspectArchive(archiveBytes);
  for (const replacement of replacementArchives) {
    replacement.entries = inspectArchive(replacement.bytes, {
      requiredPaths: ["package/package.json"],
    });
    validateReplacementArchive(replacement.entries, replacement);
  }

  const root = await mkdtemp(path.join(env.RUNNER_TEMP, "steam-releaser-npm-"));
  try {
    const npmDirectory = path.join(root, "package");
    const shimDirectory = path.join(root, "shims");
    await extractEntries(entries, npmDirectory);
    const metadata = JSON.parse(
      await readFile(path.join(npmDirectory, "package.json"), "utf8"),
    );
    const cli = path.join(npmDirectory, "bin", "npm-cli.js");
    if (
      metadata.name !== "npm" ||
      metadata.version !== pin.version ||
      !(await lstat(cli)).isFile()
    ) {
      throw new Error("Extracted npm package does not match its pin");
    }
    if (replacements.length) {
      for (const support of supportPackages) {
        await checkInstalledIdentity(
          path.join(npmDirectory, "node_modules", support.name),
          support.name,
          support.version,
        );
      }
      for (const replacement of replacementArchives) {
        const destination = path.join(npmDirectory, ...replacement.layout.path);
        if (replacement.layout.replaces) {
          await checkInstalledIdentity(
            destination,
            replacement.pin.name,
            replacement.pin.replacesVersion,
          );
          await rm(destination, { recursive: true });
        } else {
          try {
            await lstat(destination);
            throw new Error(
              `Unexpected existing ${replacement.layout.name} replacement path`,
            );
          } catch (error) {
            if (error.code !== "ENOENT") throw error;
          }
        }
        await extractEntries(replacement.entries, destination);
        await checkInstalledIdentity(
          destination,
          replacement.pin.name,
          replacement.pin.version,
        );
      }
    }
    // Recheck the signature against a fresh TUF target using the verified
    // package's client. A removed key cannot rely on the static pin alone.
    await (options.verify || verifyRegistryPackages)(packages, {
      npmDirectory,
    });
    const result = spawnSync(process.execPath, [cli, "--version"], {
      encoding: "utf8",
      timeout: 20_000,
    });
    if (
      result.error ||
      result.status !== 0 ||
      result.stdout.trim() !== pin.version
    ) {
      throw new Error(`Verified npm did not report version ${pin.version}`);
    }
    await mkdir(shimDirectory);
    await writeFile(
      path.join(shimDirectory, "npm"),
      '#!/bin/sh\nexec node "$(dirname "$0")/../package/bin/npm-cli.js" "$@"\n',
      { mode: 0o755, flag: "wx" },
    );
    await writeFile(
      path.join(shimDirectory, "npm.cmd"),
      '@echo off\r\nnode "%~dp0..\\package\\bin\\npm-cli.js" %*\r\nexit /b %errorlevel%\r\n',
      { flag: "wx" },
    );
    await writeFile(
      path.join(shimDirectory, "npm.ps1"),
      '& node "$PSScriptRoot/../package/bin/npm-cli.js" @args\nexit $LASTEXITCODE\n',
      { flag: "wx" },
    );
    await appendFile(
      env.GITHUB_ENV,
      `STEAM_RELEASER_NPM_DIR=${npmDirectory}\n`,
    );
    await appendFile(env.GITHUB_PATH, `${shimDirectory}\n`);
    return {
      npmDirectory,
      shimDirectory,
      version: pin.version,
      replacements: replacements.map(
        ({ pin: replacement }) => `${replacement.name}@${replacement.version}`,
      ),
    };
  } catch (error) {
    await rm(root, { recursive: true, force: true });
    throw error;
  }
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const result = await prepareNpm();
  console.log(
    `Prepared registry-signature- and SHA-512-verified npm ${result.version} with ${result.replacements.length} verified replacement packages`,
  );
}
