import assert from "node:assert/strict";
import { test } from "node:test";
import {
  parsePublisherPins,
  verifyNpmProvenance,
} from "./verify-npm-provenance.mjs";

const repository = "https://github.com/example/project";
const repositoryId = "123456";
const ownerId = "7890";
const version = "1.2.3";
const commit = "a".repeat(40);
const integrity = `sha512-${Buffer.alloc(64, 3).toString("base64")}`;

function fixture() {
  const location = "node_modules/example";
  const lock = {
    lockfileVersion: 3,
    packages: {
      "": {},
      [location]: {
        version,
        integrity,
        resolved: `https://registry.npmjs.org/example/-/example-${version}.tgz`,
      },
      "node_modules/other": {
        version,
        integrity,
        resolved: `https://registry.npmjs.org/other/-/other-${version}.tgz`,
      },
    },
  };
  const inventory = [
    { location, name: "example", version },
    { location: "node_modules/other", name: "other", version },
  ];
  const statement = {
    _type: "https://in-toto.io/Statement/v1",
    predicateType: "https://slsa.dev/provenance/v1",
    subject: [
      {
        name: `pkg:npm/example@${version}`,
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
          github: { repository_id: repositoryId, repository_owner_id: ownerId },
        },
        resolvedDependencies: [
          {
            uri: `git+${repository}@refs/heads/main`,
            digest: { gitCommit: commit },
          },
        ],
      },
    },
  };
  const envelope = { payloadType: "application/vnd.in-toto+json", payload: "" };
  const encode = () => {
    envelope.payload = Buffer.from(JSON.stringify(statement)).toString(
      "base64",
    );
  };
  encode();
  const report = {
    invalid: [],
    missing: [],
    verified: [
      {
        name: "example",
        version,
        location,
        registry: "https://registry.npmjs.org/",
        attestationBundles: [
          {
            predicateType: "https://slsa.dev/provenance/v1",
            bundle: { dsseEnvelope: envelope },
          },
        ],
      },
    ],
  };
  const publishers = {
    publishers: [{ name: "example", repository, repositoryId, ownerId }],
  };
  const calls = [];
  const tools = {
    purl: (name, release) => `pkg:npm/${name}@${release}`,
    verify: async (bundle, pin, revision, ref) => {
      calls.push({ bundle, pin, revision, ref });
    },
  };
  return {
    lock,
    inventory,
    statement,
    report,
    publishers,
    tools,
    calls,
    encode,
  };
}

test("binds the locked package and source commit to its pinned signer", async () => {
  const f = fixture();
  assert.deepEqual(await verifyNpmProvenance(f), {
    pinned: 1,
    otherAttested: 0,
    withoutAttestations: 1,
  });
  assert.equal(f.calls.length, 1);
  assert.equal(f.calls[0].pin.repository, repository);
  assert.equal(f.calls[0].pin.repositoryId, repositoryId);
  assert.equal(f.calls[0].pin.ownerId, ownerId);
  assert.equal(f.calls[0].revision, commit);
  assert.equal(f.calls[0].ref, "refs/heads/main");
});

test("rejects provenance for a different package digest or repository", async () => {
  const f = fixture();
  f.statement.subject[0].digest.sha512 = "b".repeat(128);
  f.encode();
  await assert.rejects(verifyNpmProvenance(f), /subject does not match/);
  f.statement.subject[0].digest.sha512 = Buffer.from(
    integrity.slice(7),
    "base64",
  ).toString("hex");
  f.statement.predicate.buildDefinition.externalParameters.workflow.repository =
    "https://github.com/attacker/project";
  f.encode();
  await assert.rejects(
    verifyNpmProvenance(f),
    /source repository does not match/,
  );
  f.statement.predicate.buildDefinition.externalParameters.workflow.repository =
    repository;
  f.statement.predicate.buildDefinition.internalParameters.github.repository_id =
    "99999";
  f.encode();
  await assert.rejects(
    verifyNpmProvenance(f),
    /source repository does not match/,
  );
});

test("rejects missing provenance, ambiguous source commits, and invalid certificates", async () => {
  const f = fixture();
  f.report.verified = [];
  await assert.rejects(verifyNpmProvenance(f), /Missing pinned provenance/);
  const g = fixture();
  g.statement.predicate.buildDefinition.resolvedDependencies.push(
    g.statement.predicate.buildDefinition.resolvedDependencies[0],
  );
  g.encode();
  await assert.rejects(verifyNpmProvenance(g), /one source commit/);
  const h = fixture();
  h.tools.verify = async () => {
    throw new Error("wrong certificate identity");
  };
  await assert.rejects(verifyNpmProvenance(h), /wrong certificate identity/);
});

test("binds a bundled child's attestation to its independently verified archive digest", async () => {
  const f = fixture();
  const location = "node_modules/parent/node_modules/example";
  delete f.lock.packages["node_modules/example"];
  f.lock.packages["node_modules/parent"] = {
    version,
    integrity,
    resolved: `https://registry.npmjs.org/parent/-/parent-${version}.tgz`,
    bundleDependencies: ["example"],
  };
  f.lock.packages[location] = { version, inBundle: true };
  f.inventory[0].location = location;
  f.report.verified[0].location = location;
  f.verifiedBundled = [{ location, name: "example", version, integrity }];
  assert.equal((await verifyNpmProvenance(f)).pinned, 1);
  f.verifiedBundled[0].integrity = `sha512-${Buffer.alloc(64, 8).toString("base64")}`;
  await assert.rejects(verifyNpmProvenance(f), /subject does not match/);
  f.verifiedBundled[0].location = "node_modules/other";
  await assert.rejects(
    verifyNpmProvenance(f),
    /Invalid verified bundled package identity/,
  );
});

test("rejects unrelated audit records, invalid audit results, and duplicate pins", async () => {
  const f = fixture();
  f.report.verified[0].location = "node_modules/not-installed";
  await assert.rejects(
    verifyNpmProvenance(f),
    /does not match the installed inventory/,
  );
  const g = fixture();
  g.report.invalid.push({ name: "example" });
  await assert.rejects(
    verifyNpmProvenance(g),
    /Invalid npm signature audit report/,
  );
  assert.throws(
    () =>
      parsePublisherPins({
        publishers: [
          { name: "example", repository, repositoryId, ownerId },
          { name: "example", repository, repositoryId, ownerId },
        ],
      }),
    /duplicate/,
  );
  assert.throws(
    () =>
      parsePublisherPins({
        publishers: [
          { name: "example", repository, repositoryId, ownerId },
          { name: "another", repository, repositoryId: "987", ownerId },
        ],
      }),
    /Conflicting npm source repository identity/,
  );
});
