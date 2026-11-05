import { lstat, readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { activeNpmDirectory } from "./verify-npm-registry.mjs";

const registry = "https://registry.npmjs.org";
const severities = new Set(["info", "low", "moderate", "high", "critical"]);

export async function inventoryNpm(npmDirectory) {
  const npm = JSON.parse(
    await readFile(path.join(npmDirectory, "package.json"), "utf8"),
  );
  if (npm.name !== "npm" || typeof npm.version !== "string") {
    throw new Error("Invalid bundled npm package metadata");
  }
  const packages = new Map([[npm.name, new Set([npm.version])]]);

  async function visitPackage(directory) {
    const metadata = JSON.parse(
      await readFile(path.join(directory, "package.json"), "utf8"),
    );
    if (
      typeof metadata.name !== "string" ||
      typeof metadata.version !== "string"
    ) {
      throw new Error(`Invalid bundled package metadata in ${directory}`);
    }
    const versions = packages.get(metadata.name) || new Set();
    versions.add(metadata.version);
    packages.set(metadata.name, versions);
    await visitModules(path.join(directory, "node_modules"), true);
  }

  async function visitModules(directory, optional = false) {
    let stat;
    try {
      stat = await lstat(directory);
    } catch (error) {
      if (optional && error.code === "ENOENT") return;
      throw error;
    }
    if (!stat.isDirectory() || stat.isSymbolicLink()) {
      throw new Error(`Unexpected bundled node_modules path: ${directory}`);
    }
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      if (entry.name.startsWith(".")) continue;
      if (entry.isSymbolicLink())
        throw new Error(`Unexpected bundled package link: ${entry.name}`);
      if (!entry.isDirectory()) continue;
      const child = path.join(directory, entry.name);
      if (entry.name.startsWith("@")) {
        for (const scoped of await readdir(child, { withFileTypes: true })) {
          if (scoped.isSymbolicLink())
            throw new Error(
              `Unexpected bundled package link: ${entry.name}/${scoped.name}`,
            );
          if (scoped.isDirectory())
            await visitPackage(path.join(child, scoped.name));
        }
      } else {
        await visitPackage(child);
      }
    }
  }

  await visitModules(path.join(npmDirectory, "node_modules"));
  return {
    version: npm.version,
    packages: Object.fromEntries(
      [...packages]
        .sort(([a], [b]) => a.localeCompare(b, "en"))
        .map(([name, versions]) => [name, [...versions].sort()]),
    ),
  };
}

export function inventoryNode(versions = process.versions) {
  if (
    typeof versions?.node !== "string" ||
    typeof versions.undici !== "string" ||
    !/^\d+\.\d+\.\d+$/.test(versions.node) ||
    !/^\d+\.\d+\.\d+$/.test(versions.undici)
  ) {
    throw new Error("Cannot identify Node.js and its built-in undici version");
  }
  return { version: versions.node, packages: { undici: [versions.undici] } };
}

export async function findAdvisories(packages, fetcher = fetch) {
  const deadline = AbortSignal.timeout(60_000);
  let response;
  for (let attempt = 0; attempt < 3; attempt++) {
    try {
      response = await fetcher(
        `${registry}/-/npm/v1/security/advisories/bulk`,
        {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify(packages),
          redirect: "error",
          signal: AbortSignal.any([deadline, AbortSignal.timeout(20_000)]),
        },
      );
    } catch (error) {
      if (attempt === 2 || deadline.aborted) throw error;
      await delay(250 * 2 ** attempt, undefined, { signal: deadline });
      continue;
    }
    if (response.ok) break;
    if ((response.status === 429 || response.status >= 500) && attempt < 2) {
      await delay(250 * 2 ** attempt, undefined, { signal: deadline });
      continue;
    }
    throw new Error(`npm advisory service returned HTTP ${response.status}`);
  }
  const data = await response.json();
  if (!data || typeof data !== "object" || Array.isArray(data)) {
    throw new Error("Invalid npm advisory response");
  }
  const issues = [];
  for (const [name, advisories] of Object.entries(data)) {
    if (!Object.hasOwn(packages, name)) {
      throw new Error(`Unexpected advisory package: ${JSON.stringify(name)}`);
    }
    if (!Array.isArray(advisories))
      throw new Error(`Invalid advisories for ${name}`);
    for (const advisory of advisories) {
      if (
        typeof advisory?.url !== "string" ||
        typeof advisory.vulnerable_versions !== "string" ||
        !severities.has(advisory.severity)
      ) {
        throw new Error(`Invalid advisory for ${name}`);
      }
      issues.push({
        name,
        installed: packages[name],
        affected: advisory.vulnerable_versions,
        severity: advisory.severity,
        url: advisory.url,
      });
    }
  }
  return issues.sort(
    (a, b) =>
      a.name.localeCompare(b.name, "en") || a.url.localeCompare(b.url, "en"),
  );
}

function unreviewedToolchainIssues(report) {
  const unreviewed = [];
  for (const component of ["npm", "node"]) {
    const toolchain = report?.[component];
    if (
      typeof toolchain?.version !== "string" ||
      !Array.isArray(toolchain.issues)
    ) {
      throw new Error(`Invalid ${component} toolchain advisory report`);
    }
    for (const issue of toolchain.issues) {
      unreviewed.push({ component, ...issue });
    }
  }
  return unreviewed;
}

export function enforceToolchainPolicy(report) {
  const unreviewed = unreviewedToolchainIssues(report);
  if (unreviewed.length) {
    throw new Error(
      `${unreviewed.length} unreviewed JavaScript toolchain advisory matches require review`,
    );
  }
}

export async function auditToolchains(
  npmDirectory,
  versions = process.versions,
  fetcher = fetch,
) {
  const npm = await inventoryNpm(npmDirectory);
  const node = inventoryNode(versions);
  const [npmIssues, nodeIssues] = await Promise.all([
    findAdvisories(npm.packages, fetcher),
    findAdvisories(node.packages, fetcher),
  ]);
  return {
    npm: { ...npm, issues: npmIssues },
    node: { ...node, issues: nodeIssues },
  };
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const report = await auditToolchains(activeNpmDirectory());
  const { npm, node } = report;
  const unreviewed = unreviewedToolchainIssues(report);
  console.log(
    `npm ${npm.version}: checked ${Object.keys(npm.packages).length} bundled package names; ${npm.issues.length} advisory matches`,
  );
  console.log(
    `Node.js ${node.version}: built-in undici ${node.packages.undici[0]}; ${node.issues.length} advisory matches`,
  );
  if (npm.issues.length || node.issues.length) {
    console.log(
      "Matches are based on package versions; reachability has not been assessed.",
    );
    for (const [component, issues] of [
      ["npm", npm.issues],
      ["Node.js", node.issues],
    ]) {
      for (const issue of issues)
        console.log(JSON.stringify({ component, ...issue }));
    }
    if (process.env.GITHUB_ACTIONS === "true" && unreviewed.length) {
      console.log(
        "::error title=JavaScript toolchain::Bundled npm or Node.js dependencies have unreviewed advisory matches. See this step for details.",
      );
    }
  }
  enforceToolchainPolicy(report);
}
