import assert from "node:assert/strict";
import {
  mkdtempSync,
  mkdirSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import {
  frontendBuildToolLocations,
  requireRolldownThirdPartyNotice,
} from "./npm-build-notices.mjs";

function fixture(t) {
  const root = mkdtempSync(path.join(tmpdir(), "frontend-build-notices-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const lock = { packages: {} };
  const install = (location, metadata) => {
    const directory = path.join(root, ...location.split("/"));
    mkdirSync(directory, { recursive: true });
    writeFileSync(
      path.join(directory, "package.json"),
      JSON.stringify(metadata),
    );
    lock.packages[location] = { version: metadata.version };
  };
  writeFileSync(path.join(root, "package.json"), "{}");
  install("node_modules/vite", {
    name: "vite",
    version: "1.0.0",
    dependencies: { rolldown: "1.0.0" },
  });
  install("node_modules/tailwindcss", {
    name: "tailwindcss",
    version: "1.0.0",
  });
  return { root, lock, install };
}

test("includes the Rolldown actually resolved by Vite, including nested installs", (t) => {
  const { root, lock, install } = fixture(t);
  install("node_modules/rolldown", { name: "rolldown", version: "2.0.0" });
  install("node_modules/vite/node_modules/rolldown", {
    name: "rolldown",
    version: "1.0.0",
  });
  assert.deepEqual(
    [...frontendBuildToolLocations(root, lock)],
    [
      ["node_modules/vite", "vite"],
      ["node_modules/tailwindcss", "tailwindcss"],
      ["node_modules/vite/node_modules/rolldown", "rolldown"],
    ],
  );
});

test("rejects a resolved build tool absent from the lockfile", (t) => {
  const { root, lock, install } = fixture(t);
  install("node_modules/rolldown", { name: "rolldown", version: "1.0.0" });
  delete lock.packages["node_modules/rolldown"];
  assert.throws(
    () => frontendBuildToolLocations(root, lock),
    /outside the locked install/,
  );
});

test("accepts a project reached through a directory symlink", (t) => {
  const { root, lock, install } = fixture(t);
  install("node_modules/rolldown", { name: "rolldown", version: "1.0.0" });
  const alias = path.join(root, "alias");
  try {
    symlinkSync(root, alias, process.platform === "win32" ? "junction" : "dir");
  } catch (error) {
    t.skip(`directory symlinks unavailable: ${error.message}`);
    return;
  }
  assert.equal(
    frontendBuildToolLocations(alias, lock).get("node_modules/rolldown"),
    "rolldown",
  );
});

test("requires a nonempty third-party license for the bundler runtime", (t) => {
  const root = mkdtempSync(path.join(tmpdir(), "rolldown-notices-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writeFileSync(path.join(root, "LICENSE"), "Rolldown license");
  assert.throws(() => requireRolldownThirdPartyNotice(root), /missing/);
  const thirdParty = path.join(root, "THIRD-PARTY-LICENSE");
  writeFileSync(thirdParty, " \n");
  assert.throws(() => requireRolldownThirdPartyNotice(root), /missing/);
  writeFileSync(thirdParty, "Third-party license");
  assert.doesNotThrow(() => requireRolldownThirdPartyNotice(root));
});
