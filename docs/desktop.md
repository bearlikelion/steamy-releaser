# Desktop app

## Launch an existing build

From the repository root on Linux:

```sh
./cmd/steam-releaser-desktop/build/bin/SteamReleaser
```

This opens the last built version. Rebuild after changing the source, or use
development mode below.

For a release archive, extract it and open the desktop app:

| Platform | Desktop app |
| --- | --- |
| Windows | `SteamReleaser.exe` |
| macOS | `steam-releaser.app` |
| Linux | `SteamReleaser` |

The archive also contains `steam-releaser` (or `steam-releaser.exe`), the CLI.
Windows needs WebView2, Linux needs GTK 3/WebKitGTK 4.1, and macOS uses its built-in
WebKit. SteamCMD is needed for uploads; an engine is needed only when building games.
The macOS archive targets Apple Silicon and requires macOS 13 Ventura or newer.

The current release workflow does not apply an Authenticode publisher signature
to the Windows executables or a Developer ID signature and notarization to the
macOS app. Operating systems may warn on launch.

The GitHub release page also provides a snapshot of the tagged project source.
The snapshot does not include the Go modules or npm packages downloaded during
the build; their versions are recorded in the project lockfiles and package
inventories. Release archives also include `FRONTEND_SOURCE_MAPS`, containing
source text recorded for the JavaScript build. Some dependencies publish
generated JavaScript, and the maps do not cover Go code, CSS, or all build
inputs. Complete corresponding-source availability remains under review.

Public releases include a SHA-256 checksum file and a GitHub build attestation.
For example, on Linux, download both files and verify them before extracting:

```sh
sha256sum --check steam-releaser-linux-amd64.tar.gz.sha256
gh attestation verify steam-releaser-linux-amd64.tar.gz --repo OWNER/REPOSITORY
```

Replace `OWNER/REPOSITORY` with the trusted release repository. On macOS,
`shasum -a 256 -c` can check the checksum file. Private-repository builds do not
receive GitHub attestations from this workflow.

## Build from source

Use Go 1.27.3+, a Node.js release allowed by the [frontend engine requirements](../cmd/steam-releaser-desktop/frontend/package.json),
and the native Wails build dependencies for your platform. Release builds use the version
in [`.nvmrc`](../.nvmrc). The
[release workflow](../.github/workflows/release.yml) shows the platform setup.

With [mise](https://mise.jdx.dev) installed, run `mise trust` once and `./gui` uses the Node and npm pinned in [`mise.toml`](../mise.toml), whatever your system has.
Pass `-debug` (`./gui build -debug`) for a binary with the web inspector enabled.

On Debian/Ubuntu with WebKitGTK 4.1, install the native development packages:

```sh
sudo apt-get install libgtk-3-dev libwebkit2gtk-4.1-dev
```

The Wails CLI is a Go tool dependency pinned by this repository's `go.mod` and
`go.sum`; no separate global Wails installation is required.

From the repository root on Linux or macOS, build without launching:

```sh
./gui build
```

The launcher selects system `pkg-config` on Linux when it can find GTK and
WebKit, avoiding Homebrew's separate search path. It honors an explicit
`PKG_CONFIG` environment variable and adds the `webkit2_41` build tag.
On macOS it defaults the compiler and linker target to macOS 13.
No shell alias or global PATH change is needed.

Wails installs the frontend dependencies and builds the bundled assets. Output
is in `cmd/steam-releaser-desktop/build/\u0062in/`; open the app listed above.
On Windows, run `go tool wails build -m -nosyncgomod -webview2 browser` from
`cmd/steam-releaser-desktop`.
If WebView2 is missing, the packaged application offers to open
[Microsoft's download page](https://developer.microsoft.com/en-us/microsoft-edge/webview2/);
the user installs the runtime separately. The application does not download and
execute the bootstrapper itself.

## Development mode

From the repository root on Linux or macOS:

```sh
./gui
```

This builds and launches the GUI with live reload. The script locates the
project directory itself, so you can also run `../../gui` when already in
`cmd/steam-releaser-desktop`. Pass extra Wails options with `./gui dev OPTIONS`.
On Windows, run `go tool wails dev -m -nosyncgomod -tags wv2runtime.browser` from
`cmd/steam-releaser-desktop`.

`npm run dev` in the frontend directory provides a browser preview with desktop
actions disabled. Use Wails to run the full app.

## Using the app

Closing the window hides it to the system tray and keeps releases running.
Use **Show Steam Releaser** in the tray menu, or launch the app again, to restore
the window. The native **File** menu includes **Hide to Tray** and **Force Exit**.
**Force Exit** in the window menu or tray menu cancels active work and exits
the app. Application shutdown waits briefly for build processes to stop and
release history to save. On Windows, macOS, and Linux, managed builders and
their child processes also stop if the application exits unexpectedly.
Linux supports StatusNotifier/AppIndicator trays and native X11 trays
such as i3bar; GNOME may need a tray extension.

The desktop app creates an annotated YAML configuration from a path, project
name, engine, and Steam App ID. It includes an active `profiles.release`, Windows,
Linux, and macOS targets with placeholder depot IDs, paths, and build settings,
and commented optional settings for the chosen engine. Replace the placeholders
before publishing. Uncomment the demo profile example or copy a profile/target
to add another one. Profile names do not change game features.

Use **Edit YAML** to open the configuration in your system's associated editor
(on macOS, the default text editor). Add profiles, targets, build arguments,
environment overrides, and advanced settings in that file. Save, then choose
**Reload and validate** to refresh the app. Invalid YAML shows an error and blocks
release actions while leaving the editor and reload controls available.
The app also checks for external configuration changes before running an action;
when it finds a change, it refreshes the displayed configuration and asks you to
review it before running again. Plan shows the resolved paths, arguments,
environment overrides, and publishing destination.

The GUI provides profile selection, target checkboxes, publishing controls,
plans, diagnostics, logs, history, and cancellation.

To create a configuration, choose **Set up a new game** and enter or browse to your game folder.
The app detects the engine, project name, and Godot export presets or Unity build profiles.
It also lists SteamPipe `app_build` VDF scripts in the folder, named through Steam's public store API when the app has a store page.
Choosing one fills the App ID, depots, content folders, mappings, and exclusions; depots whose content folders match Godot export outputs become Godot build targets.
**Import SteamPipe VDF** reads a script from elsewhere.
Review the targets and YAML preview, then choose **Create configuration**.

Upload success requires SteamCMD to report completion for the selected App ID
and the requested upload or preview mode. If the result is unconfirmed or the
upload is cancelled, check Steamworks before retrying. Confirm beta-branch
promotion in Steamworks as well; a completed build alone does not prove it.

Uploads try SteamCMD's saved login
for the selected account first. If SteamCMD asks for a password or Steam Guard
response, enter it in the app. Select **Remember Me** in the sign-in modal to keep
a successfully used password in memory and answer later password prompts while
the app remains open. The password is cleared when you turn the option off,
change accounts, clear Steam credentials, or authentication fails. This option
does not save the password to disk, and Steam Guard responses are never remembered. You can use
**Sign in to Steam** to check the saved login before uploading. Mobile approvals
are completed in Steam. The CLI also supports `auth login`.

**Sign in to Steam** works before any project is loaded.
If SteamCMD is installed, the sign-in modal asks before reading SteamCMD's saved account names from its `config/config.vdf`.
After you allow it, the modal offers **Continue as** for each saved account; only account names are read, and tokens stay in SteamCMD.
Change this choice in **Client settings**.

To use credentials from an existing file, open **Client settings** and enable
**Use an existing Steam credential file**. Select a JSON file containing a valid
Steam username and password; use the folder button to choose a different file
later. The file must already exist and contain both fields:

```json
{
  "username": "your_build_account",
  "password": "your_password"
}
```

The app remembers the selected path across restarts, restores the account, and
answers password prompts for that account. Steam Guard still requires a fresh
response. A rejected file password falls back to manual entry.

The credential file is unencrypted. Keep it in a private location outside your
project or version control, with access limited to your account. SQLite stores
only the file path and enabled setting. Steam Releaser only reads the file;
signing in, turning the option off, and **Clear cached Steam credentials** leave
its contents and permissions unchanged.

Select **Doctor** from the release actions to check the selected profile and
targets. It shows readable issues and suggested fixes, with raw diagnostics in
an expandable section. Checks cover local tools,
engine settings, prebuilt artifacts, shared depots, and publishing options;
Steam verifies account permissions when uploading.

Failed releases can retry an upload when an artifact checkpoint was recorded,
or rebuild unfinished targets while reusing completed outputs whose contents
still match their checkpoints and the Git source checkpoint is verifiable.
Changed built targets are rebuilt; changed prebuilt targets require a new
release. Without a Git source checkpoint, completed build targets are rebuilt
instead of reused; a failed source check may require a new operation. An upload
retry stops if its recorded contents changed.
Older attempts or attempts whose checkpoint could not be recorded may require
a new build or upload. Build-and-deploy and upload attempts using `existing_vdf`
do not support automatic retry: custom scripts can reference content outside
the configured targets. Review the script before starting a new operation.
Creating checkpoints reads the artifact contents and can take time for large
games.

Retries keep the original profile, target selection, account, and publishing
options, rerun copy rules when resuming a build, and validate artifacts before
uploading. Copy rules cannot change the verified contents of a reused target;
start a new release if they need to do so. Reused contents are checked again
after the other builds and copies finish. Configuration, Git commit, and source
checks are repeated after the project lock is acquired. The Git source check
reads working-tree bytes for tracked and non-ignored untracked files throughout the repository,
including initialized nested submodules, even when Git is configured to hide
tracked changes. An uninspectable submodule or an unregistered nested Git
repository prevents source verification. The check runs under the project lock
and can take time for large projects. Ignore generated build outputs in Git so
they do not invalidate a retry. Steam Releaser's own generated state and local
database are excluded.
If a new operation cannot finish the check within 30 seconds, it records no
source checkpoint and shows a warning; a retry that cannot verify its checkpoint
stops. Checkpoints made with the earlier
source-checking method require a new operation. Start a new release
after changing those inputs. Checkpoints prove the recorded artifact contents
are unchanged; they do not prove the first build was fresh or detect changes to
ignored or external source files, including symlink targets outside the listed
files. For projects outside Git, source changes cannot be checked automatically.

**Local Release History** shows up to 50 recent uncleared desktop build/upload
attempts across projects. Use **Clear** to hide the current project's displayed
entries from the GUI, including after reopening the app. Clear is available
during builds and uploads; new attempts remain visible if they finish while
clearing. Cleared records and their logs
remain in SQLite and are retained as new releases arrive. New releases appear
in the history normally.

Records are stored in the SQLite database `steam-releaser.db` in the local Steam Releaser
configuration directory
(`STEAM_RELEASER_CONFIG_DIR` overrides it). Each entry includes its destination,
targets, duration, Git version/commit when available, Steam build ID when reported,
and bounded logs. Submitted Steam passwords and Guard responses are redacted
before saving. The same database stores desktop preferences and the cached Steam
username. Existing JSON history, username files, and browser preferences are
imported automatically. Legacy history and username files remain as backups;
clearing the cached account also removes its legacy username file. Project
configuration stays in YAML.

The app remembers the last profile and each profile's target selection, preview
setting, and beta branch for each configuration and project folder. Saved
publishing settings reset if that profile's App ID changes. **Background notifications**
are enabled by default to receive system alerts for completed builds, release
outcomes, and Steam sign-in requests. Change this preference in **Client settings**
using the gear button in the app header. The preference is remembered;
notifications depend on platform availability and system permissions.
