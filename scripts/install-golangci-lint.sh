#!/usr/bin/env bash
# Installs the pinned golangci-lint release (archive checked against the release's
# checksums.txt) to <bindir>/golangci-lint. Idempotent: an installed binary of the same version is
# kept. Used by scripts/vm-test.sh (~/.local/bin) and the CI and release workflows. Needs no root.
#
#   bash scripts/install-golangci-lint.sh <bindir>
set -euo pipefail

GOLANGCI_VERSION="2.14.0"

die() {
  echo "install-golangci-lint: $*" >&2
  exit 1
}

[ "$#" -eq 1 ] || die "usage: $0 <bindir>"
bin="$1/golangci-lint"
if [ -x "${bin}" ] && "${bin}" version 2> /dev/null | grep -q "version ${GOLANGCI_VERSION} "; then
  exit 0
fi

case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64) arch=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac
echo "installing golangci-lint ${GOLANGCI_VERSION} to $1"
tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
name="golangci-lint-${GOLANGCI_VERSION}-linux-${arch}"
base="https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_VERSION}"
curl -fsSL -o "${tmp}/${name}.tar.gz" "${base}/${name}.tar.gz"
curl -fsSL -o "${tmp}/checksums.txt" "${base}/golangci-lint-${GOLANGCI_VERSION}-checksums.txt"
(cd "${tmp}" && grep " ${name}.tar.gz\$" checksums.txt | sha256sum -c -)
tar -xzf "${tmp}/${name}.tar.gz" -C "${tmp}"
mkdir -p "$1"
install -m 0755 "${tmp}/${name}/golangci-lint" "${bin}"
