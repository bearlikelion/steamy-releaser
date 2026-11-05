import { lstat, readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { isDeepStrictEqual } from "node:util";
import { verifyInstalledBins } from "./verify-installed-bins.mjs";
import { verifyInstalledBundledPackages } from "./verify-installed-bundles.mjs";
import { verifyInstalledRegistryPackages } from "./verify-installed-registry.mjs";
import { defaultLockPath, validateLockfile } from "./verify-npm-lock.mjs";

const maxMetadataBytes = 1024 * 1024;
const frontendBuildCaches = new Set([".vite", ".vite-temp", ".vitest-cache"]);

// npm audit uses the lockfile's virtual tree, not the installed tree. Confirm
// every installed package, including bundled children, appears in that tree.
export async function verifyInstalledPackages(
  lock,
  projectDirectory,
  { allowBuildCaches = false, includeInventory = false } = {},
) {
  validateLockfile(lock);
  let count = 0;
  const installed = new Map();

  async function visitPackage(directory, location, name) {
    const info = await lstat(directory);
    if (!info.isDirectory() || info.isSymbolicLink()) {
      throw new Error(`Installed package is not a directory: ${location}`);
    }
    const locked = lock.packages[location];
    if (!locked || locked.link) {
      throw new Error(`Installed package missing from lockfile: ${location}`);
    }
    const metadataPath = path.join(directory, "package.json");
    const metadataInfo = await lstat(metadataPath);
    if (
      !metadataInfo.isFile() ||
      metadataInfo.isSymbolicLink() ||
      metadataInfo.size > maxMetadataBytes
    ) {
      throw new Error(`Invalid installed package metadata: ${location}`);
    }
    const metadata = JSON.parse(await readFile(metadataPath, "utf8"));
    if (metadata.name !== name || metadata.version !== locked.version) {
      throw new Error(
        `Installed package identity does not match the lockfile: ${location}`,
      );
    }
    if (!isDeepStrictEqual(metadata.license, locked.license)) {
      throw new Error(
        `Installed package license does not match the lockfile: ${location}`,
      );
    }
    installed.set(location, { metadata, locked });
    count++;
    await visitModules(
      path.join(directory, "node_modules"),
      `${location}/node_modules`,
      true,
    );
  }

  async function visitModules(directory, location, optional = false) {
    let info;
    try {
      info = await lstat(directory);
    } catch (error) {
      if (optional && error.code === "ENOENT") return;
      throw error;
    }
    if (!info.isDirectory() || info.isSymbolicLink()) {
      throw new Error(`Installed node_modules is not a directory: ${location}`);
    }
    for (const item of await readdir(directory, { withFileTypes: true })) {
      if (item.name === ".bin" || item.name === ".package-lock.json") continue;
      if (
        allowBuildCaches &&
        location === "node_modules" &&
        frontendBuildCaches.has(item.name) &&
        item.isDirectory() &&
        !item.isSymbolicLink()
      ) {
        continue;
      }
      if (
        !item.isDirectory() ||
        item.isSymbolicLink() ||
        item.name.startsWith(".")
      ) {
        throw new Error(
          `Unexpected installed module entry: ${location}/${item.name}`,
        );
      }
      const child = path.join(directory, item.name);
      if (item.name.startsWith("@")) {
        for (const scoped of await readdir(child, { withFileTypes: true })) {
          if (
            !scoped.isDirectory() ||
            scoped.isSymbolicLink() ||
            scoped.name.startsWith(".")
          ) {
            throw new Error(
              `Unexpected installed module entry: ${location}/${item.name}/${scoped.name}`,
            );
          }
          const name = `${item.name}/${scoped.name}`;
          await visitPackage(
            path.join(child, scoped.name),
            `${location}/${name}`,
            name,
          );
        }
      } else {
        await visitPackage(child, `${location}/${item.name}`, item.name);
      }
    }
  }

  await visitModules(
    path.join(projectDirectory, "node_modules"),
    "node_modules",
  );
  if (!count) throw new Error("No installed npm packages found");
  const rootMetadataPath = path.join(projectDirectory, "package.json");
  const rootInfo = await lstat(rootMetadataPath);
  if (
    !rootInfo.isFile() ||
    rootInfo.isSymbolicLink() ||
    rootInfo.size > maxMetadataBytes
  ) {
    throw new Error("Invalid root package metadata");
  }
  const root = JSON.parse(await readFile(rootMetadataPath, "utf8"));
  verifyProductionDependencies(root, installed);
  if (includeInventory) {
    return [...installed].map(([location, { metadata }]) => ({
      location,
      name: metadata.name,
      version: metadata.version,
      license: metadata.license,
      dependencies: metadata.dependencies,
      optionalDependencies: metadata.optionalDependencies,
    }));
  }
  return count;
}

function verifyProductionDependencies(root, installed) {
  function resolveDependency(origin, name) {
    let current = origin;
    for (;;) {
      const candidate = current
        ? `${current}/node_modules/${name}`
        : `node_modules/${name}`;
      if (installed.has(candidate)) return candidate;
      if (!current) return "";
      const parent = current.lastIndexOf("/node_modules/");
      current = parent < 0 ? "" : current.slice(0, parent);
    }
  }

  function declarations(metadata, origin) {
    const result = new Map();
    const peerMetadata = metadata.peerDependenciesMeta ?? {};
    if (typeof peerMetadata !== "object" || Array.isArray(peerMetadata)) {
      throw new Error(
        `Invalid peerDependenciesMeta for ${origin || "root package"}`,
      );
    }
    for (const [field, optional] of [
      ["dependencies", false],
      ["optionalDependencies", true],
      ["peerDependencies", false],
    ]) {
      const value = metadata[field] ?? {};
      if (typeof value !== "object" || Array.isArray(value)) {
        throw new Error(`Invalid ${field} for ${origin || "root package"}`);
      }
      for (const name of Object.keys(value)) {
        if (field === "peerDependencies") {
          const peer = peerMetadata[name] ?? {};
          if (
            typeof peer !== "object" ||
            Array.isArray(peer) ||
            (peer.optional !== undefined && typeof peer.optional !== "boolean")
          ) {
            throw new Error(
              `Invalid peer metadata for ${name} from ${origin || "root package"}`,
            );
          }
          const peerOptional = peer.optional === true;
          result.set(
            name,
            result.has(name) ? result.get(name) && peerOptional : peerOptional,
          );
        } else {
          // npm lets optionalDependencies override dependencies of the same name.
          result.set(name, optional);
        }
      }
    }
    const bundled = metadata.bundleDependencies ?? metadata.bundledDependencies;
    if (Array.isArray(bundled)) {
      for (const name of bundled) {
        if (typeof name !== "string")
          throw new Error(
            `Invalid bundled dependency for ${origin || "root package"}`,
          );
        if (!result.has(name)) result.set(name, false);
      }
    } else if (bundled !== undefined && typeof bundled !== "boolean") {
      throw new Error(
        `Invalid bundled dependencies for ${origin || "root package"}`,
      );
    }
    return result;
  }

  const seen = new Set();
  const queue = [{ location: "", metadata: root }];
  for (let index = 0; index < queue.length; index++) {
    const { location, metadata } = queue[index];
    for (const [name, optional] of declarations(metadata, location)) {
      const dependency = resolveDependency(location, name);
      if (!dependency) {
        if (optional) continue;
        throw new Error(
          `Missing required production dependency ${name} from ${location || "root package"}`,
        );
      }
      const entry = installed.get(dependency);
      if (entry.locked.dev === true) {
        throw new Error(
          `Production dependency ${dependency} is marked as development-only in the lockfile`,
        );
      }
      if (seen.has(dependency)) continue;
      seen.add(dependency);
      queue.push({ location: dependency, metadata: entry.metadata });
    }
  }
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  if (
    process.argv.length < 4 ||
    process.argv.length > 5 ||
    process.argv[2] !== "--cache" ||
    !process.argv[3]
  )
    throw new Error(
      "Usage: node scripts/verify-installed-npm.mjs --cache CACHE_DIRECTORY [LOCKFILE]",
    );
  const cacheDirectory = path.resolve(process.argv[3]);
  const lockPath = path.resolve(process.argv[4] || defaultLockPath);
  const lock = JSON.parse(await readFile(lockPath, "utf8"));
  const inventory = await verifyInstalledPackages(
    lock,
    path.dirname(lockPath),
    { includeInventory: true },
  );
  const bundled = await verifyInstalledBundledPackages(
    lock,
    path.dirname(lockPath),
  );
  const registry = await verifyInstalledRegistryPackages(
    lock,
    path.dirname(lockPath),
    inventory,
    cacheDirectory,
  );
  if (registry.packages + bundled.packages !== inventory.length)
    throw new Error("Installed package files were not all verified");
  const bins = await verifyInstalledBins(path.dirname(lockPath), inventory);
  console.log(
    `Verified ${registry.files + bundled.files} installed files in ${inventory.length} npm packages and ${bins} command links or shims`,
  );
}
