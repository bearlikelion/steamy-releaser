import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  mkdirSync,
  mkdtempSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import {
  auditToolchains,
  enforceToolchainPolicy,
  findAdvisories,
  inventoryNode,
  inventoryNpm,
} from "./audit-npm-toolchain.mjs";

function writePackage(directory, name, version) {
  mkdirSync(directory, { recursive: true });
  writeFileSync(
    path.join(directory, "package.json"),
    JSON.stringify({ name, version }),
  );
}

test("inventories npm and its bundled scoped and nested packages", async (t) => {
  const root = mkdtempSync(path.join(os.tmpdir(), "steam-releaser-npm-tool-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writePackage(root, "npm", "11.20.0");
  writePackage(path.join(root, "node_modules", "alpha"), "alpha", "1.0.0");
  writePackage(
    path.join(root, "node_modules", "@scope", "beta"),
    "@scope/beta",
    "2.0.0",
  );
  writePackage(
    path.join(root, "node_modules", "@scope", "beta", "node_modules", "alpha"),
    "alpha",
    "1.1.0",
  );
  mkdirSync(path.join(root, "node_modules", ".bin"));
  const inventory = await inventoryNpm(root);
  assert.equal(inventory.version, "11.20.0");
  assert.deepEqual(inventory.packages, {
    "@scope/beta": ["2.0.0"],
    alpha: ["1.0.0", "1.1.0"],
    npm: ["11.20.0"],
  });
});

test("rejects linked packages rather than silently omitting them", async (t) => {
  const root = mkdtempSync(path.join(os.tmpdir(), "steam-releaser-npm-link-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writePackage(root, "npm", "11.20.0");
  const modules = path.join(root, "node_modules");
  mkdirSync(modules);
  const outside = path.join(root, "outside");
  writePackage(outside, "outside", "1.0.0");
  try {
    symlinkSync(outside, path.join(modules, "linked"), "dir");
  } catch (error) {
    if (error.code === "EPERM" || error.code === "EACCES") {
      t.skip("directory symlinks are unavailable");
      return;
    }
    throw error;
  }
  await assert.rejects(inventoryNpm(root), /Unexpected bundled package link/);
});

test("identifies Node's built-in undici separately from npm's packages", () => {
  assert.deepEqual(inventoryNode({ node: "26.13.0", undici: "8.12.2" }), {
    version: "26.13.0",
    packages: { undici: ["8.12.2"] },
  });
  assert.throws(
    () => inventoryNode({ node: "26.13.0" }),
    /Cannot identify Node.js and its built-in undici version/,
  );
  assert.throws(
    () => inventoryNode({ node: "26.13.0", undici: "unknown" }),
    /Cannot identify Node.js and its built-in undici version/,
  );
});

test("reports Node and npm undici advisories against their own versions", async (t) => {
  const root = mkdtempSync(path.join(os.tmpdir(), "steam-releaser-node-tool-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writePackage(root, "npm", "11.20.0");
  writePackage(path.join(root, "node_modules", "undici"), "undici", "6.28.0");
  const requests = [];
  const result = await auditToolchains(
    root,
    { node: "26.13.0", undici: "8.12.2" },
    async (_url, options) => {
      const packages = JSON.parse(options.body);
      requests.push(packages);
      const version = packages.undici[0];
      return {
        ok: true,
        json: async () => ({
          undici: [
            {
              url: `https://github.com/advisories/${version}`,
              severity: "high",
              vulnerable_versions: version,
            },
          ],
        }),
      };
    },
  );
  assert.equal(requests.length, 2);
  assert.deepEqual(result.npm.issues[0].installed, ["6.28.0"]);
  assert.deepEqual(result.node.issues[0].installed, ["8.12.2"]);
  assert.equal(
    result.npm.issues[0].url,
    "https://github.com/advisories/6.28.0",
  );
  assert.equal(
    result.node.issues[0].url,
    "https://github.com/advisories/8.12.2",
  );
});

test("queries bundled package versions and reports advisory ranges", async () => {
  const packages = { alpha: ["1.0.0", "1.1.0"], npm: ["11.20.0"] };
  const issues = await findAdvisories(packages, async (url, options) => {
    assert.equal(
      url,
      "https://registry.npmjs.org/-/npm/v1/security/advisories/bulk",
    );
    assert.equal(options.method, "POST");
    assert.equal(options.redirect, "error");
    assert.deepEqual(JSON.parse(options.body), packages);
    return {
      ok: true,
      json: async () => ({
        alpha: [
          {
            url: "https://github.com/advisories/GHSA-test",
            severity: "high",
            vulnerable_versions: "<1.1.0",
          },
        ],
      }),
    };
  });
  assert.deepEqual(issues, [
    {
      name: "alpha",
      installed: ["1.0.0", "1.1.0"],
      affected: "<1.1.0",
      severity: "high",
      url: "https://github.com/advisories/GHSA-test",
    },
  ]);
});

test("fails closed on unavailable or malformed advisory responses", async () => {
  await assert.rejects(
    findAdvisories({ npm: ["11.20.0"] }, async () => ({
      ok: false,
      status: 503,
    })),
    /HTTP 503/,
  );
  await assert.rejects(
    findAdvisories({ npm: ["11.20.0"] }, async () => ({
      ok: true,
      json: async () => ({ npm: {} }),
    })),
    /Invalid advisories/,
  );
  await assert.rejects(
    findAdvisories({ npm: ["11.20.0"] }, async () => ({
      ok: true,
      json: async () => ({ error: "unexpected response" }),
    })),
    /Unexpected advisory package/,
  );
  await assert.rejects(
    findAdvisories({ npm: ["11.20.0"] }, async () => ({
      ok: true,
      json: async () => ({
        npm: [
          {
            url: "https://example.invalid",
            vulnerable_versions: "*",
            severity: "unknown",
          },
        ],
      }),
    })),
    /Invalid advisory/,
  );
});

test("rejects toolchain advisory matches at every severity", () => {
  const glob = {
    name: "glob",
    installed: ["13.0.6"],
    affected: ">=9.0.0 <=14.0.1",
    severity: "moderate",
    url: "https://github.com/advisories/GHSA-3g3m-2fjr-6pw4",
  };
  const minimatch = {
    name: "minimatch",
    installed: ["10.2.5"],
    affected: ">=7.0.0 <11.0.1",
    severity: "moderate",
    url: "https://github.com/advisories/GHSA-g3hv-75wg-m249",
  };
  const report = (issues, version = "12.2.2", nodeIssues = []) => ({
    npm: { version, issues },
    node: { version: "26.13.0", issues: nodeIssues },
  });
  assert.doesNotThrow(() => enforceToolchainPolicy(report([])));
  for (const unreviewed of [
    report([glob]),
    report([minimatch]),
    report([{ ...glob, severity: "high" }]),
    report([{ ...glob, affected: ">=9.0.0" }]),
    report([{ ...glob, installed: ["13.0.6", "13.0.7"] }]),
    report([{ ...glob, url: "https://github.com/advisories/GHSA-new" }]),
    report([glob], "12.2.3"),
    report([], "12.2.2", [glob]),
    report([{ name: "new-package", severity: "low" }]),
    report([], "12.2.2", [{ name: "undici", severity: "moderate" }]),
  ]) {
    assert.throws(() => enforceToolchainPolicy(unreviewed), /unreviewed/i);
  }
  assert.throws(
    () => enforceToolchainPolicy({ npm: { version: "12.2.2", issues: [] } }),
    /invalid/i,
  );
});

test("the toolchain audit command fails on a new low-severity advisory", (t) => {
  const root = mkdtempSync(
    path.join(os.tmpdir(), "steam-releaser-audit-gate-"),
  );
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writePackage(root, "npm", "12.2.2");
  writePackage(
    path.join(root, "node_modules", "new-package"),
    "new-package",
    "1.0.0",
  );
  const preload = path.join(root, "mock-registry.mjs");
  writeFileSync(
    preload,
    `globalThis.fetch = async (_url, options) => ({
    ok: true,
    json: async () => Object.hasOwn(JSON.parse(options.body), "new-package")
      ? {"new-package": [{url: "https://github.com/advisories/GHSA-new", severity: "low", vulnerable_versions: "<2.0.0"}]}
      : {},
  });\n`,
  );
  const result = spawnSync(
    process.execPath,
    [
      "--import",
      pathToFileURL(preload).href,
      fileURLToPath(new URL("./audit-npm-toolchain.mjs", import.meta.url)),
    ],
    {
      encoding: "utf8",
      timeout: 10_000,
      env: {
        ...process.env,
        GITHUB_ACTIONS: "true",
        NODE_OPTIONS: "",
        STEAM_RELEASER_NPM_DIR: root,
      },
    },
  );
  assert.equal(result.error, undefined);
  assert.notEqual(result.status, 0, "a new low-severity advisory was accepted");
  assert.match(result.stdout, /::error title=JavaScript toolchain::/);
  assert.match(result.stderr, /unreviewed JavaScript toolchain advisory/);
});

test("retries a transient advisory-service failure", async () => {
  let calls = 0;
  const issues = await findAdvisories({ npm: ["11.20.0"] }, async () => {
    calls++;
    return calls === 1
      ? { ok: false, status: 503 }
      : { ok: true, json: async () => ({}) };
  });
  assert.deepEqual(issues, []);
  assert.equal(calls, 2);
});
