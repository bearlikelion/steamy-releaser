#!/usr/bin/env python3
"""Install pinned Go and Node archives before running either runtime."""

import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import time
import urllib.error
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parent.parent
MAX_DOWNLOAD = 128 * 1024 * 1024
MAX_EXTRACTED = 1024 * 1024 * 1024
MAX_NODE = 256 * 1024 * 1024
MAX_ENTRIES = 30000
VERSION = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+$")
DIGEST = re.compile(r"^[0-9a-f]{64}$")
WINDOWS_NAME = re.compile(r"^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\.|$)", re.I)


def target_from_environment(env):
    targets = {
        ("Linux", "X64"): "linux-x64",
        ("Windows", "X64"): "windows-x64",
        ("macOS", "ARM64"): "macos-arm64",
    }
    try:
        return targets[(env["RUNNER_OS"], env["RUNNER_ARCH"])]
    except KeyError as error:
        raise ValueError("Unsupported GitHub runner OS or architecture") from error


def validate_pins(pins, target, go_version, node_version):
    if not VERSION.fullmatch(go_version) or not VERSION.fullmatch(node_version):
        raise ValueError("Runtime versions must be exact release versions")
    if pins["go"]["version"] != go_version or pins["node"]["version"] != node_version:
        raise ValueError("Runtime pins do not match go.mod and .nvmrc")
    go_names = {
        "linux-x64": f"go{go_version}.linux-amd64.tar.gz",
        "windows-x64": f"go{go_version}.windows-amd64.zip",
        "macos-arm64": f"go{go_version}.darwin-arm64.tar.gz",
    }
    node_names = {
        "linux-x64": f"node-v{node_version}-linux-x64.tar.gz",
        "windows-x64": "win-x64/node.exe",
        "macos-arm64": f"node-v{node_version}-darwin-arm64.tar.gz",
    }
    selected = []
    for kind, names in (("go", go_names), ("node", node_names)):
        entry = pins[kind]["archives"][target]
        if entry["filename"] != names[target] or not DIGEST.fullmatch(entry["sha256"]):
            raise ValueError(f"Invalid {kind} runtime pin for {target}")
        selected.append(entry)
    return selected


def download_verified(url, destination, digest):
    for attempt in range(3):
        try:
            request = urllib.request.Request(url, headers={"User-Agent": "steam-releaser-ci"})
            with urllib.request.urlopen(request, timeout=60) as response:
                if not response.geturl().startswith("https://"):
                    raise ValueError("Runtime download redirected away from HTTPS")
                total = 0
                checksum = hashlib.sha256()
                with destination.open("xb") as output:
                    while True:
                        chunk = response.read(1024 * 1024)
                        if not chunk:
                            break
                        total += len(chunk)
                        if total > MAX_DOWNLOAD:
                            raise ValueError("Runtime download exceeds the size limit")
                        checksum.update(chunk)
                        output.write(chunk)
            if checksum.hexdigest() != digest:
                raise ValueError("Runtime archive SHA-256 does not match the pin")
            return
        except urllib.error.HTTPError as error:
            destination.unlink(missing_ok=True)
            if attempt == 2 or (error.code != 429 and error.code < 500):
                raise
        except (urllib.error.URLError, TimeoutError, ConnectionError):
            destination.unlink(missing_ok=True)
            if attempt == 2:
                raise
        except Exception:
            destination.unlink(missing_ok=True)
            raise
        time.sleep(0.25 * 2**attempt)
    raise RuntimeError("Runtime download failed")


def checked_archive_path(name):
    parts = name.rstrip("/").split("/")
    if not parts or parts[0] != "go" or any(
        not part
        or part in (".", "..")
        or re.search(r'[<>:"\\|?*\x00-\x1f\x7f]', part)
        or part.endswith((".", " "))
        or WINDOWS_NAME.match(part)
        for part in parts
    ):
        raise ValueError(f"Unsafe Go archive path: {name}")
    return parts


def inspect_go_members(members):
    seen = set()
    total = 0
    for name, size, kind in members:
        parts = checked_archive_path(name)
        normalized = "/".join(parts).lower()
        if normalized in seen:
            raise ValueError(f"Duplicate Go archive path: {name}")
        seen.add(normalized)
        if kind not in ("file", "directory") or size < 0:
            raise ValueError(f"Unsupported Go archive entry: {name}")
        total += size
        if len(seen) > MAX_ENTRIES or total > MAX_EXTRACTED:
            raise ValueError("Go archive exceeds the size limit")
    if not seen:
        raise ValueError("Go archive is empty")


def extract_go(archive, destination, windows):
    destination.mkdir()
    if windows:
        with zipfile.ZipFile(archive) as source:
            entries = source.infolist()
            inspect_go_members(
                (
                    item.filename,
                    item.file_size,
                    "directory" if item.is_dir() else
                    "link" if stat.S_ISLNK(item.external_attr >> 16) else "file",
                )
                for item in entries
            )
            for item in entries:
                target = destination.joinpath(*checked_archive_path(item.filename)[1:])
                if item.is_dir():
                    target.mkdir(parents=True, exist_ok=True)
                else:
                    target.parent.mkdir(parents=True, exist_ok=True)
                    with source.open(item) as data, target.open("xb") as output:
                        shutil.copyfileobj(data, output, 1024 * 1024)
    else:
        with tarfile.open(archive, "r:gz") as source:
            entries = source.getmembers()
            inspect_go_members(
                (
                    item.name,
                    item.size,
                    "directory" if item.isdir() else "file" if item.isfile() else "other",
                )
                for item in entries
            )
            for item in entries:
                target = destination.joinpath(*checked_archive_path(item.name)[1:])
                if item.isdir():
                    target.mkdir(parents=True, exist_ok=True)
                else:
                    target.parent.mkdir(parents=True, exist_ok=True)
                    with source.extractfile(item) as data, target.open("xb") as output:
                        shutil.copyfileobj(data, output, 1024 * 1024)
                    target.chmod(item.mode & 0o777)
    executable = destination / "bin" / ("go.exe" if windows else "go")
    if not executable.is_file() or executable.is_symlink():
        raise ValueError("Verified Go archive has no Go executable")


def extract_node(archive, destination, target, version):
    destination.mkdir()
    executable = destination / ("node.exe" if target == "windows-x64" else "node")
    if target == "windows-x64":
        shutil.copyfile(archive, executable)
    else:
        platform = "linux-x64" if target == "linux-x64" else "darwin-arm64"
        expected = f"node-v{version}-{platform}/bin/node"
        with tarfile.open(archive, "r:gz") as source:
            matches = [item for item in source if item.name == expected]
            if len(matches) != 1 or not matches[0].isfile() or matches[0].size > MAX_NODE:
                raise ValueError("Verified Node archive has no unique Node executable")
            with source.extractfile(matches[0]) as data, executable.open("xb") as output:
                shutil.copyfileobj(data, output, 1024 * 1024)
        executable.chmod(0o755)
    return executable


def append_actions_file(path, value):
    if not path or "\n" in value or "\r" in value:
        raise ValueError("Missing or invalid GitHub Actions environment file")
    with Path(path).open("a", encoding="utf-8", newline="\n") as output:
        output.write(value + "\n")


def runtime_environment(root, runner_temp):
    return {
        "GOROOT": str(root / "go"),
        "GOPATH": str(root / "gopath"),
        # Keep downloaded modules in this runner's temporary directory.
        "GOMODCACHE": str(Path(runner_temp) / "steam-releaser-go-mod-cache"),
        "GOTOOLCHAIN": "local",
    }


def main(argv=None):
    args = sys.argv[1:] if argv is None else argv
    if args not in ([], ["--node-only"]):
        raise ValueError("Usage: prepare-runtimes.py [--node-only]")
    node_only = args == ["--node-only"]
    env = os.environ
    target = target_from_environment(env)
    node_version = (ROOT / ".nvmrc").read_text(encoding="utf-8").strip()
    go_mod = (ROOT / "go.mod").read_text(encoding="utf-8")
    match = re.search(r"^go\s+(\d+\.\d+\.\d+)\s*$", go_mod, re.M)
    if not match:
        raise ValueError("go.mod must specify an exact Go patch")
    go_version = match.group(1)
    pins = json.loads((ROOT / "scripts/runtime-toolchain.json").read_text(encoding="utf-8"))
    go_pin, node_pin = validate_pins(pins, target, go_version, node_version)
    runner_temp = env.get("RUNNER_TEMP", "")
    if (
        not runner_temp
        or not Path(runner_temp).is_absolute()
        or any(char in runner_temp for char in "\r\n")
    ):
        raise ValueError("Missing or invalid RUNNER_TEMP")
    # Hosted runners have a clean temporary directory for each job.
    root = Path(runner_temp) / "steam-releaser-runtimes"
    root.mkdir()
    try:
        go_archive, node_archive = root / "go.download", root / "node.download"
        if not node_only:
            download_verified(
                f"https://dl.google.com/go/{go_pin['filename']}",
                go_archive,
                go_pin["sha256"],
            )
        download_verified(
            f"https://nodejs.org/dist/v{node_version}/{node_pin['filename']}",
            node_archive,
            node_pin["sha256"],
        )
        go_root, node_root, gopath = root / "go", root / "node", root / "gopath"
        node = extract_node(node_archive, node_root, target, node_version)
        node_output = subprocess.check_output([str(node), "--version"], text=True).strip()
        if node_output != f"v{node_version}":
            raise ValueError("Verified Node executable reports the wrong version")
        if node_only:
            node_archive.unlink()
            append_actions_file(env.get("GITHUB_PATH"), str(node_root))
            print(f"Prepared SHA-256-verified Node {node_version} for {target}")
            return
        extract_go(go_archive, go_root, target == "windows-x64")
        go = go_root / "bin" / ("go.exe" if target == "windows-x64" else "go")
        gopath.mkdir()
        expected_go_target = {
            "linux-x64": "linux/amd64",
            "windows-x64": "windows/amd64",
            "macos-arm64": "darwin/arm64",
        }[target]
        runtime_env = runtime_environment(root, runner_temp)
        go_env = {**env, **runtime_env}
        go_output = subprocess.check_output([str(go), "version"], text=True, env=go_env).strip()
        if go_output != f"go version go{go_version} {expected_go_target}":
            raise ValueError("Verified Go executable reports the wrong version")
        go_archive.unlink()
        node_archive.unlink()
        for name in ("GOROOT", "GOPATH", "GOMODCACHE"):
            append_actions_file(env.get("GITHUB_ENV"), f"{name}={runtime_env[name]}")
        append_actions_file(env.get("GITHUB_PATH"), str(node_root))
        append_actions_file(env.get("GITHUB_PATH"), str(go_root / "bin"))
        print(f"Prepared SHA-256-verified Go {go_version} and Node {node_version} for {target}")
    except Exception:
        shutil.rmtree(root, ignore_errors=True)
        raise


if __name__ == "__main__":
    main()
