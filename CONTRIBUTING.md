# Contributing to Steam Releaser

Steam Releaser shares its Go release services between the CLI and Wails desktop
app. Keep engine and publishing behavior in `internal/`; put desktop bridge
code in `cmd/steam-releaser-desktop` and UI state in its frontend. See the
[architecture guide](docs/architecture.md) for the release flow.

For a bug report, include your operating system, engine version, command,
expected behavior, and a minimal configuration with private paths and account
information removed. Never attach Steam passwords, Steam Guard codes, login
tokens, or an entire SteamCMD installation. Use the [security policy](SECURITY.md)
for vulnerabilities.

## Local checks

Install the Go version in `go.mod`, the Node version in `.nvmrc`, Python 3, and
the native desktop build dependencies described in the
[desktop guide](docs/desktop.md#build-from-source). From the repository root,
run the full local suite before submitting changes or tagging a release:

```sh
bash scripts/check-local.sh
```

The script runs Go, frontend, desktop, workflow, and dependency checks on the
current machine. It prepares the pinned, verified npm in a temporary directory,
installs frontend dependencies with lifecycle scripts disabled, and builds the
frontend. It needs network access for dependency and advisory checks, but no
game engine, display, or Steam account. Native tests run only for the local OS;
the CLI is also cross-compiled for the supported release platforms.

Pushes, pull requests, and schedules do not run hosted CI. Pushing a `v*` tag
starts the GitHub release builds. See the [development guide](docs/development.md)
for prerequisites and release details.

## Changes and tests

Keep pull requests focused and explain the behavior they change and how you
verified it. For security and release correctness fixes, add a regression test
that reproduces the failure. Use temporary directories and fake runners rather
than uploading to Steam or invoking a real game editor in tests. Run browser
and engine checks headlessly when available.

When adding an engine setting, keep configuration validation, plans, diagnostics,
examples, and the configuration reference consistent. When changing the desktop
bridge, update the TypeScript contracts and mock bridge together. Package new
third-party assets with their required notices; the project's AGPL-3.0-only
license does not replace dependency licenses.
