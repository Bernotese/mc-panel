#!/usr/bin/env bash
# Regenerates internal/password/common.txt from SecLists (entries >= 12 chars, lowercased, unique).
set -euo pipefail
cd "$(dirname "$0")/.."
url="https://raw.githubusercontent.com/danielmiessler/SecLists/master/Passwords/Common-Credentials/xato-net-10-million-passwords-100000.txt"
tmp="$(mktemp -d)"
curl -fsSL "$url" -o "$tmp/list.txt"
awk 'length($0) >= 12 { print tolower($0) }' "$tmp/list.txt" | LC_ALL=C sort -u > internal/password/common.txt
rm -rf "$tmp"
wc -l internal/password/common.txt
