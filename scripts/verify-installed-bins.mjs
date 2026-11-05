import {
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  readdir,
  readlink,
  realpath,
  rm,
  writeFile,
} from "node:fs/promises";
import { realpathSync } from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import { activeNpmDirectory } from "./verify-npm-registry.mjs";

// These commands are used by the checked-in frontend scripts. Do not accept
// another installed dependency exporting the same name in their place.
const buildCommands = new Map([
  ["tsc", "typescript"],
  ["oxlint", "oxlint"],
  ["prettier", "prettier"],
  ["vitest", "vitest"],
  ["vite", "vite"],
]);

function bundledTools(npmDirectory) {
  const npm = realpathSync(npmDirectory);
  const modules = realpathSync(path.join(npm, "node_modules"));
  const requireFromNpm = createRequire(path.join(npm, "package.json"));
  const load = (name) => {
    const filename = realpathSync(
      requireFromNpm.resolve(name, {
        paths: [path.join(modules, "bin-links")],
      }),
    );
    const relative = path.relative(modules, filename);
    if (
      !relative ||
      relative === ".." ||
      relative.startsWith(`..${path.sep}`) ||
      path.isAbsolute(relative)
    )
      throw new Error(
        `npm bin tool is outside the verified npm modules: ${name}`,
      );
    return requireFromNpm(filename);
  };
  return {
    normalize: load("npm-normalize-package-bin"),
    cmdShim: load("cmd-shim"),
  };
}

async function existing(filename) {
  try {
    return await lstat(filename);
  } catch (error) {
    if (error.code === "ENOENT") return null;
    throw error;
  }
}

// Call only after package.json and executable bytes have been compared with
// authenticated archives. npm-generated links and shims are not in those archives.
export async function verifyInstalledBins(
  projectDirectory,
  inventory,
  options = {},
) {
  const project = path.resolve(projectDirectory);
  const platform = options.platform || process.platform;
  const { normalize, cmdShim } =
    options.tools || bundledTools(options.npmDirectory || activeNpmDirectory());
  const requiredCommands = options.requiredCommands || buildCommands;
  const candidates = new Map();
  const moduleDirectories = new Set(["node_modules"]);
  for (const item of inventory) {
    const marker = item.location.lastIndexOf("node_modules/");
    if (marker < 0)
      throw new Error(`Invalid installed package location: ${item.location}`);
    const modules = item.location.slice(0, marker + "node_modules".length);
    moduleDirectories.add(modules);
    moduleDirectories.add(`${item.location}/node_modules`);
    const packageDirectory = path.join(project, ...item.location.split("/"));
    const metadata = JSON.parse(
      await readFile(path.join(packageDirectory, "package.json"), "utf8"),
    );
    const bins = normalize(metadata).bin || {};
    for (const [name, target] of Object.entries(bins)) {
      if (
        !/^[A-Za-z0-9_][A-Za-z0-9._-]*$/.test(name) ||
        /[.]$/.test(name) ||
        typeof target !== "string" ||
        !target ||
        target
          .split("/")
          .some(
            (part) =>
              !part || part === "." || part === ".." || part.includes("\\"),
          )
      )
        throw new Error(
          `Unsafe installed command declaration: ${item.location}/${name}`,
        );
      const source = path.join(packageDirectory, ...target.split("/"));
      const info = await existing(source);
      if (!info) continue; // npm skips a declared bin whose target is absent.
      if (!info.isFile() || info.isSymbolicLink())
        throw new Error(`Invalid command target: ${item.location}/${target}`);
      const key = `${modules}/.bin/${name}`;
      const list = candidates.get(key) || [];
      list.push({ source, location: item.location, target });
      candidates.set(key, list);
    }
  }
  const temporary =
    platform === "win32"
      ? await mkdtemp(path.join(os.tmpdir(), "steam-releaser-bin-check-"))
      : null;
  let count = 0;
  try {
    for (const modules of moduleDirectories) {
      const directory = path.join(project, ...modules.split("/"), ".bin");
      const info = await existing(directory);
      const prefix = `${modules}/.bin/`;
      const expected = [...candidates.keys()].filter(
        (key) =>
          key.startsWith(prefix) && !key.slice(prefix.length).includes("/"),
      );
      if (!info) {
        if (expected.length)
          throw new Error(`Missing installed commands: ${modules}/.bin`);
        continue;
      }
      if (!info.isDirectory() || info.isSymbolicLink())
        throw new Error(`Invalid installed command directory: ${modules}/.bin`);
      const actual = new Set(await readdir(directory));
      for (const key of expected) {
        const name = key.slice(prefix.length);
        const allowed = candidates
          .get(key)
          .filter(
            (candidate) =>
              modules !== "node_modules" ||
              !requiredCommands.has(name) ||
              candidate.location ===
                `node_modules/${requiredCommands.get(name)}`,
          );
        if (!allowed.length)
          throw new Error(`Unexpected owner of build command: ${key}`);
        const suffixes = platform === "win32" ? ["", ".cmd", ".ps1"] : [""];
        if (suffixes.some((suffix) => !actual.delete(name + suffix)))
          throw new Error(`Missing installed command: ${key}`);
        let matched = false;
        for (const candidate of allowed) {
          if (platform === "win32") {
            const fakeSource = path.join(
              temporary,
              ...candidate.location.split("/"),
              ...candidate.target.split("/"),
            );
            const fakeBin = path.join(
              temporary,
              ...modules.split("/"),
              ".bin",
              name,
            );
            await mkdir(path.dirname(fakeSource), { recursive: true });
            await writeFile(fakeSource, await readFile(candidate.source));
            await cmdShim(fakeSource, fakeBin);
            let equal = true;
            for (const suffix of suffixes) {
              const installed = path.join(directory, name + suffix);
              const stat = await lstat(installed);
              if (
                !stat.isFile() ||
                stat.isSymbolicLink() ||
                !(await readFile(installed)).equals(
                  await readFile(fakeBin + suffix),
                )
              )
                equal = false;
            }
            if (equal) {
              matched = true;
              break;
            }
          } else {
            const installed = path.join(directory, name);
            const stat = await lstat(installed);
            if (
              stat.isSymbolicLink() &&
              path.resolve(directory, await readlink(installed)) ===
                candidate.source &&
              (await realpath(installed)) === (await realpath(candidate.source))
            ) {
              matched = true;
              break;
            }
          }
        }
        if (!matched)
          throw new Error(
            `Installed command differs from its verified target: ${key}`,
          );
        count++;
      }
      if (actual.size)
        throw new Error(
          `Unexpected installed commands in ${modules}/.bin: ${[...actual].join(", ")}`,
        );
    }
    for (const name of requiredCommands.keys()) {
      if (!candidates.has(`node_modules/.bin/${name}`))
        throw new Error(`Missing required build command: ${name}`);
    }
  } finally {
    if (temporary) await rm(temporary, { recursive: true, force: true });
  }
  return count;
}
