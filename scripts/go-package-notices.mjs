import { readdirSync, realpathSync, statSync } from "node:fs";
import path from "node:path";

const noticeName =
  /^(?:licen[cs]es?|copying|notices?|copyright(?:notice)?|authors|patents|third[-_. ]?party[-_. ]?(?:licen[cs]es?|notices?|copyright(?:notice)?))(?:$|[._-])/i;

export function noticeFiles(directory) {
  return readdirSync(directory)
    .filter(
      (name) =>
        noticeName.test(name) && statSync(path.join(directory, name)).isFile(),
    )
    .sort();
}

export function goNoticeDirectories(packageDirectory, embedFiles) {
  if (
    !Array.isArray(embedFiles) ||
    embedFiles.some(
      (file) => typeof file !== "string" || !file || path.isAbsolute(file),
    )
  ) {
    throw new Error(
      `Invalid embedded-file list for Go package ${packageDirectory}`,
    );
  }
  const root = path.resolve(packageDirectory);
  const directories = [root];
  for (const file of embedFiles) {
    const target = path.resolve(root, file);
    const relative = path.relative(root, target);
    if (
      relative === ".." ||
      relative.startsWith(`..${path.sep}`) ||
      path.isAbsolute(relative)
    ) {
      throw new Error(`Embedded file is outside its Go package: ${file}`);
    }
    directories.push(path.dirname(target));
  }
  return directories;
}

// Find notices in compiled package directories and their ancestors. The module
// root is handled separately so its license remains mandatory.
export function nestedGoNotices(moduleDirectory, packageDirectories) {
  const root = realpathSync(moduleDirectory);
  const directories = new Set();
  for (const packageDirectory of packageDirectories) {
    let current = realpathSync(packageDirectory);
    const relative = path.relative(root, current);
    if (
      relative === ".." ||
      relative.startsWith(`..${path.sep}`) ||
      path.isAbsolute(relative)
    ) {
      throw new Error(
        `Go package directory is outside its module: ${packageDirectory}`,
      );
    }
    while (path.relative(root, current) !== "") {
      directories.add(current);
      const parent = path.dirname(current);
      if (parent === current)
        throw new Error(`Cannot find Go module root for ${packageDirectory}`);
      current = parent;
    }
  }
  return [...directories]
    .map((directory) => ({
      directory,
      relative: path.relative(root, directory).split(path.sep).join("/"),
      files: noticeFiles(directory),
    }))
    .filter(({ files }) => files.length > 0)
    .sort((a, b) => a.relative.localeCompare(b.relative, "en"));
}
