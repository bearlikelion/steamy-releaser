import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { gunzipSync, gzipSync } from "node:zlib";
import { archiveFor } from "./npm-archive-fixtures.mjs";
import {
  checkArchiveEntry,
  inspectArchive,
  prepareNpm,
  validatePin,
  validateReplacements,
} from "./prepare-npm.mjs";

function pinFor(bytes) {
  return {
    name: "npm",
    version: "12.2.2",
    resolved: "https://registry.npmjs.org/npm/-/npm-12.2.2.tgz",
    integrity: `sha512-${createHash("sha512").update(bytes).digest("base64")}`,
  };
}

async function fixture(t, metadataVersion = "12.2.2") {
  const root = await mkdtemp(
    path.join(os.tmpdir(), "steam-releaser-npm-test-"),
  );
  t.after(() => rm(root, { recursive: true, force: true }));
  const bytes = archiveFor([
    {
      name: "package/package.json",
      contents: JSON.stringify({ name: "npm", version: metadataVersion }),
    },
    { name: "package/bin/npm-cli.js", contents: 'console.log("12.2.2");\n' },
  ]);
  const env = {
    RUNNER_TEMP: root,
    GITHUB_PATH: path.join(root, "path"),
    GITHUB_ENV: path.join(root, "env"),
  };
  await writeFile(env.GITHUB_PATH, "");
  await writeFile(env.GITHUB_ENV, "");
  return { root, bytes, env };
}

async function replacementFixture(t, options = {}) {
  const { root, env } = await fixture(t);
  const specs = [
    {
      name: "glob",
      version: "14.0.3",
      replacesVersion: "13.0.6",
      dependencies: {
        minimatch: "^11.0.2",
        minipass: "^7.1.3",
        "path-scurry": "^3.0.0",
      },
    },
    {
      name: "minimatch",
      version: "11.0.4",
      replacesVersion: "10.2.5",
      dependencies: {
        "@isaacs/nonrecursive-glob-match": "^1.0.3",
        "brace-expansion": "^5.0.12",
      },
    },
    {
      name: "path-scurry",
      version: "3.0.0",
      dependencies: { "lru-cache": "^11.0.0", minipass: "^7.1.2" },
    },
    {
      name: "@isaacs/nonrecursive-glob-match",
      version: "1.0.3",
      dependencies: {},
    },
  ];
  const npmEntries = [
    {
      name: "package/package.json",
      contents: JSON.stringify({ name: "npm", version: "12.2.2" }),
    },
    { name: "package/bin/npm-cli.js", contents: 'console.log("12.2.2");\n' },
    ...[
      ["glob", options.globVersion || "13.0.6"],
      ["minimatch", "10.2.5"],
      ["minipass", options.minipassVersion || "7.1.3"],
      ["lru-cache", "11.5.2"],
      ["brace-expansion", "5.0.12"],
      ["balanced-match", "4.0.4"],
    ].map(([name, version]) => ({
      name: `package/node_modules/${name}/package.json`,
      contents: JSON.stringify({ name, version }),
    })),
  ];
  const npmBytes = archiveFor(npmEntries);
  const pin = {
    ...pinFor(npmBytes),
    replacementsFor: "12.2.2",
    replacements: [],
  };
  const archives = new Map([[pin.resolved, npmBytes]]);
  for (const spec of specs) {
    const metadata = {
      name: spec.name,
      version: spec.version,
      dependencies: spec.dependencies,
    };
    if (options.extraDependency && spec.name === "glob")
      metadata.dependencies = {
        ...metadata.dependencies,
        unexpected: "^1.0.0",
      };
    if (options.optionalDependency && spec.name === "glob")
      metadata.optionalDependencies = { unexpected: "^1.0.0" };
    const entries = [
      { name: "package/package.json", contents: JSON.stringify(metadata) },
      { name: "package/index.js", contents: `// ${spec.name}\n` },
    ];
    if (options.bundledChild && spec.name === "glob")
      entries.push({
        name: "package/node_modules/unexpected/index.js",
        contents: "",
      });
    const bytes = archiveFor(entries);
    const basename = spec.name.slice(spec.name.lastIndexOf("/") + 1);
    const resolved = `https://registry.npmjs.org/${spec.name}/-/${basename}-${spec.version}.tgz`;
    const replacement = {
      name: spec.name,
      version: spec.version,
      resolved,
      integrity: `sha512-${createHash("sha512").update(bytes).digest("base64")}`,
    };
    if (spec.replacesVersion)
      replacement.replacesVersion = spec.replacesVersion;
    pin.replacements.push(replacement);
    archives.set(resolved, bytes);
  }
  return {
    root,
    env,
    pin,
    archives,
    fetcher: async (url) => {
      if (!archives.has(url)) throw new Error(`unexpected URL: ${url}`);
      return new Response(archives.get(url));
    },
  };
}

test("validates the exact npm identity, source and digest", () => {
  const valid = pinFor(Buffer.from("package"));
  assert.equal(validatePin(valid).length, 64);
  for (const invalid of [
    { ...valid, name: "other" },
    { ...valid, version: "12.2.2-beta" },
    { ...valid, resolved: "https://example.com/npm.tgz" },
    { ...valid, integrity: "sha512-invalid" },
  ])
    assert.throws(() => validatePin(invalid));
});

test("rejects altered replacement plans before downloading", async (t) => {
  const { pin } = await replacementFixture(t);
  assert.equal(validateReplacements(pin).length, 4);
  for (const invalid of [
    { ...pin, replacementsFor: "12.2.1" },
    { ...pin, replacements: pin.replacements.slice(1) },
    {
      ...pin,
      replacements: pin.replacements.map((p, i) =>
        i === 0 ? { ...p, name: "other" } : p,
      ),
    },
    {
      ...pin,
      replacements: pin.replacements.map((p, i) =>
        i === 0 ? { ...p, resolved: "https://example.com/forged.tgz" } : p,
      ),
    },
    {
      ...pin,
      replacements: pin.replacements.map((p, i) =>
        i === 2 ? { ...p, replacesVersion: "2.0.2" } : p,
      ),
    },
  ])
    assert.throws(() => validateReplacements(invalid));
});

test("activates replacements only after pinned and current TUF signature checks", async (t) => {
  const { env, pin, fetcher } = await replacementFixture(t);
  const checks = [];
  const result = await prepareNpm({
    env,
    pin,
    fetcher,
    verify: async (packages, options) => {
      assert.deepEqual(
        packages.map((p) => p.name),
        [
          "npm",
          "glob",
          "minimatch",
          "path-scurry",
          "@isaacs/nonrecursive-glob-match",
        ],
      );
      assert.equal(await readFile(env.GITHUB_PATH, "utf8"), "");
      assert.equal(await readFile(env.GITHUB_ENV, "utf8"), "");
      if (options.npmDirectory) {
        const glob = JSON.parse(
          await readFile(
            path.join(options.npmDirectory, "node_modules/glob/package.json"),
            "utf8",
          ),
        );
        const scurry = JSON.parse(
          await readFile(
            path.join(
              options.npmDirectory,
              "node_modules/glob/node_modules/path-scurry/package.json",
            ),
            "utf8",
          ),
        );
        assert.equal(glob.version, "14.0.3");
        assert.equal(scurry.version, "3.0.0");
        checks.push("fresh TUF");
      } else checks.push("pinned key");
    },
  });
  assert.deepEqual(checks, ["pinned key", "fresh TUF"]);
  const minimatch = JSON.parse(
    await readFile(
      path.join(result.npmDirectory, "node_modules/minimatch/package.json"),
      "utf8",
    ),
  );
  const nonrecursive = JSON.parse(
    await readFile(
      path.join(
        result.npmDirectory,
        "node_modules/@isaacs/nonrecursive-glob-match/package.json",
      ),
      "utf8",
    ),
  );
  assert.equal(minimatch.version, "11.0.4");
  assert.equal(nonrecursive.version, "1.0.3");
  assert.equal(
    await readFile(env.GITHUB_PATH, "utf8"),
    `${result.shimDirectory}\n`,
  );
});

test("rejects tampering or a changed npm layout without activating replacements", async (t) => {
  for (const [scenario, expected, expectedChecks] of [
    ["digest", /glob tarball SHA-512/, 0],
    ["old-version", /Unexpected installed glob version/, 1],
    ["support-version", /Unexpected installed minipass version/, 1],
    ["extra-dependency", /Unexpected glob package metadata/, 1],
    ["optional-dependency", /Unexpected glob package metadata/, 1],
    ["bundled-child", /Bundled dependencies are not permitted/, 1],
    ["fresh-tuf", /signing key removed/, 2],
  ]) {
    const options = {
      globVersion: scenario === "old-version" ? "13.0.7" : undefined,
      minipassVersion: scenario === "support-version" ? "7.1.4" : undefined,
      extraDependency: scenario === "extra-dependency",
      optionalDependency: scenario === "optional-dependency",
      bundledChild: scenario === "bundled-child",
    };
    const { env, pin, archives, fetcher } = await replacementFixture(
      t,
      options,
    );
    if (scenario === "digest")
      archives.set(pin.replacements[0].resolved, Buffer.from("tampered"));
    let checks = 0;
    await assert.rejects(
      prepareNpm({
        env,
        pin,
        fetcher,
        verify: async () => {
          if (++checks === 2 && scenario === "fresh-tuf")
            throw new Error("signing key removed");
        },
      }),
      expected,
      scenario,
    );
    assert.equal(checks, expectedChecks, scenario);
    assert.equal(await readFile(env.GITHUB_PATH, "utf8"), "", scenario);
    assert.equal(await readFile(env.GITHUB_ENV, "utf8"), "", scenario);
  }
});

test("rejects links, path escapes and case-insensitive collisions in archives", () => {
  for (const entry of [
    { type: "SymbolicLink", path: "package/link", size: 0 },
    { type: "File", path: "package/../outside", size: 1 },
    { type: "File", path: "package/C:\\outside", size: 1 },
    { type: "File", path: "package/NUL.txt", size: 1 },
  ])
    assert.throws(() => checkArchiveEntry(entry, new Set()));
  const seen = new Set();
  checkArchiveEntry({ type: "File", path: "package/File", size: 1 }, seen);
  assert.throws(() =>
    checkArchiveEntry({ type: "File", path: "package/file", size: 1 }, seen),
  );
});

test("rejects corrupt headers and unsafe archive entries before extraction", () => {
  const valid = archiveFor([
    { name: "package/package.json", contents: "{}" },
    { name: "package/bin/npm-cli.js", contents: "" },
  ]);
  assert.equal(inspectArchive(valid).length, 2);
  const corrupt = gunzipSync(valid);
  corrupt[0] ^= 1;
  assert.throws(() => inspectArchive(gzipSync(corrupt)), /checksum/);
  assert.throws(
    () =>
      inspectArchive(
        archiveFor([{ name: "package/../outside", contents: "bad" }]),
      ),
    /Unsafe/,
  );
  assert.throws(
    () =>
      inspectArchive(
        archiveFor([{ name: "package/link", contents: "", type: "2" }]),
      ),
    /Unsupported/,
  );
  assert.throws(
    () => inspectArchive(gzipSync(gunzipSync(valid).subarray(0, 512))),
    /Truncated|terminator/,
  );
});

test("accepts only the expected alternate root for type package archives", () => {
  const typesArchive = archiveFor([
    { name: "react/package.json", contents: '{"name":"@types/react"}' },
    { name: "react/index.d.ts", contents: "export {};" },
  ]);
  assert.throws(
    () =>
      inspectArchive(typesArchive, { requiredPaths: ["package/package.json"] }),
    /Unexpected npm archive root/,
  );
  const files = inspectArchive(typesArchive, {
    allowedRoots: ["package", "react"],
    requiredPaths: ["package/package.json"],
  });
  assert.deepEqual(
    files.map((file) => file.path),
    ["package/package.json", "package/index.d.ts"],
  );
  const mixed = archiveFor([
    { name: "react/package.json", contents: "{}" },
    { name: "package/index.d.ts", contents: "bad" },
  ]);
  assert.throws(
    () =>
      inspectArchive(mixed, {
        allowedRoots: ["package", "react"],
        requiredPaths: ["package/package.json"],
      }),
    /more than one root/,
  );
});

test("activates npm only after digest, signature and archive checks", async (t) => {
  const { bytes, env } = await fixture(t);
  const checks = [];
  const result = await prepareNpm({
    env,
    pin: pinFor(bytes),
    fetcher: async () => new Response(bytes),
    verify: async ([pin], options) => {
      assert.equal(pin.version, "12.2.2");
      assert.equal(await readFile(env.GITHUB_PATH, "utf8"), "");
      checks.push(options.npmDirectory ? "fresh TUF" : "pinned key");
    },
  });
  assert.deepEqual(checks, ["pinned key", "fresh TUF"]);
  assert.equal(
    await readFile(env.GITHUB_PATH, "utf8"),
    `${result.shimDirectory}\n`,
  );
  assert.equal(
    await readFile(env.GITHUB_ENV, "utf8"),
    `STEAM_RELEASER_NPM_DIR=${result.npmDirectory}\n`,
  );
  if (process.platform !== "win32") {
    assert.equal(
      execFileSync(path.join(result.shimDirectory, "npm"), ["--version"], {
        encoding: "utf8",
      }).trim(),
      "12.2.2",
    );
  }
});

test("does not activate a package with the wrong digest or identity", async (t) => {
  const { bytes, env } = await fixture(t, "12.2.1");
  let verified = false;
  const verify = async () => {
    verified = true;
  };
  await assert.rejects(
    prepareNpm({
      env,
      pin: pinFor(Buffer.from("different")),
      fetcher: async () => new Response(bytes),
      verify,
    }),
    /SHA-512/,
  );
  assert.equal(verified, false);
  await assert.rejects(
    prepareNpm({
      env,
      pin: pinFor(bytes),
      fetcher: async () => new Response(bytes),
      verify,
    }),
    /does not match its pin/,
  );
  assert.equal(verified, true);
  assert.equal(await readFile(env.GITHUB_PATH, "utf8"), "");
  assert.equal(await readFile(env.GITHUB_ENV, "utf8"), "");
});

test("does not activate a package rejected by the current TUF keys", async (t) => {
  const { bytes, env } = await fixture(t);
  let checks = 0;
  await assert.rejects(
    prepareNpm({
      env,
      pin: pinFor(bytes),
      fetcher: async () => new Response(bytes),
      verify: async () => {
        if (++checks === 2) throw new Error("signing key removed");
      },
    }),
    /signing key removed/,
  );
  assert.equal(checks, 2);
  assert.equal(await readFile(env.GITHUB_PATH, "utf8"), "");
  assert.equal(await readFile(env.GITHUB_ENV, "utf8"), "");
});
