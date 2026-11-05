import { lstat, readFile } from "node:fs/promises";
import { readFileSync, realpathSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { verifyInstalledBundledPackages } from "./verify-installed-bundles.mjs";
import { verifyInstalledPackages } from "./verify-installed-npm.mjs";
import { defaultLockPath, validateLockfile } from "./verify-npm-lock.mjs";
import { activeNpmDirectory } from "./verify-npm-registry.mjs";

const pinsPath = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  "npm-source-publishers.json",
);
const provenanceType = "https://slsa.dev/provenance/v1";
const maxReportBytes = 32 * 1024 * 1024;

export function parsePublisherPins(data) {
  if (!Array.isArray(data?.publishers) || !data.publishers.length)
    throw new Error("Expected npm source publisher pins");
  const pins = new Map();
  const repositories = new Map();
  for (const pin of data.publishers) {
    if (
      typeof pin?.name !== "string" ||
      !/^(?:@[a-z0-9][a-z0-9._~-]*\/)?[a-z0-9][a-z0-9._~-]*$/.test(pin.name) ||
      typeof pin.repository !== "string" ||
      !/^https:\/\/github\.com\/[A-Za-z0-9_-][A-Za-z0-9._-]*\/[A-Za-z0-9_-][A-Za-z0-9._-]*$/.test(
        pin.repository,
      ) ||
      pin.repository.endsWith(".git") ||
      typeof pin.repositoryId !== "string" ||
      !/^[1-9][0-9]{0,19}$/.test(pin.repositoryId) ||
      typeof pin.ownerId !== "string" ||
      !/^[1-9][0-9]{0,19}$/.test(pin.ownerId) ||
      pins.has(pin.name)
    )
      throw new Error(
        `Invalid or duplicate npm source publisher: ${pin?.name}`,
      );
    const key = pin.repository.toLowerCase();
    const identity = `${pin.repositoryId}:${pin.ownerId}`;
    if (repositories.has(key) && repositories.get(key) !== identity)
      throw new Error(
        `Conflicting npm source repository identity: ${pin.repository}`,
      );
    repositories.set(key, identity);
    pins.set(pin.name, pin);
  }
  return pins;
}

function derUtf8(value) {
  const text = Buffer.from(value, "utf8");
  if (text.length < 128)
    return Buffer.concat([Buffer.from([12, text.length]), text]);
  if (text.length < 256)
    return Buffer.concat([Buffer.from([12, 129, text.length]), text]);
  if (text.length < 65536)
    return Buffer.concat([
      Buffer.from([12, 130, text.length >> 8, text.length & 255]),
      text,
    ]);
  throw new Error("Oversized provenance certificate identity");
}

export function trustedTools(npmDirectory) {
  const npm = realpathSync(npmDirectory);
  const modules = realpathSync(path.join(npm, "node_modules"));
  if (path.dirname(modules) !== npm)
    throw new Error("npm modules directory is outside verified npm");
  const requireFromNpm = createRequire(path.join(npm, "package.json"));
  const load = (name) => {
    const filename = realpathSync(requireFromNpm.resolve(name));
    const relative = path.relative(modules, filename);
    if (
      !relative ||
      relative === ".." ||
      relative.startsWith(`..${path.sep}`) ||
      path.isAbsolute(relative)
    )
      throw new Error(
        `npm provenance tool is outside the verified npm modules: ${name}`,
      );
    return requireFromNpm(filename);
  };
  const sigstore = load("sigstore");
  const npa = load("npm-package-arg");
  return {
    purl: (name, version) => npa.toPurl(npa.resolve(name, version)),
    verify: (bundle, pin, commit, ref) =>
      sigstore.verify(bundle, {
        tufForceCache: true,
        certificateIssuer: "https://token.actions.githubusercontent.com",
        certificateOIDs: {
          "1.3.6.1.4.1.57264.1.12": derUtf8(pin.repository),
          "1.3.6.1.4.1.57264.1.13": derUtf8(commit),
          "1.3.6.1.4.1.57264.1.14": derUtf8(ref),
          "1.3.6.1.4.1.57264.1.15": derUtf8(pin.repositoryId),
          "1.3.6.1.4.1.57264.1.17": derUtf8(pin.ownerId),
        },
      }),
  };
}

export async function verifyPinnedAttestation(item, entry, pin, tools) {
  const { bundle, commit, ref } = statementFor(
    item,
    entry,
    pin,
    tools.purl(entry.name, entry.version),
  );
  await tools.verify(bundle, pin, commit, ref);
}

function statementFor(item, entry, pin, purl) {
  const matches = item.attestationBundles?.filter(
    (candidate) => candidate?.predicateType === provenanceType,
  );
  if (!Array.isArray(matches) || matches.length !== 1)
    throw new Error("Missing or ambiguous SLSA provenance");
  const bundle = matches[0].bundle;
  const envelope = bundle?.dsseEnvelope;
  const payload = envelope?.payload;
  if (
    envelope?.payloadType !== "application/vnd.in-toto+json" ||
    typeof payload !== "string" ||
    payload.length > 1024 * 1024 ||
    !/^[A-Za-z0-9+/]*={0,2}$/.test(payload) ||
    Buffer.from(payload, "base64").toString("base64") !== payload
  )
    throw new Error("Invalid provenance envelope");
  const statement = JSON.parse(Buffer.from(payload, "base64").toString("utf8"));
  const subjects = statement?.subject;
  const digest = Buffer.from(entry.integrity.slice(7), "base64").toString(
    "hex",
  );
  if (
    statement?._type !== "https://in-toto.io/Statement/v1" ||
    statement.predicateType !== provenanceType ||
    !Array.isArray(subjects) ||
    subjects.length !== 1 ||
    subjects[0]?.name !== purl ||
    subjects[0]?.digest?.sha512 !== digest
  )
    throw new Error("Provenance subject does not match the locked package");
  const definition = statement.predicate?.buildDefinition;
  const workflow = definition?.externalParameters?.workflow;
  const github = definition?.internalParameters?.github;
  if (
    definition?.buildType !==
      "https://slsa-framework.github.io/github-actions-buildtypes/workflow/v1" ||
    workflow?.repository !== pin.repository ||
    github?.repository_id !== pin.repositoryId ||
    github?.repository_owner_id !== pin.ownerId ||
    typeof workflow.ref !== "string" ||
    !workflow.ref ||
    workflow.ref.length > 1024 ||
    /[\x00-\x1f\x7f]/.test(workflow.ref)
  )
    throw new Error("Provenance source repository does not match its pin");
  const dependencies = definition.resolvedDependencies;
  if (!Array.isArray(dependencies) || dependencies.length > 100)
    throw new Error("Invalid provenance source dependencies");
  const sources = dependencies.filter(
    (source) => source?.uri === `git+${pin.repository}@${workflow.ref}`,
  );
  if (
    sources.length !== 1 ||
    !/^[a-f0-9]{40}$/.test(sources[0]?.digest?.gitCommit || "")
  )
    throw new Error("Provenance does not identify one source commit");
  return { bundle, commit: sources[0].digest.gitCommit, ref: workflow.ref };
}

// npm validates SLSA signatures and subjects but does not constrain the
// keyless signer to an expected upstream repository. Keep recorded source
// identities stable across updates and bind the signed source commit to Fulcio.
export async function verifyNpmProvenance({
  lock,
  inventory,
  report,
  publishers,
  tools,
  verifiedBundled = [],
}) {
  const pins = parsePublisherPins(publishers);
  const { packages } = validateLockfile(lock);
  const entries = new Map(packages.map((entry) => [entry.location, entry]));
  if (!Array.isArray(verifiedBundled))
    throw new Error("Invalid verified bundled package inventory");
  for (const entry of verifiedBundled) {
    const locked = lock.packages[entry?.location];
    if (
      !locked?.inBundle ||
      locked.version !== entry.version ||
      entries.has(entry.location) ||
      !/^sha512-[A-Za-z0-9+/]{86}==$/.test(entry.integrity || "") ||
      Buffer.from(entry.integrity.slice(7), "base64").toString("base64") !==
        entry.integrity.slice(7)
    )
      throw new Error("Invalid verified bundled package identity");
    entries.set(entry.location, entry);
  }
  if (
    !Array.isArray(inventory) ||
    !inventory.length ||
    !Array.isArray(report?.invalid) ||
    report.invalid.length ||
    !Array.isArray(report.missing) ||
    report.missing.length ||
    !Array.isArray(report.verified)
  )
    throw new Error("Invalid npm signature audit report");
  const installed = new Map();
  for (const item of inventory) {
    if (
      typeof item?.location !== "string" ||
      installed.has(item.location) ||
      lock.packages[item.location]?.version !== item.version
    )
      throw new Error("Invalid installed npm provenance inventory");
    installed.set(item.location, item);
  }
  const attested = new Map();
  for (const item of report.verified) {
    const present = installed.get(item?.location);
    if (
      !present ||
      present.name !== item.name ||
      present.version !== item.version ||
      item.registry !== "https://registry.npmjs.org/" ||
      attested.has(item.location)
    )
      throw new Error(
        "npm audit provenance does not match the installed inventory",
      );
    attested.set(item.location, item);
  }
  const targets = [];
  for (const item of inventory) {
    if (!pins.has(item.name)) continue;
    const entry = entries.get(item.location);
    const audited = attested.get(item.location);
    if (
      !entry ||
      entry.name !== item.name ||
      entry.version !== item.version ||
      !audited
    )
      throw new Error(
        `Missing pinned provenance for ${item.name}@${item.version}`,
      );
    targets.push({ entry, audited, pin: pins.get(item.name) });
  }
  if (!targets.length)
    throw new Error("No installed npm package has a pinned source identity");
  let next = 0;
  const failures = [];
  await Promise.all(
    Array.from({ length: Math.min(targets.length, 12) }, async () => {
      while (next < targets.length) {
        const { entry, audited, pin } = targets[next++];
        try {
          await verifyPinnedAttestation(audited, entry, pin, tools);
        } catch (error) {
          failures.push(`${entry.name}@${entry.version}: ${error.message}`);
        }
      }
    }),
  );
  if (failures.length)
    throw new Error(
      `${failures.length} npm provenance checks failed: ${failures.slice(0, 5).join("; ")}`,
    );
  return {
    pinned: targets.length,
    otherAttested: attested.size - targets.length,
    withoutAttestations: installed.size - attested.size,
  };
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  if (process.argv.length < 3 || process.argv.length > 4)
    throw new Error(
      "Usage: node scripts/verify-npm-provenance.mjs AUDIT_REPORT [LOCKFILE]",
    );
  const info = await lstat(process.argv[2]);
  if (!info.isFile() || info.isSymbolicLink() || info.size > maxReportBytes)
    throw new Error("Invalid npm provenance audit report file");
  const report = JSON.parse(await readFile(process.argv[2], "utf8"));
  const lockPath = path.resolve(process.argv[3] || defaultLockPath);
  const lock = JSON.parse(await readFile(lockPath, "utf8"));
  const inventory = await verifyInstalledPackages(
    lock,
    path.dirname(lockPath),
    { includeInventory: true },
  );
  const verifiedBundled = [];
  await verifyInstalledBundledPackages(lock, path.dirname(lockPath), {
    onVerifiedChild: (entry) => verifiedBundled.push(entry),
  });
  const publishers = JSON.parse(readFileSync(pinsPath, "utf8"));
  const result = await verifyNpmProvenance({
    lock,
    inventory,
    report,
    publishers,
    verifiedBundled,
    tools: trustedTools(activeNpmDirectory()),
  });
  console.log(
    `Verified source identity for ${result.pinned} pinned npm packages; ${result.otherAttested} other installed packages have attestations and ${result.withoutAttestations} have none in the audit report`,
  );
}
