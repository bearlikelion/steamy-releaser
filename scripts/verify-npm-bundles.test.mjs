import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { test } from "node:test";
import { archiveFor } from "./npm-archive-fixtures.mjs";
import {
  verifyBundleDeclaration,
  verifyBundledArchive,
  verifyBundledChild,
} from "./verify-npm-bundles.mjs";

function fixture({ root, files } = {}) {
  const metadata = root || {
    name: "parent",
    version: "1.0.0",
    bundledDependencies: ["child", "@scope/other"],
  };
  const children = files || [
    {
      name: "package/node_modules/child/package.json",
      contents: JSON.stringify({ name: "child", version: "2.0.0" }),
    },
    { name: "package/node_modules/child/lib/package.json", contents: "{}" },
    {
      name: "package/node_modules/@scope/other/package.json",
      contents: JSON.stringify({ name: "@scope/other", version: "3.0.0" }),
    },
  ];
  const archive = archiveFor([
    { name: "package/package.json", contents: JSON.stringify(metadata) },
    ...children,
  ]);
  const entry = {
    name: "parent",
    version: "1.0.0",
    integrity: `sha512-${createHash("sha512").update(archive).digest("base64")}`,
    bundleDependencies: ["child", "@scope/other"],
    bundled: [
      { path: "node_modules/child", name: "child", version: "2.0.0" },
      {
        path: "node_modules/@scope/other",
        name: "@scope/other",
        version: "3.0.0",
      },
    ],
  };
  return { entry, metadata, archive };
}

test("authenticates all declared bundled versions against their parent archive", () => {
  const { entry, metadata, archive } = fixture();
  assert.deepEqual(verifyBundleDeclaration(entry, metadata), [
    "@scope/other",
    "child",
  ]);
  assert.doesNotThrow(() => verifyBundledArchive(entry, metadata, archive));
  assert.deepEqual(
    verifyBundleDeclaration(entry, {
      bundleDependencies: true,
      dependencies: { child: "*", "@scope/other": "*" },
    }),
    ["@scope/other", "child"],
  );
});

test("rejects omitted, forged, and conflicting bundle declarations", () => {
  const { entry, metadata } = fixture();
  for (const altered of [
    { ...metadata, bundledDependencies: ["child"] },
    { ...metadata, bundledDependencies: ["child", "child"] },
    { ...metadata, bundleDependencies: ["different"] },
  ]) {
    assert.throws(() => verifyBundleDeclaration(entry, altered));
  }
  assert.throws(
    () =>
      verifyBundleDeclaration({ ...entry, bundleDependencies: [] }, metadata),
    /does not match/,
  );
});

test("rejects forged versions and missing or extra bundled packages", () => {
  const { entry, metadata, archive } = fixture();
  const wrongVersion = structuredClone(entry);
  wrongVersion.bundled[0].version = "2.99.0";
  assert.throws(
    () => verifyBundledArchive(wrongVersion, metadata, archive),
    /identity does not match/,
  );
  const omitted = structuredClone(entry);
  omitted.bundled.pop();
  assert.throws(
    () => verifyBundledArchive(omitted, metadata, archive),
    /missing from lockfile/,
  );
  const extra = structuredClone(entry);
  extra.bundled.push({
    path: "node_modules/extra",
    name: "extra",
    version: "1.0.0",
  });
  assert.throws(
    () => verifyBundledArchive(extra, metadata, archive),
    /missing from archive/,
  );
});

test("rejects unsigned archive bytes, a different parent, and different declarations", () => {
  const good = fixture();
  assert.throws(
    () =>
      verifyBundledArchive(
        { ...good.entry, integrity: "sha512-bad" },
        good.metadata,
        good.archive,
      ),
    /SHA-512/,
  );
  const wrongParent = fixture({ root: { ...good.metadata, name: "other" } });
  assert.throws(
    () =>
      verifyBundledArchive(
        wrongParent.entry,
        good.metadata,
        wrongParent.archive,
      ),
    /package identity/,
  );
  const wrongDeclaration = fixture({
    root: { ...good.metadata, bundledDependencies: ["child"] },
  });
  assert.throws(
    () =>
      verifyBundledArchive(
        wrongDeclaration.entry,
        good.metadata,
        wrongDeclaration.archive,
      ),
    /declaration does not match/,
  );
});

function childFixture({ childFiles, publishedFiles, nested = false } = {}) {
  const childMetadata = JSON.stringify({ name: "child", version: "1.0.0" });
  const grandMetadata = JSON.stringify({ name: "grand", version: "2.0.0" });
  const inside = childFiles || [
    {
      name: "package/node_modules/child/package.json",
      contents: childMetadata,
    },
    {
      name: "package/node_modules/child/index.js",
      contents: "module.exports = 1;",
    },
    ...(nested
      ? [
          {
            name: "package/node_modules/child/node_modules/grand/package.json",
            contents: grandMetadata,
          },
          {
            name: "package/node_modules/child/node_modules/grand/index.js",
            contents: "module.exports = 2;",
          },
        ]
      : []),
  ];
  const parentMetadata = {
    name: "parent",
    version: "1.0.0",
    bundledDependencies: ["child"],
  };
  const parentArchive = archiveFor([
    { name: "package/package.json", contents: JSON.stringify(parentMetadata) },
    ...inside,
  ]);
  const child = { path: "node_modules/child", name: "child", version: "1.0.0" };
  const grand = {
    path: "node_modules/child/node_modules/grand",
    name: "grand",
    version: "2.0.0",
  };
  const parent = {
    name: "parent",
    version: "1.0.0",
    integrity: `sha512-${createHash("sha512").update(parentArchive).digest("base64")}`,
    bundleDependencies: ["child"],
    bundled: nested ? [child, grand] : [child],
  };
  const parentFiles = verifyBundledArchive(
    parent,
    parentMetadata,
    parentArchive,
  );
  const published = archiveFor(
    publishedFiles || [
      { name: "package/package.json", contents: childMetadata },
      { name: "package/index.js", contents: "module.exports = 1;" },
    ],
  );
  const official = {
    integrity: `sha512-${createHash("sha512").update(published).digest("base64")}`,
  };
  return {
    parent,
    parentFiles,
    child,
    grand,
    published,
    official,
    childMetadata,
    grandMetadata,
  };
}

test("compares every bundled child file with its own published archive", () => {
  const f = childFixture();
  assert.equal(
    verifyBundledChild(
      f.parent,
      f.parentFiles,
      f.child,
      f.official,
      f.published,
    ).size,
    2,
  );
  const tamperedParent = childFixture({
    childFiles: [
      {
        name: "package/node_modules/child/package.json",
        contents: f.childMetadata,
      },
      {
        name: "package/node_modules/child/index.js",
        contents: "module.exports = 'malicious';",
      },
    ],
  });
  assert.throws(
    () =>
      verifyBundledChild(
        tamperedParent.parent,
        tamperedParent.parentFiles,
        tamperedParent.child,
        tamperedParent.official,
        tamperedParent.published,
      ),
    /Bundled child files differ/,
  );
  assert.throws(
    () =>
      verifyBundledChild(
        f.parent,
        f.parentFiles,
        f.child,
        { integrity: "sha512-fake" },
        f.published,
      ),
    /archive SHA-512/,
  );
  for (const publishedFiles of [
    [
      { name: "package/package.json", contents: f.childMetadata },
      { name: "package/index.js", contents: "module.exports = 'altered';" },
    ],
    [{ name: "package/package.json", contents: f.childMetadata }],
    [
      { name: "package/package.json", contents: f.childMetadata },
      {
        name: "package/index.js",
        contents: "module.exports = 1;",
        mode: 0o755,
      },
    ],
    [
      {
        name: "package/package.json",
        contents: JSON.stringify({ name: "other", version: "1.0.0" }),
      },
      { name: "package/index.js", contents: "module.exports = 1;" },
    ],
  ]) {
    const changed = childFixture({ publishedFiles });
    assert.throws(
      () =>
        verifyBundledChild(
          changed.parent,
          changed.parentFiles,
          changed.child,
          changed.official,
          changed.published,
        ),
      /Bundled child (files differ|archive has a different identity)/,
    );
  }
});

test("checks nested bundled children separately without ignoring unknown nested files", () => {
  const f = childFixture({ nested: true });
  assert.equal(
    verifyBundledChild(
      f.parent,
      f.parentFiles,
      f.child,
      f.official,
      f.published,
    ).size,
    2,
  );
  const grandArchive = archiveFor([
    { name: "package/package.json", contents: f.grandMetadata },
    { name: "package/index.js", contents: "module.exports = 2;" },
  ]);
  const grandOfficial = {
    integrity: `sha512-${createHash("sha512").update(grandArchive).digest("base64")}`,
  };
  assert.equal(
    verifyBundledChild(
      f.parent,
      f.parentFiles,
      f.grand,
      grandOfficial,
      grandArchive,
    ).size,
    2,
  );
  const bad = childFixture({
    childFiles: [
      {
        name: "package/node_modules/child/package.json",
        contents: f.childMetadata,
      },
      {
        name: "package/node_modules/child/index.js",
        contents: "module.exports = 1;",
      },
      {
        name: "package/node_modules/child/node_modules/ghost/index.js",
        contents: "unexpected",
      },
    ],
  });
  assert.throws(
    () =>
      verifyBundledChild(
        bad.parent,
        bad.parentFiles,
        bad.child,
        bad.official,
        bad.published,
      ),
    /files differ/,
  );
});
