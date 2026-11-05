import { readFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { activeNpmDirectory } from "./verify-npm-registry.mjs";

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const pin = JSON.parse(
  await readFile(path.join(scriptDirectory, "npm-toolchain.json"), "utf8"),
);
if (!process.env.STEAM_RELEASER_NPM_DIR)
  throw new Error("Verified npm has not been activated");
const metadata = JSON.parse(
  await readFile(path.join(activeNpmDirectory(), "package.json"), "utf8"),
);
const result = spawnSync("npm", ["--version"], {
  encoding: "utf8",
  shell: process.platform === "win32",
  timeout: 20_000,
});
if (
  metadata.name !== "npm" ||
  metadata.version !== pin.version ||
  result.error ||
  result.status !== 0 ||
  result.stdout.trim() !== pin.version
)
  throw new Error(`Active npm does not match the verified ${pin.version} pin`);
console.log(`Active npm matches the verified ${pin.version} pin`);
