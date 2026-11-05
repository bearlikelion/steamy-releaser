import assert from "node:assert/strict";
import {
  mkdtemp,
  mkdir,
  rm,
  symlink,
  truncate,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { verifyFrontendDist } from "./verify-frontend-dist.mjs";

async function fixture(t) {
  const directory = await mkdtemp(
    path.join(os.tmpdir(), "steam-releaser-frontend-"),
  );
  t.after(() => rm(directory, { recursive: true, force: true }));
  await writeFile(path.join(directory, "index.html"), "<html></html>");
  await mkdir(path.join(directory, "assets"));
  await writeFile(path.join(directory, "assets", "app.js"), "console.log(1)");
  return directory;
}

test("accepts a bounded frontend artifact", async (t) => {
  const result = await verifyFrontendDist(await fixture(t));
  assert.equal(result.files, 2);
  assert.ok(result.bytes > 0);
});

test("rejects missing index, hidden entries, and oversized assets", async (t) => {
  const directory = await fixture(t);
  await writeFile(path.join(directory, "index.html"), "");
  await assert.rejects(verifyFrontendDist(directory), /nonempty index/);
  await writeFile(path.join(directory, "index.html"), "ok");
  await writeFile(path.join(directory, ".hidden"), "bad");
  await assert.rejects(
    verifyFrontendDist(directory),
    /Unsafe frontend artifact path/,
  );
  await rm(path.join(directory, ".hidden"));
  await truncate(
    path.join(directory, "assets", "app.js"),
    64 * 1024 * 1024 + 1,
  );
  await assert.rejects(verifyFrontendDist(directory), /exceeds the size limit/);
});

test("rejects links in the frontend artifact", async (t) => {
  const directory = await fixture(t);
  try {
    await symlink(
      path.join(directory, "index.html"),
      path.join(directory, "assets", "link"),
    );
  } catch (error) {
    if (error.code === "EPERM" || error.code === "EACCES") {
      t.skip("symlinks are unavailable");
      return;
    }
    throw error;
  }
  await assert.rejects(verifyFrontendDist(directory), /contains a link/);
});

test("bounds traversal even when an artifact contains only empty directories", async (t) => {
  const directory = await fixture(t);
  for (let start = 0; start < 4100; start += 100) {
    await Promise.all(
      Array.from({ length: 100 }, (_, i) =>
        mkdir(path.join(directory, `dir-${start + i}`)),
      ),
    );
  }
  await assert.rejects(verifyFrontendDist(directory), /too many entries/);
});
