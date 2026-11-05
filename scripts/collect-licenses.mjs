import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  goNoticeDirectories,
  nestedGoNotices,
  noticeFiles,
} from "./go-package-notices.mjs";
import {
  frontendBuildToolLocations,
  requireRolldownThirdPartyNotice,
} from "./npm-build-notices.mjs";
import { verifyInstalledPackages } from "./verify-installed-npm.mjs";

// Collect notices on the target platform, after npm ci and the frontend build.
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const frontend = path.join(root, "cmd/steam-releaser-desktop/frontend");
const [output, ...moduleFiles] = process.argv.slice(2);
if (!output) {
  throw new Error(
    "Usage: node scripts/collect-licenses.mjs OUTPUT_FILE [GO_MODULES_FILE...]",
  );
}

const licenseName = /^(?:licen[cs]e|copying)(?:$|[._-])/i;
const sections = [
  "Third-party notices for Steam Releaser",
  "",
  "This inventory includes the Go production dependency graph for this platform,",
  "the Go standard library, npm production dependencies, and selected build tools",
  "whose output may be included in the frontend. Some listed packages may be",
  "omitted from the final bundle by platform selection or tree shaking.",
  "Project source and asset notices are also provided in the LICENSES directory.",
  "",
];

function appendNotices(label, files) {
  sections.push(`\n${"=".repeat(78)}\n${label}\n${"=".repeat(78)}`);
  for (const file of files) {
    sections.push(
      `\n--- ${file.name} ---\n${readFileSync(file.path, "utf8").trimEnd()}\n`,
    );
  }
}

function addNotices(label, directory, fallback) {
  let files = noticeFiles(directory).map((name) => ({
    name,
    path: path.join(directory, name),
  }));
  if (!files.some((file) => licenseName.test(file.name))) {
    if (!fallback)
      throw new Error(`No license file for ${label} in ${directory}`);
    files = [...files, { name: path.basename(fallback), path: fallback }];
  }
  appendNotices(label, files);
}

function go(...args) {
  return execFileSync("go", args, {
    cwd: root,
    encoding: "utf8",
    maxBuffer: 16 * 1024 * 1024,
  });
}

addNotices("Go standard library", go("env", "GOROOT").trim());

const targetOS =
  process.env.GOOS ||
  (process.platform === "win32" ? "windows" : process.platform);
const tags =
  targetOS === "linux"
    ? "production,desktop,webkit2_41"
    : targetOS === "windows"
      ? "production,desktop,wv2runtime.browser"
      : "production,desktop";
const template =
  "{{if .Module}}{{if not .Module.Main}}{{.Module.Path}}\t{{.Module.Version}}\t{{.Module.Dir}}\t{{.Dir}}{{range .EmbedFiles}}\t{{.}}{{end}}{{end}}{{end}}";
const modules = new Map();
for (const line of go(
  "list",
  "-deps",
  `-tags=${tags}`,
  "-f",
  template,
  "./cmd/steam-releaser",
  "./cmd/steam-releaser-desktop",
).split(/\r?\n/)) {
  if (!line) continue;
  const fields = line.split("\t");
  const [name, version, directory, packageDirectory, ...embedFiles] = fields;
  if (!name || !version || !directory || !packageDirectory)
    throw new Error(`Invalid Go module entry: ${line}`);
  const key = `${name}@${version}`;
  if (modules.has(key) && modules.get(key).directory !== directory) {
    throw new Error(`Conflicting Go module directories for ${key}`);
  }
  if (!modules.has(key)) modules.set(key, { directory, packages: new Set() });
  for (const sourceDirectory of goNoticeDirectories(
    packageDirectory,
    embedFiles,
  )) {
    modules.get(key).packages.add(sourceDirectory);
  }
}
if (modules.size === 0) throw new Error("Go dependency list is empty");
for (const file of moduleFiles) {
  const lines = readFileSync(file, "utf8")
    .split(/\r?\n/)
    .map((line) => line.trimStart().split("\t"));
  if (
    !lines.some((fields) => fields[0] === "path") ||
    !lines.some((fields) => fields[0] === "mod")
  ) {
    throw new Error(`Missing Go build information in ${file}`);
  }
  for (const fields of lines) {
    if (fields[0] !== "dep") continue;
    const key = `${fields[1]}@${fields[2]}`;
    if (!modules.has(key))
      throw new Error(`No collected Go notices for built module ${key}`);
  }
}
for (const [name, { directory, packages }] of [...modules].sort(([a], [b]) =>
  a.localeCompare(b, "en"),
)) {
  addNotices(`Go module: ${name}`, directory);
  for (const nested of nestedGoNotices(directory, packages)) {
    appendNotices(
      `Go module: ${name} (${nested.relative})`,
      nested.files.map((file) => ({
        name: file,
        path: path.join(nested.directory, file),
      })),
    );
  }
}

const lock = JSON.parse(
  readFileSync(path.join(frontend, "package-lock.json"), "utf8"),
);
// Do not let forged license or development-only metadata hide a production
// package from the notice and license review below.
await verifyInstalledPackages(lock, frontend, { allowBuildCaches: true });
const acceptedNpmLicenses = new Set([
  "0BSD",
  "Apache-2.0",
  "ISC",
  "MIT",
  "OFL-1.1",
]);
const includedBuildTools = frontendBuildToolLocations(frontend, lock);
let npmCount = 0;
for (const [relative, metadata] of Object.entries(lock.packages).sort(
  ([a], [b]) => a.localeCompare(b, "en"),
)) {
  if (
    !relative.startsWith("node_modules/") ||
    (metadata.dev && !includedBuildTools.has(relative))
  )
    continue;
  const directory = path.resolve(frontend, relative);
  if (!existsSync(directory)) {
    if (metadata.optional) continue;
    throw new Error(
      `Missing npm package ${relative}; run npm ci before collecting notices`,
    );
  }
  const installed = JSON.parse(
    readFileSync(path.join(directory, "package.json"), "utf8"),
  );
  if (installed.version !== metadata.version) {
    throw new Error(
      `Installed ${relative} does not match package-lock.json; run npm ci`,
    );
  }
  if (!acceptedNpmLicenses.has(metadata.license)) {
    throw new Error(
      `Review the license for ${relative}: ${metadata.license || "not specified"}`,
    );
  }
  const label = `npm package: ${installed.name}@${metadata.version} (${metadata.license})`;
  if (includedBuildTools.get(relative) === "rolldown")
    requireRolldownThirdPartyNotice(directory);
  // The 2.3.8 npm tarball omits its MIT license; retain the upstream notice locally.
  const fallback =
    installed.name === "react-remove-scroll-bar" && metadata.version === "2.3.8"
      ? path.join(root, "LICENSES/react-remove-scroll-bar-MIT.txt")
      : undefined;
  addNotices(label, directory, fallback);
  npmCount++;
}
if (npmCount === 0) throw new Error("npm dependency list is empty");

writeFileSync(output, `${sections.join("\n")}\n`);
console.log(
  `Wrote notices for ${modules.size} Go modules and ${npmCount} npm packages to ${output}`,
);
