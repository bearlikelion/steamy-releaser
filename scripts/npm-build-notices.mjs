import { readFileSync, realpathSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { noticeFiles } from "./go-package-notices.mjs";

export function requireRolldownThirdPartyNotice(directory) {
  const found = noticeFiles(directory).some(
    (name) =>
      /^third[-_. ]?party[-_. ]?licen[cs]es?(?:$|[._-])/i.test(name) &&
      readFileSync(path.join(directory, name), "utf8").trim().length > 0,
  );
  if (!found) {
    throw new Error("Review Rolldown's missing third-party license notice");
  }
}

// Vite inserts its own module-preload polyfill and Rolldown runtime helpers
// into the frontend. Tailwind's CSS can also be copied into the output. Resolve
// the actual installed tools so a nested Rolldown is not silently skipped.
export function frontendBuildToolLocations(projectDirectory, lock) {
  const root = realpathSync(projectDirectory);
  const projectRequire = createRequire(path.join(root, "package.json"));
  const vite = projectRequire.resolve("vite/package.json");
  const viteMetadata = JSON.parse(readFileSync(vite, "utf8"));
  if (!Object.hasOwn(viteMetadata.dependencies ?? {}, "rolldown")) {
    throw new Error("Review the Vite build tool dependency and its notices");
  }
  const entries = [
    ["vite", vite],
    ["tailwindcss", projectRequire.resolve("tailwindcss/package.json")],
    ["rolldown", createRequire(vite).resolve("rolldown/package.json")],
  ];
  const locations = new Map();
  for (const [name, metadataPath] of entries) {
    const relative = path
      .relative(root, path.dirname(metadataPath))
      .split(path.sep)
      .join("/");
    if (!relative.startsWith("node_modules/") || !lock.packages[relative]) {
      throw new Error(`Build tool ${name} is outside the locked install`);
    }
    locations.set(relative, name);
  }
  return locations;
}
