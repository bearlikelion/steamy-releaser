#!/usr/bin/env bash
# Run the development and supply-chain checks on this machine, without Actions.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

for command in go node python3; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'Missing %s; see docs/development.md for local prerequisites.\n' "$command" >&2
    exit 1
  fi
done

# Do not let Go silently download a different toolchain while running checks.
export GOTOOLCHAIN=local GOFLAGS=-mod=readonly
# Tests must execute on the current machine, even if the caller cross-compiles.
unset GOOS GOARCH
expected_go=$(awk '$1 == "go" { print $2; exit }' go.mod)
actual_go=$(go env GOVERSION)
if [[ "$actual_go" != "go$expected_go" ]]; then
  printf 'Go %s is required; PATH currently selects %s.\n' "$expected_go" "$actual_go" >&2
  exit 1
fi
expected_node=$(<.nvmrc)
if [[ "$(node --version)" != "v$expected_node" ]]; then
  printf 'Node %s is required; PATH currently selects %s.\n' "$expected_node" "$(node --version)" >&2
  exit 1
fi

platform=$(go env GOOS)
case "$platform" in
  linux)
    # Homebrew pkg-config on Linux may omit the system GTK/WebKit paths.
    if [[ -z "${PKG_CONFIG:-}" && -x /usr/bin/pkg-config ]] && \
      /usr/bin/pkg-config --exists gtk+-3.0 webkit2gtk-4.1; then
      export PKG_CONFIG=/usr/bin/pkg-config
    fi
    if ! "${PKG_CONFIG:-pkg-config}" --exists gtk+-3.0 webkit2gtk-4.1; then
      printf 'Linux desktop checks need GTK 3 and WebKitGTK 4.1 development libraries.\n' >&2
      exit 1
    fi
    if ! command -v dbus-run-session >/dev/null 2>&1; then
      printf 'Linux tray tests need dbus-run-session.\n' >&2
      exit 1
    fi
    ;;
  darwin|windows) ;;
  *) printf 'Unsupported local test platform: %s\n' "$platform" >&2; exit 1 ;;
esac

work=$(mktemp -d "${TMPDIR:-/tmp}/steam-releaser-check.XXXXXXXX")
placeholder=0
created_dist=0
dist="$root/cmd/steam-releaser-desktop/frontend/dist"
cleanup() {
  if [[ "$placeholder" == 1 ]]; then rm -f "$dist/index.html"; fi
  if [[ "$created_dist" == 1 ]]; then rmdir "$dist" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

# The release npm bootstrap accepts Actions environment files. Use private local
# files here; it prepares verified npm in this temporary directory only.
touch "$work/github-env" "$work/github-path"
export RUNNER_TEMP="$work" GITHUB_ENV="$work/github-env" GITHUB_PATH="$work/github-path"
if [[ "$platform" == windows ]]; then
  if ! command -v cygpath >/dev/null 2>&1; then
    printf 'Native Windows checks require Git Bash and cygpath.\n' >&2
    exit 1
  fi
  RUNNER_TEMP=$(cygpath -m "$work")
  GITHUB_ENV=$(cygpath -m "$work/github-env")
  GITHUB_PATH=$(cygpath -m "$work/github-path")
  export RUNNER_TEMP GITHUB_ENV GITHUB_PATH
fi
# Native Go on Windows needs a Windows-formatted absolute cache path.
export GOCACHE="$RUNNER_TEMP/go-build-cache"

printf '\n== Bootstrap and script tests ==\n'
python3 scripts/test-prepare-runtimes.py
node scripts/prepare-npm.mjs
STEAM_RELEASER_NPM_DIR=$(awk -F= '$1 == "STEAM_RELEASER_NPM_DIR" { sub(/^[^=]*=/, ""); print; exit }' "$work/github-env")
npm_shims=$(head -n 1 "$work/github-path")
if [[ -z "$STEAM_RELEASER_NPM_DIR" || -z "$npm_shims" ]]; then
  printf 'Verified npm bootstrap did not produce its environment.\n' >&2
  exit 1
fi
if [[ "$platform" == windows ]]; then npm_shims=$(cygpath -u "$npm_shims"); fi
export STEAM_RELEASER_NPM_DIR PATH="$npm_shims:$PATH"
node scripts/check-active-npm.mjs
script_tests=()
for test_file in scripts/*.test.mjs; do
  if [[ "$platform" != linux ]]; then
    case "$test_file" in
      scripts/check-runtimes.test.mjs|scripts/verify-embedded-frontend.test.mjs|scripts/verify-npm-registry.test.mjs|scripts/verify-npm-bundles.test.mjs|scripts/verify-npm-locked-provenance.test.mjs) continue ;;
    esac
  fi
  script_tests+=("$test_file")
done
node --test "${script_tests[@]}"
if [[ "$platform" == linux ]]; then bash scripts/test-release-archives.sh; fi

printf '\n== Dependency and toolchain checks ==\n'
node scripts/verify-npm-lock.mjs
node scripts/verify-npm-registry.mjs
node scripts/verify-npm-locked-provenance.mjs
node scripts/audit-npm-toolchain.mjs
node scripts/check-runtimes.mjs
go mod download
go mod verify

printf '\n== Go tests and CLI builds ==\n'
go test -race -coverprofile="$work/coverage.out" ./internal/... ./cmd/steam-releaser
go tool cover -func="$work/coverage.out" | tail -n 1
go test -race ./cmd/steam-releaser-desktop/app.go ./cmd/steam-releaser-desktop/app_test.go ./cmd/steam-releaser-desktop/window_test.go ./cmd/steam-releaser-desktop/tray.go ./cmd/steam-releaser-desktop/tray_test.go ./cmd/steam-releaser-desktop/menu.go ./cmd/steam-releaser-desktop/menu_test.go
if [[ "$platform" == linux ]]; then
  dbus-run-session -- env STEAM_RELEASER_TEST_TRAY=1 go test -race ./cmd/steam-releaser-desktop/tray.go ./cmd/steam-releaser-desktop/tray_linux_test.go ./cmd/steam-releaser-desktop/tray_legacy_linux.go ./cmd/steam-releaser-desktop/tray_legacy_linux_test.go
fi
if [[ "$platform" == windows ]]; then
  set +e
  output=$(go test ./cmd/steam-releaser-desktop/webview2_strategy_windows.go 2>&1)
  status=$?
  set -e
  if [[ "$status" -eq 0 || "$output" != *"undefined: buildWindowsDesktopWithWebView2BrowserStrategy"* ]]; then
    printf 'Unexpected Windows strategy guard result:\n%s\n' "$output" >&2
    exit 1
  fi
fi
go vet ./internal/... ./cmd/steam-releaser
go tool govulncheck ./internal/... ./cmd/steam-releaser
go tool govulncheck tool
cli="$work/steam-releaser"
if [[ "$platform" == windows ]]; then cli="$cli.exe"; fi
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$cli" ./cmd/steam-releaser
"$cli" --help >/dev/null
for target in linux/amd64 windows/amd64 darwin/arm64; do
  GOOS=${target%/*} GOARCH=${target#*/} CGO_ENABLED=0 go build -o "$work/cli-${target%/*}-${target#*/}" ./cmd/steam-releaser
done

printf '\n== Frontend dependency verification ==\n'
frontend="$root/cmd/steam-releaser-desktop/frontend"
(cd "$frontend" && npm ci --ignore-scripts --cache "$work/npm-cache")
node scripts/verify-installed-npm.mjs --cache "$work/npm-cache"
(cd "$frontend" && npm audit --audit-level=low)
(cd "$frontend" && npm audit signatures --json --include-attestations > "$work/npm-attestations.json")
node scripts/verify-npm-provenance.mjs "$work/npm-attestations.json"
npm --prefix "$frontend" sbom --sbom-format=cyclonedx --include=dev --include=optional --include=peer > "$work/FRONTEND_SBOM.cdx.json"
node scripts/verify-frontend-sbom.mjs --installed "$work/FRONTEND_SBOM.cdx.json"

printf '\n== Desktop checks ==\n'
# Go embed needs an index file; never replace an existing local build.
if [[ ! -d "$dist" ]]; then mkdir -p "$dist"; created_dist=1; fi
if [[ ! -e "$dist/index.html" && ! -L "$dist/index.html" ]]; then
  printf '%s\n' '<!doctype html>' > "$dist/index.html"
  placeholder=1
fi
wails_args=(build -m -nosyncgomod -dryrun -s -skipbindings -skipembedcreate)
case "$platform" in
  linux) desktop_tags=production,desktop,webkit2_41; wails_args+=(-tags webkit2_41) ;;
  windows) desktop_tags=production,desktop,wv2runtime.browser; wails_args+=(-webview2 browser) ;;
  darwin) desktop_tags=production,desktop ;;
esac
if [[ "$platform" == windows ]]; then
  go build -tags=bindings -o "$work/wails-bindings-check.exe" ./cmd/steam-releaser-desktop
fi
go tool govulncheck -tags="$desktop_tags" ./cmd/steam-releaser-desktop
(cd cmd/steam-releaser-desktop && go tool wails "${wails_args[@]}")
node scripts/collect-licenses.mjs "$work/THIRD_PARTY_NOTICES.txt"
if [[ "$placeholder" == 1 ]]; then rm -f "$dist/index.html"; placeholder=0; fi
if [[ "$created_dist" == 1 ]]; then rmdir "$dist" 2>/dev/null || true; created_dist=0; fi

printf '\n== Frontend tests and build ==\n'
(cd "$frontend" && npm run check && npm test && npm run build)
node scripts/verify-frontend-dist.mjs "$dist"
printf '\nLocal checks passed.\n'
