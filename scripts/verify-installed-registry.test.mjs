import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { archiveFor } from "./npm-archive-fixtures.mjs";
import { verifyInstalledRegistryPackages } from "./verify-installed-registry.mjs";

async function fixture(t, name = "example", archiveRoot = "package") {
  const root = await mkdtemp(
    path.join(os.tmpdir(), "steam-installed-registry-"),
  );
  t.after(() => rm(root, { recursive: true, force: true }));
  const version = "1.0.0";
  const packageJson = JSON.stringify({ name, version });
  const archive = archiveFor([
    { name: `${archiveRoot}/package.json`, contents: packageJson },
    { name: `${archiveRoot}/index.js`, contents: "module.exports = 1;\n" },
  ]);
  const integrity = `sha512-${createHash("sha512").update(archive).digest("base64")}`;
  const basename = name.slice(name.lastIndexOf("/") + 1);
  const location = `node_modules/${name}`;
  const lock = {
    lockfileVersion: 3,
    packages: {
      "": {},
      [location]: {
        version,
        integrity,
        resolved: `https://registry.npmjs.org/${name}/-/${basename}-${version}.tgz`,
      },
    },
  };
  const inventory = [{ location, name, version }];
  const directory = path.join(root, ...location.split("/"));
  await mkdir(directory, { recursive: true });
  await writeFile(path.join(directory, "package.json"), packageJson);
  await writeFile(path.join(directory, "index.js"), "module.exports = 1;\n");
  const events = [];
  const options = {
    verifyRegistry: async (entries) => {
      events.push("signatures");
      assert.equal(entries.length, 1);
    },
    readArchive: async (digest) => {
      events.push("archive");
      assert.equal(digest, integrity);
      return archive;
    },
  };
  return {
    root,
    lock,
    inventory,
    directory,
    archive,
    integrity,
    options,
    events,
  };
}

test("authenticates metadata before comparing cached archive files", async (t) => {
  const f = await fixture(t);
  assert.deepEqual(
    await verifyInstalledRegistryPackages(
      f.lock,
      f.root,
      f.inventory,
      "cache",
      f.options,
    ),
    { packages: 1, files: 2 },
  );
  assert.deepEqual(f.events, ["signatures", "archive"]);
  await writeFile(path.join(f.directory, "index.js"), "module.exports = 2;\n");
  await assert.rejects(
    verifyInstalledRegistryPackages(
      f.lock,
      f.root,
      f.inventory,
      "cache",
      f.options,
    ),
    /differs from signed archive/,
  );
});

test("rejects a changed cached archive and unexpected or missing installed files", async (t) => {
  const f = await fixture(t);
  await assert.rejects(
    verifyInstalledRegistryPackages(f.lock, f.root, f.inventory, "cache", {
      ...f.options,
      readArchive: async () => Buffer.from("not the archive"),
    }),
    /does not match its signed digest/,
  );
  await writeFile(path.join(f.directory, "extra.js"), "extra");
  await assert.rejects(
    verifyInstalledRegistryPackages(
      f.lock,
      f.root,
      f.inventory,
      "cache",
      f.options,
    ),
    /differs from signed archive/,
  );
  await rm(path.join(f.directory, "extra.js"));
  await rm(path.join(f.directory, "index.js"));
  await assert.rejects(
    verifyInstalledRegistryPackages(
      f.lock,
      f.root,
      f.inventory,
      "cache",
      f.options,
    ),
    /missing signed files/,
  );
});

test("supports the separately named root used by type package archives", async (t) => {
  const f = await fixture(t, "@types/example", "example");
  assert.deepEqual(
    await verifyInstalledRegistryPackages(
      f.lock,
      f.root,
      f.inventory,
      "cache",
      f.options,
    ),
    { packages: 1, files: 2 },
  );
});

test("rejects a cached archive whose package identity differs from the lockfile", async (t) => {
  const f = await fixture(t);
  const archive = archiveFor([
    {
      name: "package/package.json",
      contents: '{"name":"other","version":"1.0.0"}',
    },
    { name: "package/index.js", contents: "module.exports = 1;\n" },
  ]);
  const integrity = `sha512-${createHash("sha512").update(archive).digest("base64")}`;
  f.lock.packages[f.inventory[0].location].integrity = integrity;
  await assert.rejects(
    verifyInstalledRegistryPackages(f.lock, f.root, f.inventory, "cache", {
      ...f.options,
      readArchive: async () => archive,
    }),
    /different identity/,
  );
});
