import assert from "node:assert/strict";
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import {
  mkdirSync,
  mkdtempSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { archiveFor } from "./npm-archive-fixtures.mjs";
import {
  activeNpmDirectory,
  bundledNpmDirectory,
  loadBundledTuf,
  loadRegistryKeyTarget,
  parseHistoricalSignatures,
  parseTrustedKeys,
  verifyRegistryDelegation,
  verifyRegistryTargetRole,
  verifyRegistryPackages,
} from "./verify-npm-registry.mjs";

test("requires the reviewed TUF registry delegation topology", () => {
  const role = {
    name: "registry.npmjs.org",
    terminating: true,
    paths: ["registry.npmjs.org/*"],
  };
  const client = (delegations) => ({
    updater: { trustedSet: { targets: { signed: { delegations } } } },
  });
  const delegations = { roles: { "registry.npmjs.org": role } };
  assert.doesNotThrow(() => verifyRegistryDelegation(client(delegations)));
  for (const changed of [
    undefined,
    { roles: {} },
    { roles: { ...delegations.roles, other: { name: "other", paths: ["*"] } } },
    { roles: { "registry.npmjs.org": { ...role, paths: ["*"] } } },
    { roles: { "registry.npmjs.org": { ...role, terminating: false } } },
    { roles: { "registry.npmjs.org": { ...role, pathHashPrefixes: ["ab"] } } },
    { ...delegations, succinctRoles: {} },
  ])
    assert.throws(
      () => verifyRegistryDelegation(client(changed)),
      /Unexpected npm TUF delegation/,
    );
});

test("requires the registry keys target in the reviewed role without nested delegations", () => {
  const signed = { targets: { "registry.npmjs.org/keys.json": {} } };
  const target = signed.targets["registry.npmjs.org/keys.json"];
  const client = (role) => ({
    updater: {
      trustedSet: {
        getRole: (name) => (name === "registry.npmjs.org" ? role : undefined),
      },
    },
  });
  assert.doesNotThrow(() =>
    verifyRegistryTargetRole(client({ signed }), target),
  );
  assert.throws(
    () => verifyRegistryTargetRole(client({ signed }), {}),
    /Unexpected npm TUF target role/,
  );
  for (const role of [
    undefined,
    { signed: { targets: {} } },
    { signed: { targets: [], delegations: null } },
    { signed: { ...signed, delegations: { roles: {} } } },
  ])
    assert.throws(
      () => verifyRegistryTargetRole(client(role), target),
      /Unexpected npm TUF target role/,
    );
});

test("checks the resolved TUF role before downloading registry keys", async () => {
  const events = [];
  const role = { signed: { targets: { "registry.npmjs.org/keys.json": {} } } };
  const delegations = {
    roles: {
      "registry.npmjs.org": {
        name: "registry.npmjs.org",
        terminating: true,
        paths: ["registry.npmjs.org/*"],
      },
    },
  };
  const store = { targets: { signed: { delegations } }, getRole: () => role };
  const client = {
    updater: {
      trustedSet: store,
      getTargetInfo: async () => {
        events.push("metadata");
        return role.signed.targets["registry.npmjs.org/keys.json"];
      },
    },
    getTarget: async () => {
      events.push("target");
      return "keys";
    },
  };
  assert.equal(await loadRegistryKeyTarget(client), "keys");
  assert.deepEqual(events, ["metadata", "target"]);
  events.length = 0;
  role.signed.delegations = { roles: {} };
  await assert.rejects(
    loadRegistryKeyTarget(client),
    /Unexpected npm TUF target role/,
  );
  assert.deepEqual(events, ["metadata"]);
  events.length = 0;
  store.targets.signed.delegations = { roles: {} };
  await assert.rejects(
    loadRegistryKeyTarget(client),
    /Unexpected npm TUF delegation/,
  );
  assert.deepEqual(events, []);
});

test("uses the verified npm directory for registry checks when activated", () => {
  assert.equal(activeNpmDirectory({}), bundledNpmDirectory());
  assert.equal(
    activeNpmDirectory({ STEAM_RELEASER_NPM_DIR: "./verified-npm" }),
    path.resolve("./verified-npm"),
  );
});

const { publicKey, privateKey } = generateKeyPairSync("ec", {
  namedCurve: "prime256v1",
});
const keyId = "test-registry-key";
const entry = {
  name: "@scope/example",
  version: "1.2.3",
  resolved: "https://registry.npmjs.org/@scope/example/-/example-1.2.3.tgz",
  integrity: `sha512-${Buffer.alloc(64, 42).toString("base64")}`,
};
const message = Buffer.from(
  `${entry.name}@${entry.version}:${entry.integrity}`,
);
const manifest = {
  name: entry.name,
  version: entry.version,
  dist: {
    tarball: entry.resolved,
    integrity: entry.integrity,
    signatures: [
      {
        keyid: keyId,
        sig: sign("sha256", message, privateKey).toString("base64"),
      },
    ],
  },
};

function trustedKeys(end, start) {
  return parseTrustedKeys({
    keys: [
      {
        keyId,
        keyUsage: "npm:signatures",
        publicKey: {
          keyDetails: "PKIX_ECDSA_P256_SHA_256",
          rawBytes: publicKey
            .export({ format: "der", type: "spki" })
            .toString("base64"),
          validFor: { ...(end ? { end } : {}), ...(start ? { start } : {}) },
        },
      },
    ],
  });
}

function response(value) {
  return { ok: true, json: async () => value };
}

test("does not load a TUF client outside npm's bundled modules", (t) => {
  const root = mkdtempSync(path.join(os.tmpdir(), "steam-releaser-tuf-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const npm = path.join(root, "npm");
  const scope = path.join(npm, "node_modules", "@sigstore");
  const outside = path.join(root, "outside");
  mkdirSync(scope, { recursive: true });
  mkdirSync(outside);
  writeFileSync(path.join(npm, "package.json"), "{}");
  try {
    symlinkSync(outside, path.join(scope, "tuf"), "dir");
  } catch (error) {
    if (error.code === "EPERM" || error.code === "EACCES") {
      t.skip("directory symlinks are unavailable");
      return;
    }
    throw error;
  }
  assert.throws(() => loadBundledTuf(npm), /outside npm's bundled modules/);
});

test("matches and authenticates locked metadata using a trusted registry key", async () => {
  await verifyRegistryPackages([entry], {
    trustedKeys: trustedKeys(),
    fetcher: async (url, options) => {
      assert.equal(url, "https://registry.npmjs.org/%40scope%2Fexample/1.2.3");
      assert.equal(options.redirect, "error");
      return response(manifest);
    },
  });
});

test("authenticates the parent and child signatures before comparing bundled bytes", async () => {
  const childMetadata = JSON.stringify({ name: "child", version: "2.0.0" });
  const archive = archiveFor([
    {
      name: "package/package.json",
      contents: JSON.stringify({
        name: entry.name,
        version: entry.version,
        bundledDependencies: ["child"],
      }),
    },
    {
      name: "package/node_modules/child/package.json",
      contents: childMetadata,
    },
  ]);
  const childArchive = archiveFor([
    { name: "package/package.json", contents: childMetadata },
  ]);
  const childEntry = {
    name: "child",
    version: "2.0.0",
    resolved: "https://registry.npmjs.org/child/-/child-2.0.0.tgz",
    integrity: `sha512-${createHash("sha512").update(childArchive).digest("base64")}`,
  };
  const childManifest = {
    name: childEntry.name,
    version: childEntry.version,
    dist: {
      tarball: childEntry.resolved,
      integrity: childEntry.integrity,
      signatures: [
        {
          keyid: keyId,
          sig: sign(
            "sha256",
            Buffer.from(
              `${childEntry.name}@${childEntry.version}:${childEntry.integrity}`,
            ),
            privateKey,
          ).toString("base64"),
        },
      ],
    },
  };
  const parent = {
    ...entry,
    integrity: `sha512-${createHash("sha512").update(archive).digest("base64")}`,
    bundleDependencies: ["child"],
    bundled: [{ path: "node_modules/child", name: "child", version: "2.0.0" }],
  };
  const signedMessage = Buffer.from(
    `${parent.name}@${parent.version}:${parent.integrity}`,
  );
  const signedManifest = {
    name: parent.name,
    version: parent.version,
    bundledDependencies: ["child"],
    dist: {
      tarball: parent.resolved,
      integrity: parent.integrity,
      signatures: [
        {
          keyid: keyId,
          sig: sign("sha256", signedMessage, privateKey).toString("base64"),
        },
      ],
    },
  };
  let archiveRequests = 0;
  let childArchiveRequests = 0;
  let currentChildArchive = childArchive;
  let currentManifest = signedManifest;
  let currentChildManifest = childManifest;
  const fetcher = async (url) => {
    if (url === parent.resolved) {
      archiveRequests++;
      return new Response(archive);
    }
    if (url === childEntry.resolved) {
      childArchiveRequests++;
      return new Response(currentChildArchive);
    }
    if (url === "https://registry.npmjs.org/child/2.0.0") {
      return response(currentChildManifest);
    }
    return response(currentManifest);
  };
  const checkedChildren = [];
  const checkedParents = [];
  const options = {
    trustedKeys: trustedKeys(),
    fetcher,
    onVerifiedEntry: async (parentEntry, metadata) => {
      checkedParents.push([parentEntry.name, metadata.dist.integrity]);
    },
    onBundledChild: async (parentEntry, child, files, published, metadata) => {
      checkedChildren.push([
        parentEntry.name,
        child.name,
        files.size,
        published.integrity,
        metadata.dist.integrity,
      ]);
    },
  };
  await verifyRegistryPackages([parent], options);
  assert.equal(archiveRequests, 1);
  assert.equal(childArchiveRequests, 1);
  assert.deepEqual(checkedParents, [[parent.name, parent.integrity]]);
  assert.deepEqual(checkedChildren, [
    [parent.name, "child", 1, childEntry.integrity, childEntry.integrity],
  ]);
  const forged = structuredClone(parent);
  forged.bundled[0].version = "2.99.0";
  await assert.rejects(
    verifyRegistryPackages([forged], options),
    /Bundled package identity does not match/,
  );
  currentChildManifest = {
    ...childManifest,
    dist: { ...childManifest.dist, signatures: [] },
  };
  childArchiveRequests = 0;
  await assert.rejects(
    verifyRegistryPackages([parent], options),
    /No valid trusted registry signature/,
  );
  assert.equal(childArchiveRequests, 0);
  currentChildManifest = childManifest;
  currentChildArchive = Buffer.from("not the signed child archive");
  await assert.rejects(
    verifyRegistryPackages([parent], options),
    /Bundled child archive SHA-512 does not match/,
  );
  currentManifest = {
    ...signedManifest,
    dist: { ...signedManifest.dist, signatures: [] },
  };
  archiveRequests = 0;
  await assert.rejects(
    verifyRegistryPackages([parent], options),
    /No valid trusted registry signature/,
  );
  assert.equal(archiveRequests, 0);
});

test("rejects registry metadata that differs from the lockfile", async () => {
  for (const changed of [
    { ...manifest, name: "@scope/other" },
    { ...manifest, version: "1.2.4" },
    {
      ...manifest,
      dist: { ...manifest.dist, tarball: "https://example.invalid/other.tgz" },
    },
    { ...manifest, dist: { ...manifest.dist, integrity: "sha512-other" } },
  ]) {
    await assert.rejects(
      verifyRegistryPackages([entry], {
        trustedKeys: trustedKeys(),
        fetcher: async () => response(changed),
      }),
    );
  }
});

test("rejects unsigned, forged, or untrusted matching metadata", async () => {
  for (const signatures of [
    [],
    [{ keyid: keyId, sig: Buffer.alloc(64).toString("base64") }],
    [{ ...manifest.dist.signatures[0], keyid: "unknown-key" }],
  ]) {
    const changed = { ...manifest, dist: { ...manifest.dist, signatures } };
    let visited = false;
    await assert.rejects(
      verifyRegistryPackages([entry], {
        trustedKeys: trustedKeys(),
        fetcher: async () => response(changed),
        onVerifiedEntry: () => {
          visited = true;
        },
      }),
      /No valid trusted registry signature/,
    );
    assert.equal(visited, false);
  }
});

test("rejects matching lockfile and registry digests with a signature for different bytes", async () => {
  const changedEntry = { ...entry, integrity: "sha512-attacker" };
  const changedManifest = {
    ...manifest,
    dist: { ...manifest.dist, integrity: changedEntry.integrity },
  };
  await assert.rejects(
    verifyRegistryPackages([changedEntry], {
      trustedKeys: trustedKeys(),
      fetcher: async () => response(changedManifest),
    }),
    /No valid trusted registry signature/,
  );
});

test("rejects an unapproved expired signature even with a backdated publication time", async () => {
  const keys = trustedKeys("2025-01-29T00:00:00.000Z");
  const packument = {
    name: entry.name,
    versions: { [entry.version]: manifest },
    time: { [entry.version]: "2024-01-01T00:00:00.000Z" },
  };
  const requested = [];
  const fetcher = async (url) => (
    requested.push(url),
    response(url.endsWith("/1.2.3") ? manifest : packument)
  );
  await assert.rejects(
    verifyRegistryPackages([entry], {
      trustedKeys: keys,
      historicalSignatures: { keys: [] },
      fetcher,
    }),
    /Unapproved expired npm registry signature/,
  );
  assert.deepEqual(requested, [
    "https://registry.npmjs.org/%40scope%2Fexample/1.2.3",
  ]);
});

test("requires an exact historical key, expiry, package, version, and digest", async () => {
  const expiry = "2025-01-29T00:00:00.000Z";
  const approval = {
    keys: [
      {
        keyId,
        expiresAt: expiry,
        packages: [
          {
            name: entry.name,
            version: entry.version,
            integrity: entry.integrity,
          },
        ],
      },
    ],
  };
  const options = {
    trustedKeys: trustedKeys(expiry),
    historicalSignatures: approval,
    fetcher: async () => response(manifest),
  };
  await verifyRegistryPackages([entry], options);
  for (const mutated of [
    (v) => (v.keys[0].keyId = "different-key"),
    (v) => (v.keys[0].expiresAt = "2025-01-28T00:00:00.000Z"),
    (v) => (v.keys[0].packages[0].name = "@scope/other"),
    (v) => (v.keys[0].packages[0].version = "1.2.4"),
    (v) =>
      (v.keys[0].packages[0].integrity = `sha512-${Buffer.alloc(64, 13).toString("base64")}`),
  ]) {
    const changed = structuredClone(approval);
    mutated(changed);
    await assert.rejects(
      verifyRegistryPackages([entry], {
        ...options,
        historicalSignatures: changed,
      }),
      /Unapproved expired npm registry signature/,
    );
  }
  const forgedEntry = {
    ...entry,
    integrity: `sha512-${Buffer.alloc(64, 13).toString("base64")}`,
  };
  const forgedManifest = {
    ...manifest,
    dist: {
      ...manifest.dist,
      integrity: forgedEntry.integrity,
      signatures: [
        {
          keyid: keyId,
          sig: sign(
            "sha256",
            Buffer.from(
              `${forgedEntry.name}@${forgedEntry.version}:${forgedEntry.integrity}`,
            ),
            privateKey,
          ).toString("base64"),
        },
      ],
    },
  };
  await assert.rejects(
    verifyRegistryPackages([forgedEntry], {
      ...options,
      fetcher: async () => response(forgedManifest),
    }),
    /Unapproved expired npm registry signature/,
  );
  await assert.rejects(
    verifyRegistryPackages([entry], {
      ...options,
      trustedKeys: new Map(),
    }),
    /No valid trusted registry signature/,
  );
});

test("rejects malformed or duplicate historical approvals", () => {
  const valid = {
    keys: [
      {
        keyId,
        expiresAt: "2025-01-29T00:00:00.000Z",
        packages: [
          {
            name: entry.name,
            version: entry.version,
            integrity: entry.integrity,
          },
        ],
      },
    ],
  };
  assert.equal(parseHistoricalSignatures(valid).size, 1);
  for (const mutated of [
    (v) => (v.keys[0].expiresAt = "not-a-date"),
    (v) => (v.keys[0].packages[0].integrity = "sha512-invalid"),
    (v) => v.keys[0].packages.push(v.keys[0].packages[0]),
    (v) => v.keys.push(v.keys[0]),
  ]) {
    const changed = structuredClone(valid);
    mutated(changed);
    assert.throws(() => parseHistoricalSignatures(changed));
  }
});

test("accepts only npm signature keys during their validity period", async () => {
  const options = {
    trustedKeys: trustedKeys(
      "2027-01-01T00:00:00.000Z",
      "2026-01-01T00:00:00.000Z",
    ),
    historicalSignatures: { keys: [] },
    fetcher: async () => response(manifest),
  };
  await verifyRegistryPackages([entry], {
    ...options,
    now: Date.parse("2026-01-01T00:00:00.000Z"),
  });
  await assert.rejects(
    verifyRegistryPackages([entry], {
      ...options,
      now: Date.parse("2025-12-31T23:59:59.999Z"),
    }),
    /not yet valid/,
  );
  await assert.rejects(
    verifyRegistryPackages([entry], {
      ...options,
      now: Date.parse("2027-01-01T00:00:00.000Z"),
    }),
    /Unapproved expired npm registry signature/,
  );
  const rawBytes = publicKey
    .export({ format: "der", type: "spki" })
    .toString("base64");
  assert.throws(
    () =>
      parseTrustedKeys({
        keys: [
          {
            keyId,
            keyUsage: "npm:attestations",
            publicKey: { keyDetails: "PKIX_ECDSA_P256_SHA_256", rawBytes },
          },
        ],
      }),
    /No supported npm signing keys/,
  );
  assert.throws(
    () =>
      parseTrustedKeys({
        keys: [
          {
            keyId,
            keyUsage: "npm:signatures",
            publicKey: {
              keyDetails: "PKIX_ECDSA_P256_SHA_256",
              rawBytes,
              validFor: {
                start: "2027-01-01T00:00:00Z",
                end: "2026-01-01T00:00:00Z",
              },
            },
          },
        ],
      }),
    /validity period/,
  );
});

test("fails closed when trusted keys or registry metadata are unavailable", async () => {
  assert.throws(
    () => parseTrustedKeys({ keys: [] }),
    /No supported npm signing keys/,
  );
  await assert.rejects(
    verifyRegistryPackages([entry], {
      trustedKeys: trustedKeys(),
      fetcher: async () => ({ ok: false, status: 503 }),
    }),
    /Registry returned HTTP 503/,
  );
});

test("retries a transient registry failure", async () => {
  let calls = 0;
  await verifyRegistryPackages([entry], {
    trustedKeys: trustedKeys(),
    fetcher: async () => {
      calls++;
      return calls === 1 ? { ok: false, status: 503 } : response(manifest);
    },
  });
  assert.equal(calls, 2);
});
