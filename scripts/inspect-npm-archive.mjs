import { setTimeout as delay } from "node:timers/promises";
import { gunzipSync } from "node:zlib";

export const maxArchiveBytes = 16 * 1024 * 1024;
const maxExtractedBytes = 64 * 1024 * 1024;

export function checkArchiveEntry(entry, seen) {
  if (entry.type !== "File" && entry.type !== "Directory") {
    throw new Error(`Unsupported npm archive entry type: ${entry.type}`);
  }
  const name = entry.path.replace(/\/$/, "");
  const segments = name.split("/");
  if (
    segments[0] !== "package" ||
    (segments.length === 1 && entry.type !== "Directory") ||
    segments.some(
      (part) =>
        !part ||
        part === "." ||
        part === ".." ||
        /[<>:"\\|?*\x00-\x1f\x7f]/.test(part) ||
        /[. ]$/.test(part) ||
        /^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)/i.test(part),
    )
  )
    throw new Error(`Unsafe npm archive path: ${entry.path}`);
  const key = name.toLowerCase();
  if (seen.has(key))
    throw new Error(`Duplicate npm archive path: ${entry.path}`);
  seen.add(key);
  if (!Number.isSafeInteger(entry.size) || entry.size < 0) {
    throw new Error(`Invalid npm archive entry size: ${entry.path}`);
  }
  return entry.size;
}

function tarString(bytes) {
  const end = bytes.indexOf(0);
  return new TextDecoder("utf-8", { fatal: true }).decode(
    end < 0 ? bytes : bytes.subarray(0, end),
  );
}

function tarNumber(bytes) {
  const text = tarString(bytes).trim();
  if (!/^[0-7]+$/.test(text)) throw new Error("Invalid npm archive number");
  const value = Number.parseInt(text, 8);
  if (!Number.isSafeInteger(value))
    throw new Error("Invalid npm archive number");
  return value;
}

// npm's pinned tarball uses regular ustar files. Reject extensions and links
// rather than interpreting archive features the bootstrap does not need.
export function inspectArchive(
  compressed,
  {
    requiredPaths = ["package/package.json", "package/bin/npm-cli.js"],
    allowedRoots = ["package"],
  } = {},
) {
  if (
    !Buffer.isBuffer(compressed) ||
    compressed.length > maxArchiveBytes ||
    !Array.isArray(allowedRoots) ||
    !allowedRoots.length ||
    allowedRoots.some(
      (root) =>
        typeof root !== "string" ||
        !/^[A-Za-z0-9_][A-Za-z0-9._-]*$/.test(root) ||
        /[.]$/.test(root),
    )
  ) {
    throw new Error("Invalid or oversized npm archive");
  }
  const bytes = gunzipSync(compressed, { maxOutputLength: maxExtractedBytes });
  if (bytes.length % 512 !== 0) throw new Error("Truncated npm archive");
  const seen = new Set();
  const entries = [];
  let total = 0;
  let offset = 0;
  let terminated = false;
  let root;
  while (offset + 512 <= bytes.length) {
    const header = bytes.subarray(offset, offset + 512);
    offset += 512;
    if (header.every((value) => value === 0)) {
      if (
        offset + 512 > bytes.length ||
        !bytes.subarray(offset).every((value) => value === 0)
      ) {
        throw new Error("Invalid npm archive terminator");
      }
      terminated = true;
      break;
    }
    if (header.subarray(257, 265).toString("binary") !== "ustar\x0000") {
      throw new Error("Unsupported npm archive format");
    }
    let checksum = 0;
    for (let i = 0; i < header.length; i++)
      checksum += i >= 148 && i < 156 ? 32 : header[i];
    if (tarNumber(header.subarray(148, 156)) !== checksum)
      throw new Error("Invalid npm archive header checksum");
    const type =
      header[156] === 0 || header[156] === 48
        ? "File"
        : header[156] === 53
          ? "Directory"
          : "Unsupported";
    const name = tarString(header.subarray(0, 100));
    const prefix = tarString(header.subarray(345, 500));
    const size = tarNumber(header.subarray(124, 136));
    const original = prefix ? `${prefix}/${name}` : name;
    const segments = original.split("/");
    if (root === undefined) {
      root = segments[0];
      if (!allowedRoots.includes(root))
        throw new Error(`Unexpected npm archive root: ${root}`);
    }
    if (segments[0] !== root)
      throw new Error("npm archive contains more than one root");
    segments[0] = "package";
    const entry = {
      type,
      path: segments.join("/"),
      size,
      mode: tarNumber(header.subarray(100, 108)) & 0o777,
    };
    total += checkArchiveEntry(entry, seen);
    if (total > maxExtractedBytes || entries.length >= 100_000)
      throw new Error("npm archive exceeds the size limit");
    const paddedSize = Math.ceil(size / 512) * 512;
    if (offset + paddedSize > bytes.length)
      throw new Error("Truncated npm archive entry");
    if (type === "Directory" && size !== 0)
      throw new Error("Invalid npm archive directory");
    entry.data = bytes.subarray(offset, offset + size);
    entries.push(entry);
    offset += paddedSize;
  }
  if (!terminated) throw new Error("Missing npm archive terminator");
  if (requiredPaths.some((required) => !seen.has(required.toLowerCase()))) {
    throw new Error("npm archive is missing required files");
  }
  return entries;
}

export async function downloadArchive(url, fetcher) {
  const deadline = AbortSignal.timeout(60_000);
  let response;
  for (let attempt = 0; attempt < 3; attempt++) {
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
    if (response.ok) break;
    if ((response.status !== 429 && response.status < 500) || attempt === 2)
      throw new Error(`npm tarball returned HTTP ${response.status}`);
    await delay(250 * 2 ** attempt, undefined, { signal: deadline });
  }
  if (!response?.ok || !response.body)
    throw new Error("npm tarball response has no body");
  const chunks = [];
  let size = 0;
  for await (const chunk of response.body) {
    size += chunk.length;
    if (size > maxArchiveBytes)
      throw new Error("npm tarball exceeds the size limit");
    chunks.push(chunk);
  }
  return Buffer.concat(chunks, size);
}
