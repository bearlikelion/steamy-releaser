# Development guide

Install the exact Go version in `go.mod`, the Node version in `.nvmrc`, Python 3,
and the native desktop build dependencies in the
[desktop guide](desktop.md#build-from-source). On Linux, the tray test also needs
`dbus-run-session`, GTK 3, and WebKitGTK 4.1 development libraries. Native
Windows runs require Git Bash, `cygpath`, and Python 3 on `PATH`. From the
repository root, run the full local suite:

```bash
bash scripts/check-local.sh
```

The script creates a temporary npm installation and build cache; it does not
replace your installed Go, Node, or npm. It requires network access to download
dependencies and consult the package registries and advisory services. It runs
native tests for the current OS and cross-compiles the CLI for Linux, Windows,
and macOS; cross-compilation does not execute the other platforms' tests.
`npm ci` replaces the frontend's `node_modules`, and the frontend build replaces
its `dist` directory. Run the suite on other local operating systems when their
native behavior needs verification.

The CLI and desktop app share the Go services in `internal/`. An optional Unity
Editor build method is available in [integrations/unity](../integrations/unity).

The desktop frontend uses React, strict TypeScript, Vite, Tailwind CSS v4, and
shadcn/ui (Radix primitives and Sonner notifications). See the [desktop guide](desktop.md#development-mode) to launch the GUI with
live reload, including the Linux build tag. Use `npm run dev` from the frontend
directory for a browser preview with desktop actions disabled.
Assets, including fonts, are bundled locally for offline use.

Frontend views live in `src/components`, Wails event and operation state in
`src/hooks/use-release-console.ts`, and the typed Go bridge contracts in
`src/lib/types.ts`. Review the source and license of a shadcn/ui component before
adding it; `components.json` records the registry configuration.
Use `npm run format` to format changes. `npm run check` runs TypeScript, Oxlint
(including React hooks and accessibility checks), and Prettier. `npm test` runs
Vitest and Testing Library workflow tests with a mocked desktop bridge.

Tests use temporary projects and fake build/SteamCMD runners; no engine installation
or Steam account is needed. Run the local suite before opening a pull request or
pushing a release tag. Pushes, pull requests, schedules, and manual dispatches do
not start hosted CI. The native build and packaging matrix runs on GitHub only
for release tags (`v*`); the tag does not rerun the test suite. Packaged CLIs
are smoke tested before upload. Releases are created once; publish a new tag
instead of overwriting existing release assets.
Scheduled Dependabot version updates are disabled in `.github/dependabot.yml`;
GitHub's independently configured security updates and dependency alerts are
unaffected.

The local suite verifies Go modules, scans Go application and build-tool code with
the pinned `govulncheck` tool, audits npm advisories, compares registry-sourced
packages with signed registry metadata, and verifies installed npm signatures
and available provenance. It compares declared bundled package names and
versions with their digest-verified parent archives, then compares each bundled
child's regular files and modes with its separately signature-verified npm
archive. Intentional modifications to a bundled child require review. Each
local run verifies the lockfile before installation and checks installed package
identities before the audits or frontend code. The installed check also
reauthenticates the archives and compares the installed files of every registry
package with its digest-verified installation-cache archive. It checks npm's
generated command links or complete Windows shims separately and binds build
commands to their expected packages. It does not check filesystem modes or
prevent later changes.
For packages listed in `scripts/npm-source-publishers.json`, the local suite also requires
SLSA provenance whose package digest matches the signed registry archive and
whose GitHub OIDC certificate binds the recorded repository, repository ID,
owner ID, commit, and ref. The local scan checks every pinned lockfile entry,
including optional platform packages without installing them; each local
platform separately checks its installed packages. A pinned package whose
source identity changes or whose provenance disappears fails the check. The
baseline was recorded from signed metadata and attestations, and checked
against GitHub's repository and owner IDs; independently review the expected
repositories and any changes. The scan flags unpinned packages for which the
registry starts reporting provenance. Unpinned packages are not covered by this
source-identity check, and a matching certificate does not establish that the
publisher's source or build is safe.
Packages without reported provenance must also match an exact identity and
digest in `scripts/npm-unattested.json`. Independently review additions and
updates, and remove entries when they are no longer needed. This baseline was
recorded from authenticated registry metadata; it does not prove the source or
publisher is trustworthy.
Release packaging downloads the Go and Node versions in
`scripts/runtime-toolchain.json` and verifies their pinned SHA-256 hashes before
running them. The bootstrap extracts only the Node executable. The local suite
uses the Go and Node installations on `PATH` and checks their versions; it does
not authenticate those installations. Local checks use a fresh temporary Go
cache and finish before running frontend dependency code. The desktop scan can
use an existing frontend build or a temporary placeholder asset because JavaScript
assets do not affect the Go scan. When updating Go or Node,
independently check Go's release metadata and Node's signed checksum list, then
update the three platform hashes alongside `go.mod` or `.nvmrc`. Local checks and
release packaging bootstrap the npm version in `scripts/npm-toolchain.json` after
verifying its registry signature, pinned SHA-512, and archive entries. The
bootstrap also installs four pinned, signature-verified replacement packages
to remove npm 12.2.2's vulnerable `glob` and `minimatch` versions. It checks
the original and supporting package versions and the replacements' dependency
metadata before activation. These replacements cross npm's declared major
version ranges; the local suite exercises npm's install, workspace, audit, signature, SBOM,
and frontend build paths, but npm does not officially support this combination.
Remove or reassess them when npm publishes an upstream fix. The local suite also checks for
new releases of each replacement. The bootstrap uses
a pinned signing key authenticated through Sigstore TUF, then rechecks all
signatures against current TUF keys with the verified npm's client. Registry
verification requires the reviewed TUF delegation topology and direct key
target; changes require review because the replacement `minimatch` also
evaluates delegated paths.
The npm registry verifier accepts an expired signing key only for an exact
key, expiry, package identity, and SHA-512 digest listed in
`scripts/npm-historical-signatures.json`. Review any addition independently;
the registry's publication timestamp is not covered by its package signature.
Remove entries that are no longer needed. The recorded entries are a baseline,
not proof that the old signatures predate expiry or that their code is safe.
Review and update `scripts/npm-bootstrap-keys.json` from an authenticated TUF
target when npm rotates signing keys. Dependency installation disables lifecycle
scripts and rejects unsupported Node and npm versions. The local check bootstrap
does not modify a locally installed npm; the unmodified 12.2.2 bundle still matches the
two advisories until npm publishes a fix.
Release packaging rechecks the JavaScript toolchain and builds the frontend in
a separate Linux job after its dependency and signature audits. The native
Linux, Windows, and macOS packaging jobs validate the resulting data artifact
and do not restore Go caches or run frontend dependency code. They block npm
while Wails skips frontend installation and compilation. The blocker must
resolve and return its expected failure before Wails starts. Each package job
checks that the built asset paths and bytes appear in the desktop executable.
The frontend build also emits hidden source maps, which are removed from the
embedded assets and included separately in each release archive. Each native
packaging job checks their JavaScript and map hashes and compares the recorded
source text with its fresh checkout and installed dependencies. This checks the
recorded inputs, not the correctness or completeness of the mappings; some
npm packages supply generated JavaScript rather than their preferred source.
The publication and attestation jobs download only the three named platform
archives and verify their checksums before use.
The local suite also checks npm's bundled dependencies and Node's built-in undici for
version-based advisory matches before running frontend code. Matches at any
severity fail the check; the current patched toolchain has no
version-level matches. This check does not establish code reachability or cover
every native component in Node.
Update tools through the module and npm lockfiles, and keep the pinned Node
version in `.nvmrc` current. Local checks cover Node's published LTS support dates, the
latest patches for every permitted frontend Node major, supported Go patches,
and the latest npm release whenever the suite is run.
The frontend's manifest and lockfile must use matching bounded Node major
ranges. No routine hosted test matrix is run; release tags package Linux,
Windows, and macOS. The Wails CLI's
`github.com/leaanthony/clir` dependency is at
1.4.0: versions 1.5.0 through 1.7.0 break Wails 2.17.1's default build options.
Systray remains at 1.12.1 because its 1.12.2 Linux menu refresh has a data race
in the isolated tray integration test. The local suite checks both the Wails build command
and the tray race test so future updates can be evaluated safely.

Release archives have SHA-256 checksum files. Public-repository releases also
receive GitHub build attestations, which consumers can verify with
`gh attestation verify ARCHIVE --repo OWNER/REPOSITORY`. Repository administrators
should protect release tags and enable private vulnerability reporting and
dependency alerts; workflow files cannot enforce those repository settings.
Each archive includes the same CycloneDX inventory of the npm dependencies
installed for the Linux frontend build and Go module lists extracted from the
two built executables. The npm inventory includes build and test dependencies
and tree-shaken packages, so it is not a list of code proven to ship. The local suite checks
installed identities, license labels, and dependency and optional-dependency
edges against it. Peer edges and dynamically loaded inputs are not fully
verified, and operating-system libraries are not fully inventoried there.

After installing frontend dependencies and building the frontend, run
`node scripts/collect-licenses.mjs /tmp/steam-releaser-notices.txt` from the
repository root to generate the dependency notices for the current platform.
Release packaging runs this on each target and fails when a license file is
missing or an npm license needs review. It includes notices in the ancestors of
compiled Go packages and embedded assets as well as module-root notices. It also
includes Vite, Tailwind, and the Rolldown package resolved by Vite, including
Rolldown's third-party licenses for code that may appear in the bundle. Review
Go dependency licenses, file-level terms, and third-party notices when updating
dependencies; collecting files is not an automated compatibility assessment.
The installed npm check also compares license declarations and verifies that
reachable production dependencies are not marked development-only. It does not
prove that every dynamically loaded build input is represented in the inventory.

See the [contributing guide](../CONTRIBUTING.md), [architecture](architecture.md),
[security policy](../SECURITY.md), and [audit](audit.md) for project practices and
remaining release risks.
