import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  mkdtemp,
  mkdir,
  readFile,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import {
  collectFrontendSourceMaps,
  verifyFrontendSourceMaps,
} from "./frontend-source-maps.mjs";

async function fixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), "steam-source-maps-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const frontend = path.join(root, "frontend");
  const maps = path.join(root, "maps");
  await mkdir(path.join(frontend, "src"), { recursive: true });
  await mkdir(path.join(frontend, "dist", "assets"), { recursive: true });
  await writeFile(
    path.join(frontend, "src", "app.ts"),
    "export const value = 1;\n",
  );
  await writeFile(path.join(frontend, "dist", "index.html"), "<!doctype html>");
  await writeFile(
    path.join(frontend, "dist", "assets", "bundle.js"),
    "const value=1;\n",
  );
  const map = {
    version: 3,
    file: "bundle.js",
    sources: ["../../src/app.ts"],
    sourcesContent: ["export const value = 1;\n"],
    names: [],
    mappings: "AAAA",
  };
  const originalMap = path.join(frontend, "dist", "assets", "bundle.js.map");
  await writeFile(originalMap, JSON.stringify(map));
  return { root, frontend, maps, map, originalMap };
}

test("moves source maps outside the frontend and verifies assets and source contents", async (t) => {
  const { frontend, maps, originalMap } = await fixture(t);
  assert.deepEqual(await collectFrontendSourceMaps(frontend, maps), {
    maps: 1,
    sources: 1,
  });
  await assert.rejects(readFile(originalMap), { code: "ENOENT" });
  assert.deepEqual(await verifyFrontendSourceMaps(frontend, maps), {
    maps: 1,
    sources: 1,
  });
  assert.equal(
    await readFile(path.join(frontend, "dist", "assets", "bundle.js"), "utf8"),
    "const value=1;\n",
  );
});

test("rejects missing maps, altered source text, and unsafe source paths", async (t) => {
  const f = await fixture(t);
  await rm(f.originalMap);
  await assert.rejects(
    collectFrontendSourceMaps(f.frontend, f.maps),
    /one source map/,
  );
  await writeFile(
    f.originalMap,
    JSON.stringify({ ...f.map, sourcesContent: ["forged"] }),
  );
  await assert.rejects(
    collectFrontendSourceMaps(f.frontend, f.maps),
    /differs from the checked input/,
  );
  await writeFile(path.join(f.root, "outside.ts"), "outside");
  await writeFile(
    f.originalMap,
    JSON.stringify({
      ...f.map,
      sources: ["../../../outside.ts"],
      sourcesContent: ["outside"],
    }),
  );
  await assert.rejects(
    collectFrontendSourceMaps(f.frontend, f.maps),
    /escapes or repeats/,
  );
});

test("rejects source-file symlinks that escape the checkout", async (t) => {
  const f = await fixture(t);
  const external = path.join(f.root, "outside.ts");
  const link = path.join(f.frontend, "src", "link.ts");
  await writeFile(external, "outside");
  try {
    await symlink(external, link);
  } catch (error) {
    if (["EPERM", "EACCES"].includes(error.code))
      return t.skip("symlinks unavailable");
    throw error;
  }
  await writeFile(
    f.originalMap,
    JSON.stringify({
      ...f.map,
      sources: ["../../src/link.ts"],
      sourcesContent: ["outside"],
    }),
  );
  await assert.rejects(
    collectFrontendSourceMaps(f.frontend, f.maps),
    /Invalid or oversized/,
  );
});

test("rejects changes to the JavaScript, checked source, or map after collection", async (t) => {
  const f = await fixture(t);
  await collectFrontendSourceMaps(f.frontend, f.maps);
  const asset = path.join(f.frontend, "dist", "assets", "bundle.js");
  const source = path.join(f.frontend, "src", "app.ts");
  const map = path.join(f.maps, "assets", "bundle.js.map");
  await writeFile(asset, "altered");
  await assert.rejects(
    verifyFrontendSourceMaps(f.frontend, f.maps),
    /checksum mismatch/,
  );
  await writeFile(asset, "const value=1;\n");
  await writeFile(source, "altered source");
  await assert.rejects(
    verifyFrontendSourceMaps(f.frontend, f.maps),
    /differs from the checked input/,
  );
  await writeFile(source, "export const value = 1;\n");
  const changed = { ...f.map, sourcesContent: ["forged"] };
  const bytes = Buffer.from(JSON.stringify(changed));
  await writeFile(map, bytes);
  const manifestPath = path.join(f.maps, "manifest.json");
  const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
  manifest.maps[0].mapSha256 = createHash("sha256").update(bytes).digest("hex");
  await writeFile(manifestPath, JSON.stringify(manifest));
  await assert.rejects(
    verifyFrontendSourceMaps(f.frontend, f.maps),
    /differs from the checked input/,
  );
});

test("rejects extra artifact files and map metadata that points elsewhere", async (t) => {
  const f = await fixture(t);
  await collectFrontendSourceMaps(f.frontend, f.maps);
  await writeFile(path.join(f.maps, "extra"), "unexpected");
  await assert.rejects(
    verifyFrontendSourceMaps(f.frontend, f.maps),
    /Unexpected or missing/,
  );
  await rm(path.join(f.maps, "extra"));
  const manifestPath = path.join(f.maps, "manifest.json");
  const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
  manifest.maps[0].map = "../outside.map";
  await writeFile(manifestPath, JSON.stringify(manifest));
  await assert.rejects(
    verifyFrontendSourceMaps(f.frontend, f.maps),
    /Invalid frontend source-map manifest entry/,
  );
});

test("bounds traversal of an untrusted source-map artifact", async (t) => {
  const f = await fixture(t);
  await collectFrontendSourceMaps(f.frontend, f.maps);
  const directory = path.join(f.maps, "many");
  await mkdir(directory);
  for (let start = 0; start < 4100; start += 100) {
    await Promise.all(
      Array.from({ length: 100 }, (_, i) =>
        writeFile(path.join(directory, `file-${start + i}`), ""),
      ),
    );
  }
  await assert.rejects(
    verifyFrontendSourceMaps(f.frontend, f.maps),
    /too many entries/,
  );
});
