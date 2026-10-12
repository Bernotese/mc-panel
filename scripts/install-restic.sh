#!/usr/bin/env bash
# Installs the pinned restic release to /usr/local/bin/restic (sub-project 4). Idempotent: an
# installed binary of the same version is kept. Used by scripts/vm-test.sh and the Go job of the
# CI (backup integration tests), and later by the instances job and scripts/testvm-deploy.sh.
# Needs root (run it with sudo).
#
#   sudo bash scripts/install-restic.sh
set -euo pipefail

RESTIC_VERSION="0.19.1"
# SHA-256 of restic_<version>_linux_<arch>.bz2 from the release's SHA256SUMS.
RESTIC_SHA256_AMD64="f415415624dcc452f2a02b8c33641791a8c6d6d3b65bbb3543fcf9a25151585c"
RESTIC_SHA256_ARM64="a5f64aaab53d51e311fa3829124c5b703f2d14cf187d8640b6be3b2b49376465"
BIN="/usr/local/bin/restic"

die() {
  echo "install-restic: $*" >&2
  exit 1
}

if [ -x "${BIN}" ] && "${BIN}" version 2> /dev/null | grep -q "^restic ${RESTIC_VERSION} "; then
  exit 0
fi
[ "$(id -u)" -eq 0 ] || die "must run as root (sudo bash $0)"

case "$(uname -m)" in
  x86_64) arch=amd64 sum="${RESTIC_SHA256_AMD64}" ;;
  aarch64 | arm64) arch=arm64 sum="${RESTIC_SHA256_ARM64}" ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac
command -v bunzip2 > /dev/null || apt-get install -y bzip2

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
name="restic_${RESTIC_VERSION}_linux_${arch}.bz2"
curl -fsSL -o "${tmp}/${name}" "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/${name}"
echo "${sum}  ${tmp}/${name}" | sha256sum -c -
bunzip2 "${tmp}/${name}"
install -m 0755 "${tmp}/${name%.bz2}" "${BIN}"
"${BIN}" version
