#!/usr/bin/env bash
# Installs the pinned golangci-lint release (the version CI and the test VM lint with) to
# <dir>/golangci-lint, checked against the SHA-256 pinned below. Idempotent: an installed binary of
# the same version is kept. Used by scripts/vm-test.sh (~/.local/bin) and the Go job of the CI.
# No root needed.
#
#   bash scripts/install-golangci-lint.sh <dir>
set -euo pipefail

GOLANGCI_VERSION="2.14.0"
# SHA-256 of golangci-lint-<version>-linux-<arch>.tar.gz from the release's checksums file.
GOLANGCI_SHA256_AMD64="ab90aeb7b066f92a33415b638a50fe5344bbb75a0d32ad30cc248d88f81032ab"
GOLANGCI_SHA256_ARM64="ee7ec5f3453d15ddf106fae5a4d6c71737712348a979d1fe9cd52ec7ea299bae"

die() {
  echo "install-golangci-lint: $*" >&2
  exit 1
}

[ "$#" -eq 1 ] || die "usage: $0 <dir>"
bin="$1/golangci-lint"
if [ -x "${bin}" ] && "${bin}" version 2> /dev/null | grep -q "version ${GOLANGCI_VERSION} "; then
  exit 0
fi

case "$(uname -m)" in
  x86_64) arch=amd64 sum="${GOLANGCI_SHA256_AMD64}" ;;
  aarch64 | arm64) arch=arm64 sum="${GOLANGCI_SHA256_ARM64}" ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac
echo "installing golangci-lint ${GOLANGCI_VERSION} to $1"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
name="golangci-lint-${GOLANGCI_VERSION}-linux-${arch}"
base="https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_VERSION}"
curl -fsSL -o "${tmp}/${name}.tar.gz" "${base}/${name}.tar.gz"
echo "${sum}  ${tmp}/${name}.tar.gz" | sha256sum -c -
tar -xzf "${tmp}/${name}.tar.gz" -C "${tmp}"
mkdir -p "$1"
install -m 0755 "${tmp}/${name}/golangci-lint" "${bin}"
