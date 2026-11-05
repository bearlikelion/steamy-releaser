import assert from "node:assert/strict";
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import {
  lstat,
  mkdir,
  mkdtemp,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { archiveFor } from "./npm-archive-fixtures.mjs";
import { parseTrustedKeys } from "./verify-npm-registry.mjs";
import {
  verifyInstalledBundle,
  verifyInstalledBundledPackages,
} from "./verify-installed-bundles.mjs";

async function fixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), "steam-installed-bundle-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const child = { path: "node_modules/child", name: "child", version: "1.0.0" };
  const parent = { location: "node_modules/parent", bundled: [child] };
  const directory = path.join(root, parent.location, child.path);
  await mkdir(directory, { recursive: true });
  const expected = new Map([
    [
      "package.json",
      { data: Buffer.from('{"name":"child","version":"1.0.0"}') },
    ],
    ["index.js", { data: Buffer.from("module.exports = 1;\n") }],
  ]);
  for (const [name, file] of expected)
    await writeFile(path.join(directory, name), file.data);
  return { root, parent, child, directory, expected };
}

test("compares installed bundled files with the verified archive", async (t) => {
  const f = await fixture(t);
  assert.equal(
    await verifyInstalledBundle(f.root, f.parent, f.child, f.expected),
    2,
  );
  await writeFile(path.join(f.directory, "index.js"), "module.exports = 2;\n");
  await assert.rejects(
    verifyInstalledBundle(f.root, f.parent, f.child, f.expected),
    /differs from signed archive/,
  );
});

test("rejects missing, extra, and linked installed files", async (t) => {
  const f = await fixture(t);
  const file = path.join(f.directory, "index.js");
  await rm(file);
  await assert.rejects(
    verifyInstalledBundle(f.root, f.parent, f.child, f.expected),
    /missing signed files/,
  );
  await writeFile(file, f.expected.get("index.js").data);
  const extra = path.join(f.directory, "extra.js");
  await writeFile(extra, "extra");
  await assert.rejects(
    verifyInstalledBundle(f.root, f.parent, f.child, f.expected),
    /differs from signed archive/,
  );
  await rm(extra);
  await rm(file);
  try {
    await symlink(path.join(f.directory, "package.json"), file);
  } catch (error) {
    if (["EPERM", "EACCES"].includes(error.code))
      return t.skip("symlinks unavailable");
    throw error;
  }
  assert.ok((await lstat(file)).isSymbolicLink());
  await assert.rejects(
    verifyInstalledBundle(f.root, f.parent, f.child, f.expected),
    /contains a link/,
  );
});

test("checks nested bundled children independently", async (t) => {
  const f = await fixture(t);
  const grand = {
    path: "node_modules/child/node_modules/grand",
    name: "grand",
    version: "2.0.0",
  };
  f.parent.bundled.push(grand);
  const directory = path.join(f.root, f.parent.location, grand.path);
  await mkdir(directory, { recursive: true });
  await writeFile(path.join(directory, "index.js"), "grand");
  const expected = new Map([["index.js", { data: Buffer.from("grand") }]]);
  assert.equal(
    await verifyInstalledBundle(f.root, f.parent, f.child, f.expected),
    2,
  );
  assert.equal(
    await verifyInstalledBundle(f.root, f.parent, grand, expected),
    1,
  );
  await writeFile(path.join(directory, "index.js"), "wrong");
  await assert.rejects(
    verifyInstalledBundle(f.root, f.parent, grand, expected),
    /differs from signed archive/,
  );
});

test("permits an absent optional bundled parent without network access", async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "steam-optional-bundle-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const integrity = `sha512-${Buffer.alloc(64).toString("base64")}`;
  const lock = {
    lockfileVersion: 3,
    packages: {
      "": {},
      "node_modules/parent": {
        version: "1.0.0",
        optional: true,
        resolved: "https://registry.npmjs.org/parent/-/parent-1.0.0.tgz",
        integrity,
        bundleDependencies: ["child"],
      },
      "node_modules/parent/node_modules/child": {
        version: "1.0.0",
        optional: true,
        inBundle: true,
      },
    },
  };
  const result = await verifyInstalledBundledPackages(lock, root, {
    fetcher: () => {
      throw new Error("unexpected network");
    },
  });
  assert.deepEqual(result, { packages: 0, files: 0 });
  lock.packages["node_modules/parent"].optional = false;
  await assert.rejects(verifyInstalledBundledPackages(lock, root), {
    code: "ENOENT",
  });
});

test("authenticates archives and checks present optional children after installation", async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "steam-signed-bundle-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const { publicKey, privateKey } = generateKeyPairSync("ec", {
    namedCurve: "prime256v1",
  });
  const keyId = "fixture-key";
  const trustedKeys = parseTrustedKeys({
    keys: [
      {
        keyId,
        keyUsage: "npm:signatures",
        publicKey: {
          keyDetails: "PKIX_ECDSA_P256_SHA_256",
          rawBytes: publicKey
            .export({ format: "der", type: "spki" })
            .toString("base64"),
        },
      },
    ],
  });
  const childMetadata = JSON.stringify({ name: "child", version: "1.0.0" });
  const parentMetadata = {
    name: "parent",
    version: "1.0.0",
    bundledDependencies: ["child"],
  };
  const parentArchive = archiveFor([
    { name: "package/package.json", contents: JSON.stringify(parentMetadata) },
    {
      name: "package/node_modules/child/package.json",
      contents: childMetadata,
    },
    { name: "package/node_modules/child/index.js", contents: "safe" },
  ]);
  const childArchive = archiveFor([
    { name: "package/package.json", contents: childMetadata },
    { name: "package/index.js", contents: "safe" },
  ]);
  function signed(name, archive) {
    const version = "1.0.0";
    const integrity = `sha512-${createHash("sha512").update(archive).digest("base64")}`;
    const tarball = `https://registry.npmjs.org/${name}/-/${name}-${version}.tgz`;
    const sig = sign(
      "sha256",
      Buffer.from(`${name}@${version}:${integrity}`),
      privateKey,
    ).toString("base64");
    return {
      name,
      version,
      dist: { integrity, tarball, signatures: [{ keyid: keyId, sig }] },
    };
  }
  const parent = {
    ...signed("parent", parentArchive),
    bundledDependencies: ["child"],
  };
  const child = signed("child", childArchive);
  const lock = {
    lockfileVersion: 3,
    packages: {
      "": {},
      "node_modules/parent": {
        version: parent.version,
        optional: true,
        resolved: parent.dist.tarball,
        integrity: parent.dist.integrity,
        bundleDependencies: ["child"],
      },
      "node_modules/parent/node_modules/child": {
        version: child.version,
        optional: true,
        inBundle: true,
      },
    },
  };
  const fetcher = async (url) => {
    if (url === parent.dist.tarball) return new Response(parentArchive);
    if (url === child.dist.tarball) return new Response(childArchive);
    if (url === "https://registry.npmjs.org/parent/1.0.0")
      return { ok: true, json: async () => parent };
    if (url === "https://registry.npmjs.org/child/1.0.0")
      return { ok: true, json: async () => child };
    throw new Error(`Unexpected URL: ${url}`);
  };
  const identities = [];
  const options = {
    trustedKeys,
    fetcher,
    historicalSignatures: { keys: [] },
    onVerifiedChild: (entry) => identities.push(entry),
  };
  const parentDir = path.join(root, "node_modules", "parent");
  const childDir = path.join(parentDir, "node_modules", "child");
  await mkdir(parentDir, { recursive: true });
  assert.deepEqual(await verifyInstalledBundledPackages(lock, root, options), {
    packages: 0,
    files: 0,
  });
  await mkdir(childDir, { recursive: true });
  await writeFile(path.join(childDir, "package.json"), childMetadata);
  await assert.rejects(
    verifyInstalledBundledPackages(lock, root, options),
    /missing signed files/,
  );
  await writeFile(path.join(childDir, "index.js"), "evil");
  await assert.rejects(
    verifyInstalledBundledPackages(lock, root, options),
    /differs from signed archive/,
  );
  assert.deepEqual(identities, []);
  await writeFile(path.join(childDir, "index.js"), "safe");
  assert.deepEqual(await verifyInstalledBundledPackages(lock, root, options), {
    packages: 1,
    files: 2,
  });
  assert.deepEqual(identities, [
    {
      name: "child",
      version: "1.0.0",
      resolved: child.dist.tarball,
      integrity: child.dist.integrity,
      location: "node_modules/parent/node_modules/child",
    },
  ]);
});
