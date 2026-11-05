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
  goNoticeDirectories,
  nestedGoNotices,
  noticeFiles,
} from "./go-package-notices.mjs";

test("collects standalone third-party licenses and copyright notices", (t) => {
  const root = mkdtempSync(path.join(tmpdir(), "dependency-notices-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  for (const name of [
    "LICENSE",
    "THIRD-PARTY-LICENSE",
    "THIRD_PARTY_NOTICES.md",
    "CopyrightNotice.txt",
    "README.md",
  ])
    writeFileSync(path.join(root, name), name);
  assert.deepEqual(noticeFiles(root), [
    "CopyrightNotice.txt",
    "LICENSE",
    "THIRD-PARTY-LICENSE",
    "THIRD_PARTY_NOTICES.md",
  ]);
});

test("collects notices from compiled package ancestors without duplicate or unrelated notices", (t) => {
  const root = mkdtempSync(path.join(tmpdir(), "go-notices-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const compiled = path.join(root, "internal", "fork", "compiled");
  const another = path.join(root, "internal", "fork", "another");
  const unused = path.join(root, "internal", "unused");
  const embedded = path.join(compiled, "assets", "icons");
  for (const directory of [compiled, another, unused, embedded])
    mkdirSync(directory, { recursive: true });
  writeFileSync(path.join(root, "LICENSE"), "module license");
  writeFileSync(path.join(root, "internal", "NOTICE"), "ancestor notice");
  writeFileSync(
    path.join(root, "internal", "fork", "LICENSE.txt"),
    "nested license",
  );
  writeFileSync(path.join(compiled, "AUTHORS"), "compiled authors");
  writeFileSync(path.join(embedded, "LICENSE"), "embedded asset license");
  writeFileSync(path.join(unused, "LICENSE"), "unrelated license");

  const notices = nestedGoNotices(root, [
    ...goNoticeDirectories(compiled, ["assets/icons/icon.png"]),
    another,
    compiled,
  ]);
  assert.deepEqual(
    notices.map(({ relative, files }) => ({ relative, files })),
    [
      { relative: "internal", files: ["NOTICE"] },
      { relative: "internal/fork", files: ["LICENSE.txt"] },
      { relative: "internal/fork/compiled", files: ["AUTHORS"] },
      { relative: "internal/fork/compiled/assets/icons", files: ["LICENSE"] },
    ],
  );
});

test("rejects a package outside its module, including through a symlink", (t) => {
  const base = mkdtempSync(path.join(tmpdir(), "go-notice-boundary-"));
  t.after(() => rmSync(base, { recursive: true, force: true }));
  const root = path.join(base, "module");
  const outside = path.join(base, "outside");
  mkdirSync(root);
  mkdirSync(outside);
  assert.throws(
    () => goNoticeDirectories(root, ["../outside/asset"]),
    /outside its Go package/,
  );
  assert.throws(() => nestedGoNotices(root, [outside]), /outside its module/);
  const alias = path.join(root, "alias");
  try {
    symlinkSync(
      outside,
      alias,
      process.platform === "win32" ? "junction" : "dir",
    );
  } catch (error) {
    t.diagnostic(`symlinks unavailable: ${error.message}`);
    return;
  }
  assert.throws(() => nestedGoNotices(root, [alias]), /outside its module/);
});
