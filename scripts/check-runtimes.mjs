import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

function versionParts(version) {
  if (!/^\d+\.\d+\.\d+$/.test(version))
    throw new Error(`Invalid version: ${version}`);
  return version.split(".").map(Number);
}

function newest(versions) {
  return versions.sort((a, b) => {
    const left = versionParts(a);
    const right = versionParts(b);
    return right[0] - left[0] || right[1] - left[1] || right[2] - left[2];
  })[0];
}

function validDate(value) {
  if (typeof value !== "string" || !/^\d{4}-\d{2}-\d{2}$/.test(value))
    return false;
  const parsed = new Date(`${value}T00:00:00Z`);
  return (
    !Number.isNaN(parsed.getTime()) &&
    parsed.toISOString().slice(0, 10) === value
  );
}

function checkNodeSupport(major, schedule, asOf) {
  const release =
    schedule && Object.hasOwn(schedule, `v${major}`) && schedule[`v${major}`];
  if (
    !validDate(asOf) ||
    !release ||
    !validDate(release.lts) ||
    !validDate(release.end) ||
    release.lts >= release.end
  ) {
    throw new Error(`Invalid or missing Node ${major} support schedule`);
  }
  if (asOf < release.lts || asOf >= release.end) {
    throw new Error(
      `Node ${major} is outside its LTS support window (${release.lts} up to ${release.end})`,
    );
  }
}

function latestNodeForMajor(nodeReleases, major) {
  return newest(
    nodeReleases
      .filter(
        (release) => release.lts && /^v\d+\.\d+\.\d+$/.test(release.version),
      )
      .map((release) => release.version.slice(1))
      .filter((version) => versionParts(version)[0] === major),
  );
}

export function checkRuntimes(
  nodeVersion,
  goVersion,
  nodeReleases,
  goReleases,
  nodeSchedule,
  asOf = new Date().toISOString().slice(0, 10),
) {
  const [nodeMajor] = versionParts(nodeVersion);
  const [goMajor, goMinor] = versionParts(goVersion);
  checkNodeSupport(nodeMajor, nodeSchedule, asOf);
  const latestNode = latestNodeForMajor(nodeReleases, nodeMajor);
  const latestGo = newest(
    goReleases
      .filter(
        (release) =>
          release.stable && /^go\d+\.\d+\.\d+$/.test(release.version),
      )
      .map((release) => release.version.slice(2))
      .filter((version) => {
        const [major, minor] = versionParts(version);
        return major === goMajor && minor === goMinor;
      }),
  );
  if (!latestNode || latestNode !== nodeVersion) {
    throw new Error(
      `Pinned Node ${nodeVersion} is not the latest supported LTS patch for its major version (${latestNode || "unsupported"})`,
    );
  }
  if (!latestGo || latestGo !== goVersion) {
    throw new Error(
      `Pinned Go ${goVersion} is not the latest supported patch for its minor version (${latestGo || "unsupported"})`,
    );
  }
}

// Limit local frontend installs to explicitly reviewed, currently supported
// LTS majors and keep their minimum patches current as releases are published.
export function checkFrontendNodeEngines(
  range,
  lockedRange,
  nodeVersion,
  nodeReleases,
  nodeSchedule,
  asOf = new Date().toISOString().slice(0, 10),
) {
  if (typeof range !== "string" || range !== lockedRange) {
    throw new Error("Frontend Node engine does not match the lockfile");
  }
  const majors = new Set();
  let includesPinned = false;
  for (const raw of range.split("||")) {
    const match = /^\^([1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.exec(raw.trim());
    if (!match)
      throw new Error("Frontend Node engine must use bounded LTS-major ranges");
    const version = `${match[1]}.${match[2]}.${match[3]}`;
    const major = Number(match[1]);
    if (majors.has(major))
      throw new Error(`Duplicate Node major ${major} in frontend engines`);
    majors.add(major);
    checkNodeSupport(major, nodeSchedule, asOf);
    const latest = latestNodeForMajor(nodeReleases, major);
    if (!latest || version !== latest) {
      throw new Error(
        `Frontend Node ${major} minimum ${version} is not its latest LTS patch (${latest || "missing"})`,
      );
    }
    if (version === nodeVersion) includesPinned = true;
  }
  if (!includesPinned)
    throw new Error("Frontend Node engines do not include the pinned runtime");
}

export function checkNpmRelease(pinned, metadata) {
  versionParts(pinned);
  if (metadata?.name !== "npm" || typeof metadata.version !== "string") {
    throw new Error("Unexpected npm release metadata");
  }
  versionParts(metadata.version);
  if (metadata.version !== pinned) {
    throw new Error(
      `Pinned npm ${pinned} is not the latest release (${metadata.version})`,
    );
  }
}

export function checkNpmReplacementReleases(pins, metadata) {
  if (
    !Array.isArray(pins) ||
    !Array.isArray(metadata) ||
    pins.length !== metadata.length
  ) {
    throw new Error("Invalid npm replacement release metadata");
  }
  const names = new Set();
  for (let index = 0; index < pins.length; index++) {
    const pin = pins[index];
    const latest = metadata[index];
    if (
      typeof pin?.name !== "string" ||
      !pin.name ||
      names.has(pin.name) ||
      latest?.name !== pin.name ||
      typeof latest.version !== "string"
    ) {
      throw new Error("Unexpected npm replacement release metadata");
    }
    names.add(pin.name);
    versionParts(pin.version);
    versionParts(latest.version);
    if (pin.version !== latest.version) {
      throw new Error(
        `Pinned npm replacement ${pin.name}@${pin.version} is not the latest release (${latest.version})`,
      );
    }
  }
}

async function fetchJSON(url) {
  const response = await fetch(url, { signal: AbortSignal.timeout(15000) });
  if (!response.ok)
    throw new Error(`Could not check ${url}: HTTP ${response.status}`);
  const value = await response.json();
  if (!Array.isArray(value))
    throw new Error(`Unexpected release index at ${url}`);
  return value;
}

async function fetchLatestPackage(name) {
  const response = await fetch(
    `https://registry.npmjs.org/${encodeURIComponent(name)}/latest`,
    {
      redirect: "error",
      signal: AbortSignal.timeout(15_000),
    },
  );
  if (!response.ok)
    throw new Error(`Could not check latest ${name}: HTTP ${response.status}`);
  return response.json();
}

async function fetchNodeSchedule() {
  const response = await fetch(
    "https://raw.githubusercontent.com/nodejs/Release/main/schedule.json",
    {
      redirect: "error",
      signal: AbortSignal.timeout(15_000),
    },
  );
  if (!response.ok)
    throw new Error(
      `Could not check Node support schedule: HTTP ${response.status}`,
    );
  const value = await response.json();
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Unexpected Node support schedule");
  }
  return value;
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const nodeVersion = (
    await readFile(path.join(root, ".nvmrc"), "utf8")
  ).trim();
  const goMod = await readFile(path.join(root, "go.mod"), "utf8");
  const goVersion = /^go\s+(\d+\.\d+\.\d+)\s*$/m.exec(goMod)?.[1];
  if (!goVersion)
    throw new Error("go.mod must declare an exact Go patch version");
  const npmPin = JSON.parse(
    await readFile(path.join(root, "scripts", "npm-toolchain.json"), "utf8"),
  );
  const replacements = npmPin.replacements ?? [];
  if (
    !Array.isArray(replacements) ||
    replacements.some((pin) => typeof pin?.name !== "string" || !pin.name)
  ) {
    throw new Error("Invalid npm replacement pins");
  }
  const frontend = path.join(root, "cmd", "steam-releaser-desktop", "frontend");
  const manifest = JSON.parse(
    await readFile(path.join(frontend, "package.json"), "utf8"),
  );
  const lock = JSON.parse(
    await readFile(path.join(frontend, "package-lock.json"), "utf8"),
  );
  const [
    nodeReleases,
    goReleases,
    npmRelease,
    nodeSchedule,
    replacementReleases,
  ] = await Promise.all([
    fetchJSON("https://nodejs.org/dist/index.json"),
    fetchJSON("https://go.dev/dl/?mode=json"),
    fetchLatestPackage("npm"),
    fetchNodeSchedule(),
    Promise.all(replacements.map((pin) => fetchLatestPackage(pin.name))),
  ]);
  const asOf = new Date().toISOString().slice(0, 10);
  checkRuntimes(
    nodeVersion,
    goVersion,
    nodeReleases,
    goReleases,
    nodeSchedule,
    asOf,
  );
  checkFrontendNodeEngines(
    manifest.engines?.node,
    lock.packages?.[""]?.engines?.node,
    nodeVersion,
    nodeReleases,
    nodeSchedule,
    asOf,
  );
  checkNpmRelease(npmPin.version, npmRelease);
  checkNpmReplacementReleases(replacements, replacementReleases);
  console.log(
    `Current supported runtime patches: Node ${nodeVersion}, Go ${goVersion}, npm ${npmPin.version}; ${replacements.length} npm replacements are current`,
  );
}
