#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo 'Usage: bash scripts/verify-release-archives.sh DIRECTORY' >&2
  exit 1
fi

for platform in linux-amd64 windows-amd64 macos-arm64; do
  archive="$1/$platform/steam-releaser-$platform.tar.gz"
  checksum="$archive.sha256"
  if [[ ! -f "$archive" || -L "$archive" || ! -f "$checksum" || -L "$checksum" ]]; then
    echo "Missing or unsafe release archive or checksum for $platform" >&2
    exit 1
  fi
  line="$(cat -- "$checksum")"
  expected="${line%% *}"
  if [[ ! "$expected" =~ ^[0-9a-f]{64}$ || "$line" != "$expected  ${archive##*/}" ]]; then
    echo "Invalid release checksum for $platform" >&2
    exit 1
  fi
  actual="$(sha256sum -- "$archive")"
  if [[ "${actual%% *}" != "$expected" ]]; then
    echo "Release archive checksum mismatch for $platform" >&2
    exit 1
  fi
  echo "Verified release archive for $platform"
done
