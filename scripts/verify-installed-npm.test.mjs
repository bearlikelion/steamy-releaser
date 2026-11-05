import assert from "node:assert/strict";
import { mkdtemp, mkdir, rm, symlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { verifyInstalledPackages } from "./verify-installed-npm.mjs";

async function fixture(t) {
  const directory = await mkdtemp(
    path.join(os.tmpdir(), "steam-releaser-installed-"),
  );
  t.after(() => rm(directory, { recursive: true, force: true }));
  const integrity = `sha512-${Buffer.alloc(64).toString("base64")}`;
  const lock = {
    lockfileVersion: 3,
    packages: {
      "": { dependencies: { parent: "1.0.0" } },
      "node_modules/parent": {
        version: "1.0.0",
        license: "MIT",
        resolved: "https://registry.npmjs.org/parent/-/parent-1.0.0.tgz",
        integrity,
        bundleDependencies: ["child"],
      },
      "node_modules/parent/node_modules/child": {
        version: "2.0.0",
        inBundle: true,
      },
      "node_modules/@scope/optional": {
        version: "3.0.0",
        resolved:
          "https://registry.npmjs.org/@scope/optional/-/optional-3.0.0.tgz",
        integrity,
        optional: true,
      },
    },
  };
  async function put(location, name, version, extra = {}) {
    const packageDirectory = path.join(directory, ...location.split("/"));
    await mkdir(packageDirectory, { recursive: true });
    await writeFile(
      path.join(packageDirectory, "package.json"),
      JSON.stringify({ name, version, ...extra }),
    );
  }
  await writeFile(
    path.join(directory, "package.json"),
    JSON.stringify({ name: "fixture", dependencies: { parent: "1.0.0" } }),
  );
  await put("node_modules/parent", "parent", "1.0.0", {
    license: "MIT",
    dependencies: { child: "2.0.0" },
    peerDependencies: { "@scope/optional": "3.0.0" },
    peerDependenciesMeta: { "@scope/optional": { optional: true } },
    bundleDependencies: ["child"],
  });
  await put("node_modules/parent/node_modules/child", "child", "2.0.0");
  return { directory, lock, put };
}

test("matches installed packages and bundled children while allowing uninstalled optional packages", async (t) => {
  const { directory, lock, put } = await fixture(t);
  await mkdir(path.join(directory, "node_modules", ".bin"));
  await writeFile(
    path.join(directory, "node_modules", ".package-lock.json"),
    "{}",
  );
  assert.equal(await verifyInstalledPackages(lock, directory), 2);
  const inventory = await verifyInstalledPackages(lock, directory, {
    includeInventory: true,
  });
  assert.deepEqual(
    inventory.map(({ location, name, version }) => ({
      location,
      name,
      version,
    })),
    [
      { location: "node_modules/parent", name: "parent", version: "1.0.0" },
      {
        location: "node_modules/parent/node_modules/child",
        name: "child",
        version: "2.0.0",
      },
    ],
  );
  await put("node_modules/@scope/optional", "@scope/optional", "3.0.0");
  assert.equal(await verifyInstalledPackages(lock, directory), 3);
});

test("rejects forged installed license declarations", async (t) => {
  const { directory, lock } = await fixture(t);
  lock.packages["node_modules/parent"].license = "ISC";
  await assert.rejects(
    verifyInstalledPackages(lock, directory),
    /license.*does not match/i,
  );
});

test("rejects production dependencies hidden as development packages", async (t) => {
  const { directory, lock, put } = await fixture(t);
  for (const location of [
    "node_modules/parent",
    "node_modules/parent/node_modules/child",
  ]) {
    lock.packages[location].dev = true;
    await assert.rejects(
      verifyInstalledPackages(lock, directory),
      /production dependency.*development/i,
    );
    delete lock.packages[location].dev;
  }
  await put("node_modules/@scope/optional", "@scope/optional", "3.0.0");
  lock.packages["node_modules/@scope/optional"].dev = true;
  await assert.rejects(
    verifyInstalledPackages(lock, directory),
    /production dependency.*development/i,
  );
});

test("rejects a missing required transitive production dependency", async (t) => {
  const { directory, lock } = await fixture(t);
  await rm(path.join(directory, "node_modules/parent/node_modules/child"), {
    recursive: true,
  });
  await assert.rejects(
    verifyInstalledPackages(lock, directory),
    /missing required production dependency/i,
  );
});

test("rejects a missing required peer but permits an explicitly optional peer", async (t) => {
  const { directory, lock } = await fixture(t);
  await writeFile(
    path.join(directory, "package.json"),
    JSON.stringify({
      name: "fixture",
      dependencies: { parent: "1.0.0" },
      peerDependencies: { missing: "1.0.0" },
    }),
  );
  await assert.rejects(
    verifyInstalledPackages(lock, directory),
    /missing required production dependency missing/i,
  );
});

test("rejects an unexpected installed package and a forged bundled version", async (t) => {
  const { directory, lock, put } = await fixture(t);
  lock.packages["node_modules/parent/node_modules/child"].version = "2.99.0";
  await assert.rejects(
    verifyInstalledPackages(lock, directory),
    /identity does not match/,
  );
  lock.packages["node_modules/parent/node_modules/child"].version = "2.0.0";
  await put("node_modules/extra", "extra", "1.0.0");
  await assert.rejects(
    verifyInstalledPackages(lock, directory),
    /missing from lockfile/,
  );
});

test("rejects installed package links and unexpected hidden entries", async (t) => {
  const { directory, lock } = await fixture(t);
  const modules = path.join(directory, "node_modules");
  await mkdir(path.join(modules, ".hidden"));
  await assert.rejects(
    verifyInstalledPackages(lock, directory),
    /Unexpected installed module entry/,
  );
  await rm(path.join(modules, ".hidden"), { recursive: true });
  try {
    await symlink(
      path.join(modules, "parent"),
      path.join(modules, "linked"),
      "dir",
    );
  } catch (error) {
    if (error.code === "EPERM" || error.code === "EACCES") return;
    throw error;
  }
  await assert.rejects(
    verifyInstalledPackages(lock, directory),
    /Unexpected installed module entry/,
  );
});

test("notice collection permits known build caches without weakening the pre-build check", async (t) => {
  const { directory, lock } = await fixture(t);
  const modules = path.join(directory, "node_modules");
  for (const name of [".vite", ".vite-temp", ".vitest-cache"]) {
    await mkdir(path.join(modules, name));
  }
  await assert.rejects(
    verifyInstalledPackages(lock, directory),
    /Unexpected installed module entry/,
  );
  assert.equal(
    await verifyInstalledPackages(lock, directory, { allowBuildCaches: true }),
    2,
  );
  await mkdir(path.join(modules, ".unknown"));
  await assert.rejects(
    verifyInstalledPackages(lock, directory, { allowBuildCaches: true }),
    /Unexpected installed module entry/,
  );
});
