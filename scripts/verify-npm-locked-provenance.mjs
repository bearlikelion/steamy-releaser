import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { defaultLockPath, validateLockfile } from "./verify-npm-lock.mjs";
import {
  parsePublisherPins,
  trustedTools,
  verifyPinnedAttestation,
} from "./verify-npm-provenance.mjs";
import {
  activeNpmDirectory,
  verifyRegistryPackages,
} from "./verify-npm-registry.mjs";

const pinsPath = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  "npm-source-publishers.json",
);
const unattestedPath = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  "npm-unattested.json",
);
const maxAttestationBytes = 4 * 1024 * 1024;

export function parseUnattestedPackages(data) {
  if (!Array.isArray(data?.packages))
    throw new Error("Invalid unattested npm baseline");
  const result = new Map();
  for (const entry of data.packages) {
    if (
      typeof entry?.name !== "string" ||
      !/^(?:@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*$/.test(entry.name) ||
      typeof entry.version !== "string" ||
      !/^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(
        entry.version,
      ) ||
      typeof entry.integrity !== "string" ||
      !/^sha512-[A-Za-z0-9+/]{86}==$/.test(entry.integrity) ||
      Buffer.from(entry.integrity.slice(7), "base64").toString("base64") !==
        entry.integrity.slice(7)
    )
      throw new Error("Invalid unattested npm baseline entry");
    const key = JSON.stringify([entry.name, entry.version]);
    if (result.has(key)) throw new Error("Duplicate unattested npm entry");
    result.set(key, entry.integrity);
  }
  return result;
}

function attestationURL(entry, manifest) {
  let url;
  try {
    url = new URL(manifest?.dist?.attestations?.url);
  } catch {
    throw new Error("Pinned package has no valid attestation URL");
  }
  const prefix = "/-/npm/v1/attestations/";
  let subject;
  try {
    subject = decodeURIComponent(url.pathname.slice(prefix.length));
  } catch {
    throw new Error("Invalid attestation URL encoding");
  }
  if (
    url.protocol !== "https:" ||
    url.hostname !== "registry.npmjs.org" ||
    url.port ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    !url.pathname.startsWith(prefix) ||
    subject !== `${entry.name}@${entry.version}`
  )
    throw new Error("Attestation URL does not match the npm package");
  return url.href;
}

async function fetchAttestation(entry, manifest, fetcher) {
  const url = attestationURL(entry, manifest);
  let response;
  for (let attempt = 0; attempt < 3; attempt++) {
    try {
      response = await fetcher(url, {
        redirect: "error",
        signal: AbortSignal.timeout(20_000),
      });
    } catch (error) {
      if (attempt === 2) throw error;
      await new Promise((resolve) => setTimeout(resolve, 250 * 2 ** attempt));
      continue;
    }
    if (response.ok) break;
    if ((response.status !== 429 && response.status < 500) || attempt === 2)
      throw new Error(`npm attestation returned HTTP ${response.status}`);
    await new Promise((resolve) => setTimeout(resolve, 250 * 2 ** attempt));
  }
  if (!response?.ok || !response.body)
    throw new Error("npm attestation response has no body");
  const chunks = [];
  let size = 0;
  for await (const chunk of response.body) {
    size += chunk.length;
    if (size > maxAttestationBytes)
      throw new Error("npm attestation response exceeds the size limit");
    chunks.push(chunk);
  }
  const report = JSON.parse(Buffer.concat(chunks, size).toString("utf8"));
  if (!Array.isArray(report?.attestations) || report.attestations.length > 20)
    throw new Error("Invalid npm attestation response");
  return { attestationBundles: report.attestations };
}

// Verify all pinned lockfile sources, including packages that this operating
// system does not install. Bundled child digests come only from their separately
// signed archive after it has been compared with the signed parent archive.
export async function verifyLockedNpmProvenance(
  lock,
  publishers,
  tools,
  options = {},
) {
  const pins = parsePublisherPins(publishers);
  const unattested = parseUnattestedPackages(options.unattested);
  const { packages } = validateLockfile(lock);
  const expected = new Set();
  const expectedUnattested = new Set();
  for (const [location] of Object.entries(lock.packages)) {
    if (!location) continue;
    const name = location.slice(
      location.lastIndexOf("node_modules/") + "node_modules/".length,
    );
    if (pins.has(name)) expected.add(location);
    else expectedUnattested.add(location);
  }
  if (!expected.size)
    throw new Error("No locked npm package has a pinned source identity");
  const seen = new Set();
  const seenUnattested = new Set();
  const seenUnattestedIdentities = new Set();
  let withoutProvenance = 0;
  async function check(entry, manifest) {
    const pin = pins.get(entry.name);
    if (!pin) {
      if (manifest?.dist?.attestations?.provenance)
        throw new Error(
          `Unpinned npm package now has provenance: ${entry.name}@${entry.version}`,
        );
      const key = JSON.stringify([entry.name, entry.version]);
      if (
        !expectedUnattested.has(entry.location) ||
        seenUnattested.has(entry.location) ||
        !unattested.has(key) ||
        unattested.get(key) !== entry.integrity
      )
        throw new Error(
          `Unreviewed npm package without provenance: ${entry.name}@${entry.version}`,
        );
      seenUnattested.add(entry.location);
      seenUnattestedIdentities.add(key);
      withoutProvenance++;
      return;
    }
    if (!expected.has(entry.location) || seen.has(entry.location))
      throw new Error(
        `Unexpected or duplicate pinned npm package: ${entry.location}`,
      );
    const attestation = await fetchAttestation(
      entry,
      manifest,
      options.fetcher || fetch,
    );
    await verifyPinnedAttestation(attestation, entry, pin, tools);
    seen.add(entry.location);
  }
  const verifyPackages = options.verifyPackages || verifyRegistryPackages;
  await verifyPackages(packages, {
    ...options.registryOptions,
    onVerifiedEntry: check,
    onBundledChild: async (parent, child, _files, published, manifest) => {
      await check(
        { ...published, location: `${parent.location}/${child.path}` },
        manifest,
      );
    },
  });
  if (seen.size !== expected.size)
    throw new Error("Not all pinned npm packages were authenticated");
  if (
    seenUnattested.size !== expectedUnattested.size ||
    seenUnattestedIdentities.size !== unattested.size
  )
    throw new Error("Unattested npm baseline does not match the lockfile");
  return { pinned: seen.size, withoutProvenance };
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  if (process.argv.length > 3)
    throw new Error(
      "Usage: node scripts/verify-npm-locked-provenance.mjs [LOCKFILE]",
    );
  const lock = JSON.parse(
    await readFile(process.argv[2] || defaultLockPath, "utf8"),
  );
  const publishers = JSON.parse(await readFile(pinsPath, "utf8"));
  const unattested = JSON.parse(await readFile(unattestedPath, "utf8"));
  const result = await verifyLockedNpmProvenance(
    lock,
    publishers,
    trustedTools(activeNpmDirectory()),
    { unattested },
  );
  console.log(
    `Verified source identity for ${result.pinned} locked npm packages; ${result.withoutProvenance} have no recorded provenance`,
  );
}
