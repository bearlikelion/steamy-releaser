import { createHash } from "node:crypto";
import { realpathSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { inspectArchive, maxArchiveBytes } from "./inspect-npm-archive.mjs";
import { verifyInstalledFiles } from "./verify-installed-bundles.mjs";
import { validateLockfile } from "./verify-npm-lock.mjs";
import {
  activeNpmDirectory,
  verifyRegistryPackages,
} from "./verify-npm-registry.mjs";

function npmCacheReader(npmDirectory, cacheDirectory) {
  const npm = realpathSync(npmDirectory);
  const modules = realpathSync(path.join(npm, "node_modules"));
  if (path.dirname(modules) !== npm)
    throw new Error("npm modules directory is outside verified npm");
  const cacheModule = realpathSync(path.join(modules, "cacache"));
  const relative = path.relative(modules, cacheModule);
  if (
    !relative ||
    relative === ".." ||
    relative.startsWith(`..${path.sep}`) ||
    path.isAbsolute(relative)
  ) {
    throw new Error("npm cache reader is outside npm's bundled modules");
  }
  const cacache = createRequire(path.join(npm, "package.json"))(cacheModule);
  return async (integrity) => {
    const stream = cacache.get.stream.byDigest(
      path.join(cacheDirectory, "_cacache"),
      integrity,
    );
    const chunks = [];
    let size = 0;
    for await (const chunk of stream) {
      size += chunk.length;
      if (size > maxArchiveBytes) {
        stream.destroy();
        throw new Error("Cached npm archive exceeds the size limit");
      }
      chunks.push(chunk);
    }
    return Buffer.concat(chunks, size);
  };
}

// Registry signatures bind the lockfile digest. Compare each installed
// registry package with that exact tarball from npm's content-addressed cache.
export async function verifyInstalledRegistryPackages(
  lock,
  projectDirectory,
  inventory,
  cacheDirectory,
  options = {},
) {
  if (!Array.isArray(inventory) || !cacheDirectory) {
    throw new Error("Expected an installed inventory and npm cache directory");
  }
  const { packages } = validateLockfile(lock);
  const byLocation = new Map(packages.map((entry) => [entry.location, entry]));
  const installed = new Set();
  const entries = [];
  for (const item of inventory) {
    if (typeof item?.location !== "string" || installed.has(item.location)) {
      throw new Error("Invalid or duplicate installed package location");
    }
    installed.add(item.location);
    const locked = lock.packages[item.location];
    if (!locked || locked.version !== item.version) {
      throw new Error(
        `Installed package is missing from the lockfile: ${item.location}`,
      );
    }
    if (locked.inBundle) continue;
    const entry = byLocation.get(item.location);
    if (!entry || entry.name !== item.name || entry.version !== item.version) {
      throw new Error(
        `Installed registry identity does not match: ${item.location}`,
      );
    }
    entries.push(entry);
  }
  if (!entries.length) throw new Error("No installed registry packages found");
  await (options.verifyRegistry || verifyRegistryPackages)(
    entries,
    options.registryOptions || {},
  );
  const readArchive =
    options.readArchive ||
    npmCacheReader(
      options.npmDirectory || activeNpmDirectory(),
      path.resolve(cacheDirectory),
    );
  const allLocations = Object.keys(lock.packages);
  let files = 0;
  for (const entry of entries) {
    const bytes = await readArchive(entry.integrity);
    if (
      !Buffer.isBuffer(bytes) ||
      bytes.length > maxArchiveBytes ||
      `sha512-${createHash("sha512").update(bytes).digest("base64")}` !==
        entry.integrity
    ) {
      throw new Error(
        `Cached npm archive does not match its signed digest: ${entry.location}`,
      );
    }
    const archive = inspectArchive(bytes, {
      requiredPaths: ["package/package.json"],
      allowedRoots: entry.name.startsWith("@types/")
        ? ["package", entry.name.slice("@types/".length)]
        : ["package"],
    });
    const metadata = archive.find(
      (file) => file.path === "package/package.json" && file.type === "File",
    );
    if (!metadata)
      throw new Error(`Cached npm archive lacks metadata: ${entry.location}`);
    const packageJson = JSON.parse(metadata.data.toString("utf8"));
    if (
      packageJson.name !== entry.name ||
      packageJson.version !== entry.version
    ) {
      throw new Error(
        `Cached npm archive has a different identity: ${entry.location}`,
      );
    }
    const prefix = `${entry.location}/node_modules/`;
    const nested = allLocations
      .filter((location) => location.startsWith(prefix))
      .map((location) => location.slice(entry.location.length + 1));
    const insideNested = (name) =>
      nested.some((value) => name === value || name.startsWith(`${value}/`));
    const expected = new Map();
    for (const file of archive) {
      if (file.type !== "File" || !file.path.startsWith("package/")) continue;
      const name = file.path.slice("package/".length);
      if (insideNested(name)) continue;
      if (
        name === "node_modules/.package-lock.json" ||
        name.startsWith("node_modules/.bin/")
      ) {
        throw new Error(
          `npm archive contains generated installation paths: ${entry.location}/${name}`,
        );
      }
      expected.set(name, file);
    }
    files += await verifyInstalledFiles(
      projectDirectory,
      entry.location,
      expected,
      {
        nested,
        allowNpmArtifacts: true,
        label: "registry package",
      },
    );
  }
  return { packages: entries.length, files };
}
