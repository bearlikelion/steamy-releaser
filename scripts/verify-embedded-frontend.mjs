import { lstat, readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

// Go embeds the frontend files verbatim. Check their names and bytes in the
// packaged executable after Wails builds from the separately audited frontend.
export async function verifyEmbeddedFrontend(binaryPath, frontendDirectory) {
  const binaryInfo = await lstat(binaryPath);
  const rootInfo = await lstat(frontendDirectory);
  if (!binaryInfo.isFile() || !rootInfo.isDirectory()) {
    throw new Error("Expected a regular executable and frontend directory");
  }
  const binary = await readFile(binaryPath);
  const files = [];
  async function visit(directory, parts = []) {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const next = [...parts, entry.name];
      const filename = path.join(directory, entry.name);
      if (entry.isDirectory()) await visit(filename, next);
      else if (entry.isFile()) files.push({ path: next.join("/"), filename });
      else throw new Error(`Unsupported frontend output: ${next.join("/")}`);
    }
  }
  await visit(frontendDirectory);
  if (!files.some((file) => file.path === "index.html")) {
    throw new Error("Frontend output is missing index.html");
  }
  for (const file of files) {
    const contents = await readFile(file.filename);
    if (file.path === "index.html" && contents.length === 0) {
      throw new Error("Frontend index.html is empty");
    }
    if (!binary.includes(Buffer.from(`frontend/dist/${file.path}`))) {
      throw new Error(
        `Frontend asset path missing from executable: ${file.path}`,
      );
    }
    if (contents.length && !binary.includes(contents)) {
      throw new Error(
        `Frontend asset bytes missing from executable: ${file.path}`,
      );
    }
  }
  return files.length;
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  if (process.argv.length !== 4) {
    throw new Error(
      "Usage: node scripts/verify-embedded-frontend.mjs EXECUTABLE FRONTEND_DIST",
    );
  }
  const count = await verifyEmbeddedFrontend(process.argv[2], process.argv[3]);
  console.log(
    `Verified ${count} frontend asset paths and contents in the executable`,
  );
}
