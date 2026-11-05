import assert from "node:assert/strict";
import test from "node:test";
import {
  verifyFrontendSbom,
  verifyInstalledSbomEdges,
} from "./verify-frontend-sbom.mjs";

function fixture() {
  const root = {
    name: "example",
    version: "1.0.0",
    license: "AGPL-3.0-only",
    dependencies: { react: "19.3.1", "react-dom": "19.3.1" },
    devDependencies: { vite: "8.3.5" },
  };
  const installed = [
    {
      location: "node_modules/react",
      name: "react",
      version: "19.3.1",
      license: "MIT",
    },
    {
      location: "node_modules/react-dom",
      name: "react-dom",
      version: "19.3.1",
      license: "MIT",
      dependencies: { scheduler: "0.28.0" },
    },
    {
      location: "node_modules/scheduler",
      name: "scheduler",
      version: "0.28.0",
      license: "MIT",
    },
    {
      location: "node_modules/vite",
      name: "vite",
      version: "8.3.5",
      license: "MIT",
      dependencies: { rolldown: "1.2.16" },
      optionalDependencies: { "platform-helper": "1.0.0" },
    },
    {
      location: "node_modules/vite/node_modules/rolldown",
      name: "rolldown",
      version: "1.2.16",
      license: "MIT",
    },
  ];
  const component = (name, version) => ({
    name,
    version,
    type: "library",
    "bom-ref": `${name}@${version}`,
    purl: `pkg:npm/${name}@${version}`,
    licenses: [{ license: { id: "MIT" } }],
  });
  const sbom = {
    bomFormat: "CycloneDX",
    specVersion: "1.5",
    metadata: {
      component: {
        name: root.name,
        version: root.version,
        "bom-ref": "example@1.0.0",
        licenses: [{ license: { id: root.license } }],
      },
    },
    components: installed.map(({ name, version }) => component(name, version)),
    dependencies: [
      {
        ref: "example@1.0.0",
        dependsOn: ["react@19.3.1", "react-dom@19.3.1", "vite@8.3.5"],
      },
      { ref: "react@19.3.1", dependsOn: [] },
      { ref: "react-dom@19.3.1", dependsOn: ["scheduler@0.28.0"] },
      { ref: "scheduler@0.28.0", dependsOn: [] },
      { ref: "vite@8.3.5", dependsOn: ["rolldown@1.2.16"] },
      { ref: "rolldown@1.2.16", dependsOn: [] },
    ],
  };
  return { root, installed, sbom };
}

test("accepts the installed build graph, including a nested bundler and absent optional dependency", () => {
  const { root, installed, sbom } = fixture();
  assert.equal(verifyFrontendSbom(sbom, root, installed), 5);
  assert.doesNotThrow(() => verifyInstalledSbomEdges(sbom, root, installed));
});

test("rejects an inventory missing a shipped production dependency", () => {
  const { root, installed, sbom } = fixture();
  sbom.components = sbom.components.filter((entry) => entry.name !== "react");
  assert.throws(
    () => verifyFrontendSbom(sbom, root, installed),
    /missing react@19/,
  );
});

test("rejects false package URLs, license metadata, and dangling references", () => {
  const { root, installed, sbom } = fixture();
  sbom.components[0].purl = "pkg:npm/other@19.3.1";
  assert.throws(() => verifyFrontendSbom(sbom, root, installed), /Unexpected/);
  sbom.components[0].purl = "pkg:npm/react@19.3.1";
  sbom.components[0].licenses = [{ license: { id: "ISC" } }];
  assert.throws(() => verifyFrontendSbom(sbom, root, installed), /Unexpected/);
  sbom.components[0].licenses = [{ license: { id: "MIT" } }];
  sbom.dependencies[0].dependsOn.push("not-a-component");
  assert.throws(
    () => verifyFrontendSbom(sbom, root, installed),
    /dependency references/,
  );
});

test("rejects missing edges even when all components are listed", () => {
  const { root, installed, sbom } = fixture();
  sbom.dependencies.find(
    (entry) => entry.ref === "react-dom@19.3.1",
  ).dependsOn = [];
  assert.equal(verifyFrontendSbom(sbom, root, installed), 5);
  assert.throws(
    () => verifyInstalledSbomEdges(sbom, root, installed),
    /react-dom -> scheduler/,
  );
});
