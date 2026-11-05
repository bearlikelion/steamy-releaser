import { createHash } from "node:crypto";
import { inspectArchive } from "./inspect-npm-archive.mjs";

function bundleNames(metadata) {
  const declarations = [
    metadata.bundleDependencies,
    metadata.bundledDependencies,
  ].filter((value) => value !== undefined && value !== false);
  const lists = declarations.map((value) => {
    const names =
      value === true ? Object.keys(metadata.dependencies || {}) : value;
    if (
      !Array.isArray(names) ||
      names.some((name) => typeof name !== "string" || !name) ||
      new Set(names).size !== names.length
    ) {
      throw new Error("Invalid bundled dependency declaration");
    }
    return [...names].sort();
  });
  if (
    lists.length > 1 &&
    JSON.stringify(lists[0]) !== JSON.stringify(lists[1])
  ) {
    throw new Error("Conflicting bundled dependency declarations");
  }
  return lists[0] || [];
}

export function verifyBundleDeclaration(entry, manifest) {
  const declared = bundleNames(manifest);
  if (
    JSON.stringify(declared) !==
    JSON.stringify([...entry.bundleDependencies].sort())
  ) {
    throw new Error(
      "Registry bundled dependency declaration does not match the lockfile",
    );
  }
  return declared;
}

// The parent package's signed SHA-512 covers bundled dependencies. Compare the
// names and versions in that exact archive with the lockfile used by npm audit.
export function verifyBundledArchive(entry, manifest, compressed) {
  const actualIntegrity = `sha512-${createHash("sha512").update(compressed).digest("base64")}`;
  if (actualIntegrity !== entry.integrity) {
    throw new Error(
      "Bundled package archive SHA-512 does not match the lockfile",
    );
  }
  const files = inspectArchive(compressed, {
    requiredPaths: ["package/package.json"],
    allowedRoots: entry.name.startsWith("@types/")
      ? ["package", entry.name.slice("@types/".length)]
      : ["package"],
  });
  const root = files.find(
    (file) => file.path === "package/package.json" && file.type === "File",
  );
  if (!root) throw new Error("Bundled archive has no package metadata");
  const rootMetadata = JSON.parse(root.data.toString("utf8"));
  if (
    rootMetadata.name !== entry.name ||
    rootMetadata.version !== entry.version
  ) {
    throw new Error(
      "Bundled archive package identity does not match the lockfile",
    );
  }
  if (
    JSON.stringify(bundleNames(rootMetadata)) !==
    JSON.stringify(bundleNames(manifest))
  ) {
    throw new Error(
      "Archive bundled dependency declaration does not match registry metadata",
    );
  }

  const expected = new Map(entry.bundled.map((item) => [item.path, item]));
  const actual = new Set();
  const packagePath =
    /^package\/(node_modules\/(?:@[^/]+\/)?[^/]+(?:\/node_modules\/(?:@[^/]+\/)?[^/]+)*)\/package\.json$/;
  for (const file of files) {
    const match = packagePath.exec(file.path);
    if (!match || file.type !== "File") continue;
    const relative = match[1];
    const locked = expected.get(relative);
    if (!locked)
      throw new Error(`Bundled package missing from lockfile: ${relative}`);
    const metadata = JSON.parse(file.data.toString("utf8"));
    if (metadata.name !== locked.name || metadata.version !== locked.version) {
      throw new Error(
        `Bundled package identity does not match the lockfile: ${relative}`,
      );
    }
    actual.add(relative);
  }
  for (const relative of expected.keys()) {
    if (!actual.has(relative))
      throw new Error(`Bundled package missing from archive: ${relative}`);
  }
  return files;
}

// An authentic parent archive can still carry modified bytes under a child's
// published name and version. Compare each child with its own signed npm
// archive before attributing the standalone package's advisories or provenance.
export function verifyBundledChild(
  parent,
  parentFiles,
  child,
  official,
  compressed,
) {
  const actualIntegrity = `sha512-${createHash("sha512").update(compressed).digest("base64")}`;
  if (actualIntegrity !== official.integrity) {
    throw new Error(
      `Bundled child archive SHA-512 does not match: ${child.path}`,
    );
  }
  const files = inspectArchive(compressed, {
    requiredPaths: ["package/package.json"],
    allowedRoots: child.name.startsWith("@types/")
      ? ["package", child.name.slice("@types/".length)]
      : ["package"],
  });
  const metadata = files.find(
    (file) => file.path === "package/package.json" && file.type === "File",
  );
  if (!metadata)
    throw new Error(`Bundled child archive has no metadata: ${child.path}`);
  const pkg = JSON.parse(metadata.data.toString("utf8"));
  if (pkg.name !== child.name || pkg.version !== child.version) {
    throw new Error(
      `Bundled child archive has a different identity: ${child.path}`,
    );
  }

  const nested = parent.bundled
    .filter((other) => other.path.startsWith(`${child.path}/node_modules/`))
    .map((other) => other.path.slice(child.path.length + 1));
  const insideNested = (name) =>
    nested.some((prefix) => name === prefix || name.startsWith(`${prefix}/`));
  function regularFiles(entries, prefix) {
    const result = new Map();
    for (const file of entries) {
      if (file.type !== "File" || !file.path.startsWith(prefix)) continue;
      const name = file.path.slice(prefix.length);
      if (!insideNested(name)) result.set(name, file);
    }
    return result;
  }
  const bundled = regularFiles(parentFiles, `package/${child.path}/`);
  const published = regularFiles(files, "package/");
  if (bundled.size !== published.size) {
    throw new Error(
      `Bundled child files differ from its published archive: ${child.path}`,
    );
  }
  for (const [name, file] of published) {
    const other = bundled.get(name);
    if (!other || other.mode !== file.mode || !other.data.equals(file.data)) {
      throw new Error(
        `Bundled child files differ from its published archive: ${child.path}/${name}`,
      );
    }
  }
  return bundled;
}
