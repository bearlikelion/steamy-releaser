import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
export const defaultLockPath = path.join(
  root,
  "cmd/steam-releaser-desktop/frontend/package-lock.json",
);

export function validateLockfile(lock) {
  if (lock.lockfileVersion !== 3 || !lock.packages || !lock.packages[""]) {
    throw new Error("Expected an npm v3 lockfile with a root package");
  }

  const packages = [];
  const packagesByLocation = new Map();
  const bundledEntries = [];
  let bundled = 0;
  for (const [location, entry] of Object.entries(lock.packages)) {
    if (!location) continue;
    for (const flag of ["dev", "optional", "devOptional", "peer", "inBundle"]) {
      if (entry[flag] !== undefined && typeof entry[flag] !== "boolean") {
        throw new Error(`Invalid ${flag} flag for ${location}`);
      }
    }
    if (
      !/^node_modules\/(?:@[^/]+\/)?[^/]+(?:\/node_modules\/(?:@[^/]+\/)?[^/]+)*$/.test(
        location,
      ) ||
      location
        .split("/")
        .some((part) => part === "." || part === ".." || part.includes("\\")) ||
      !entry.version ||
      entry.link
    ) {
      throw new Error(`Unexpected package entry: ${location}`);
    }
    const nested = location.lastIndexOf("/node_modules/");
    const packageName = location.slice(
      nested < 0 ? "node_modules/".length : nested + "/node_modules/".length,
    );
    if (!/^(?:@[^/]+\/)?[^/]+$/.test(packageName)) {
      throw new Error(`Unexpected package name: ${location}`);
    }
    if (entry.inBundle) {
      const separator = "/node_modules/";
      const index = location.lastIndexOf(separator);
      const parent = lock.packages[location.slice(0, index)];
      const child = location.slice(index + separator.length);
      if (
        index < 0 ||
        !Array.isArray(parent?.bundleDependencies) ||
        !parent.bundleDependencies.includes(child)
      ) {
        throw new Error(`Bundled package has no declaring parent: ${location}`);
      }
      bundledEntries.push({
        location,
        name: packageName,
        version: entry.version,
      });
      bundled++;
      continue;
    }
    let url;
    try {
      url = new URL(entry.resolved);
    } catch {
      throw new Error(`Missing or invalid package URL: ${location}`);
    }
    if (
      url.protocol !== "https:" ||
      url.hostname !== "registry.npmjs.org" ||
      url.port ||
      url.username ||
      url.password ||
      url.search ||
      url.hash
    ) {
      throw new Error(
        `Package does not use the npm registry over HTTPS: ${location}`,
      );
    }
    const basename = packageName.slice(packageName.lastIndexOf("/") + 1);
    const expectedPath = `/${packageName}/-/${basename}-${entry.version}.tgz`;
    if (url.pathname !== expectedPath) {
      throw new Error(
        `Tarball does not match package and version: ${location}`,
      );
    }
    if (!/^sha512-[A-Za-z0-9+/]{86}==$/.test(entry.integrity || "")) {
      throw new Error(
        `Package is missing a SHA-512 integrity hash: ${location}`,
      );
    }
    if (
      entry.bundleDependencies !== undefined &&
      (!Array.isArray(entry.bundleDependencies) ||
        entry.bundleDependencies.some((name) => typeof name !== "string") ||
        new Set(entry.bundleDependencies).size !==
          entry.bundleDependencies.length)
    ) {
      throw new Error(`Invalid bundled dependency declaration: ${location}`);
    }
    const registryEntry = {
      location,
      name: packageName,
      version: entry.version,
      resolved: entry.resolved,
      integrity: entry.integrity,
      bundleDependencies: entry.bundleDependencies || [],
      bundled: [],
    };
    packages.push(registryEntry);
    packagesByLocation.set(location, registryEntry);
  }
  for (const bundledEntry of bundledEntries) {
    let parentLocation = bundledEntry.location;
    do {
      const index = parentLocation.lastIndexOf("/node_modules/");
      if (index < 0) {
        throw new Error(
          `Bundled package has no registry parent: ${bundledEntry.location}`,
        );
      }
      parentLocation = parentLocation.slice(0, index);
    } while (lock.packages[parentLocation]?.inBundle);
    const owner = packagesByLocation.get(parentLocation);
    if (!owner) {
      throw new Error(
        `Bundled package has no registry parent: ${bundledEntry.location}`,
      );
    }
    owner.bundled.push({
      path: bundledEntry.location.slice(parentLocation.length + 1),
      name: bundledEntry.name,
      version: bundledEntry.version,
    });
  }
  if (!packages.length)
    throw new Error("The npm lockfile contains no registry packages");
  return { packages, bundled };
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  if (process.argv.length > 3)
    throw new Error("Usage: node scripts/verify-npm-lock.mjs [LOCKFILE]");
  const lock = JSON.parse(
    readFileSync(process.argv[2] || defaultLockPath, "utf8"),
  );
  const { packages, bundled } = validateLockfile(lock);
  console.log(
    `Verified ${packages.length} registry packages and ${bundled} bundled package entries`,
  );
}
