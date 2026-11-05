import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { activeNpmDirectory } from "./verify-npm-registry.mjs";

test("verified npm preserves the registry TUF delegation boundary", (t) => {
  if (!process.env.STEAM_RELEASER_NPM_DIR) {
    if (process.env.GITHUB_ACTIONS === "true")
      throw new Error("Verified npm is not active");
    t.skip("verified npm is not active");
    return;
  }
  const npmDirectory = activeNpmDirectory();
  const requireFromNpm = createRequire(path.join(npmDirectory, "package.json"));
  const { DelegatedRole } = requireFromNpm(
    path.join(npmDirectory, "node_modules/@tufjs/models/dist/role.js"),
  );
  const role = new DelegatedRole({
    keyIDs: ["test-key"],
    threshold: 1,
    name: "registry.npmjs.org",
    terminating: true,
    paths: ["registry.npmjs.org/*"],
  });
  for (const [target, expected] of [
    ["registry.npmjs.org/keys.json", true],
    ["registry.npmjs.org/keys.json/extra", false],
    ["registry.npmjs.org.evil/keys.json", false],
    ["REGISTRY.NPMJS.ORG/keys.json", false],
    ["other/keys.json", false],
    ["registry.npmjs.org/.keys.json", false],
  ])
    assert.equal(role.isDelegatedPath(target), expected, target);
  for (const pattern of [
    "other/*",
    "registry.npmjs.org/*.txt",
    "registry.npmjs.org/KEYS.json",
  ]) {
    const other = new DelegatedRole({
      keyIDs: ["test-key"],
      threshold: 1,
      name: "other",
      terminating: true,
      paths: [pattern],
    });
    assert.equal(
      other.isDelegatedPath("registry.npmjs.org/keys.json"),
      false,
      pattern,
    );
  }
});

test("verified npm maps workspace patterns with its replacement dependencies", async (t) => {
  if (!process.env.STEAM_RELEASER_NPM_DIR) {
    if (process.env.GITHUB_ACTIONS === "true")
      throw new Error("Verified npm is not active");
    t.skip("verified npm is not active");
    return;
  }
  const npmDirectory = activeNpmDirectory();
  const requireFromNpm = createRequire(path.join(npmDirectory, "package.json"));
  const mapWorkspaces = requireFromNpm("@npmcli/map-workspaces");
  const root = mkdtempSync(
    path.join(os.tmpdir(), "steam-releaser-workspaces with spaces-"),
  );
  t.after(() => rmSync(root, { recursive: true, force: true }));
  for (const [directory, name] of [
    ["packages/alpha", "alpha"],
    ["packages/beta", "beta"],
    ["packages/nested/deep", "deep"],
    ["extra/gamma", "gamma"],
  ]) {
    const target = path.join(root, directory);
    mkdirSync(target, { recursive: true });
    writeFileSync(
      path.join(target, "package.json"),
      JSON.stringify({ name, version: "1.0.0" }),
    );
  }
  for (const { patterns, expected } of [
    { patterns: ["packages/*"], expected: ["alpha", "beta"] },
    { patterns: ["packages/*", "!packages/beta"], expected: ["alpha"] },
    {
      patterns: ["packages/*", "!packages/beta", "packages/beta"],
      expected: ["alpha", "beta"],
    },
    {
      patterns: ["packages/**", "!packages/beta", "extra/*"],
      expected: ["alpha", "deep", "gamma"],
    },
    {
      patterns: { packages: ["extra/*", "packages/alpha"] },
      expected: ["gamma", "alpha"],
    },
  ]) {
    const actual = await mapWorkspaces({
      cwd: root,
      pkg: { name: "workspace-root", version: "1.0.0", workspaces: patterns },
    });
    assert.deepEqual([...actual.keys()], expected);
  }
});
