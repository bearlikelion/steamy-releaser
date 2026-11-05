import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { runInNewContext } from "node:vm";
import { fileURLToPath } from "node:url";

const release = readFileSync(
  fileURLToPath(new URL("../.github/workflows/release.yml", import.meta.url)),
  "utf8",
);

// These gates use boolean operators shared by GitHub expressions and JS.
// The Go workflow test also parses the file as YAML.
function jobCondition(job) {
  const section = release.split(new RegExp(`\\n  ${job}:\\n`))[1];
  assert.ok(section, `missing ${job} job`);
  const match = section.match(/^    if: \$\{\{\s*(.*?)\s*\}\}\s*$/m);
  assert.ok(match, `missing ${job} job condition`);
  return match[1];
}

function evaluate(
  condition,
  { isPrivate, packageResult, attestResult, cancelled = false },
) {
  return runInNewContext(condition, {
    github: { event: { repository: { private: isPrivate } } },
    needs: {
      package: { result: packageResult },
      attest: { result: attestResult },
    },
    cancelled: () => cancelled,
  });
}

test("private releases do not start an attestation runner", () => {
  const condition = jobCondition("attest");
  assert.equal(evaluate(condition, { isPrivate: true }), false);
  assert.equal(evaluate(condition, { isPrivate: false }), true);
});

test("release publication requires successful builds and public attestations", () => {
  const condition = jobCondition("publish");
  const cases = [
    {
      isPrivate: true,
      packageResult: "success",
      attestResult: "skipped",
      expected: true,
    },
    {
      isPrivate: false,
      packageResult: "success",
      attestResult: "success",
      expected: true,
    },
    {
      isPrivate: false,
      packageResult: "success",
      attestResult: "skipped",
      expected: false,
    },
    {
      isPrivate: false,
      packageResult: "success",
      attestResult: "failure",
      expected: false,
    },
    {
      isPrivate: true,
      packageResult: "failure",
      attestResult: "skipped",
      expected: false,
    },
    {
      isPrivate: false,
      packageResult: "failure",
      attestResult: "success",
      expected: false,
    },
    {
      isPrivate: true,
      packageResult: "success",
      attestResult: "skipped",
      cancelled: true,
      expected: false,
    },
  ];
  for (const item of cases) {
    assert.equal(
      evaluate(condition, item),
      item.expected,
      JSON.stringify(item),
    );
  }
});
