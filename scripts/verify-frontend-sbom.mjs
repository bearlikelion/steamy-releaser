import { lstat, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { verifyInstalledPackages } from "./verify-installed-npm.mjs";
import { defaultLockPath, validateLockfile } from "./verify-npm-lock.mjs";

const maxSbomBytes = 16 * 1024 * 1024;
const identity = (entry) => JSON.stringify([entry.name, entry.version]);

function hasLicense(component, expected) {
  return (
    typeof expected !== "string" ||
    (Array.isArray(component.licenses) &&
      component.licenses.some(
        (item) =>
          item?.license?.id === expected ||
          item?.license?.name === expected ||
          item?.expression === expected,
      ))
  );
}

function lockedInventory(lock) {
  validateLockfile(lock);
  return Object.entries(lock.packages)
    .filter(([location]) => location.startsWith("node_modules/"))
    .map(([location, metadata]) => {
      const separator = "/node_modules/";
      const nested = location.lastIndexOf(separator);
      return {
        location,
        name: location.slice(
          nested < 0 ? "node_modules/".length : nested + separator.length,
        ),
        version: metadata.version,
        license: metadata.license,
        optional: metadata.optional === true || metadata.devOptional === true,
      };
    });
}

// The release SBOM describes the Linux frontend build's installed npm tree.
// Require package identities and consistent references rather than assuming
// npm's --omit selectors preserve packages also used by production code.
export function verifyFrontendSbom(sbom, root, required, allowed = required) {
  if (
    typeof root?.name !== "string" ||
    typeof root.version !== "string" ||
    sbom?.bomFormat !== "CycloneDX" ||
    sbom.specVersion !== "1.5" ||
    !Array.isArray(sbom.components) ||
    !Array.isArray(sbom.dependencies) ||
    sbom.metadata?.component?.name !== root.name ||
    sbom.metadata.component.version !== root.version ||
    !hasLicense(sbom.metadata.component, root.license)
  ) {
    throw new Error("Invalid frontend CycloneDX inventory or root package");
  }
  const allowedIdentities = new Map();
  for (const entry of allowed) {
    const key = identity(entry);
    const licenses = allowedIdentities.get(key) || new Set();
    licenses.add(entry.license);
    allowedIdentities.set(key, licenses);
  }
  const found = new Set();
  const references = new Set();
  const rootRef = sbom.metadata.component["bom-ref"];
  if (typeof rootRef !== "string" || !rootRef)
    throw new Error("Frontend SBOM is missing its root reference");
  references.add(rootRef);
  for (const component of sbom.components) {
    if (
      typeof component?.name !== "string" ||
      typeof component.version !== "string" ||
      component.type !== "library" ||
      typeof component["bom-ref"] !== "string" ||
      !component["bom-ref"]
    ) {
      throw new Error("Invalid frontend SBOM component");
    }
    const key = identity(component);
    const packageURL = `pkg:npm/${encodeURIComponent(component.name).replace(/%2F/gi, "/")}@${encodeURIComponent(component.version)}`;
    const licenses = allowedIdentities.get(key);
    if (
      !licenses ||
      component.purl !== packageURL ||
      ![...licenses].every((license) => hasLicense(component, license))
    ) {
      throw new Error(
        `Unexpected frontend SBOM component ${component.name}@${component.version}`,
      );
    }
    if (references.has(component["bom-ref"])) {
      throw new Error(
        `Duplicate frontend SBOM reference ${component["bom-ref"]}`,
      );
    }
    found.add(key);
    references.add(component["bom-ref"]);
  }
  for (const entry of required) {
    if (!found.has(identity(entry))) {
      throw new Error(
        `Frontend SBOM is missing ${entry.name}@${entry.version}`,
      );
    }
  }
  const dependencyRefs = new Set();
  for (const dependency of sbom.dependencies) {
    if (
      typeof dependency?.ref !== "string" ||
      !references.has(dependency.ref) ||
      dependencyRefs.has(dependency.ref) ||
      !Array.isArray(dependency.dependsOn) ||
      dependency.dependsOn.some((ref) => !references.has(ref))
    ) {
      throw new Error("Invalid frontend SBOM dependency references");
    }
    dependencyRefs.add(dependency.ref);
  }
  if ([...references].some((ref) => !dependencyRefs.has(ref))) {
    throw new Error("Frontend SBOM is missing a dependency entry");
  }
  return found.size;
}

// Check actual dependency and optional-dependency edges. Peer dependencies use
// npm's peer-placement rules and are checked for consistent references above.
export function verifyInstalledSbomEdges(sbom, root, installed) {
  const byLocation = new Map(installed.map((entry) => [entry.location, entry]));
  const byIdentity = new Map(
    sbom.components.map((component) => [
      identity(component),
      component["bom-ref"],
    ]),
  );
  const edges = new Map(
    sbom.dependencies.map((entry) => [entry.ref, new Set(entry.dependsOn)]),
  );

  function resolve(origin, name) {
    let current = origin;
    for (;;) {
      const location = current
        ? `${current}/node_modules/${name}`
        : `node_modules/${name}`;
      if (byLocation.has(location)) return byLocation.get(location);
      if (!current) return undefined;
      const parent = current.lastIndexOf("/node_modules/");
      current = parent < 0 ? "" : current.slice(0, parent);
    }
  }

  for (const entry of [{ ...root, location: "" }, ...installed]) {
    const ref = entry.location
      ? byIdentity.get(identity(entry))
      : sbom.metadata.component["bom-ref"];
    const declarations = new Map();
    const fields = [
      ...(entry.location ? [] : [["devDependencies", false]]),
      ["dependencies", false],
      ["optionalDependencies", true],
    ];
    for (const [field, optional] of fields) {
      const value = entry[field] ?? {};
      if (typeof value !== "object" || Array.isArray(value)) {
        throw new Error(
          `Invalid ${field} for ${entry.location || "root package"}`,
        );
      }
      for (const name of Object.keys(value)) declarations.set(name, optional);
    }
    for (const [name, optional] of declarations) {
      const dependency = resolve(entry.location, name);
      if (!dependency) {
        if (optional) continue;
        throw new Error(
          `Missing installed dependency ${name} from ${entry.location || "root package"}`,
        );
      }
      if (!edges.get(ref)?.has(byIdentity.get(identity(dependency)))) {
        throw new Error(
          `Frontend SBOM is missing the ${entry.name} -> ${name} dependency edge`,
        );
      }
    }
  }
}

async function main() {
  const [mode, sbomPath] = process.argv.slice(2);
  if (!sbomPath || !["--installed", "--locked"].includes(mode)) {
    throw new Error(
      "Usage: node scripts/verify-frontend-sbom.mjs --installed|--locked SBOM",
    );
  }
  const info = await lstat(sbomPath);
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxSbomBytes) {
    throw new Error("Invalid frontend SBOM file");
  }
  const sbom = JSON.parse(await readFile(sbomPath, "utf8"));
  const project = path.dirname(defaultLockPath);
  const lock = JSON.parse(await readFile(defaultLockPath, "utf8"));
  const root = JSON.parse(
    await readFile(path.join(project, "package.json"), "utf8"),
  );
  const locked = lockedInventory(lock);
  const installed =
    mode === "--installed"
      ? await verifyInstalledPackages(lock, project, { includeInventory: true })
      : undefined;
  const required = installed || locked.filter((entry) => !entry.optional);
  const count = verifyFrontendSbom(sbom, root, required, installed || locked);
  if (installed) verifyInstalledSbomEdges(sbom, root, installed);
  console.log(`Verified ${count} frontend SBOM component identities`);
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  await main();
}
