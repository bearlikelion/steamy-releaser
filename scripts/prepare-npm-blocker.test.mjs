import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { checkNpmBlocker, prepareNpmBlocker } from "./prepare-npm-blocker.mjs";

test("resolves the blocker and rejects a missing blocker", async (t) => {
  const root = await mkdtemp(
    path.join(os.tmpdir(), "steam-releaser-block-test-"),
  );
  t.after(() => rm(root, { recursive: true, force: true }));
  const blocker = await prepareNpmBlocker(root);
  const env = {
    ...process.env,
    PATH: `${blocker}${path.delimiter}${process.env.PATH || ""}`,
  };
  delete env.Path;
  assert.doesNotThrow(() => checkNpmBlocker(env));
  env.PATH = root;
  assert.throws(() => checkNpmBlocker(env), /did not resolve the npm blocker/);
});
