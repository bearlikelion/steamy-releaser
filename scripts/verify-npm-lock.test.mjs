import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { validateLockfile } from "./verify-npm-lock.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const script = path.join(root, "scripts/verify-npm-lock.mjs");
const lock = JSON.parse(
  readFileSync(
    path.join(root, "cmd/steam-releaser-desktop/frontend/package-lock.json"),
    "utf8",
  ),
);

function verify(t, mutate) {
  const directory = mkdtempSync(path.join(os.tmpdir(), "steam-releaser-lock-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const fixture = structuredClone(lock);
  mutate(fixture.packages);
  const file = path.join(directory, "package-lock.json");
  writeFileSync(file, JSON.stringify(fixture));
  return spawnSync(process.execPath, [script, file], { encoding: "utf8" });
}

test("accepts the current lockfile", (t) => {
  const result = verify(t, () => {});
  assert.equal(result.status, 0, result.stderr);
});

test("rejects missing integrity and untrusted registries", (t) => {
  for (const mutate of [
    (entry) => delete entry.integrity,
    (entry) => {
      entry.resolved = "https://example.invalid/package.tgz";
    },
  ]) {
    const result = verify(t, (packages) => {
      const key = Object.keys(packages).find(
        (name) => name && !packages[name].inBundle,
      );
      mutate(packages[key]);
    });
    assert.notEqual(result.status, 0);
  }
});

test("rejects non-boolean dependency classification flags", (t) => {
  for (const flag of ["dev", "optional", "devOptional", "peer", "inBundle"]) {
    const result = verify(t, (packages) => {
      packages["node_modules/react"][flag] = "false";
    });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, new RegExp(`Invalid ${flag} flag`));
  }
});

test("rejects another package or version from the npm registry", (t) => {
  for (const mutate of [
    (packages) => {
      const entry = packages["node_modules/@radix-ui/number"];
      const other = packages["node_modules/@radix-ui/rect"];
      entry.resolved = other.resolved;
      entry.integrity = other.integrity;
    },
    (packages) => {
      packages["node_modules/@radix-ui/number"].version = "1.1.4";
    },
  ]) {
    const result = verify(t, mutate);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /Tarball does not match package and version/);
  }
});

test("rejects a bundled dependency without a declaring parent", (t) => {
  const result = verify(t, (packages) => {
    const key = Object.keys(packages).find((name) => packages[name].inBundle);
    assert.ok(key, "expected a bundled dependency fixture");
    const parent = key.slice(0, key.lastIndexOf("/node_modules/"));
    packages[parent].bundleDependencies = [];
  });
  assert.notEqual(result.status, 0);
});

test("associates bundled packages with their registry parent", () => {
  const { packages, bundled } = validateLockfile(lock);
  assert.equal(
    packages.reduce((count, item) => count + item.bundled.length, 0),
    bundled,
  );
  const [location, info] = Object.entries(lock.packages).find(
    ([, item]) => item.inBundle,
  );
  let owner = location.slice(0, location.lastIndexOf("/node_modules/"));
  while (lock.packages[owner].inBundle)
    owner = owner.slice(0, owner.lastIndexOf("/node_modules/"));
  const parent = packages.find(
    (item) => item.resolved === lock.packages[owner].resolved,
  );
  const relative = location.slice(owner.length + 1);
  const separator = relative.lastIndexOf("/node_modules/");
  const name = relative.slice(
    separator < 0
      ? "node_modules/".length
      : separator + "/node_modules/".length,
  );
  assert.deepEqual(
    parent.bundled.find((item) => item.path === relative),
    {
      path: relative,
      name,
      version: info.version,
    },
  );
});

test("rejects package paths that escape or include non-package segments", (t) => {
  for (const invalid of [
    "node_modules/../escape",
    "node_modules/parent/extra/node_modules/child",
    "node_modules/name\\escape",
  ]) {
    const result = verify(t, (packages) => {
      packages[invalid] = { version: "1.0.0", inBundle: true };
    });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /Unexpected package entry/);
  }
});
