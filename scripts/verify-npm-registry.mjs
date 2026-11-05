import { createPublicKey, verify } from "node:crypto";
import { readFileSync, realpathSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { downloadArchive } from "./inspect-npm-archive.mjs";
import {
  verifyBundleDeclaration,
  verifyBundledArchive,
  verifyBundledChild,
} from "./verify-npm-bundles.mjs";
import { defaultLockPath, validateLockfile } from "./verify-npm-lock.mjs";

const registry = "https://registry.npmjs.org";
const historicalSignaturesPath = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  "npm-historical-signatures.json",
);

export function parseTrustedKeys(target) {
  if (!Array.isArray(target?.keys))
    throw new Error("Invalid npm TUF key target");
  const keys = new Map();
  for (const record of target.keys) {
    if (record?.keyUsage !== "npm:signatures") continue;
    const publicKey = record.publicKey;
    if (publicKey?.keyDetails !== "PKIX_ECDSA_P256_SHA_256") continue;
    if (
      typeof record.keyId !== "string" ||
      typeof publicKey.rawBytes !== "string"
    ) {
      throw new Error("Invalid npm registry signing key");
    }
    const key = createPublicKey({
      key: Buffer.from(publicKey.rawBytes, "base64"),
      format: "der",
      type: "spki",
    });
    if (
      key.asymmetricKeyType !== "ec" ||
      key.asymmetricKeyDetails?.namedCurve !== "prime256v1"
    ) {
      throw new Error("Unsupported npm registry signing key");
    }
    const start = publicKey.validFor?.start;
    const startsAt = start == null ? null : Date.parse(start);
    const expiry = publicKey.validFor?.end;
    const expiresAt = expiry == null ? null : Date.parse(expiry);
    if (
      (start != null && typeof start !== "string") ||
      (expiry != null && typeof expiry !== "string") ||
      (startsAt !== null && !Number.isFinite(startsAt)) ||
      (expiresAt !== null && !Number.isFinite(expiresAt)) ||
      (startsAt !== null && expiresAt !== null && startsAt >= expiresAt)
    ) {
      throw new Error("Invalid npm registry signing key validity period");
    }
    const candidates = keys.get(record.keyId) || [];
    candidates.push({ key, startsAt, expiresAt });
    keys.set(record.keyId, candidates);
  }
  if (!keys.size)
    throw new Error("No supported npm signing keys in the TUF target");
  return keys;
}

function historicalKey(keyId, expiresAt, entry) {
  return JSON.stringify([
    keyId,
    expiresAt,
    entry.name,
    entry.version,
    entry.integrity,
  ]);
}

// A registry publication timestamp is not covered by the registry signature.
// Approve specific old identities and digests instead of accepting arbitrary
// packages merely because the registry says they predate an expired key.
export function parseHistoricalSignatures(data) {
  if (!Array.isArray(data?.keys))
    throw new Error("Invalid historical npm signatures");
  const approved = new Set();
  const keyIds = new Set();
  for (const record of data.keys) {
    const expiresAt = Date.parse(record?.expiresAt);
    if (
      typeof record?.keyId !== "string" ||
      !record.keyId ||
      keyIds.has(record.keyId) ||
      !Number.isFinite(expiresAt) ||
      new Date(expiresAt).toISOString() !== record.expiresAt ||
      !Array.isArray(record.packages)
    ) {
      throw new Error("Invalid historical npm signing key approval");
    }
    keyIds.add(record.keyId);
    for (const entry of record.packages) {
      if (
        typeof entry?.name !== "string" ||
        !entry.name ||
        typeof entry.version !== "string" ||
        !entry.version ||
        typeof entry.integrity !== "string" ||
        !/^sha512-[A-Za-z0-9+/]{86}==$/.test(entry.integrity) ||
        Buffer.from(entry.integrity.slice(7), "base64").toString("base64") !==
          entry.integrity.slice(7)
      ) {
        throw new Error("Invalid historical npm package approval");
      }
      const key = historicalKey(record.keyId, expiresAt, entry);
      if (approved.has(key))
        throw new Error("Duplicate historical npm package approval");
      approved.add(key);
    }
  }
  return approved;
}

export function loadBundledTuf(npmDirectory) {
  const npmPath = realpathSync(npmDirectory);
  const modules = realpathSync(path.join(npmPath, "node_modules"));
  const clientPath = realpathSync(path.join(modules, "@sigstore", "tuf"));
  const relative = path.relative(modules, clientPath);
  if (
    relative === "" ||
    relative === ".." ||
    relative.startsWith(`..${path.sep}`) ||
    path.isAbsolute(relative)
  ) {
    throw new Error("Sigstore TUF client is outside npm's bundled modules");
  }
  return createRequire(path.join(npmPath, "package.json"))(clientPath);
}

export function bundledNpmDirectory() {
  const nodeDirectory = path.dirname(realpathSync(process.execPath));
  return process.platform === "win32"
    ? path.join(nodeDirectory, "node_modules", "npm")
    : path.join(nodeDirectory, "..", "lib", "node_modules", "npm");
}

export function activeNpmDirectory(env = process.env) {
  return env.STEAM_RELEASER_NPM_DIR
    ? path.resolve(env.STEAM_RELEASER_NPM_DIR)
    : bundledNpmDirectory();
}

// The patched npm toolchain changes minimatch, which the TUF client uses to
// authorize delegated paths. Require the reviewed registry delegation until a
// changed topology and its matching behavior can be reviewed explicitly.
export function verifyRegistryDelegation(client) {
  const delegations = client?.updater?.trustedSet?.targets?.signed?.delegations;
  const roles = delegations?.roles;
  if (
    !roles ||
    typeof roles !== "object" ||
    Array.isArray(roles) ||
    delegations.succinctRoles != null
  ) {
    throw new Error(
      "Unexpected npm TUF delegation topology; review the trust configuration",
    );
  }
  const names = Object.keys(roles);
  const role = roles["registry.npmjs.org"];
  if (
    names.length !== 1 ||
    names[0] !== "registry.npmjs.org" ||
    role?.name !== "registry.npmjs.org" ||
    role.terminating !== true ||
    !Array.isArray(role.paths) ||
    role.paths.length !== 1 ||
    role.paths[0] !== "registry.npmjs.org/*" ||
    role.pathHashPrefixes != null
  ) {
    throw new Error(
      "Unexpected npm TUF delegation topology; review the trust configuration",
    );
  }
}

// Do not accept a nested delegation or a different role as the source of the
// registry key target without reviewing its path-matching behavior as well.
export function verifyRegistryTargetRole(client, targetInfo) {
  const store = client?.updater?.trustedSet;
  const role =
    typeof store?.getRole === "function"
      ? store.getRole("registry.npmjs.org")
      : undefined;
  const signed = role?.signed;
  if (
    !signed?.targets ||
    typeof signed.targets !== "object" ||
    Array.isArray(signed.targets) ||
    !Object.hasOwn(signed.targets, "registry.npmjs.org/keys.json") ||
    signed.targets["registry.npmjs.org/keys.json"] !== targetInfo ||
    signed.delegations != null
  ) {
    throw new Error(
      "Unexpected npm TUF target role; review the trust configuration",
    );
  }
}

export async function loadRegistryKeyTarget(client) {
  const targetPath = "registry.npmjs.org/keys.json";
  verifyRegistryDelegation(client);
  if (typeof client.updater.getTargetInfo !== "function") {
    throw new Error("npm TUF key target was not found");
  }
  const targetInfo = await client.updater.getTargetInfo(targetPath);
  if (!targetInfo) throw new Error("npm TUF key target was not found");
  verifyRegistryTargetRole(client, targetInfo);
  return client.getTarget(targetPath);
}

async function loadTrustedKeys(npmDirectory) {
  // The bootstrapped npm is digest- and signature-verified before activation.
  // During bootstrap, use the client in the pinned Node distribution instead.
  let tuf;
  try {
    tuf = loadBundledTuf(npmDirectory);
  } catch (error) {
    throw new Error(
      `Cannot load npm's bundled Sigstore TUF client: ${error.message}`,
    );
  }
  const client = await tuf.initTUF({ timeout: 10_000 });
  const target = await loadRegistryKeyTarget(client);
  return parseTrustedKeys(JSON.parse(target));
}

export function verifyRegistryMetadata(entry, manifest) {
  if (manifest?.name !== entry.name || manifest.version !== entry.version) {
    throw new Error("Registry package identity does not match the lockfile");
  }
  if (manifest.dist?.tarball !== entry.resolved) {
    throw new Error("Registry tarball URL does not match the lockfile");
  }
  if (manifest.dist?.integrity !== entry.integrity) {
    throw new Error("Registry integrity does not match the lockfile");
  }
}

export async function verifyRegistryPackages(packages, options = {}) {
  const fetcher = options.fetcher || fetch;
  const trustedKeys =
    options.trustedKeys ||
    (await loadTrustedKeys(options.npmDirectory || activeNpmDirectory()));
  const historical = parseHistoricalSignatures(
    options.historicalSignatures ??
      JSON.parse(readFileSync(historicalSignaturesPath, "utf8")),
  );
  const now = options.now ?? Date.now();
  if (!Number.isFinite(now)) throw new Error("Invalid registry check time");
  let next = 0;
  const failures = [];
  const bundleArchives = new Map();
  const childArchives = new Map();
  const deadline = AbortSignal.timeout(60_000);
  async function getMetadata(url) {
    for (let attempt = 0; attempt < 3; attempt++) {
      let response;
      try {
        response = await fetcher(url, {
          redirect: "error",
          signal: AbortSignal.any([deadline, AbortSignal.timeout(20_000)]),
        });
      } catch (error) {
        if (attempt === 2 || deadline.aborted) throw error;
        await delay(250 * 2 ** attempt, undefined, { signal: deadline });
        continue;
      }
      if (response.ok) return response.json();
      if ((response.status === 429 || response.status >= 500) && attempt < 2) {
        await delay(250 * 2 ** attempt, undefined, { signal: deadline });
        continue;
      }
      throw new Error(`Registry returned HTTP ${response.status}`);
    }
  }
  async function verifySignedMetadata(entry, manifest) {
    verifyRegistryMetadata(entry, manifest);
    const message = Buffer.from(
      `${entry.name}@${entry.version}:${entry.integrity}`,
    );
    let expired = false;
    let future = false;
    for (const signature of manifest.dist?.signatures || []) {
      if (typeof signature?.sig !== "string") continue;
      for (const candidate of trustedKeys.get(signature.keyid) || []) {
        let valid = false;
        try {
          valid = verify(
            "sha256",
            message,
            candidate.key,
            Buffer.from(signature.sig, "base64"),
          );
        } catch {
          // A malformed signature cannot authenticate the registry metadata.
        }
        if (!valid) continue;
        if (candidate.startsAt !== null && now < candidate.startsAt) {
          future = true;
          continue;
        }
        if (candidate.expiresAt === null || now < candidate.expiresAt) return;
        expired = true;
        if (
          historical.has(
            historicalKey(signature.keyid, candidate.expiresAt, entry),
          )
        )
          return;
      }
    }
    if (expired) throw new Error("Unapproved expired npm registry signature");
    if (future) throw new Error("npm registry signing key is not yet valid");
    throw new Error("No valid trusted registry signature");
  }
  async function signedBundledChild(child) {
    if (
      typeof child?.name !== "string" ||
      !/^(?:@[a-z0-9][a-z0-9._~-]*\/)?[a-z0-9][a-z0-9._~-]*$/.test(
        child.name,
      ) ||
      typeof child.version !== "string" ||
      !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(
        child.version,
      )
    ) {
      throw new Error("Invalid bundled child registry identity");
    }
    const key = JSON.stringify([child.name, child.version]);
    if (!childArchives.has(key)) {
      childArchives.set(
        key,
        (async () => {
          const metadataURL = `${registry}/${encodeURIComponent(child.name)}/${encodeURIComponent(child.version)}`;
          const manifest = await getMetadata(metadataURL);
          const integrity = manifest?.dist?.integrity;
          if (
            typeof integrity !== "string" ||
            !/^sha512-[A-Za-z0-9+/]{86}==$/.test(integrity) ||
            Buffer.from(integrity.slice(7), "base64").toString("base64") !==
              integrity.slice(7)
          ) {
            throw new Error(
              `Invalid bundled child integrity: ${child.name}@${child.version}`,
            );
          }
          const basename = child.name.slice(child.name.lastIndexOf("/") + 1);
          const published = {
            name: child.name,
            version: child.version,
            integrity,
            resolved: `${registry}/${child.name}/-/${basename}-${child.version}.tgz`,
          };
          await verifySignedMetadata(published, manifest);
          return {
            published,
            bytes: await downloadArchive(published.resolved, fetcher),
            manifest,
          };
        })(),
      );
    }
    return childArchives.get(key);
  }
  async function worker() {
    while (next < packages.length) {
      const entry = packages[next++];
      try {
        const packageURL = `${registry}/${encodeURIComponent(entry.name)}`;
        const manifest = await getMetadata(
          `${packageURL}/${encodeURIComponent(entry.version)}`,
        );
        await verifySignedMetadata(entry, manifest);
        if (options.onVerifiedEntry)
          await options.onVerifiedEntry(entry, manifest);
        if (Object.hasOwn(entry, "bundled")) {
          const declared = verifyBundleDeclaration(entry, manifest);
          if (declared.length || entry.bundled.length) {
            if (entry.bundled.length > 128)
              throw new Error("Too many bundled npm packages to verify");
            if (!bundleArchives.has(entry.integrity)) {
              bundleArchives.set(
                entry.integrity,
                downloadArchive(entry.resolved, fetcher),
              );
            }
            const parentFiles = verifyBundledArchive(
              entry,
              manifest,
              await bundleArchives.get(entry.integrity),
            );
            for (const child of entry.bundled) {
              const {
                published,
                bytes,
                manifest: childManifest,
              } = await signedBundledChild(child);
              const files = verifyBundledChild(
                entry,
                parentFiles,
                child,
                published,
                bytes,
              );
              if (options.onBundledChild)
                await options.onBundledChild(
                  entry,
                  child,
                  files,
                  published,
                  childManifest,
                );
            }
          }
        }
      } catch (error) {
        failures.push(`${entry.name}@${entry.version}: ${error.message}`);
      }
    }
  }
  await Promise.all(
    Array.from({ length: Math.min(packages.length, 16) }, () => worker()),
  );
  if (failures.length) {
    throw new Error(
      `${failures.length} npm registry checks failed: ${failures.slice(0, 5).join("; ")}`,
    );
  }
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  if (process.argv.length > 3)
    throw new Error("Usage: node scripts/verify-npm-registry.mjs [LOCKFILE]");
  const lock = JSON.parse(
    readFileSync(process.argv[2] || defaultLockPath, "utf8"),
  );
  const { packages, bundled } = validateLockfile(lock);
  await verifyRegistryPackages(packages);
  console.log(
    `Verified signed registry metadata for ${packages.length} locked npm packages and published archive contents for ${bundled} bundled entries`,
  );
}
