import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { writeFile } from "node:fs/promises";
import path from "node:path";

const files = process.argv.slice(2);
if (files.length === 0)
  throw new Error("Usage: node scripts/checksums.mjs FILE...");

for (const file of files) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(file)) hash.update(chunk);
  await writeFile(
    `${file}.sha256`,
    `${hash.digest("hex")}  ${path.basename(file)}\n`,
    {
      flag: "wx",
    },
  );
}
