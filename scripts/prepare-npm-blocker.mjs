import { spawnSync } from "node:child_process";
import { chmod, mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const message = "Unexpected npm invocation during Wails build";

export async function prepareNpmBlocker(root) {
  if (!path.isAbsolute(root) || /[\r\n]/.test(root)) {
    throw new Error("Expected an absolute temporary directory");
  }
  const directory = path.join(root, "steam-releaser-block-npm");
  await mkdir(directory, { recursive: true });
  const unix = path.join(directory, "npm");
  await writeFile(unix, `#!/bin/sh\necho "${message}" >&2\nexit 1\n`, {
    mode: 0o755,
  });
  await chmod(unix, 0o755);
  await writeFile(
    path.join(directory, "npm.cmd"),
    `@echo off\r\necho ${message} 1>&2\r\nexit /b 1\r\n`,
  );
  return directory;
}

export function checkNpmBlocker(env = process.env) {
  const result = spawnSync("npm", ["--version"], {
    encoding: "utf8",
    env,
    shell: process.platform === "win32",
    timeout: 10_000,
  });
  if (
    result.error ||
    result.status !== 1 ||
    !result.stderr?.includes(message)
  ) {
    throw new Error("The Wails build did not resolve the npm blocker");
  }
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  if (process.argv.length !== 3) {
    throw new Error(
      "Usage: node scripts/prepare-npm-blocker.mjs TEMP_DIR|--check",
    );
  }
  if (process.argv[2] === "--check") {
    checkNpmBlocker();
    console.log("Verified npm is blocked during the Wails build");
  } else {
    await prepareNpmBlocker(process.argv[2]);
  }
}
