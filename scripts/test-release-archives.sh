#!/usr/bin/env bash
set -euo pipefail

root="$(mktemp -d)"
trap 'rm -rf -- "$root"' EXIT
dist="$root/release files"
helper="$(dirname "$0")/verify-release-archives.sh"

for platform in linux-amd64 windows-amd64 macos-arm64; do
  mkdir -p "$dist/$platform"
  archive="$dist/$platform/steam-releaser-$platform.tar.gz"
  printf '%s\n' "$platform" > "$archive"
  (cd "$dist/$platform" && sha256sum -- "${archive##*/}" > "${archive##*/}.sha256")
done

# Extra files do not influence the exact archives selected for release.
printf 'unexpected\n' > "$dist/unexpected.tar.gz"
bash "$helper" "$dist"

archive="$dist/linux-amd64/steam-releaser-linux-amd64.tar.gz"
printf 'modified\n' >> "$archive"
if bash "$helper" "$dist" >/dev/null 2>&1; then
  echo 'Accepted a modified archive' >&2
  exit 1
fi
printf 'linux-amd64\n' > "$archive"

checksum="$dist/windows-amd64/steam-releaser-windows-amd64.tar.gz.sha256"
mv "$checksum" "$checksum.saved"
if bash "$helper" "$dist" >/dev/null 2>&1; then
  echo 'Accepted a missing checksum' >&2
  exit 1
fi
ln -s "$checksum.saved" "$checksum"
if bash "$helper" "$dist" >/dev/null 2>&1; then
  echo 'Accepted a linked checksum' >&2
  exit 1
fi
rm "$checksum"
mv "$checksum.saved" "$checksum"
printf '%064d  other-file.tar.gz\n' 0 > "$checksum"
if bash "$helper" "$dist" >/dev/null 2>&1; then
  echo 'Accepted a checksum for a different filename' >&2
  exit 1
fi
echo 'Release archive verification tests passed'
