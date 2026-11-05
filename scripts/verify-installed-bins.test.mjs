import assert from "node:assert/strict";
import {
  mkdir,
  mkdtemp,
  readFile,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { verifyInstalledBins } from "./verify-installed-bins.mjs";

const normalize = (metadata) => metadata;
const cmdShim = async (source, destination) => {
  await mkdir(path.dirname(destination), { recursive: true });
  for (const suffix of ["", ".cmd", ".ps1"])
    await writeFile(
      destination + suffix,
      `${path.relative(path.dirname(destination), source)}:${suffix}:${await readFile(source, "utf8")}`,
    );
};

async function fixture(t, windows = false) {
  const root = await mkdtemp(path.join(os.tmpdir(), "steam-bin-test-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const directory = path.join(root, "node_modules", "example");
  const bin = path.join(root, "node_modules", ".bin", "example");
  await mkdir(directory, { recursive: true });
  await mkdir(path.dirname(bin), { recursive: true });
  await writeFile(
    path.join(directory, "package.json"),
    JSON.stringify({ name: "example", bin: { example: "index.js" } }),
  );
  await writeFile(path.join(directory, "index.js"), "#!/usr/bin/env node\n");
  if (windows) await cmdShim(path.join(directory, "index.js"), bin);
  else await symlink("../example/index.js", bin);
  return {
    root,
    bin,
    inventory: [{ location: "node_modules/example" }],
    options: {
      platform: windows ? "win32" : "linux",
      tools: { normalize, cmdShim },
      requiredCommands: new Map([["example", "example"]]),
    },
  };
}

test("checks installed POSIX command links and rejects a redirected link", async (t) => {
  if (process.platform === "win32") {
    t.skip("POSIX symbolic link test");
    return;
  }
  const f = await fixture(t);
  assert.equal(await verifyInstalledBins(f.root, f.inventory, f.options), 1);
  await rm(f.bin);
  await symlink("../example/package.json", f.bin);
  await assert.rejects(
    verifyInstalledBins(f.root, f.inventory, f.options),
    /differs from its verified target/,
  );
});

test("compares complete Windows shim contents and rejects injected commands", async (t) => {
  const f = await fixture(t, true);
  assert.equal(await verifyInstalledBins(f.root, f.inventory, f.options), 1);
  await writeFile(f.bin + ".cmd", "injected command\n", { flag: "a" });
  await assert.rejects(
    verifyInstalledBins(f.root, f.inventory, f.options),
    /differs from its verified target/,
  );
});

test("rejects unexpected commands and missing required command owners", async (t) => {
  const f = await fixture(t, true);
  await writeFile(
    path.join(path.dirname(f.bin), "unexpected.cmd"),
    "unexpected",
  );
  await assert.rejects(
    verifyInstalledBins(f.root, f.inventory, f.options),
    /Unexpected installed commands/,
  );
  await rm(path.join(path.dirname(f.bin), "unexpected.cmd"));
  f.options.requiredCommands.set("example", "another-package");
  await assert.rejects(
    verifyInstalledBins(f.root, f.inventory, f.options),
    /Unexpected owner/,
  );
});
