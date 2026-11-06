# Configuration

Save `steam-release.yaml` in your game project or beside your prebuilt artifacts.
For a prebuilt game, this is enough:

```yaml
app_id: 1000000
targets:
  windows:
    depot_id: 1000001
    content: builds/windows
```

Use your own Steam App and depot IDs. `content` is the directory uploaded to the
depot; it must exist and contain files. Run `steam-releaser plan` to inspect the
resolved configuration before building or uploading.

## Building your game

For builds, provide an output path and the engine's build settings. The engine
is inferred from those settings, and `content` defaults to the output's parent
directory. For example:

```yaml
app_id: 1000000
targets:
  windows:
    depot_id: 1000001
    output: builds/windows/Game.exe
    build:
      preset: Windows Desktop
```

The `build` fields select the engine:

| Engine | Build settings |
| --- | --- |
| Godot | `preset: Windows Desktop`, an existing export preset |
| Unity | `profile: Assets/Settings/Windows.asset`, a Unity 6 Build Profile |
| Unity with a custom method | `target: StandaloneWindows64` and `execute_method: Company.Build.Release` |
| Custom | `command: [make, "steam-{target}", "OUTPUT={output}"]` |
| Prebuilt | Omit `build`; provide `content` |

See the complete [Godot](../examples/godot.yaml), [Unity](../examples/unity.yaml),
[custom](../examples/custom.yaml), and [prebuilt](../examples/prebuilt.yaml) examples.

`build.args` appends an ordered array of argument tokens for any build engine.
Spaces, duplicates, and empty arguments are preserved; do not add shell quotes
around values. `build.env` supplies literal string environment overrides for
that target's build process, on top of the inherited process environment. Quote
numeric and boolean values in YAML. An empty string defines an empty variable.
Neither field affects Godot's shared import step or SteamCMD, and prebuilt
targets cannot use them. Environment names must be nonempty and cannot contain
`=` or NUL; argument tokens and environment values cannot contain NUL.

For example, a custom build script can consume these inputs:

```yaml
build:
  command: [./scripts/build-game, "{output}"]
  args: [--edition, demo, "--label=Steam Demo"]
  env:
    ENABLE_ANALYTICS: "0"
    OPTIONAL_SETTING: ""
```

The script defines what those options mean. No feature is enabled automatically
by a release profile name or environment variable. For Godot, select an existing
export preset with custom feature tags such as `demo`; for Unity, select a Build
Profile with the desired scripting symbols, scenes, and settings. Keep separate
output directories for demo and full builds. Managed engine flags repeated in
`args` produce warnings in Doctor, Plan, and build output. Use the corresponding
configuration fields to change project, output, profile, or method settings.
Godot arguments after `--` or `++` are project arguments and are left untouched.

The desktop app's **Set up a new game** form writes an annotated YAML with an active `profiles.release` and commented optional fields for the selected engine.
It can fill App and depot IDs, content folders, mappings, and exclusions from SteamPipe `app_build` scripts it finds in the game folder, and Godot export presets or Unity build profiles from the project.
A live preview validates the file before it is written.
Use **Edit YAML** to define additional profiles/targets and edit `build.args`
and `build.env`, then save and choose **Reload and validate**. The app displays
parse/validation errors and blocks release actions until the configuration is
valid. Plan shows the resolved executable, argument tokens, directory,
environment overrides, and any argument warnings; raw JSON remains available.
Configured environment values appear in YAML and plans; keep credentials in
your build machine or CI environment, outside this file.

Creating a starter configuration requires either hard-link or exclusive-rename
support in its destination directory. If neither is available, create the YAML
file manually. An interrupted initialization may leave a `.steam-releaser-*.tmp`
file in that directory; review it before removing it. Configuration edits and
forced initialization take a file lock, and edits check for other changes before
writing. Other programs may bypass the lock or change the file after that check.
Existing files are still written in place, so keep a backup when editing: a
write failure can leave the YAML incomplete.

The CLI accepts repeatable `--build-arg=TOKEN` and `--build-env=NAME=VALUE`
with `init`, `config add-profile`, and `config add-target`. When creating multiple
targets, configure their options individually to avoid ambiguous ownership.
Replace only the supplied option groups on an existing target:

```bash
steam-releaser config set-build-options windows demo \
  --build-arg=--verbose --build-env=ENABLE_ANALYTICS=0
steam-releaser config set-build-options windows demo --clear-build-args
```

Omitting an option group preserves it; `--clear-build-args` and
`--clear-build-env` remove that group. Environment values may contain `=` and
spaces, and empty values are supported. Older binaries reject `build.env`; use
a release supporting this field. Existing version-1 files retain their behavior.

Custom commands execute
without a shell; placeholders `{project}`, `{profile}`, `{target}`, `{output}`,
and `{content}` are replaced once in each command argument. Unknown placeholders
and placeholder-like text within resolved paths stay literal.

Unity builds run one Editor process at a time. With an execute method, the
arguments `-steamReleaserOutput`, `-steamReleaserProfile`, and
`-steamReleaserTarget` are also passed. An optional method is provided in
[integrations/unity](../integrations/unity). When used with a Unity 6 Build
Profile, that method builds the active profile, including its scene and build
settings. For standalone `build.target` values without an execute method,
Steam Releaser uses Unity's platform-specific Player build argument because
Unity 6 requires a Build Profile when using `-build`. `steam-releaser doctor`
checks that each configured profile asset exists in the project. Unity builds
use `-nographics` by default so they can run without a display. If your build
needs a graphics device, set `build.no_graphics: false` for that target and
provide a display on the build machine. Unity cannot bake global illumination
with `-nographics`.

Each engine still needs its export templates/modules and any platform SDKs.
Use a `.zip` output for Godot macOS exports from Windows to retain executable
permissions. Signing and notarization remain part of your engine or CI workflow.

When a build does not produce an output or leaves an existing output unchanged,
Steam Releaser requires a newly created or changed post-build source to supply
it; otherwise the build stops before upload. An unchanged post-build source also
cannot replace different existing files or links in the selected build content. Static
files can still be added to content after the build changed its output. Chained
copy rules may be rejected when their source cannot be verified; arrange for
the build command to produce the source directly in that case.

The checks walk directory outputs and post-build sources, and hash sources that
can affect the selected content trees. This adds filesystem I/O for large
builds. Each source and destination path set is limited to one million entries
and 128 MiB of path names. If a post-build source supplies an output the command
did not change, the operation continues with a warning.

This is not proof of freshness: touching a file or changing another file in an
output directory can satisfy the check while an old artifact remains. A build
that intentionally preserves all output timestamps and metadata can also be
rejected. For intentionally reused artifacts, review the content and use the
separate `upload` command or configure a prebuilt project.

## Defaults and overrides

| Setting | When omitted |
| --- | --- |
| `version` | `1`; other explicit versions are rejected |
| `project.root` | Directory containing the YAML file |
| `project.name` | Project directory name |
| `project.engine.type` | Inferred from `build` settings; `prebuilt` when none are present |
| `platform` | Inferred from target names `windows`, `linux`, or `macos`, including `-`/`_` suffixes; otherwise unspecified |
| `architecture` | Unspecified; your engine's build settings determine it |
| `content` | Parent directory of `output` |
| `output` for prebuilt targets | Same directory as `content` |
| File mappings | All files in `content`, recursively; `.gitkeep` excluded |

Set `content` explicitly when the output is a directory whose contents should
be uploaded, or when the upload directory differs from the output's parent.
An inferred content directory cannot contain the project itself; set `content`
explicitly if that is intentional. Paths are never guessed from Steam IDs.

Optional project and tool overrides:

```yaml
project:
  name: My Game
  root: ../game
  engine:
    type: godot
    executable: /path/to/godot
    import_before_build: false
    max_parallel: 2
tools:
  steamcmd: /path/to/steamcmd
```

`type` accepts `godot`, `unity`, `custom`, `prebuilt`, or `auto`. Keep one engine
per configuration. `execute_method` under `project.engine` supplies a default
Unity method. Godot imports before exporting and builds targets in parallel by
default; custom commands default to serial execution. `max_parallel` limits
Godot/custom builds; Unity always stays serial. Targets with overlapping build
outputs are rejected. When target content or output paths overlap without
overlapping outputs, builds run serially and report a warning. Shared files can
still be overwritten, so review the resulting artifacts before upload. On macOS,
the path check conservatively ignores case even on case-sensitive volumes.

Only one Steam Releaser build, upload, or ship operation can use the same
resolved project root at a time on the same machine, including across CLI and
desktop processes. A competing operation fails so it can be retried after the
first finishes. On Windows, different drive mappings to the same directory may
not share a lock. This does not prevent other programs or separate projects
from changing shared paths.

Tool discovery checks `GODOT_BIN`, `UNITY_BIN`, or `STEAMCMD_BIN`, then an explicit
executable, `PATH`, and known installation locations. SteamCMD and build engines
are separate installations; their paths usually do not belong in the YAML.
Passwords and Steam Guard codes never belong in this file.

For Unity, point `UNITY_BIN` or `project.engine.executable` to the Editor
executable when auto-discovery cannot find it. Steam Releaser recognizes Editor
installations in Unity Hub folders and Editor executables linked on `PATH`,
regardless of where the installation root is. When several Editors are found,
the version in `ProjectSettings/ProjectVersion.txt` takes priority. The separate
`unity` CLI is not an Editor executable.

Paths resolve as follows:

- `project.root`, `tools.steamcmd`, and `existing_vdf`: relative to the YAML file.
- `output`, `content`, `build_output`, and engine executable paths: relative to
  `project.root` (a bare executable name is searched on `PATH`).
- `required`: glob patterns relative to the target's `content`.
- Absolute paths remain absolute.

If a shared YAML file lives outside the game checkout, leave `project.root`
unset and choose a local **Game project folder** in the desktop app, or pass
`--project-root PATH` to the CLI. The override applies only to that run; the
desktop app remembers it locally for each configuration file. It does not edit
the YAML.

For Unity, choose the directory containing `Assets/` and
`ProjectSettings/ProjectVersion.txt`. If the repository contains the project in
a subfolder such as `Fletch/`, choose that subfolder. Build and ship check this
before creating build directories or starting the Editor; Doctor reports the
same issue and suggests a child project when there is exactly one match.

## Optional profiles

Use `profiles` when one configuration needs separate apps, demos, or playtests:

```yaml
profiles:
  release:
    app_id: 1000000
    targets:
      windows:
        depot_id: 1000001
        content: builds/release/windows
  demo:
    app_id: 2000000
    targets:
      windows:
        depot_id: 2000001
        content: builds/demo/windows
```

Run `steam-releaser ship demo` to select one. Without a profile argument, the CLI
uses `release`, or the only profile if there is just one. Otherwise it asks you
to name a profile. The desktop app selects the same default.

The short top-level form creates a `release` profile internally. Use either the
short form or `profiles` in one file. Unknown fields and duplicate keys are rejected.

## Optional upload settings

These fields belong alongside `app_id` and `targets`, or inside a named profile:

- `description`: defaults to `Build uploaded by Steam Releaser`.
- `build_output`: SteamPipe logs/output directory. By default, each upload uses
  its own directory under `.steam-releaser/output`; an explicit path is used as
  configured and can be shared by multiple uploads.
- `existing_vdf`: use an existing app build script. It cannot be combined with
  target filtering, `--preview`, or `--set-live`.
- `post_build`: copy files into target content directories after building.

Generated Steam build scripts reject double quotes, control characters, invalid
UTF-8, and values of 4096 bytes or more in descriptions, resolved paths,
mappings, and exclusions. Mapping and exclusion paths must be relative and
cannot contain parent traversal, drive prefixes, or NTFS stream names. Remove
or rename the affected value before planning an upload. Windows backslashes in
paths and mappings remain supported. These checks do not inspect or rewrite a
user-supplied `existing_vdf` script.

Generated scripts and the default `.steam-releaser/output` directory must stay
within the project, including through symlinks. Profile names used for generated
scripts cannot end in a dot or space. An explicitly configured `build_output`
may point outside the project. Generated scripts and the Steam output directory
must not overlap any selected target's content directory, even if mappings or
exclusions would filter those files. This prevents the generated files and
Steam output from entering the selected content through those paths. Other
programs can still change paths while an upload is running.

Each generated upload uses a separate script directory under
`.steam-releaser/generated/<profile>/`. A plan displays `<run-id>` in the script
path because the directory is chosen when the upload starts; the upload result
reports the actual script and Steam output paths. Generated scripts and default
output are retained for inspection and can be removed when no upload is using
them. SteamPipe stores
chunk caches in its output directory, so these per-upload directories can use
substantial disk space and do not reuse the previous run's cache; uploads can
take longer. To reuse that cache, configure a stable `build_output` outside the
content and coordinate any machines sharing it. See the [SteamPipe upload
documentation](https://partner.steamgames.com/doc/sdk/uploading). An
`existing_vdf` is used unchanged and may refer to shared files or directories.

Targets may add `required`, `mappings`, and `exclusions`:

```yaml
required: [Game.exe, "Game_Data/**"]
mappings:
  - local_path: "*"
    depot_path: .
    recursive: true
exclusions: ["*.pdb"]
```

Targets may share a depot ID when they have the same `content` directory and
each defines its own file mappings. Steam Releaser combines those mappings into
one depot script. For example, a Godot project can export three platforms into
`builds/steam` while mapping `windows/*`, `macos/*`, and `linux/*` to their
respective depot subdirectories. Build targets can be selected individually,
but an upload must include every target sharing that depot ID. An exclusion on
any shared target applies to the entire depot.

For post-build copies, sources are globs relative to the project root and
`to` is relative to each selected target's content directory:

```yaml
post_build:
  - from: notices/LICENSES.txt
    to: legal
    targets: [windows, linux]
```

Omit `to` to copy to the content root, and omit `targets` to apply the rule to
all selected targets. Sources and destinations must stay within their respective
project/content directories, including paths reached through symlinks. Directory
copies preserve relative symlinks within the copied tree, such as macOS framework
links. Escaping or broken links, special files, copying a file onto itself, and
copying a directory inside itself are rejected. Files and links are staged
before replacing their destinations, so a failed copy preserves the existing
entry. A copied link cannot replace an existing directory.

## GitHub Actions

The CLI uses the same services as the desktop app for builds, post-build copies,
artifact checks, depot selection, previews, and beta-branch uploads. In Actions,
`CI=true` or `GITHUB_ACTIONS=true` automatically disables SteamCMD prompts. You
can also pass `--non-interactive` explicitly. Missing or expired credentials fail
the command rather than requesting a password from an unattended job.

Steam Releaser's username cache is separate from SteamCMD's login token. Before
running CI, sign in as the runner's OS user with the SteamCMD installation that
will perform uploads:

```bash
steam-releaser --config /path/to/game/steam-release.yaml \
  --non-interactive=false auth login BUILD_ACCOUNT
```

Complete the password and Steam Guard prompts once. Preserve SteamCMD's
`config/config.vdf` between jobs, including updates SteamCMD makes after login.
Future runs supply the username without a password. This follows
[Valve's SteamPipe CI/CD setup](https://partner.steamgames.com/doc/sdk/uploading#Using_SteamPipe_In_A_CI_CD_Environment).
The GUI's in-memory Remember Me password is not available to another process.
If the token expires, repeat the interactive login before retrying CI.

The [example workflow](../examples/github-actions.yml) uses a Linux x64
self-hosted runner labeled `steam`, with SteamCMD and its configuration outside
the game checkout. Install Go, Git/Git LFS, and the selected engine's build tools,
export templates/platform modules, SDKs, and any required engine license on that
runner. Configure these values in the game repository:

| Name | Location | Value |
| --- | --- | --- |
| `STEAM_USERNAME` | Actions secret | The authenticated Steam build account |
| `STEAMCMD_BIN` | Actions variable | Absolute path to that runner's SteamCMD executable |
| `STEAM_RELEASER_VERSION` | Actions variable | The full commit SHA of the reviewed release containing the CLI options used by the example |

Resolve the intended release tag to its commit SHA and review that revision
before updating the variable. A full SHA prevents a moved tag from silently
changing the source built by a later job.

Copy the example into the game's `.github/workflows` directory and keep the
game's `steam-release.yaml` at the checkout root. The workflow checks the selected
profile, verifies the saved login, builds and uploads, and saves the JSON result.
It defaults to a preview; disable preview to upload content. Set a beta branch
only when preview is disabled. If no branch is supplied, the build is uploaded
without being set live. The default branch must be managed in Steamworks.

For GitHub-hosted runners, provision SteamCMD and securely restore its saved
configuration before running these commands; preserve the updated token after
the job. Do not store credentials in the game YAML or publish them as artifacts.
Serialize jobs using the same Steam account with workflow `concurrency`, as in
the example.

To upload artifacts built in an earlier job, use `upload` instead of `ship` after
downloading them into the configured content directories. Use `--target NAME`
repeatedly to select targets. Use `--project-root "$GITHUB_WORKSPACE"` when the
YAML lives outside the checkout. Preview and live-branch flags can be checked
with `plan` before running the build:

```bash
steam-releaser --non-interactive --json plan release --set-live beta
steam-releaser --non-interactive --json --timeout 60m \
  ship release --set-live beta > steam-upload.json
```

`--timeout` applies to the whole operation, including all build stages and the
upload for `ship`. It terminates running processes through the shared process
runner. Success returns `0`, other failures return `1`, timeouts return `124`,
and cancellation returns `130`. JSON results go to stdout, progress events to
stderr, and errors return a nonzero exit code. `build`, `auth login`, and
`auth logout` produce an `{ "action": "...", "ok": true }` completion result
when `--json` is enabled.
