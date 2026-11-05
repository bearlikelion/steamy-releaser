# Steam Releaser security and architecture audit

> The CI descriptions below record the checks in place during the audit.
> Routine verification now runs locally with `bash scripts/check-local.sh`;
> hosted Actions run only for tagged releases. See the
> [development guide](development.md) for the current process.

## November 4, 2026 supply-chain follow-up

Go, npm, Wails, and GitHub Actions dependencies were checked for updates. The
npm direct dependencies and Go application dependencies were updated, with two
compatibility exceptions. Wails 2.17.1 uses a locked Go tool graph; `clir`
remains at 1.4.0 because 1.5.0–1.7.0 break its build-command defaults. Systray
remains at 1.12.1: the isolated Linux tray race test found a data race in
1.12.2's asynchronous menu refresh, while 1.12.1 passed repeated runs. CI
checked the Wails command, the tray, the Go module cache, vulnerability reports,
npm registry signatures, lockfile sources, and supported Go and Node patches.
A weekly Linux run also checked for new vulnerabilities and runtime updates.
The prior runtime check accepted Node 20's final patch even after its April 2026
end of life because the release index retains its historical LTS label. The
check now also uses [Node's support schedule](https://github.com/nodejs/Release/blob/main/schedule.json)
and rejects a major before LTS or on/after its end date. It checks every Node
major permitted by the frontend manifest, requires matching bounded ranges in
the lockfile, and requires each minimum to be that major's latest LTS patch.
The previous open-ended range also accepted a simulated Node 27 version in an
npm dry run. The frontend now lists only supported 22, 24, and 26 majors;
its Node 24 and 26 minimums were updated. These checks require the upstream
release indexes and support schedule to be available.
The Wails build tool's `github.com/xo/terminfo` dependency was updated to
1.3.1 to fix malformed terminal-data crashes and bound formatting output; the
vulnerability scanner's `golang.org/x/telemetry` dependency was also updated.
Comparing available module updates with the native release binaries found only
the deliberately pinned systray version among their compiled dependencies.

A controlled lockfile substitution installed one npm package under another
package's name while both the former source check and `npm audit signatures`
passed. The lockfile check now binds registry tarball paths to package names and
versions, and each CI platform compares each registry-sourced package's identity, URL, and SHA-512
integrity with npm's registry metadata. It verifies the signature over each
locked identity and digest using npm signing keys authenticated through Sigstore
TUF, including packages not installed on Linux. A simulated registry response
showed that a newly signed digest could pass the former expired-key check when
the unsigned publication timestamp was backdated. The check now accepts an
expired key only for an exact package identity and digest recorded in
`scripts/npm-historical-signatures.json`, while the key and its expiry must
still match the authenticated TUF target. The checks currently use 42 such
identities, including one bundled child; 37 had the same digest in the
pre-audit lockfile. These recorded exceptions do not independently prove when
they were signed or that the package contents are safe. A changed or new
historical exception requires independent review. The verifier also limits keys
to npm signature usage and checks their validity start time.

The update to jsdom 31 introduced four test-only packages whose registry
signatures use the expired key: `bcp-47-match@2.0.3`, `is-alphabetical@2.0.1`,
`is-alphanumerical@2.0.1`, and `is-decimal@2.0.1`. Their version tags point to
[59d8bf28481e990aa7b38807270d76d2fad98606](https://github.com/wooorm/bcp-47-match/commit/59d8bf28481e990aa7b38807270d76d2fad98606),
[d3534699722c6b1b2dd156ed2de102f0df0f0d02](https://github.com/wooorm/is-alphabetical/commit/d3534699722c6b1b2dd156ed2de102f0df0f0d02),
[026b5f7b5f5888ff231c1db4e46fde2e0d774b61](https://github.com/wooorm/is-alphanumerical/commit/026b5f7b5f5888ff231c1db4e46fde2e0d774b61), and
[6ebcbb7ca2b61486f276c813ff4d7b6ab99b1961](https://github.com/wooorm/is-decimal/commit/6ebcbb7ca2b61486f276c813ff4d7b6ab99b1961).
Each commit's PGP signature verified using the key with fingerprint
`D931CE24F33F92FCC7361084E6E581152ED04E2E`, published by the repository owner;
GitHub reports verifying the signatures in November 2024. The published
JavaScript, package metadata and licenses match those commits byte for byte.
Each archive also contains a generated `index.d.ts` absent from the commit, and
the `is-decimal` README has an added table of contents. Those differences were
reviewed and remain covered by the exact archive digest, not by the source
commit. This evidence does not establish when npm made its registry signatures.
`npm audit signatures` remains in place to verify signatures and available
provenance for installed packages on each platform. Each CI platform now checks
signed lockfile metadata before installation and audits the installed packages
before running frontend checks or builds. Previously, the Windows and macOS
jobs could run frontend code before the Linux registry check completed, and
all three jobs ran it before their installed-package signature checks.

A later test changed a bundled `tslib` version in the lockfile from 2.8.1 to
2.99.0. Both previous verifiers accepted it even though the parent's signed
archive contained 2.8.1, and npm loaded the false version into its virtual
audit tree. CI and packaging now compare declared bundle lists against registry
metadata, verify the parent archive's signed SHA-512, and match the bundled
package identities and versions in that archive to the lockfile. A later test
showed that altered child code with the same name and version still passed that
identity check. The verifier now downloads each child's separately signed npm
archive and compares its regular file paths, modes, and bytes with the copy in
the signed parent. All six current bundled children match. This strict rule
will require review if a parent intentionally modifies a bundled child; it does
not establish that the published code is benign. In a separate clean-install
test, modifying one installed child file left both the identity check and
`npm audit signatures` green. Before the audits and frontend code, each active
platform now checks installed identities and compares every installed registry
package's file paths and bytes against its signed, digest-checked archive from
the installation cache. A controlled change to Vite's installed executable also
passed the former identity and signature checks; the expanded check rejected it.
Generated command links and complete Windows shims are checked separately
against the verified packages, with the build commands bound to their expected
owners. A clean Linux install passed for 200 packages, 10,383 files and 12
command links. The installed check does not compare filesystem modes, which npm
can normalize, and cannot prevent later changes by other processes or frontend
code. Archive inspection is bounded and accepts regular ustar archives and the
alternate root used by the current type packages; unsupported formats or size
limits fail the check. Archives for optional packages that are not installed
are not all independently inspected. These checks establish metadata
consistency, not that a publisher's code is benign.

An unminified build of the frontend contains Rolldown runtime helpers, but the
previous native release notices omitted Rolldown and its third-party licenses.
Rolldown's [upstream license notes](https://github.com/rolldown/rolldown#licenses)
identify code derived from Rollup and esbuild. The collector now resolves the
Rolldown package actually used by Vite, includes its license and third-party
license file, and fails if that third-party file is missing. It also recognizes
third-party notice and copyright-notice filenames. A fresh Linux inventory
includes the omitted notices, and tests cover nested npm installations. These
checks do not establish complete file-level license coverage or compatibility.

The native release's frontend SBOM omitted React, React DOM, and Scheduler,
although the installed packages and bundled frontend contain them. The pinned
`npm sbom --omit=dev` reproduced the omission. The release workflow now creates
a full inventory from the installed Linux frontend build dependencies before
frontend code runs and includes that same inventory in every platform archive.
Verification compares all installed identities, package URLs, license labels,
and dependency and optional-dependency edges against the SBOM; packaging also
checks its identities and references against the lockfile. The inventory is
conservative: it includes test and build tools and packages removed by tree
shaking. It does not prove that every listed package is shipped, verify all peer
edges, or cover dynamically loaded inputs and operating-system libraries.

Wails could reinstall frontend dependencies after the release job audited its
first installation. Release packaging now rechecks the JavaScript toolchain and
signed lockfile metadata, audits installed dependencies and their signatures,
and runs Wails with its frontend step disabled and npm blocked. It checks the
resulting executable for the built frontend asset paths and bytes. A headless
Linux build and native package builds on all three release platforms confirmed
that Wails used prebuilt assets with npm blocked. A
separate [platform check](https://github.com/thornity/steam-releaser/actions/runs/41947379999)
verified the stricter blocker resolution in the release shell on all three.

A later controlled frontend build showed that dependency code could append to
the Actions PATH and alter the Go module cache before native compilation, even
with installation scripts disabled. The release workflow now builds the
frontend in a separate Linux job and passes its output as a size-limited data
artifact to fresh native packaging jobs. Those jobs do not restore Go caches or
run frontend dependency code; npm installation still uses disabled lifecycle
scripts for inventories and audits. Before attestation and publication, the
workflow downloads only the three named platform artifacts and checks each
archive against its checksum. CI also uses fresh Go caches and finishes its Go
checks before executing frontend dependency code, so the frontend cannot change
the remaining Go checks or a cache saved for a later run. The desktop Go scan
uses a placeholder frontend asset; the frontend checks and build run afterward.
These boundaries do not establish that frontend
code is benign, or independently secure the npm CLI, native compilers,
operating system, or GitHub-hosted runners.

An unchanged-output guard checks configured outputs after their build commands.
Missing outputs, empty output directories, or unchanged outputs need evidence
from a newly created or changed post-build source before they can proceed.
Existing post-build sources that can affect selected content are fingerprinted
before the build, and unchanged sources cannot replace different content files
or links. The copy verifies its source bytes again before replacing an output.
Chained copies are rejected when their origin
cannot be verified; adding static files to a directory the build changed remains
supported. Reused targets' entire content trees cannot be changed by copies and
are fingerprinted again after other builds and copies. These checks add I/O and
bound the tracked source and destination path sets to one million entries and
128 MiB of path names each.

The guard does not delete or move project outputs, but earlier copy rules can
have completed before a later rule is rejected. It is not a provenance check:
an unrelated change or timestamp update can still mask stale content, and
deterministic builds that preserve metadata can be rejected. Other programs can
change files outside the cooperative project lock. The staging and
source-identity finding remains open below.

On Unix, post-build and fingerprint checks now open candidate files in
nonblocking mode and verify their type after opening. A named pipe previously
blocked post-build inspection even after its context expired; the same open
race affected artifact and source fingerprinting. Tests cover the named pipe
and the nonblocking open. Other slow filesystem operations can still delay a
cancellation.

A real subprocess fixture returned exit code zero while its stdout reported
`ERROR!`, but stderr was interleaved between the two fragments and the upload
was falsely reported as successful. The runner now retains bounded stdout and
stderr separately for SteamCMD status checks while preserving the combined log
for display. Login, upload, and logout inspect each stream, and truncated logout
output is unconfirmed. A second fixture showed that empty or unrelated output,
the wrong App ID, and a preview were also accepted as successful uploads. A
successful upload now requires a recognized completion for the selected App ID
and requested mode; a preview is explicitly reported as such. Known branch
commit failures are treated as errors, and requested branch promotion still
needs confirmation in Steamworks. Cancelled and unconfirmed uploads direct users
to check Steamworks before retrying.

For the default generated output, a fresh, exclusive per-run directory is held
open while SteamCMD runs. Its bounded, regular build log can supply a missing
console completion; conflicting results and a replaced output directory fail
closed. A shared `build_output` or `existing_vdf` log cannot establish that it
belongs to the current run and is not used as a fallback. The recognized output
format is based on [observed SteamCMD build logs](https://docs.rs/steamship/latest/src/steamship/upload.rs.html)
and may change; [Valve's issue tracker](https://github.com/ValveSoftware/Source-1-Games/issues/1929)
also records console output being lost when redirected. No live Steam upload was
performed in this review.

Generated Steam scripts previously prefixed double quotes in configuration
values with backslashes. Valve's public [KeyValues parser](https://github.com/ValveSoftware/source-sdk-2013/blob/master/src/public/tier1/KeyValues.h)
has escape sequences disabled by default, and its [SteamPipe examples](https://partner.steamgames.com/doc/sdk/uploading)
use literal Windows backslashes. Under the default parser behavior, a quote in
a description or mapping can terminate the value and introduce another field.
A local tokenizer exercise produced an extra `Preview` field from such a value;
the actual SteamCMD parser mode and a live upload were not tested. Generated
scripts now reject quotes, control characters, invalid UTF-8, and oversized
values during planning and again when serializing, before files or build outputs
are created. Windows backslashes remain literal. User-supplied `existing_vdf`
scripts are not rewritten or inspected by these checks.

Generated mapping sources, destinations, and exclusions previously accepted
absolute paths and parent traversal. SteamPipe documents mappings relative to
their content roots. Generated scripts now reject portable absolute, traversal,
drive, and stream syntax with either slash style before planning or publishing.
This lexical check does not sandbox SteamCMD or inspect custom VDF scripts.

A project symlink could redirect generated Steam scripts or the default
Steam output directory outside the project. Generated-state paths are now
checked during preflight, and script creation and replacement use project-root
confined filesystem operations. An explicitly configured `build_output` may
still be external. Another process can change paths after validation or while
SteamCMD is running; the project lock coordinates only Steam Releaser processes.

A Windows CI run exposed SQLite lock errors during concurrent history saves.
Completed legacy migrations now use a read-only check instead of taking a
write lock for every history operation, and database connections wait up to
30 seconds for a lock rather than five. Concurrent migration and delayed-write
tests cover these paths. Contention lasting beyond the wait can still fail.

A retry's former top-level Git diff could report the same fingerprint for
different tracked changes inside a dirty submodule, or when Git's index flags
or stat cache hid a changed file. Source checkpoints now read the actual
working-tree bytes of tracked and non-ignored untracked files and recursively
inspect initialized submodules throughout the repository. Steam Releaser's
generated state and local database are excluded. Uninspectable submodules,
unregistered nested repositories, and checks exceeding 30 seconds fail the
checkpoint; Git repository-selection environment variables cannot redirect it
to a different index. Partial retries without a Git checkpoint rebuild
completed build targets instead of reusing them; an explicitly failed
checkpoint requires a new operation. Desktop source capture and retry
verification run under the project lock, and failed verification does not carry
old checkpoints into the new history entry. Older source checkpoints use a
different format and require a new operation. Ignored and external build inputs,
including symlink targets not otherwise enumerated, remain outside this check,
so it does not establish complete source provenance.

The project's npm audit does not cover npm's own bundled dependencies. Node
26.13.0 contains npm 11.20.0; its package versions matched 19 advisories,
including six high, on November 4. New npm releases appeared later that day.
The unmodified npm 12.2.2 archive matched two moderate advisories, in `glob`
and `minimatch`, and no high advisories in the same registry check. These are
version-based matches; reachability has not been established. npm 12.2.2 also
includes the upstream fix for the
[bundled undici issue](https://github.com/npm/cli/issues/10163).

CI and release packaging now replace those two bundled packages with signed,
digest-pinned `glob` 14.0.3 and `minimatch` 11.0.4, plus their pinned
`path-scurry` 3.0.0 and `@isaacs/nonrecursive-glob-match` 1.0.3 dependencies.
Bootstrap validates the original npm package versions, supporting dependencies,
replacement archive contents, dependency declarations, and current registry
signatures before activation. The resulting toolchain had no version-level npm
advisory matches in the November 5 check. These replacements cross npm's
declared major-version ranges and are not an upstream-supported npm build.
Because the TUF client uses `minimatch` for delegation paths, registry-key
verification also requires the currently reviewed signed delegation topology
(`registry.npmjs.org/*`) and a direct key target with no nested delegations;
it checks the resolved metadata before downloading the keys. A topology change
fails until reviewed. Compatibility tests exercise allowed and rejected target
paths with the replacement matcher.
Clean installation, workspace patterns, audits, signature verification, SBOM
generation, frontend tests, and a clean build were exercised; other npm commands
and inputs can still be incompatible. Reassess or remove the replacements when
npm publishes a suitable fix.

A prior clean Linux run with Node 26.13.0 and unmodified npm 12.2.2 used V8
coverage while checking signed lock metadata, installing dependencies, running
audits, frontend checks and tests, building, and generating the SBOM. For those
inputs it did not observe calls to the glob walker or minimatch preprocessing
functions identified in the two advisories. This does not establish whether
other inputs, npm commands, or platforms reach them.

The frontend previously warned but continued when `npm ci` ran with an
unsupported Node version. An isolated test with Node 24.17.0 reproduced that
behavior; its bundled undici had high-severity version matches. The frontend now
enables strict engine checks and requires npm 12.2.2 or newer as well as a
supported Node patch. Tests exercise rejection of older Node and npm versions.
This blocks dependency installation; it does not prevent an older package
manager from starting or protect an already-installed dependency tree.

The npm and replacement tarballs' fixed SHA-512 digests and registry signatures
are checked before their code runs. Bootstrap verifies signatures against a pinned npm signing
key authenticated through Sigstore TUF during review, then uses Node's built-in
archive primitives to reject unsafe entries and extract into an isolated runner
directory. Before running npm, it checks all signatures again against current
TUF keys using the verified package's own client. The pinned key must be reviewed
and updated when npm rotates signing keys.

The previously pinned `setup-node` action ran the Node distribution's npm
11.20.0 and the runner's Yarn with `--version` even with npm caching disabled;
the CI logs show both executions. CI and packaging now download pinned Go and
Node releases and check their SHA-256 hashes before extracting or executing
either runtime. Only the Node executable is extracted, so its bundled npm is not
used. The Go archive digests were checked against [Go's release metadata](https://go.dev/dl/?mode=json&include=all).
The Node checksum list was verified against the [Node release-key repository](https://github.com/nodejs/release-keys/tree/481637f813e912c4aa3622d7964ab426c97b8e8d);
its signer fingerprint was
`C82FA3AE1CBEDC6BE46B9360C43CEC45C17AB93C`. The pinned hashes must be reviewed
when updating either runtime. The bootstrap relies on Python, the Actions runner,
and the operating system provided by GitHub; it does not independently verify
those components or native compiler and system-library dependencies.

Node's built-in `fetch` uses a separate copy of `undici`, including for the
registry verification and advisory checks. Node 26.13.0 contains undici
8.12.2, which had no npm advisory matches on November 4. CI checks version-based
matches for both npm's packages and Node's built-in undici on each active
platform before running frontend code. Matches at any severity fail the check;
the patched npm 12.2.2 toolchain had none on November 5. The report does not
establish complete Node vulnerability coverage or assess reachability. These
checks now run locally instead of in weekly or Dependabot-triggered workflows.

For the updated Linux npm installation, all 200 packages checked had valid
registry signatures; 126 also had verified provenance attestations. npm's
signature audit checks attestation signatures and subjects, but does not
require the signer to belong to a particular source repository. The
source-identity check pins repositories and their GitHub repository and owner
IDs for 168 locked packages, including optional native variants and four
bundled children. It checks each SLSA subject against its signed package digest
and binds the repository, commit, ref and IDs to the GitHub OIDC issuer and
corresponding [Fulcio certificate extensions](https://github.com/sigstore/fulcio/blob/main/docs/oid-info.md).
The Linux scan checks all pinned lockfile entries, including Windows and macOS
variants, without installing or building them. The registry reported no
provenance for another 104 locked entries. Their exact identities and signed
digests are recorded in `scripts/npm-unattested.json`; a new, changed or
removed unattested identity now requires an explicit baseline update. The
baseline records existing inputs, not independent proof of their sources or
safety. The initial source-identity pins came from signed
installed package metadata and attestations; native variants use the identities
of their verified siblings, and bundled children are compared with their own
signed archives. All 45 unique repository IDs and their owner IDs matched
GitHub's API on November 5. That agreement is not independent proof that the
repositories are authorized publishers. Independently review existing and new
pins. Of the 46 npm
packages named in the sampled frontend JavaScript source map, 35 had this
repository and commit evidence and 11 did not. This does not cover CSS or
establish that the referenced source is complete or safe. Simulated
macOS and Windows installs verified the signatures of their installed packages.

For the 11 packages in that JavaScript sample without npm provenance, the
following candidate source commits were located on November 5. Repository
names come from package metadata, and npm's `gitHead` field is unsigned. A
matching tag or GitHub-verified commit does not prove that it produced an npm
archive. Links to these commits also do not preserve the source for a release.

| Package | Candidate source evidence |
| --- | --- |
| `aria-hidden@1.2.6` | The [version tag](https://github.com/theKashey/aria-hidden/commit/720e8a8e1cfa047bd299a929d95d47ac860a5c1a) matches npm's `gitHead`; the commit is unsigned. |
| `class-variance-authority@0.7.1` | A [version-tagged commit](https://github.com/joe-bell/cva/commit/45462dd239546f570bca7821ab56bcef61feb900) has a GitHub-verified signature. npm reports no `gitHead`. |
| `clsx@2.1.1` | The [version tag](https://github.com/lukeed/clsx/commit/925494cf31bcd97d3337aacd34e659e80cae7fe2) matches npm's `gitHead`; the commit is unsigned. |
| `get-nonce@1.0.1` | npm's `gitHead` exists as an [unsigned commit](https://github.com/theKashey/get-nonce/commit/fe602104915e8a18049dc96dfecbc10a96c03068); the repository had no tags. |
| `react-remove-scroll@2.7.2` | The [version tag](https://github.com/theKashey/react-remove-scroll/commit/ef5c312e4c1c6bc5d4cd60b03261d686b8cb165f) matches npm's `gitHead`; the commit is unsigned. |
| `react-remove-scroll-bar@2.3.8` | npm's `gitHead` was unavailable in the stated repository, which had no tag for this version. The unsigned [2.3.7 tag](https://github.com/theKashey/react-remove-scroll-bar/commit/29e9fcd1eecf7d3b77a767941c4a57fe461fc1e4) provides a candidate source for the sampled files. |
| `react-style-singleton@2.2.3` | npm's `gitHead` was unavailable in the stated repository, which had no tag for this version. The unsigned [2.2.2 tag](https://github.com/theKashey/react-style-singleton/commit/0855d4b3478326fda30efff7e0511540be7f57e8) provides a candidate source for the sampled files. |
| `sonner@2.0.8` | The [version tag](https://github.com/emilkowalski/sonner/commit/6739aca5f668a33a4fd1d2fd9f758dff95c57466) is one commit after npm's `gitHead` and changes only `package.json`'s version. GitHub verifies the parent commit's signature; the tagged commit is unsigned. |
| `tslib@2.9.1` | The sampled `tslib.es6.mjs` matches the [version-tagged commit](https://github.com/microsoft/tslib/commit/f16778939f07f107f3caa52db371f0a853a96708) byte for byte. GitHub verifies that commit's signature. npm reports no `gitHead`. |
| `use-callback-ref@1.3.3` | The [version tag](https://github.com/theKashey/use-callback-ref/commit/bd99fd56601f693ecdda88e2d4455cfa03c5689d) matches npm's `gitHead`; the commit is unsigned. |
| `use-sidecar@1.1.3` | The [version tag](https://github.com/theKashey/use-sidecar/commit/0ceffd96208b62a66883fd472fa48a2ccd4acaa8) matches npm's `gitHead`; the commit is unsigned. |

Of the version-matching candidate commits, only `tslib` contained a
byte-for-byte match for the sampled npm JavaScript. A separate network-isolated
TypeScript emit reproduced all 20 sampled JavaScript files from `aria-hidden`,
`get-nonce`, `react-remove-scroll`, `use-callback-ref`, `use-sidecar`,
`react-remove-scroll-bar`, and `react-style-singleton`. The last two used their
previous-version source tags because the current versions' commits were
unavailable. The emit used TypeScript 3.8.3, 4.6.3, or 5.8.3, as recorded in
the respective candidate source's Yarn lockfile, with ES5 target, ES2015
modules, and `--noEmitOnError false`; comments were removed only for
`get-nonce`. The compiler archives matched those lockfile digests. For five of
the packages, the first emit reported errors parsing the current Node type
declarations. Further emits excluded Node and Jest ambient types, added ES2015
ambient libraries for three packages, and used lockfile-pinned React types for
two; all five then exited successfully and reproduced the sampled files. These
type substitutions and the other current dependencies mean the comparison is
not a complete reproduction of each upstream build environment.

For `class-variance-authority`, an isolated SWC 1.4.16 transform using the
candidate commit's configuration reproduced the sampled JavaScript after the
newline added by SWC CLI 0.3.12. The SWC package digests matched that commit's
`pnpm-lock.yaml`. An isolated Terser 4.8.0 minification also reproduced the
sampled `clsx` file. The candidate commit specifies that Terser version but has
no lockfile; the Terser and `source-map@0.6.1` archive digests came from current
npm metadata, not an independent pre-existing pin.

For `sonner`, an isolated Bunchee 6.3.3 build reproduced the published ESM and
CommonJS JavaScript and TypeScript declarations byte for byte; its stylesheet
also matched the candidate source. The local build used 107 manually assembled
package archives whose digests matched the candidate commit's `pnpm-lock.yaml`,
excluding optional binaries for other platforms or libc variants. One temporary
tool archive had two normalized copies of a file; they were accepted only after
their bytes and modes matched. This did not change the project's package
verification policy. These comparisons do not establish publisher
authorization, prove the original build used these steps, or review every file
in any package. In particular, the current `gitHead` references for the two
packages above remain unavailable.

The npm audit reported no known vulnerabilities. An earlier Go scan reported no
reachable third-party vulnerability in the Linux application or tools, but did
report standard-library vulnerabilities with the locally available Go 1.27.1.
The full local suite later ran with the required Go 1.27.3 and pinned Node
26.13.0; its Go scan found no reachable vulnerabilities. It also reported one
module-level vulnerability in the unused legacy `x/crypto/openpgp` package;
the application and build tools do not import that package.

The private repository's default branch had 38 open Dependabot alerts when
checked on November 4. Comparing their affected version ranges with the draft
Go module graph and npm lockfile found 15 alerts for packages absent from the
draft and 23 for packages outside the affected ranges. GitHub must rescan the
default branch after the changes merge to confirm which alerts close.

Public release archives are configured to receive GitHub provenance
attestations; archives also contain checksums, a frontend CycloneDX inventory,
Go build module lists, and third-party notices checked against the binaries.
The release packaging steps and platform matrix were exercised in a one-time,
read-only pull request workflow on native Linux, Windows, and macOS runners.
All three archive checksums, target architectures, inventories, and notices
were checked. A later [native packaging run](https://github.com/thornity/steam-releaser/actions/runs/41946248435)
repeated the release job with unmodified npm 12.2.2 and the audited frontend
build on all three platforms. Its checksums, binary architectures, included
AGPL licenses, Go manifests, and frontend CycloneDX inventories were verified.
A [Linux packaging rehearsal](https://github.com/thornity/steam-releaser/actions/runs/42002166711)
later passed with the npm replacements, before the TUF delegation guard was
added; its checksum and bundled frontend inventory were verified independently.
A later [one-time native packaging rehearsal](https://github.com/thornity/steam-releaser/actions/runs/42008587640)
passed on Linux, Windows, and macOS with the replacements and TUF guards. Its
archive checksums, target architectures, frontend inventories, embedded frontend
assets, AGPL license, and Windows WebView2 strategy were checked independently.
The rehearsal used the release frontend and packaging steps without publishing;
tagged publication and public attestations remain untested.

After the latest frontend dependency updates, a network-isolated local Linux
rehearsal built the Wails desktop application and CLI with Go 1.27.3 and Node
26.13.0, using the previously verified clean npm installation and frontend
build. It rechecked the inventory, source map and embedded frontend, collected
notices for 26 Go modules and 90 npm packages, checked the Go notices against
binary metadata, and verified the archive checksum. The checkout used for that
rehearsal differed from the current draft only in this audit document and the
order of identical historical signature entries. Windows and macOS packaging
have not been rerun for these latest dependency changes.

A text-pattern scan of the selected Linux, Windows, and macOS Go dependency
graphs examined 1,208 source files; one embedded binary was excluded. Generated
`modernc.org/libc` and `modernc.org/sqlite` files contain GCC license notices
referencing the [GCC Runtime Library Exception](https://www.gnu.org/licenses/gcc-exception-3.1.html),
and macOS-selected files also reference the [LLVM exception](https://github.com/llvm/llvm-project/blob/main/LICENSE.TXT).
Wails' `pkg/menu/menuroles.go` references the [Electron license](https://github.com/electron/electron/blob/main/LICENSE).
The current Linux notice report and earlier Windows/macOS reports do not include
those exception texts or the Electron copyright notice. A package graph does not
prove which source bytes are in the linked binaries, and this pattern scan is
not a complete license review. Review the applicability of these notices and
retain the appropriate texts before distributing release binaries.

An isolated frontend rebuild with hidden source maps produced the same eight
frontend assets as that rehearsal, byte for byte. Its map recorded source text
for 132 files, all matching the checked-out project or installed npm files.
The release workflow now keeps maps outside the app and checks their asset and
map hashes and source text against the clean input tree in each packaging job.
This does not establish that a map's mappings are complete or correct; npm
packages may contain generated JavaScript rather than preferred source. CSS,
Go, operating-system libraries, and other build inputs also need separate review.
A [one-time source-map packaging rehearsal](https://github.com/thornity/steam-releaser/actions/runs/42016402035)
passed on Linux, Windows, and macOS. The three archive checksums, included map
files, target architectures, and embedded frontend assets were independently
checked; the rehearsal did not publish a release.
After adding traversal and file-size bounds, a separate [native verification run](https://github.com/thornity/steam-releaser/actions/runs/42017465184)
rechecked the source maps against fresh Linux, Windows, and macOS inputs.
The earlier frontend-artifact check bounded files and bytes but accepted 4,100
empty directories. It now streams directory entries and limits their total as
well, with a regression test for directory-only artifacts.

The first macOS package declared support for macOS 10.13, while
[Go 1.27 requires macOS 13 or newer](https://go.dev/doc/go1.27#darwin). The
tracked Wails property-list templates and release compiler/linker settings now
target macOS 13. The release workflow checks both the app's declared minimum
and its binaries' minimum versions, and rejects unexpected source changes after
the desktop build. The revised native archives passed these checks.

The live private repository's default workflow token is now read-only,
Dependabot alerts and automated security-update pull requests are enabled, and
the Actions policy allows GitHub-owned and local actions. An active tag ruleset
restricts creating, updating, or deleting `v*` release tags to repository
administrators. Its default branch still contains older manifests and mutable
action tags until the draft changes are merged. Repository-wide action SHA
enforcement should be configured after the pinned workflows are in place.
Private vulnerability reporting and public-repository secret scanning should be
enabled when supported for the intended public repository.

## November 4, 2026 open-source review

- The current tree uses `AGPL-3.0-only`. Earlier commits contain an MIT license;
  changing the current license does not revoke any rights granted for earlier
  distributions. The three commits have one author, but commit authorship alone
  does not establish ownership of every contribution or asset.
- Go 1.27.1's package-level vulnerability scan reported 12 standard-library
  findings and two additional module-level findings. These include
  [GO-2026-6604](https://pkg.go.dev/vuln/GO-2026-6604), which affects Windows
  `os.Root` directory creation used by artifact copying, and
  [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809) concerning certificate
  trust. The minimum Go version is now 1.27.3, which includes their fixes.
  Tests and cross-builds during this review ran with the locally available
  1.27.1; CI uses 1.27.3. The earlier cached vulnerability scanner could
  not perform a call-graph scan; the newer pinned scanner was able to do so
  with the locally available 1.27.1 compiler.
- A fresh npm audit reported two high and two moderate findings in development
  dependencies. Compatible lockfile updates cleared the report; frontend checks,
  79 tests, and the production build passed after the updates.
- Release packaging now collects Go, npm, and Go standard-library license files
  for each target. The shadcn/ui and Geist notices, and the upstream notice for
  an npm package that omits it, are retained in `LICENSES`. The collector was
  exercised against Linux, Windows, and macOS Go dependency graphs and rejects
  unreviewed npm license types. Collection does not by itself establish legal
  compatibility or discover every notice embedded inside third-party source.
- A bounded scan of current candidate files and 245 Git-history blobs found no
  private-key headers or the GitHub, AWS, Slack, Google, and npm token formats
  checked. Credential-like high-entropy matches were test fixtures. This is not
  a guarantee that the history has no secrets. Git history also exposes the
  commit author's name and email; review that disclosure before publication.

The artifact freshness, concurrent release, and YAML edit risks below remain
open. Verify copyright ownership and third-party notices,
enable a private vulnerability-reporting channel, and run the release matrix
before publishing binaries. The module path and generated configuration's
documentation link now use `github.com/thornity/steam-release-tool`.

## October 7, 2026 review

Reviewed on October 7, 2026, against the working tree based on `b9f357f`.
The CLI, desktop bridge, engine adapters, artifact handling, Steam publishing,
dependencies, and workflows were reviewed. Existing edits were preserved;
authentication and release-history work also changed during this pass.

The shared service and argument-array process runner provide a useful foundation.
Concrete filesystem and process-output failures were reproduced and fixed.
Release isolation, artifact freshness, and complete dependency notices remain
open before treating the packaging workflow as ready for unattended publishing.

## Fixed findings

| Priority | Finding | Resolution and evidence |
| --- | --- | --- |
| P1 | Post-build sources and destinations could escape their roots through file or directory symlinks. | Copies use `os.Root` for project reads and content writes. Regression tests cover all four escape cases and check that external files remain unchanged. |
| P1 | Copying a file onto itself, including through a hard link, truncated the source. A directory copied beneath itself could grow recursively. | Same-file and directory containment checks reject these cases. Files are staged before replacement. Regression tests preserve original bytes and reject recursive copies. |
| P2 | Framework directory symlinks were treated as files, breaking macOS bundle copies. | Directory copies preserve relative links within the copied tree. A framework fixture checks both directory and executable links. |
| P2 | Replacing a copied symlink removed the destination before creating its replacement. | Stage the link and rename it into place; reject an existing directory rather than removing it. A regression test preserves the original directory. |
| P2 | Artifact validation ignored traversal errors and accepted escaping/broken links and special files. | Validation reports traversal errors, confines symlink resolution, and requires regular files/directories. Escaping and broken artifact-link tests pass. |
| P1 | A process output line over 1 MiB stopped the scanner and could leave the child blocked indefinitely. | The runner drains long lines in bounded fragments. A 2 MiB line reproduced the timeout before the fix and completes afterward. Already-cancelled contexts also return before starting a child. |
| P1 | SteamCMD errors after the 4 MiB retained-output limit could be hidden while publishing reported success. | Truncation is explicit in runner results. Upload and login return an unconfirmed result rather than success; tests check that no success event or new username cache is produced. An actual upload may already have completed, so the error directs users to verify Steamworks before retrying. |
| P1 | Interleaved stdout and stderr could split a SteamCMD error, and a zero exit with no matching build completion was treated as a successful upload. | Check each bounded stream separately, require a matching App ID and upload/preview completion, and consult only a fresh default build log. Tests reproduce both false-success cases and reject stale, unsafe, or conflicting logs. Live SteamCMD behavior and branch promotion remain unverified. |
| P1 | Two Steam Releaser processes could concurrently build or upload the same project's shared outputs and VDFs. | Build, upload, and ship acquire a cooperative project lock; `ship` holds it through both phases. Tests cover competing processes, abrupt exit, and the absence of lock files in artifact content. The lock's limitations are listed below. |
| P1 | Backslash-escaped quotes in generated Steam scripts could be parsed as syntax when KeyValues escape sequences are disabled. | Reject quotes, control characters, invalid UTF-8, and overlong values before planning or publishing generated scripts; test every serialized input and check that rejected inputs do not start a build or overwrite existing scripts. The SteamCMD parser mode was not directly verified. |
| P1 | Generated Steam mappings accepted paths outside the documented content-relative form. | Reject absolute paths, parent traversal, drive prefixes, and stream syntax in mapping and exclusion paths using both slash styles on every host. This is a lexical guard, not a SteamCMD sandbox. |
| P1 | A symlink under generated Steam state could redirect script writes or the default output directory outside the project. | Check existing paths before building and use project-root confined operations when creating and replacing scripts and the default output directory. Explicit external `build_output` paths remain supported; other processes can still change paths while SteamCMD runs. |
| P1 | Concurrent uploads could overwrite each other's generated scripts and default Steam output. | Each generated upload creates a random, exclusive script directory and a separate default output directory. Tests run simultaneous generations and verify each retains its own options and output. Explicit `build_output` paths and user-supplied VDFs remain shared if configured that way. |
| P1 | Generated scripts or Steam output could overlap selected content, changing or entering the uploaded files after artifact validation. | Preflight rejects overlapping content and generated-state paths, resolving existing symlinks and checking again during script generation. Tests cover both containment directions, aliases, and failure before building or starting SteamCMD. Path checks cannot prevent other programs from changing paths afterward or identify every alias. |
| P1 | Targets could concurrently write to shared content, or write to the same configured output. | Planning rejects overlapping outputs and serializes targets with overlapping content/output paths. Existing symlinks and hard links are checked, and paths are rechecked after taking the project lock. Shared files can still be overwritten; the remaining limitations are below. |
| P1 | Desktop retries could reuse modified artifacts or upload changed files after a failed attempt. | Completed targets and pre-upload content receive SHA-256 fingerprints. Retries verify them under the project lock, rebuild changed built targets, and reject changed prebuilt or upload content. Configuration bytes actually loaded, Git commit, and tracked changes are also rechecked under the lock. Missing checkpoints cannot authorize automatic reuse; custom `existing_vdf` build-and-deploy or upload attempts cannot be retried automatically because their scripts can reference other content. The first-build and source-identity limitations remain below. |
| P1 | A stale post-build copy could make an unchanged output appear fresh, replace newly built output or sidecars, or change reused content after its checkpoint was verified. | Snapshot relevant copy sources before building, reject unverified replacements in selected content, verify copied bytes before replacement, and recheck reused content after other builds and copies. Tests cover file and directory outputs, sidecars, chained copies, unchanged and fresh sources, and retries. Full provenance and isolation remain open. |
| P2 | A named pipe or a file replaced by one could block post-build or fingerprint inspection. | Use nonblocking opens on Unix, then check that the opened object is a regular file. Named-pipe regression tests complete without a writer. Other slow filesystem calls can still delay cancellation. |
| P2 | Invalid or missing custom Steam upload scripts could be discovered only after a build. | Upload planning, desktop readiness, upload, and ship check script selection and `existing_vdf` constraints before starting; generation repeats the checks before use. Tests cover missing paths, directories, incompatible options, and partial target selection without creating build outputs. |
| P2 | Initializers could overwrite each other, and a failed write could leave an incomplete new configuration. | Write a temporary file and publish it without replacing an existing entry, using a hard link or a platform-specific exclusive rename. Competing initializers and dangling symlinks are rejected; a file-size-limit test verifies failed writes leave no configuration. The filesystem must support one of these publishing methods, and a crash can leave the temporary file behind. Existing-file writes remain open below. |
| P2 | Overlapping configuration edits could silently lose changes, and validation and editing could read different versions of the YAML. | Edits and forced initialization lock the existing file; edits validate the same bytes they change and check both file identity and contents before writing. Tests cover overlapping edits, external modification, path replacement, links, and permissions. Locks and checks cannot stop every external race. |
| P2 | Wails could reinstall frontend dependencies after the release job's dependency and signature audits. | Packaging now rechecks signed lockfile metadata, audits and builds the frontend once, blocks npm during Wails, and checks that the built asset paths and bytes appear in each packaged executable. The revised native package matrix passed on Linux, Windows, and macOS. |
| P2 | A bundled dependency's version or code could differ from the separately published package while audits attributed it to that package. | Compare declared identities with the signed parent and each child's regular file paths, modes, and bytes with its separately signed registry archive. Verify installed package identities before the audit. Tests reject forged versions, altered child files, missing packages, untrusted signatures, and altered archives. |
| P2 | A forged lockfile license or development-only flag could mislabel or omit an installed production dependency from release notices and the production SBOM. | Compare installed license declarations with the lockfile and traverse installed production, optional, peer, and bundled dependencies to reject production packages marked development-only. Tests and a fixture using the actual installed dependency graph reject both forgeries and missing required production dependencies. Dynamic or undeclared build-time inputs still need review. |
| P1 | Wails' default Windows WebView2 setup downloads an installer to a predictable temporary path and executes it without a signature check. | Build Windows with Wails' browser strategy: when WebView2 is missing, the user can open Microsoft's download page and install it separately. A build guard rejects the default strategy, and release packaging verifies the build tags and executable for known downloader and embedded-bootstrapper bytes. The previous Windows packaging artifact contained the downloader; a native Windows package passed the new check and its checksum was verified independently. A missing-runtime GUI session has not been tested. |
| P2 | Generated third-party notices omitted license and attribution files nested below Go module roots. | Collect notices from compiled Go package directories, embedded-asset directories, and their ancestors as well as module roots. Platform-specific dependency graphs include previously missed Wails and WebView2 loader notices; tests cover nested files and module boundaries. Review of file-level and other bundled notices remains open below. |
| P2 | Release notices omitted Rolldown and its third-party licenses even though the frontend contains its runtime helpers. | Resolve the installed Rolldown used by Vite and include its own and third-party license files. Tests cover nested installations and the previously omitted notice filenames. Other file-level license review remains open. |
| P1 | `npm sbom --omit=dev` omitted React, React DOM, and Scheduler from the release inventory despite their inclusion in the frontend. | Generate a full inventory from the actual Linux frontend installation, verify installed identities and dependency edges, and include it in every platform archive. The inventory also lists build, test, and tree-shaken dependencies. Peer-edge and dynamic-input coverage remain limited. |
| P2 | The npm scan reported seven high-severity dependency entries arising from two advisories. | Removed the `shadcn` CLI dependency after retaining its required checkbox CSS variant locally; updated `source-map-js`. The final npm scan reports zero vulnerabilities. These findings concerned tool dependencies rather than a demonstrated remote attack on the packaged UI. |
| P2 | Workflow action tags were mutable and packaging jobs had repository write permissions. | Verified current upstream action commits and pinned every action to a full SHA. Checkout no longer persists credentials. Only the release publishing job receives `contents: write`. |
| P2 | Regular CI did not run portable Go tests on Windows/macOS or gate npm advisories. | Added the three-OS Go matrix and a high-severity npm audit gate on Linux. Native desktop packaging remains in the tag workflow. |
| P2 | Tagged archives omitted the Geist font's license. | Added its supplied OFL notice under `LICENSES` and included that directory in release archives. |
| P3 | Contributor guidance, a security reporting policy, and package boundaries were undocumented. | Added `CONTRIBUTING.md`, `SECURITY.md`, and the architecture guide; linked them from the README. Environment-file ignore patterns now include `.env.*` while allowing `.env.example`. |

The npm advisory sources are [braces recursion denial of service](https://github.com/advisories/GHSA-vfj7-8cjw-p6xm)
and [source-map-js indexed-map denial of service](https://github.com/advisories/GHSA-68fv-2mgg-jv7q).
Action pinning and token scoping follow [GitHub's secure-use guidance](https://docs.github.com/en/actions/reference/security/secure-use).

## Open findings

P1 means potential artifact/data loss or incorrect publishing; P2 means a
reliability, packaging, or maintenance gap. These are follow-up design choices,
not claims that the local tool provides an execution sandbox.

| Priority | Finding and trigger | Next step |
| --- | --- | --- |
| P1 | **Artifact freshness is unproven.** Missing outputs, empty output directories, or unchanged outputs require evidence from a newly created or changed post-build source. Unchanged sources cannot replace different existing bytes in selected content. Touching a file, changing an unrelated file in an output directory, or creating a new source from stale content can still hide an old artifact. Retry fingerprints detect changes to recorded artifact bytes, tracked files, and non-ignored untracked files, including submodules, but do not fingerprint ignored, external, or otherwise unreported build inputs. | Build into isolated staging and record a source identity covering the actual build inputs. Until then, inspect artifacts before upload and start a fresh build after project changes. |
| P1 | **Some releases still share mutable paths.** Generated scripts and default Steam output are isolated per upload, but builds and content, explicitly configured Steam output, and user-supplied VDFs may still be shared. The project lock serializes operations for the same resolved project on one machine; other programs, separate project roots, and other machines can still write shared paths. On Windows, different drive mappings to the same directory may not share a lock. Target checks cannot detect every alias or writes outside configured paths, and serialized targets can still overwrite shared files. | Give each build isolated staging paths. Use external coordination for shared projects across machines and review shared artifacts before upload. |
| P2 | **Existing configuration writes can truncate YAML on failure.** Edits and forced initialization write the existing inode in place to retain its links, ownership, and platform-specific access controls. A disk or I/O failure during the write can still leave incomplete YAML; an external program can race the checks or bypass the lock. | Design a staged replacement that retains the original file's permissions and access controls on each supported platform, including link behavior. Keep a backup until then. |
| P2 | **An uninterruptible non-file input reader can stall the internal process runner.** Go's process wait can remain blocked copying such a reader after the child exits or the context is cancelled. The desktop uses an OS file pipe, and normal CLI input uses an OS file; an internal caller supplying another reader must ensure it reaches EOF or is closed. | Define and test an interruptible input ownership contract before introducing other long-lived input sources. |
| P2 | **Login and logout lack positive confirmation.** A zero-exit SteamCMD process with empty output was accepted for both operations; login cached the username and logout reported success after clearing it locally. Clearing that username does not establish that SteamCMD revoked its token. | Identify reliable login and logout confirmation across supported SteamCMD versions, and test against a dedicated Steam account before relying on it. |
| P2 | **Release binaries lack publisher identity signatures.** The Windows CLI and app have no embedded Authenticode certificates; the macOS app and CLI have ad hoc signatures, and the workflow does not notarize them. Checksums and public GitHub attestations provide separate integrity and provenance evidence but do not supply OS-native publisher identity. | If native publisher identity is required, provision trusted Windows signing and Apple Developer ID identities, design protected signing and notarization steps, and verify the results on native runners. |
| P2 | **Complete corresponding source for release binaries has not been verified.** Release archives include source maps for frontend JavaScript, but some mapped inputs are generated code, and the maps do not cover every input. GitHub's generated tag snapshot contains repository files but not downloaded Go modules or npm packages. Lockfiles and module lists identify dependencies, but do not themselves preserve or establish the preferred source for every bundled dependency. | Before distributing binaries, identify and retain the corresponding project and dependency sources and build scripts, provide clear access from the release, and review the result against the [AGPL source requirements](https://www.gnu.org/licenses/agpl-3.0.html) and [GitHub's source archive behavior](https://docs.github.com/en/repositories/working-with-files/using-files/downloading-source-code-archives). |
| P2 | **Dependency license review remains necessary.** The collector now includes module-root notices and notices in compiled Go package and embedded-asset ancestor directories, along with npm and project asset notices. It does not inspect every file-level license or establish compatibility. | Review the generated notices, file-level terms, and native platform dependencies before the next public release and whenever dependencies change. |

## Verification

Headless checks passed: Go race tests for `internal/...` and the CLI; desktop
backend tests without the native window entry point; Go vet; frontend TypeScript,
Oxlint, Prettier, Vitest, and production build; and `git diff --check`.
Windows amd64 and macOS arm64 CLI builds were cross-compiled on Linux. The built
CSS retains the checkbox checked-state variant after dependency removal.

`npm audit` reports zero findings. Go vulnerability scans at package and module
level report no findings. The cached `govulncheck` 1.1.4 call-graph scan cannot
handle this Go 1.27 toolchain and panics in its older SSA builder; those results
therefore do not establish call-level vulnerability coverage.

A signature scan of the worktree and 205 historical Git blobs found no private
key headers, GitHub token formats, AWS access-key IDs, or Slack token formats.
That bounded scan does not detect every possible credential format.

At the October review, native desktop compilation and live
Godot/Unity/SteamCMD operations were not verified, and no desktop windows or
real publishing operations were started. Later native packaging verification
is recorded above; tagged publication remains untested.

Configurations and projects remain trusted inputs. Engine/editor scripts and
custom commands execute with the current user's privileges; rooted artifact
copying protects filesystem boundaries within that operation. See the
[architecture guide](architecture.md) and [security policy](../SECURITY.md).
