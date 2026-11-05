import { lstat, opendir, readFile, realpath } from "node:fs/promises";
import path from "node:path";
import { validateLockfile } from "./verify-npm-lock.mjs";
import { verifyRegistryPackages } from "./verify-npm-registry.mjs";

const maxEntries = 100_000;
const maxPathBytes = 4096;

function samePath(a, b) {
  return process.platform === "win32"
    ? a.toLowerCase() === b.toLowerCase()
    : a === b;
}

// npm can normalize installed modes, so compare regular file paths and bytes.
export async function verifyInstalledFiles(
  projectDirectory,
  location,
  expected,
  { nested = [], allowNpmArtifacts = false, label = "package" } = {},
) {
  const project = path.resolve(projectDirectory);
  if (
    !/^node_modules\/(?:@[^/]+\/)?[^/]+(?:\/node_modules\/(?:@[^/]+\/)?[^/]+)*$/.test(
      location,
    ) ||
    location
      .split("/")
      .some((part) => part === "." || part === ".." || part.includes("\\"))
  ) {
    throw new Error(`Invalid installed bundled package path: ${location}`);
  }
  const directory = path.join(project, ...location.split("/"));
  const info = await lstat(directory);
  if (!info.isDirectory() || info.isSymbolicLink()) {
    throw new Error(`Installed ${label} is not a directory: ${location}`);
  }
  const expectedReal = path.join(
    await realpath(project),
    ...location.split("/"),
  );
  if (!samePath(await realpath(directory), expectedReal)) {
    throw new Error(`Installed ${label} has a linked ancestor: ${location}`);
  }
  const isNested = (name) =>
    nested.some((prefix) => name === prefix || name.startsWith(`${prefix}/`));
  const seen = new Set();
  let entries = 0;
  async function visit(current, parts = []) {
    const stream = await opendir(current);
    for await (const entry of stream) {
      const next = [...parts, entry.name];
      const name = next.join("/");
      if (
        ++entries > maxEntries ||
        next.length > 40 ||
        Buffer.byteLength(name, "utf8") > maxPathBytes
      ) {
        throw new Error(
          `Installed ${label} exceeds traversal limits: ${location}`,
        );
      }
      if (isNested(name)) continue;
      const filename = path.join(current, entry.name);
      const item = await lstat(filename);
      if (item.isSymbolicLink()) {
        throw new Error(
          `Installed ${label} contains a link: ${location}/${name}`,
        );
      }
      if (
        allowNpmArtifacts &&
        name === "node_modules/.bin" &&
        item.isDirectory()
      )
        continue;
      if (
        allowNpmArtifacts &&
        name === "node_modules/.package-lock.json" &&
        item.isFile()
      )
        continue;
      if (item.isDirectory()) {
        await visit(filename, next);
      } else if (item.isFile()) {
        const file = expected.get(name);
        if (
          !file ||
          item.size !== file.data.length ||
          !(await readFile(filename)).equals(file.data)
        ) {
          throw new Error(
            `Installed ${label} file differs from signed archive: ${location}/${name}`,
          );
        }
        seen.add(name);
      } else {
        throw new Error(
          `Installed ${label} contains a special file: ${location}/${name}`,
        );
      }
    }
  }
  await visit(directory);
  if (seen.size !== expected.size) {
    throw new Error(`Installed ${label} is missing signed files: ${location}`);
  }
  return seen.size;
}

// Compare a bundled child with the files matched between its signed parent and
// separately signed registry archive.
export async function verifyInstalledBundle(
  projectDirectory,
  parent,
  child,
  expected,
) {
  const nested = parent.bundled
    .filter((other) => other.path.startsWith(`${child.path}/node_modules/`))
    .map((other) => other.path.slice(child.path.length + 1));
  return verifyInstalledFiles(
    projectDirectory,
    `${parent.location}/${child.path}`,
    expected,
    {
      nested,
      label: "bundled package",
    },
  );
}

export async function verifyInstalledBundledPackages(
  lock,
  projectDirectory,
  options = {},
) {
  const project = path.resolve(projectDirectory);
  const { packages } = validateLockfile(lock);
  const parents = [];
  for (const parent of packages.filter((entry) => entry.bundled.length)) {
    const directory = path.join(project, ...parent.location.split("/"));
    let info;
    try {
      info = await lstat(directory);
    } catch (error) {
      const flags = lock.packages[parent.location];
      if (
        error.code === "ENOENT" &&
        (flags?.optional || flags?.devOptional || flags?.dev)
      )
        continue;
      throw error;
    }
    if (!info.isDirectory() || info.isSymbolicLink()) {
      throw new Error(
        `Installed bundle parent is not a directory: ${parent.location}`,
      );
    }
    parents.push(parent);
  }
  if (!parents.length) return { packages: 0, files: 0 };
  let count = 0;
  let files = 0;
  await verifyRegistryPackages(parents, {
    ...options,
    onBundledChild: async (parent, child, expected, published) => {
      const location = `${parent.location}/${child.path}`;
      try {
        await lstat(path.join(project, ...location.split("/")));
      } catch (error) {
        const info = lock.packages[location];
        if (error.code === "ENOENT" && (info?.optional || info?.devOptional))
          return;
        throw error;
      }
      const checked = await verifyInstalledBundle(
        project,
        parent,
        child,
        expected,
      );
      files += checked;
      count++;
      if (options.onVerifiedChild)
        await options.onVerifiedChild({ ...published, location });
    },
  });
  return { packages: count, files };
}
