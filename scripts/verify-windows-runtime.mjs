import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const downloadPage =
  "https://developer.microsoft.com/en-us/microsoft-edge/webview2/";
const forbiddenMarkers = [
  "https://go.microsoft.com/fwlink/p/?LinkId=2124703",
  "webview2runtime.downloadBootstrapper",
  "webview2runtime.InstallUsingBootstrapper",
  "webview2runtime.InstallUsingEmbeddedBootstrapper",
];

export function verifyWindowsRuntime(buildInfo, executable, bootstrapper) {
  const lines = buildInfo.split(/\r?\n/).map((line) => line.trim());
  function field(name) {
    const prefix = `build\t${name}=`;
    const matches = lines.filter((line) => line.startsWith(prefix));
    if (matches.length !== 1)
      throw new Error(`Expected exactly one ${name} build setting`);
    return matches[0].slice(prefix.length);
  }
  if (field("GOOS") !== "windows" || field("GOARCH") !== "amd64") {
    throw new Error(
      "Desktop executable is not the expected Windows amd64 build",
    );
  }
  const tags = new Set(field("-tags").split(","));
  if (
    !tags.has("desktop") ||
    !tags.has("production") ||
    !tags.has("wv2runtime.browser") ||
    ["wv2runtime.download", "wv2runtime.embed", "wv2runtime.error"].some(
      (tag) => tags.has(tag),
    )
  ) {
    throw new Error("Windows build must use the WebView2 browser strategy");
  }
  if (
    !Buffer.isBuffer(executable) ||
    !Buffer.isBuffer(bootstrapper) ||
    !bootstrapper.length
  ) {
    throw new Error("Missing executable or Wails WebView2 bootstrapper bytes");
  }
  if (!executable.includes(Buffer.from(downloadPage))) {
    throw new Error(
      "Windows executable lacks the expected Microsoft download page",
    );
  }
  for (const marker of forbiddenMarkers) {
    if (executable.includes(Buffer.from(marker))) {
      throw new Error(
        `Windows executable contains an automatic installer marker: ${marker}`,
      );
    }
  }
  if (executable.includes(bootstrapper)) {
    throw new Error(
      "Windows executable contains the bundled WebView2 bootstrapper",
    );
  }
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  if (process.argv.length !== 5)
    throw new Error(
      "Usage: node scripts/verify-windows-runtime.mjs EXE BUILD_INFO BOOTSTRAPPER",
    );
  verifyWindowsRuntime(
    readFileSync(process.argv[3], "utf8"),
    readFileSync(process.argv[2]),
    readFileSync(process.argv[4]),
  );
  console.log("Verified the Windows WebView2 browser strategy and executable");
}
