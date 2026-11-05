import assert from "node:assert/strict";
import { test } from "node:test";
import {
  parseUnattestedPackages,
  verifyLockedNpmProvenance,
} from "./verify-npm-locked-provenance.mjs";

const version = "1.0.0";
const integrity = `sha512-${Buffer.alloc(64, 7).toString("base64")}`;
const repository = "https://github.com/example/project";
const pin = { repository, repositoryId: "123", ownerId: "456" };

function fixture() {
  const parentLocation = "node_modules/parent";
  const childLocation = `${parentLocation}/node_modules/child`;
  const lock = {
    lockfileVersion: 3,
    packages: {
      "": {},
      [parentLocation]: {
        version,
        integrity,
        optional: true,
        resolved: `https://registry.npmjs.org/parent/-/parent-${version}.tgz`,
        bundleDependencies: ["child"],
      },
      [childLocation]: { version, inBundle: true, optional: true },
      "node_modules/other": {
        version,
        integrity,
        resolved: `https://registry.npmjs.org/other/-/other-${version}.tgz`,
      },
    },
  };
  const publishers = {
    publishers: [
      { name: "parent", ...pin },
      { name: "child", ...pin },
    ],
  };
  const manifest = (name) => ({
    dist: {
      attestations: {
        url: `https://registry.npmjs.org/-/npm/v1/attestations/${name}@${version}`,
        provenance: {},
      },
    },
  });
  const parentManifest = manifest("parent");
  const childManifest = manifest("child");
  const otherManifest = { dist: {} };
  const childPublished = {
    name: "child",
    version,
    integrity,
    resolved: `https://registry.npmjs.org/child/-/child-${version}.tgz`,
  };
  const requests = [];
  const verifications = [];
  const tools = {
    purl: (name, v) => `pkg:npm/${name}@${v}`,
    verify: async (_bundle, source, commit, ref) =>
      verifications.push({ source, commit, ref }),
  };
  const fetcher = async (url, options) => {
    requests.push({ url, redirect: options.redirect });
    const name = url.includes("/parent@") ? "parent" : "child";
    const statement = {
      _type: "https://in-toto.io/Statement/v1",
      predicateType: "https://slsa.dev/provenance/v1",
      subject: [
        {
          name: `pkg:npm/${name}@${version}`,
          digest: {
            sha512: Buffer.from(integrity.slice(7), "base64").toString("hex"),
          },
        },
      ],
      predicate: {
        buildDefinition: {
          buildType:
            "https://slsa-framework.github.io/github-actions-buildtypes/workflow/v1",
          externalParameters: {
            workflow: { repository, ref: "refs/heads/main" },
          },
          internalParameters: {
            github: {
              repository_id: pin.repositoryId,
              repository_owner_id: pin.ownerId,
            },
          },
          resolvedDependencies: [
            {
              uri: `git+${repository}@refs/heads/main`,
              digest: { gitCommit: "a".repeat(40) },
            },
          ],
        },
      },
    };
    return new Response(
      JSON.stringify({
        attestations: [
          {
            predicateType: "https://slsa.dev/provenance/v1",
            bundle: {
              dsseEnvelope: {
                payloadType: "application/vnd.in-toto+json",
                payload: Buffer.from(JSON.stringify(statement)).toString(
                  "base64",
                ),
              },
            },
          },
        ],
      }),
    );
  };
  const verifyPackages = async (packages, callbacks) => {
    const parent = packages.find((x) => x.name === "parent");
    const other = packages.find((x) => x.name === "other");
    await callbacks.onVerifiedEntry(parent, parentManifest);
    await callbacks.onVerifiedEntry(other, otherManifest);
    await callbacks.onBundledChild(
      parent,
      parent.bundled[0],
      new Map(),
      childPublished,
      childManifest,
    );
  };
  return {
    lock,
    publishers,
    tools,
    options: {
      fetcher,
      verifyPackages,
      unattested: { packages: [{ name: "other", version, integrity }] },
    },
    requests,
    verifications,
    parentManifest,
    otherManifest,
    childManifest,
    childPublished,
  };
}

test("verifies source identities for all locked packages, including optional bundled children", async () => {
  const f = fixture();
  assert.deepEqual(
    await verifyLockedNpmProvenance(f.lock, f.publishers, f.tools, f.options),
    { pinned: 2, withoutProvenance: 1 },
  );
  assert.equal(f.verifications.length, 2);
  assert.deepEqual(
    f.requests.map((x) => x.redirect),
    ["error", "error"],
  );
});

test("requires an exact baseline for packages without provenance", async () => {
  const f = fixture();
  f.options.unattested.packages[0].integrity = `sha512-${Buffer.alloc(64, 8).toString("base64")}`;
  await assert.rejects(
    verifyLockedNpmProvenance(f.lock, f.publishers, f.tools, f.options),
    /Unreviewed npm package without provenance: other/,
  );
  f.options.unattested.packages[0].integrity = integrity;
  f.options.unattested.packages[0].version = "2.0.0";
  await assert.rejects(
    verifyLockedNpmProvenance(f.lock, f.publishers, f.tools, f.options),
    /Unreviewed npm package without provenance: other/,
  );
  const stale = fixture();
  stale.options.unattested.packages.push({ name: "old", version, integrity });
  await assert.rejects(
    verifyLockedNpmProvenance(
      stale.lock,
      stale.publishers,
      stale.tools,
      stale.options,
    ),
    /baseline does not match/,
  );
  const incomplete = fixture();
  const verifyPackages = incomplete.options.verifyPackages;
  incomplete.options.verifyPackages = (packages, callbacks) =>
    verifyPackages(packages, {
      ...callbacks,
      onVerifiedEntry: (entry, manifest) =>
        entry.name === "other"
          ? undefined
          : callbacks.onVerifiedEntry(entry, manifest),
    });
  await assert.rejects(
    verifyLockedNpmProvenance(
      incomplete.lock,
      incomplete.publishers,
      incomplete.tools,
      incomplete.options,
    ),
    /baseline does not match/,
  );
});

test("requires the separately authenticated digest for unattested bundled children", async () => {
  const f = fixture();
  f.publishers.publishers.pop();
  f.childManifest.dist = {};
  f.options.unattested.packages.push({ name: "child", version, integrity });
  assert.deepEqual(
    await verifyLockedNpmProvenance(f.lock, f.publishers, f.tools, f.options),
    { pinned: 1, withoutProvenance: 2 },
  );
  f.childPublished.integrity = `sha512-${Buffer.alloc(64, 8).toString("base64")}`;
  await assert.rejects(
    verifyLockedNpmProvenance(f.lock, f.publishers, f.tools, f.options),
    /Unreviewed npm package without provenance: child/,
  );
});

test("rejects invalid or duplicate unattested baseline records", () => {
  const entry = { name: "other", version, integrity };
  assert.throws(() => parseUnattestedPackages({}), /Invalid unattested/);
  assert.throws(
    () => parseUnattestedPackages({ packages: [entry, entry] }),
    /Duplicate unattested/,
  );
  assert.throws(
    () =>
      parseUnattestedPackages({
        packages: [{ ...entry, integrity: "sha512-bad" }],
      }),
    /Invalid unattested/,
  );
});

test("rejects unrelated attestation URLs and unpinned newly available provenance", async () => {
  const f = fixture();
  f.parentManifest.dist.attestations.url = `https://evil.invalid/-/npm/v1/attestations/parent@${version}`;
  await assert.rejects(
    verifyLockedNpmProvenance(f.lock, f.publishers, f.tools, f.options),
    /Attestation URL does not match/,
  );
  f.parentManifest.dist.attestations.url = `https://registry.npmjs.org/-/npm/v1/attestations/other@${version}`;
  await assert.rejects(
    verifyLockedNpmProvenance(f.lock, f.publishers, f.tools, f.options),
    /Attestation URL does not match/,
  );
  const g = fixture();
  g.otherManifest.dist.attestations = { provenance: {} };
  await assert.rejects(
    verifyLockedNpmProvenance(g.lock, g.publishers, g.tools, g.options),
    /Unpinned npm package now has provenance/,
  );
});

test("bounds the attestation response before parsing it", async () => {
  const f = fixture();
  f.options.fetcher = async () =>
    new Response(Buffer.alloc(4 * 1024 * 1024 + 1));
  await assert.rejects(
    verifyLockedNpmProvenance(f.lock, f.publishers, f.tools, f.options),
    /exceeds the size limit/,
  );
});

test("rejects an incomplete scan and a different signed bundled-child digest", async () => {
  const f = fixture();
  f.options.verifyPackages = async () => {};
  await assert.rejects(
    verifyLockedNpmProvenance(f.lock, f.publishers, f.tools, f.options),
    /Not all pinned npm packages/,
  );
  const g = fixture();
  g.childPublished.integrity = `sha512-${Buffer.alloc(64, 8).toString("base64")}`;
  await assert.rejects(
    verifyLockedNpmProvenance(g.lock, g.publishers, g.tools, g.options),
    /subject does not match/,
  );
});
