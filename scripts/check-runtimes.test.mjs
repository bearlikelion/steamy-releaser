import assert from "node:assert/strict";
import { test } from "node:test";
import {
  checkFrontendNodeEngines,
  checkNpmReplacementReleases,
  checkNpmRelease,
  checkRuntimes,
} from "./check-runtimes.mjs";

const nodeReleases = [
  { version: "v22.23.4", lts: "Jod" },
  { version: "v24.22.2", lts: "Krypton" },
  { version: "v26.1.0", lts: "Example" },
  { version: "v26.2.0", lts: "Example" },
  { version: "v26.3.0-rc.1", lts: false },
];
const goReleases = [
  { version: "go1.27.1", stable: true },
  { version: "go1.27.2", stable: true },
];
const nodeSchedule = {
  v22: { lts: "2024-10-29", end: "2027-04-30" },
  v24: { lts: "2025-10-28", end: "2028-04-30" },
  v26: { lts: "2026-10-28", end: "2029-04-30" },
};
const asOf = "2026-11-05";

test("accepts the latest patches even if the release indexes are unordered", () => {
  assert.doesNotThrow(() =>
    checkRuntimes(
      "26.2.0",
      "1.27.2",
      nodeReleases,
      goReleases,
      nodeSchedule,
      asOf,
    ),
  );
});

test("rejects stale or unsupported runtime versions", () => {
  assert.throws(() =>
    checkRuntimes(
      "26.1.0",
      "1.27.2",
      nodeReleases,
      goReleases,
      nodeSchedule,
      asOf,
    ),
  );
  assert.throws(() =>
    checkRuntimes(
      "26.2.0",
      "1.27.1",
      nodeReleases,
      goReleases,
      nodeSchedule,
      asOf,
    ),
  );
  assert.throws(() =>
    checkRuntimes(
      "24.9.0",
      "1.27.2",
      nodeReleases,
      goReleases,
      nodeSchedule,
      asOf,
    ),
  );
  assert.throws(() =>
    checkRuntimes(
      "26.2.0",
      "1.25.0",
      nodeReleases,
      goReleases,
      nodeSchedule,
      asOf,
    ),
  );
});

test("rejects Node versions outside the official LTS support window", () => {
  for (const date of ["2026-10-27", "2029-04-30", "2030-01-01"]) {
    assert.throws(
      () =>
        checkRuntimes(
          "26.2.0",
          "1.27.2",
          nodeReleases,
          goReleases,
          nodeSchedule,
          date,
        ),
      /LTS support window/,
    );
  }
  for (const schedule of [
    {},
    { v26: { lts: "2026-02-30", end: "2029-04-30" } },
    { v26: { lts: "2029-05-01", end: "2029-04-30" } },
  ]) {
    assert.throws(
      () =>
        checkRuntimes(
          "26.2.0",
          "1.27.2",
          nodeReleases,
          goReleases,
          schedule,
          asOf,
        ),
      /Node.*schedule/,
    );
  }
});

test("checks every permitted frontend Node major and minimum against current LTS releases", () => {
  const check = (
    engines,
    lock = engines,
    schedule = nodeSchedule,
    date = asOf,
  ) =>
    checkFrontendNodeEngines(
      engines,
      lock,
      "26.2.0",
      nodeReleases,
      schedule,
      date,
    );
  assert.doesNotThrow(() => check("^22.23.4 || ^24.22.2 || ^26.2.0"));
  assert.throws(() => check(">=26.2.0"), /bounded/);
  assert.throws(() => check("^26.1.0"), /latest/);
  assert.throws(() => check("^26.2.0", "^26.1.0"), /lockfile/);
  assert.throws(() => check("^26.2.0 || ^26.2.0"), /duplicate/i);
  assert.throws(
    () => check("^26.2.0", "^26.2.0", nodeSchedule, "2029-04-30"),
    /LTS support window/,
  );
  assert.throws(() => check("^27.1.0"), /Node.*schedule/);
  assert.throws(
    () =>
      check(
        "^22.23.4 || ^24.22.2 || ^26.2.0",
        undefined,
        nodeSchedule,
        "2027-04-30",
      ),
    /LTS support window/,
  );
});

test("checks the pinned npm version against the latest registry release", () => {
  assert.doesNotThrow(() =>
    checkNpmRelease("12.2.2", { name: "npm", version: "12.2.2" }),
  );
  assert.throws(
    () => checkNpmRelease("12.2.1", { name: "npm", version: "12.2.2" }),
    /not the latest/,
  );
  assert.throws(
    () => checkNpmRelease("12.2.2", { name: "other", version: "12.2.2" }),
    /Unexpected/,
  );
  assert.throws(
    () => checkNpmRelease("12.2.2", { name: "npm", version: "latest" }),
    /Invalid version/,
  );
});

test("checks all replacement versions against their current registry releases", () => {
  const pins = [
    { name: "glob", version: "14.0.3" },
    { name: "@isaacs/nonrecursive-glob-match", version: "1.0.3" },
  ];
  assert.doesNotThrow(() => checkNpmReplacementReleases(pins, pins));
  assert.doesNotThrow(() => checkNpmReplacementReleases([], []));
  assert.throws(() => checkNpmReplacementReleases(pins, [pins[0]]), /Invalid/);
  assert.throws(
    () =>
      checkNpmReplacementReleases(pins, [
        pins[0],
        { ...pins[1], version: "1.0.4" },
      ]),
    /not the latest/,
  );
  assert.throws(
    () => checkNpmReplacementReleases(pins, [pins[1], pins[0]]),
    /Unexpected/,
  );
  assert.throws(
    () => checkNpmReplacementReleases([pins[0], pins[0]], [pins[0], pins[0]]),
    /Unexpected/,
  );
});
