import { createHash } from "node:crypto";
import { constants } from "node:fs";
import {
  copyFile,
  lstat,
  mkdir,
  opendir,
  readFile,
  realpath,
  rm,
  writeFile,
} from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { verifyFrontendDist } from "./verify-frontend-dist.mjs";

const maxMaps = 128;
const maxMapBytes = 16 * 1024 * 1024;
const maxTotalBytes = 64 * 1024 * 1024;
const maxSources = 10000;
const maxEntries = 4096;
const manifestName = "manifest.json";
const hash = (data) => createHash("sha256").update(data).digest("hex");

function contained(root, target) {
  const relative = path.relative(root, target);
  return (
    relative !== "" &&
    relative !== ".." &&
    !relative.startsWith(`..${path.sep}`) &&
    !path.isAbsolute(relative)
  );
}

function safeAssetPath(name) {
  return (
    typeof name === "string" &&
    name.length <= 200 &&
    name.split("/").every((part) => /^[A-Za-z0-9_][A-Za-z0-9._-]*$/.test(part))
  );
}

async function filesBelow(directory, parts = [], state = { entries: 0 }) {
  const result = [];
  const entries = await opendir(path.join(directory, ...parts));
  for await (const entry of entries) {
    if (++state.entries > maxEntries) {
      throw new Error("Frontend source-map artifact has too many entries");
    }
    const next = [...parts, entry.name];
    const relative = next.join("/");
    if (!safeAssetPath(relative) || next.length > 10) {
      throw new Error(`Unsafe source-map artifact path: ${relative}`);
    }
    const info = await lstat(path.join(directory, ...next));
    if (info.isSymbolicLink())
      throw new Error(`Source-map artifact contains a link: ${relative}`);
    if (info.isDirectory())
      result.push(...(await filesBelow(directory, next, state)));
    else if (info.isFile()) result.push(relative);
    else throw new Error(`Unsupported source-map artifact entry: ${relative}`);
  }
  return result;
}

async function regularFile(filename, maxBytes, expectedBytes) {
  const info = await lstat(filename);
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxBytes) {
    throw new Error(`Invalid or oversized source-map input: ${filename}`);
  }
  if (expectedBytes !== undefined && info.size !== expectedBytes) {
    throw new Error(
      `Mapped source differs from the checked input: ${filename}`,
    );
  }
  return readFile(filename);
}

async function verifyMap(frontendRoot, distRoot, asset, mapBytes) {
  let map;
  try {
    map = JSON.parse(mapBytes.toString("utf8"));
  } catch {
    throw new Error(`Invalid source map for ${asset}`);
  }
  if (
    map?.version !== 3 ||
    map.file !== path.posix.basename(asset) ||
    (map.sourceRoot !== undefined && map.sourceRoot !== "") ||
    !Array.isArray(map.sources) ||
    !Array.isArray(map.sourcesContent) ||
    !map.sources.length ||
    map.sources.length > maxSources ||
    map.sources.length !== map.sourcesContent.length ||
    typeof map.mappings !== "string" ||
    !Array.isArray(map.names)
  ) {
    throw new Error(`Unsupported source-map structure for ${asset}`);
  }
  const realRoot = await realpath(frontendRoot);
  const virtualMap = path.join(distRoot, ...`${asset}.map`.split("/"));
  let total = 0;
  const seen = new Set();
  for (let i = 0; i < map.sources.length; i++) {
    const source = map.sources[i];
    const content = map.sourcesContent[i];
    if (
      typeof source !== "string" ||
      !source ||
      source.length > 4096 ||
      /[\\\x00-\x1f\x7f]/.test(source) ||
      source.startsWith("/") ||
      /^[A-Za-z]:/.test(source) ||
      source.includes("://") ||
      typeof content !== "string"
    ) {
      throw new Error(`Unsafe or missing mapped source in ${asset}`);
    }
    const filename = path.resolve(path.dirname(virtualMap), source);
    const sourceKey =
      process.platform === "win32" ? filename.toLowerCase() : filename;
    if (!contained(frontendRoot, filename) || seen.has(sourceKey)) {
      throw new Error(
        `Mapped source escapes or repeats in ${asset}: ${source}`,
      );
    }
    seen.add(sourceKey);
    const bytes = Buffer.from(content, "utf8");
    total += bytes.length;
    if (total > maxTotalBytes)
      throw new Error(`Mapped source contents exceed the limit in ${asset}`);
    const actual = await regularFile(filename, maxTotalBytes, bytes.length);
    if (
      !contained(realRoot, await realpath(filename)) ||
      !actual.equals(bytes)
    ) {
      throw new Error(
        `Mapped source differs from the checked input: ${source}`,
      );
    }
  }
  return map.sources.length;
}

function validManifest(manifest) {
  if (
    manifest?.format !== 1 ||
    !Array.isArray(manifest.maps) ||
    !manifest.maps.length ||
    manifest.maps.length > maxMaps
  ) {
    throw new Error("Invalid frontend source-map manifest");
  }
  const seen = new Set();
  for (const item of manifest.maps) {
    if (
      !safeAssetPath(item?.asset) ||
      !item.asset.endsWith(".js") ||
      item.map !== `${item.asset}.map` ||
      !/^[a-f0-9]{64}$/.test(item.assetSha256) ||
      !/^[a-f0-9]{64}$/.test(item.mapSha256) ||
      seen.has(item.asset.toLowerCase())
    ) {
      throw new Error("Invalid frontend source-map manifest entry");
    }
    seen.add(item.asset.toLowerCase());
  }
}

export async function verifyFrontendSourceMaps(
  frontendDirectory,
  mapsDirectory,
) {
  const frontendRoot = path.resolve(frontendDirectory);
  const distRoot = path.join(frontendRoot, "dist");
  const mapsRoot = path.resolve(mapsDirectory);
  const rootInfo = await lstat(mapsRoot);
  if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink()) {
    throw new Error("Frontend source maps must be a directory");
  }
  await verifyFrontendDist(distRoot);
  const distFiles = await filesBelow(distRoot);
  if (distFiles.some((file) => file.endsWith(".map"))) {
    throw new Error("Source maps must not remain in the frontend artifact");
  }
  const raw = await regularFile(path.join(mapsRoot, manifestName), 1024 * 1024);
  let manifest;
  try {
    manifest = JSON.parse(raw.toString("utf8"));
  } catch {
    throw new Error("Invalid frontend source-map manifest");
  }
  validManifest(manifest);
  const expectedAssets = distFiles
    .filter((file) => file.endsWith(".js"))
    .sort();
  const actualAssets = manifest.maps.map((item) => item.asset).sort();
  if (JSON.stringify(expectedAssets) !== JSON.stringify(actualAssets)) {
    throw new Error("Frontend source maps do not cover the JavaScript assets");
  }
  const mapFiles = (await filesBelow(mapsRoot)).sort();
  const expectedFiles = [
    manifestName,
    ...manifest.maps.map((item) => item.map),
  ].sort();
  if (JSON.stringify(mapFiles) !== JSON.stringify(expectedFiles)) {
    throw new Error("Unexpected or missing frontend source-map artifact files");
  }
  let sources = 0;
  let totalBytes = 0;
  for (const item of manifest.maps) {
    const asset = await regularFile(
      path.join(distRoot, ...item.asset.split("/")),
      maxTotalBytes,
    );
    const mapBytes = await regularFile(
      path.join(mapsRoot, ...item.map.split("/")),
      maxMapBytes,
    );
    totalBytes += mapBytes.length;
    if (
      totalBytes > maxTotalBytes ||
      hash(asset) !== item.assetSha256 ||
      hash(mapBytes) !== item.mapSha256
    ) {
      throw new Error(`Frontend source-map checksum mismatch: ${item.asset}`);
    }
    sources += await verifyMap(frontendRoot, distRoot, item.asset, mapBytes);
  }
  return { maps: manifest.maps.length, sources };
}

export async function collectFrontendSourceMaps(
  frontendDirectory,
  mapsDirectory,
) {
  const frontendRoot = path.resolve(frontendDirectory);
  const distRoot = path.join(frontendRoot, "dist");
  const mapsRoot = path.resolve(mapsDirectory);
  if (mapsRoot === frontendRoot || contained(frontendRoot, mapsRoot)) {
    throw new Error("Source-map output must be outside the frontend directory");
  }
  await verifyFrontendDist(distRoot);
  const files = await filesBelow(distRoot);
  const assets = files.filter((file) => file.endsWith(".js")).sort();
  const maps = files.filter((file) => file.endsWith(".map")).sort();
  if (
    !assets.length ||
    assets.length > maxMaps ||
    JSON.stringify(maps) !==
      JSON.stringify(assets.map((asset) => `${asset}.map`).sort())
  ) {
    throw new Error(
      "Expected one source map for each frontend JavaScript asset",
    );
  }
  const records = [];
  let totalBytes = 0;
  for (const asset of assets) {
    const map = `${asset}.map`;
    const assetBytes = await regularFile(
      path.join(distRoot, ...asset.split("/")),
      maxTotalBytes,
    );
    const mapBytes = await regularFile(
      path.join(distRoot, ...map.split("/")),
      maxMapBytes,
    );
    totalBytes += mapBytes.length;
    if (totalBytes > maxTotalBytes)
      throw new Error("Frontend source maps exceed the limit");
    await verifyMap(frontendRoot, distRoot, asset, mapBytes);
    records.push({
      asset,
      map,
      assetSha256: hash(assetBytes),
      mapSha256: hash(mapBytes),
    });
  }
  await mkdir(mapsRoot);
  for (const item of records) {
    const destination = path.join(mapsRoot, ...item.map.split("/"));
    await mkdir(path.dirname(destination), { recursive: true });
    await copyFile(
      path.join(distRoot, ...item.map.split("/")),
      destination,
      constants.COPYFILE_EXCL,
    );
  }
  await writeFile(
    path.join(mapsRoot, manifestName),
    `${JSON.stringify({ format: 1, maps: records }, null, 2)}\n`,
    { flag: "wx" },
  );
  for (const item of records)
    await rm(path.join(distRoot, ...item.map.split("/")));
  return verifyFrontendSourceMaps(frontendRoot, mapsRoot);
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const [, , command, frontend, maps] = process.argv;
  if (process.argv.length !== 5 || !["collect", "verify"].includes(command)) {
    throw new Error(
      "Usage: node scripts/frontend-source-maps.mjs collect|verify FRONTEND MAPS_DIRECTORY",
    );
  }
  const result = await (
    command === "collect" ? collectFrontendSourceMaps : verifyFrontendSourceMaps
  )(frontend, maps);
  console.log(
    `Verified ${result.maps} frontend source maps with ${result.sources} checked source files`,
  );
}
