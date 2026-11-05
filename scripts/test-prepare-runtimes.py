import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import sys
import tarfile
import tempfile
import unittest
from unittest import mock
import zipfile


SCRIPT = Path(__file__).with_name("prepare-runtimes.py")
sys.dont_write_bytecode = True
SPEC = importlib.util.spec_from_file_location("prepare_runtimes", SCRIPT)
runtime = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(runtime)


class Response(io.BytesIO):
    def geturl(self):
        return "https://example.invalid/archive"


def tar_archive(path, entries):
    with tarfile.open(path, "w:gz") as output:
        for name, contents, kind in entries:
            member = tarfile.TarInfo(name)
            member.mode = 0o755
            if kind == "link":
                member.type = tarfile.SYMTYPE
                member.linkname = "../../outside"
                output.addfile(member)
            else:
                member.size = len(contents)
                output.addfile(member, io.BytesIO(contents))


class RuntimeBootstrapTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)

    def test_runtime_pins_match_versions_and_expected_official_filenames(self):
        pins = json.loads((SCRIPT.parent / "runtime-toolchain.json").read_text())
        go_version = pins["go"]["version"]
        node_version = pins["node"]["version"]
        for target in ("linux-x64", "windows-x64", "macos-arm64"):
            runtime.validate_pins(pins, target, go_version, node_version)
        with self.assertRaisesRegex(ValueError, "do not match"):
            runtime.validate_pins(pins, "linux-x64", "0.0.0", node_version)
        pins["node"]["archives"]["linux-x64"]["filename"] = "https://other.invalid/node"
        with self.assertRaisesRegex(ValueError, "Invalid node runtime pin"):
            runtime.validate_pins(pins, "linux-x64", go_version, node_version)
        with self.assertRaisesRegex(ValueError, "Unsupported"):
            runtime.target_from_environment({"RUNNER_OS": "Linux", "RUNNER_ARCH": "ARM64"})

    def test_download_requires_pinned_digest_and_removes_bad_bytes(self):
        destination = self.root / "download"
        contents = b"runtime archive"
        digest = hashlib.sha256(contents).hexdigest()
        def fake_download(*args, **kwargs):
            return Response(contents)
        with mock.patch.object(runtime.urllib.request, "urlopen", side_effect=fake_download):
            runtime.download_verified("https://example.invalid/archive", destination, digest)
            self.assertEqual(destination.read_bytes(), contents)
            destination.unlink()
            with self.assertRaisesRegex(ValueError, "SHA-256"):
                runtime.download_verified("https://example.invalid/archive", destination, "0" * 64)
        self.assertFalse(destination.exists())

    def test_no_runtime_executes_if_download_fails_integrity(self):
        project = self.root / "project"
        (project / "scripts").mkdir(parents=True)
        pins = json.loads((SCRIPT.parent / "runtime-toolchain.json").read_text())
        (project / ".nvmrc").write_text(pins["node"]["version"] + "\n")
        (project / "go.mod").write_text(
            "module example.invalid/test\n\ngo " + pins["go"]["version"] + "\n"
        )
        environment = {
            "RUNNER_OS": "Linux", "RUNNER_ARCH": "X64", "RUNNER_TEMP": str(self.root),
            "GITHUB_ENV": str(self.root / "env"), "GITHUB_PATH": str(self.root / "path"),
        }
        for failed in ("go", "node"):
            with self.subTest(failed=failed):
                sample = b"valid mock Go archive"
                modified = json.loads(json.dumps(pins))
                if failed == "node":
                    modified["go"]["archives"]["linux-x64"]["sha256"] = (
                        hashlib.sha256(sample).hexdigest()
                    )
                (project / "scripts/runtime-toolchain.json").write_text(json.dumps(modified))
                responses = iter([sample, b"altered"] if failed == "node" else [b"altered"])
                def fake_download(*args, **kwargs):
                    return Response(next(responses))
                with mock.patch.object(runtime, "ROOT", project), \
                     mock.patch.dict(os.environ, environment, clear=True), \
                     mock.patch.object(runtime.urllib.request, "urlopen", side_effect=fake_download), \
                     mock.patch.object(runtime.subprocess, "check_output") as execute:
                    with self.assertRaisesRegex(ValueError, "SHA-256"):
                        runtime.main([])
                execute.assert_not_called()
                self.assertFalse((self.root / "steam-releaser-runtimes").exists())

    def test_node_only_does_not_prepare_or_execute_go(self):
        project = self.root / "project"
        (project / "scripts").mkdir(parents=True)
        pins = json.loads((SCRIPT.parent / "runtime-toolchain.json").read_text())
        (project / ".nvmrc").write_text(pins["node"]["version"] + "\n")
        (project / "go.mod").write_text("go " + pins["go"]["version"] + "\n")
        (project / "scripts/runtime-toolchain.json").write_text(json.dumps(pins))
        environment = {
            "RUNNER_OS": "Linux", "RUNNER_ARCH": "X64", "RUNNER_TEMP": str(self.root),
            "GITHUB_PATH": str(self.root / "path"), "GITHUB_ENV": str(self.root / "env"),
        }
        urls = []

        def download(url, destination, digest):
            urls.append(url)
            destination.write_bytes(b"verified fixture")

        def extract(archive, destination, target, version):
            destination.mkdir()
            return destination / "node"

        with mock.patch.object(runtime, "ROOT", project), \
             mock.patch.dict(os.environ, environment, clear=True), \
             mock.patch.object(runtime, "download_verified", side_effect=download), \
             mock.patch.object(runtime, "extract_node", side_effect=extract), \
             mock.patch.object(runtime, "extract_go") as extract_go, \
             mock.patch.object(runtime.subprocess, "check_output", return_value="v" + pins["node"]["version"] + "\n") as execute:
            runtime.main(["--node-only"])
        self.assertEqual(len(urls), 1)
        self.assertTrue(urls[0].startswith("https://nodejs.org/"))
        extract_go.assert_not_called()
        self.assertEqual(execute.call_count, 1)
        self.assertFalse((self.root / "env").exists())
        self.assertIn("steam-releaser-runtimes", (self.root / "path").read_text())

    def test_go_tar_extracts_regular_files_and_rejects_traversal_links_and_collisions(self):
        archive = self.root / "go.tar.gz"
        tar_archive(archive, [
            ("go/bin/go", b"executable", "file"),
            ("go/src/example.go", b"source", "file"),
        ])
        runtime.extract_go(archive, self.root / "ok", windows=False)
        self.assertEqual((self.root / "ok/bin/go").read_bytes(), b"executable")
        for index, extra in enumerate([
            ("go/../../outside", b"bad", "file"),
            ("go/link", b"", "link"),
            ("go/BIN/go", b"duplicate", "file"),
        ]):
            tar_archive(archive, [("go/bin/go", b"executable", "file"), extra])
            with self.assertRaises(ValueError):
                runtime.extract_go(archive, self.root / f"bad-{index}", windows=False)
        self.assertFalse((self.root / "outside").exists())

    def test_go_zip_rejects_symlinks_and_traversal(self):
        archive = self.root / "go.zip"
        with zipfile.ZipFile(archive, "w") as output:
            output.writestr("go/bin/go.exe", b"executable")
            output.writestr("go/src/source.go", b"source")
        runtime.extract_go(archive, self.root / "ok", windows=True)
        self.assertEqual((self.root / "ok/bin/go.exe").read_bytes(), b"executable")
        for index, name in enumerate(["go/../outside", "go/bin/GO.EXE"]):
            with zipfile.ZipFile(archive, "w") as output:
                output.writestr("go/bin/go.exe", b"executable")
                output.writestr(name, b"bad")
            with self.assertRaises(ValueError):
                runtime.extract_go(archive, self.root / f"bad-{index}", windows=True)
        with zipfile.ZipFile(archive, "w") as output:
            output.writestr("go/bin/go.exe", b"executable")
            link = zipfile.ZipInfo("go/link")
            link.create_system = 3
            link.external_attr = (stat.S_IFLNK | 0o777) << 16
            output.writestr(link, "../../outside")
        with self.assertRaises(ValueError):
            runtime.extract_go(archive, self.root / "bad-link", windows=True)

    def test_node_extracts_only_the_expected_regular_executable(self):
        archive = self.root / "node.tar.gz"
        name = "node-v26.13.0-linux-x64/bin/node"
        tar_archive(archive, [(name, b"node binary", "file"), ("../outside", b"ignored", "file")])
        node = runtime.extract_node(archive, self.root / "node", "linux-x64", "26.13.0")
        self.assertEqual(node.read_bytes(), b"node binary")
        self.assertFalse((self.root / "outside").exists())
        tar_archive(archive, [(name, b"", "link")])
        with self.assertRaises(ValueError):
            runtime.extract_node(archive, self.root / "bad-node", "linux-x64", "26.13.0")

    def test_environment_file_rejects_line_injection(self):
        output = self.root / "env"
        runtime.append_actions_file(str(output), "GOPATH=ordinary path")
        self.assertEqual(output.read_text(), "GOPATH=ordinary path\n")
        with self.assertRaises(ValueError):
            runtime.append_actions_file(str(output), "GOPATH=good\nBAD=bad")

    def test_runtime_and_module_cache_paths_are_stable_and_separate(self):
        root = self.root / "steam-releaser-runtimes"
        first = runtime.runtime_environment(root, str(self.root))
        second = runtime.runtime_environment(root, str(self.root))
        self.assertEqual(first, second)
        self.assertNotEqual(first["GOROOT"], first["GOMODCACHE"])
        self.assertNotEqual(first["GOPATH"], first["GOMODCACHE"])
        self.assertEqual(first["GOTOOLCHAIN"], "local")


if __name__ == "__main__":
    unittest.main()
