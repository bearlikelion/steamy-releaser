import { lstat, opendir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const maxFiles = 1024;
const maxEntries = 4096;
const maxBytes = 64 * 1024 * 1024;
const invalidName = /[<>:"\\|?*\x00-\x1f\x7f]/;
const reservedName = /^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)/i;

// Treat the frontend artifact as data: reject links, unsafe paths and
// unexpectedly large payloads before passing it to the Go build.
export async function verifyFrontendDist(directory) {
  const info = await lstat(directory);
  if (!info.isDirectory() || info.isSymbolicLink()) {
    throw new Error("Frontend artifact must be a directory");
  }
  const seen = new Set();
  let files = 0;
  let entries = 0;
  let bytes = 0;
  let index = false;
  async function visit(current, parts = []) {
    const directory = await opendir(current);
    for await (const entry of directory) {
      if (++entries > maxEntries) {
        throw new Error("Frontend artifact has too many entries");
      }
      const name = entry.name;
      const next = [...parts, name];
      const relative = next.join("/");
      if (
        name.startsWith(".") ||
        invalidName.test(name) ||
        reservedName.test(name) ||
        /[. ]$/.test(name) ||
        Buffer.byteLength(relative, "utf8") > 200 ||
        next.length > 10
      ) {
        throw new Error(`Unsafe frontend artifact path: ${relative}`);
      }
      const key = relative.toLowerCase();
      if (seen.has(key))
        throw new Error(`Duplicate frontend artifact path: ${relative}`);
      seen.add(key);
      const full = path.join(current, name);
      const child = await lstat(full);
      if (child.isSymbolicLink())
        throw new Error(`Frontend artifact contains a link: ${relative}`);
      if (child.isDirectory()) {
        await visit(full, next);
      } else if (child.isFile()) {
        files++;
        bytes += child.size;
        if (files > maxFiles || bytes > maxBytes) {
          throw new Error("Frontend artifact exceeds the size limit");
        }
        if (relative === "index.html" && child.size > 0) index = true;
      } else {
        throw new Error(`Unsupported frontend artifact entry: ${relative}`);
      }
    }
  }
  await visit(directory);
  if (!index)
    throw new Error("Frontend artifact is missing a nonempty index.html");
  return { files, bytes };
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  if (process.argv.length !== 3)
    throw new Error("Usage: node scripts/verify-frontend-dist.mjs DIRECTORY");
  const result = await verifyFrontendDist(process.argv[2]);
  console.log(
    `Verified frontend artifact: ${result.files} files, ${result.bytes} bytes`,
  );
}
