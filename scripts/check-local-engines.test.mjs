import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  copyFileSync,
  existsSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { activeNpmDirectory } from "./verify-npm-registry.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const frontend = path.join(root, "cmd/steam-releaser-desktop/frontend");

test("frontend installation rejects unsupported Node and npm versions", (t) => {
  const npmDirectory = activeNpmDirectory();
  const cli = path.join(npmDirectory, "bin", "npm-cli.js");
  if (!existsSync(cli)) {
    t.skip(
      "npm installation not found beside Node or in STEAM_RELEASER_NPM_DIR",
    );
    return;
  }
  const directory = mkdtempSync(
    path.join(os.tmpdir(), "steam-releaser-engines-"),
  );
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  for (const name of ["package.json", "package-lock.json", ".npmrc"]) {
    copyFileSync(path.join(frontend, name), path.join(directory, name));
  }
  const project = JSON.parse(
    readFileSync(path.join(frontend, "package.json"), "utf8"),
  );
  const nodeVersion = `v${readFileSync(path.join(root, ".nvmrc"), "utf8").trim()}`;
  const npmVersion = JSON.parse(
    readFileSync(path.join(root, "scripts/npm-toolchain.json"), "utf8"),
  ).version;
  const npmPackage =
    'require(require("node:path").join(process.env.STEAM_RELEASER_NPM_DIR, "package.json"))';
  const cases = [
    ...["v22.23.4", "v24.22.2", "v26.13.0"].map((version) => ({
      name: `supported ${version}`,
      accepted: true,
      preload: `Object.defineProperty(process, "version", { value: ${JSON.stringify(version)} }); ${npmPackage}.version = ${JSON.stringify(npmVersion)};\n`,
    })),
    {
      name: "old Node",
      preload: `Object.defineProperty(process, "version", { value: "v24.17.0" }); ${npmPackage}.version = ${JSON.stringify(npmVersion)};\n`,
    },
    {
      name: "unreviewed Node major",
      preload: `Object.defineProperty(process, "version", { value: "v27.0.0" }); ${npmPackage}.version = ${JSON.stringify(npmVersion)};\n`,
    },
    {
      name: "stale Node 24 patch",
      preload: `Object.defineProperty(process, "version", { value: "v24.22.1" }); ${npmPackage}.version = ${JSON.stringify(npmVersion)};\n`,
    },
    {
      name: "stale Node 26 patch",
      preload: `Object.defineProperty(process, "version", { value: "v26.12.1" }); ${npmPackage}.version = ${JSON.stringify(npmVersion)};\n`,
    },
    {
      name: "old npm",
      preload: `Object.defineProperty(process, "version", { value: ${JSON.stringify(nodeVersion)} }); ${npmPackage}.version = "11.20.0";\n`,
    },
  ];
  for (const item of cases) {
    const preload = path.join(directory, "simulate-engine.cjs");
    writeFileSync(preload, item.preload);
    const result = spawnSync(
      process.execPath,
      [
        "--require",
        preload,
        cli,
        "ci",
        "--ignore-scripts",
        "--no-audit",
        "--dry-run",
      ],
      {
        cwd: directory,
        encoding: "utf8",
        timeout: 30000,
        env: {
          ...process.env,
          STEAM_RELEASER_NPM_DIR: npmDirectory,
          NODE_OPTIONS: "",
          npm_config_cache: path.join(directory, "cache"),
          npm_config_offline: "true",
        },
      },
    );
    assert.equal(result.error, undefined, `${item.name}: ${result.error}`);
    if (item.accepted) {
      assert.equal(result.status, 0, `${item.name}: ${result.stderr}`);
    } else {
      assert.notEqual(result.status, 0, `${item.name} was accepted`);
      assert.match(
        result.stderr,
        /EBADENGINE/,
        `${item.name}: ${result.stderr}`,
      );
      assert.ok(
        result.stderr.includes(`${project.name}@${project.version}`),
        result.stderr,
      );
    }
  }
});
