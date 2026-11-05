# Command-line guide

All commands below run from the repository root.

## Quick start

End users need SteamCMD for uploads and an engine only when building games.
Tagged releases package the desktop app and CLI for each supported platform.
Windows needs WebView2, Linux needs GTK 3/WebKitGTK 4.1, and macOS uses its built-in
WebKit.

To build the CLI from source, use Go 1.27.3+ from the repository root:

```bash
go build -o bin/steam-releaser ./cmd/steam-releaser
```

Save `steam-release.yaml` beside your game or artifacts. For prebuilt files:

```yaml
app_id: 1000000
targets:
  windows:
    depot_id: 1000001
    content: builds/windows
```

Replace the IDs and content path with yours. See [examples](../examples) for Godot,
Unity, and custom builds, or the [configuration reference](configuration.md)
for optional settings. `project.root` can point to a different project directory.

Inspect your release, authenticate, then build and upload:

```bash
./bin/steam-releaser doctor
./bin/steam-releaser plan
./bin/steam-releaser auth login BUILD_ACCOUNT
./bin/steam-releaser ship
```

Use `build` or `upload` to run either stage separately. Named profiles are
optional: `ship demo` selects a `demo` profile. Uploads validate artifacts first.

To generate a configuration:

```bash
./bin/steam-releaser init --app-id 1000000 --engine prebuilt \
  --target 'windows,1000001,builds/windows'
```

For engines, add a fourth target field: a Godot preset, Unity Build Profile
path/target, or custom executable. The generated YAML uses named profiles and
includes commented optional settings for the selected engine, plus an example
second profile. Edit the file directly and use `plan` to inspect the result.

You can also add profiles and targets from the CLI:

```bash
./bin/steam-releaser config add-profile demo --app-id 2000000 \
  --target 'windows,2000001,builds/demo/windows'
./bin/steam-releaser config add-target demo \
  --target 'linux,2000002,builds/demo/linux' --architecture x86_64
```

These commands preserve existing YAML settings and comments. For engine builds,
include the fourth target field as with `init`.

SteamCMD handles passwords, Steam Guard, and login tokens. Steam Releaser caches
only the username on disk. Use `auth status` to view it or `auth logout` to sign out.

## Options

- `--config PATH`: select a configuration; otherwise, discover
  `steam-release.yaml` or `steam-release.yml` in the current directory or a parent.
- `--project-root PATH`: use a machine-local game checkout for a shared YAML
  file without changing `project.root` in the file.
- `--target NAME`: select a target for `doctor`, `plan`, `build`, `upload`, or
  `ship`. Repeat for multiple targets; omit to select all.
- `--preview`: preview an upload without publishing content. Available on
  `plan`, `upload`, and `ship`.
- `--set-live BRANCH`: set an uploaded build live on a named beta branch.
  Cannot be combined with `--preview` or used for the default branch.
- `--json`: write structured results to stdout and progress events to stderr.
- `--non-interactive`: disable SteamCMD prompts and use its saved login token.
  Enabled automatically when `CI` or `GITHUB_ACTIONS` is set to a nonempty value
  other than `false` or `0`. Use `--non-interactive=false` for local prompts.
- `--timeout DURATION`: limit a build, upload, ship, login, or logout operation
  (for example `60m`). Defaults to no limit. A timeout exits with code `124`;
  cancellation exits with `130`, and other failures with `1`.

Use `./bin/steam-releaser COMMAND --help` for all options. Commands execute
without a shell.

An upload is confirmed only when SteamCMD exits successfully and reports a
completed build for the selected App ID and requested mode. For the default
per-run output directory, Steam Releaser can also read that run's build log.
Shared `build_output` directories and custom VDFs use console output only, so
an omitted or unrecognized completion is reported as unconfirmed. Check
Steamworks before retrying an unconfirmed or cancelled upload. A completed
build does not independently confirm that a `--set-live` branch was promoted;
check the branch in Steamworks.

## GitHub Actions

Use the [game publishing workflow example](../examples/github-actions.yml) and
[runner/authentication setup](configuration.md#github-actions) to run the
same build, copy, artifact validation, and SteamPipe upload flow as the GUI.
`--json` keeps results on stdout, including completion results for build, login,
and logout; progress stays on stderr. A preview still authenticates with SteamCMD.
