#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (c) 2023-2026 Valentino Međimorec
# Compatibility entry point: all upgrade logic lives in the Go CLI.
set -euo pipefail

# Release packaging replaces this value with the archive's exact version.
DEFAULT_RELEASE=latest

if [[ -n "${DRUPAL_UPDATE_BINARY:-}" ]]; then
  exec "$DRUPAL_UPDATE_BINARY" "$@"
fi

script_dir=''
# bash -s / curl | bash has no source filename. Never resolve it as this project.
if [[ -n "${BASH_SOURCE[0]:-}" && -f "${BASH_SOURCE[0]}" ]]; then
  script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
fi
if [[ -n "$script_dir" ]]; then
  for binary in "$script_dir/drupal-update" "$script_dir/drupal-update.exe"; do
    if [[ -x "$binary" ]]; then
      exec "$binary" "$@"
    fi
  done
fi
if command -v drupal-update >/dev/null 2>&1; then
  exec drupal-update "$@"
fi

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/drupal-update-wrapper.XXXXXXXX")
trap 'rm -rf -- "$work_dir"' EXIT

# A source checkout can build the current code without waiting for a release.
# Build in the script directory, then run in the caller's original project.
if [[ -n "$script_dir" && -f "$script_dir/go.mod" && -f "$script_dir/main.go" ]] && command -v go >/dev/null 2>&1; then
  (cd -- "$script_dir" && go build -buildvcs=false -o "$work_dir/drupal-update" .)
  "$work_dir/drupal-update" "$@"
  exit $?
fi

release=${DRUPAL_UPDATE_VERSION:-$DEFAULT_RELEASE}
if [[ "$release" != latest && ! "$release" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9][A-Za-z0-9.-]*)?$ ]]; then
  echo "Invalid DRUPAL_UPDATE_VERSION; use latest or a full release tag." >&2
  exit 1
fi
case "$(uname -s)" in
  Linux) target_os=linux; suffix=tar.gz; executable=drupal-update ;;
  Darwin) target_os=darwin; suffix=tar.gz; executable=drupal-update ;;
  MINGW*|MSYS*|CYGWIN*) target_os=windows; suffix=zip; executable=drupal-update.exe ;;
  *) echo "Unsupported platform; set DRUPAL_UPDATE_BINARY to an installed Go CLI." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) target_arch=amd64 ;;
  aarch64|arm64) target_arch=arm64 ;;
  *) echo "Unsupported architecture; set DRUPAL_UPDATE_BINARY to an installed Go CLI." >&2; exit 1 ;;
esac
for required in curl awk; do
  command -v "$required" >/dev/null 2>&1 || { echo "$required is required to download a binary." >&2; exit 1; }
done
if [[ "$suffix" == zip ]]; then
  command -v unzip >/dev/null 2>&1 || { echo "unzip is required." >&2; exit 1; }
else
  command -v tar >/dev/null 2>&1 || { echo "tar is required." >&2; exit 1; }
fi
if command -v sha256sum >/dev/null 2>&1; then
  checksum=(sha256sum --check)
elif command -v shasum >/dev/null 2>&1; then
  checksum=(shasum -a 256 --check)
else
  echo "sha256sum or shasum is required to verify the downloaded binary." >&2
  exit 1
fi

repository_url=https://github.com/valicm/drupal-update
if [[ "$release" == latest ]]; then
  # Follow GitHub's stable release redirect to obtain the full archive tag.
  resolved_url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "$repository_url/releases/latest")
  release=${resolved_url##*/}
  if [[ ! "$release" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Latest stable release has no Go binaries yet. Set DRUPAL_UPDATE_VERSION to a Go release such as v5.0.0-rc.1." >&2
    exit 1
  fi
fi
archive="drupal-update_${release}_${target_os}_${target_arch}.${suffix}"
release_url="$repository_url/releases/download/$release"
echo "Downloading Drupal Update $release ($target_os/$target_arch)..." >&2
curl -fsSL "$release_url/$archive" -o "$work_dir/$archive"
curl -fsSL "$release_url/SHA256SUMS" -o "$work_dir/SHA256SUMS"
(
  cd -- "$work_dir"
  awk -v name="$archive" '$2 == name' SHA256SUMS > selected-checksum
  if [[ ! -s selected-checksum ]]; then
    echo "Release checksum missing for $archive." >&2
    exit 1
  fi
  "${checksum[@]}" selected-checksum >&2
  if [[ "$suffix" == zip ]]; then
    unzip -q "$archive"
  else
    tar -xzf "$archive"
  fi
)
# Do not exec here: the EXIT trap must clean the temporary archive and binary.
"$work_dir/$executable" "$@"
