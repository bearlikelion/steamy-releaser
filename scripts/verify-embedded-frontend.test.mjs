import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { verifyEmbeddedFrontend } from "./verify-embedded-frontend.mjs";

test("checks frontend paths and contents against the executable", async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "steam-releaser-embed-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const dist = path.join(root, "dist");
  const binary = path.join(root, "app");
  await mkdir(path.join(dist, "assets"), { recursive: true });
  await writeFile(path.join(dist, "index.html"), "<html>example</html>");
  await writeFile(path.join(dist, "assets", "app.js"), "compiled frontend");
  const complete =
    "frontend/dist/index.html<html>example</html>frontend/dist/assets/app.jscompiled frontend";
  await writeFile(binary, complete);
  assert.equal(await verifyEmbeddedFrontend(binary, dist), 2);
  await writeFile(
    binary,
    complete.replace("compiled frontend", "modified frontend"),
  );
  await assert.rejects(
    verifyEmbeddedFrontend(binary, dist),
    /asset bytes missing/,
  );
  await writeFile(
    binary,
    complete.replace(
      "frontend/dist/assets/app.js",
      "frontend/dist/assets/old.js",
    ),
  );
  await assert.rejects(
    verifyEmbeddedFrontend(binary, dist),
    /asset path missing/,
  );
});

test("rejects missing or unsafe frontend outputs", async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "steam-releaser-embed-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const dist = path.join(root, "dist");
  const binary = path.join(root, "app");
  await mkdir(dist);
  await writeFile(binary, "frontend/dist/index.html");
  await assert.rejects(
    verifyEmbeddedFrontend(binary, dist),
    /missing index.html/,
  );
  await writeFile(path.join(dist, "index.html"), "");
  await assert.rejects(verifyEmbeddedFrontend(binary, dist), /empty/);
  try {
    await symlink(binary, path.join(dist, "link"));
  } catch (error) {
    if (error.code === "EPERM" || error.code === "EACCES") {
      t.skip("symlinks unavailable");
      return;
    }
    throw error;
  }
  await assert.rejects(
    verifyEmbeddedFrontend(binary, dist),
    /Unsupported frontend output/,
  );
});
