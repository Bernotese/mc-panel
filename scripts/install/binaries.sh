# shellcheck shell=bash
# binaries.sh - installer spec §4.4: the release tarball and checksums.txt of the same release,
# the signature of checksums.txt (release_verify, SEC-07), SHA-256 check, extraction into
# $WORK, atomic install of panel, agent and files into BIN_DIR and of the tarball's LICENSE
# into DOC_DIR. Nothing is written to the system before the download is verified. With
# --from-dir the local build is used as is (no network). Sets BIN_SOURCE_DIR (render_template
# then takes deploy/ from there) and BIN_CHANGED (units.sh restarts the services when a binary
# changed); RELEASE_RECORD says how the installed release was verified (doctor).

BIN_NAMES=(panel agent files)
BIN_CHANGED=0

# bin_version prints the target version without a leading "v".
bin_version() {
  local v="${TARGET_VERSION:-$MCPANEL_VERSION}"
  echo "${v#v}"
}

# bin_url NAME prints the download URL of release asset NAME of the target version.
bin_url() {
  printf '%s/download/v%s/%s\n' "$RELEASE_BASE" "$(bin_version)" "$1"
}

# bin_tar_safe TARBALL: 0 if every member is a regular file or a directory with a relative path
# without "..": no symlinks, hard links or devices that could point extraction elsewhere.
bin_tar_safe() {
  local name types
  types="$(tar -tvzf "$1" 2>/dev/null | cut -c1 | LC_ALL=C sort -u | tr -d '\n')" || return 1
  [[ -n $types && $types =~ ^[-d]+$ ]] || return 1
  while IFS= read -r name; do
    case "/$name/" in
    //* | */../*) return 1 ;;
    esac
  done < <(tar -tzf "$1")
}

# bin_source prints the directory that holds panel, agent, files and deploy/: FROM_DIR, or the
# verified and extracted release tarball. Runs in $(...): every failure dies explicitly. The
# verification result goes to $WORK/release-verified for bin_install: "signed FINGERPRINT",
# "unsigned" or "from-dir" (update verifies the download first and passes it on as FROM_DIR,
# which keeps the result of that download).
bin_source() {
  if [[ -n $FROM_DIR ]]; then
    if [[ $FROM_DIR != "$WORK/release" || ! -f $WORK/release-verified ]]; then
      echo from-dir >"$WORK/release-verified"
    fi
    echo "$FROM_DIR"
    return 0
  fi
  local v tarball dir verified
  v="$(bin_version)"
  [[ $v != dev ]] || die "this install.sh has no release version; use --version or --from-dir"
  tarball="mc-panel_${v}_linux_${ARCH}.tar.gz"
  fetch "$(bin_url checksums.txt)" "$WORK/checksums.txt" || die "download of checksums.txt failed"
  verified="$(release_verify "$RELEASE_BASE/download/v$v" "$WORK/checksums.txt" "$v")" || exit 1
  fetch "$(bin_url "$tarball")" "$WORK/$tarball" || die "download of $tarball failed"
  sha256_verify "$WORK/$tarball" "$WORK/checksums.txt" "$tarball" || die "checksum of $tarball does not match"
  bin_tar_safe "$WORK/$tarball" || die "$tarball contains unsafe entries (absolute paths, .., links)"
  dir="$WORK/release"
  rm -rf -- "$dir"
  mkdir -m 0700 -- "$dir" || die "cannot create $dir"
  tar -xzf "$WORK/$tarball" -C "$dir" --no-same-owner --no-same-permissions || die "cannot extract $tarball"
  printf '%s\n' "$verified" >"$WORK/release-verified"
  echo "$dir"
}

# bin_record writes RELEASE_RECORD (version and how it was verified, from bin_source).
bin_record() {
  local how="" r
  if [[ -f $WORK/release-verified ]]; then
    how="$(head -n 1 -- "$WORK/release-verified")"
  fi
  r="$(printf 'version=%s\nverified=%s\nkey=%s\n' "$(bin_version)" "${how%% *}" \
    "$([[ $how == "signed "* ]] && echo "${how#signed }")" |
    write_file "$RELEASE_RECORD" 0644 root root)" || return 1
}

# bin_license SRC: installs SRC/LICENSE (the AGPL text at the tarball root) as
# DOC_DIR/LICENSE. Releases before the license and local builds without one have none: then
# nothing is written and an installed copy stays (it is the same license).
bin_license() {
  local r
  [[ -f $1/LICENSE && ! -L $1/LICENSE ]] || return 0
  r="$(ensure_dir "$DOC_DIR" 0755 root root)" || return 1
  r="$(write_file "$DOC_DIR/LICENSE" 0644 root root <"$1/LICENSE")" || return 1
  [[ $r == unchanged ]] || log_changed "license text in $DOC_DIR"
}

# shellcheck disable=SC2034 # BIN_SOURCE_DIR (render_template, update) and BIN_CHANGED (units.sh)
bin_install() {
  log_step "binaries"
  local src b r changed=()
  src="$(bin_source)" || return 1
  for b in "${BIN_NAMES[@]}"; do
    if [[ ! -f $src/$b || -L $src/$b || ! -x $src/$b ]]; then
      warn "$src/$b is missing or not an executable file"
      return 1
    fi
  done
  if [[ ! -d $src/deploy || -L $src/deploy ]]; then
    warn "$src/deploy is missing"
    return 1
  fi
  if [[ ! -d $ROOT$BIN_DIR ]]; then
    r="$(ensure_dir "$BIN_DIR" 0755 root root)" || return 1
  fi
  for b in "${BIN_NAMES[@]}"; do
    # write_file: temp file in BIN_DIR, fsync, rename; unchanged binaries are left alone.
    r="$(write_file "$BIN_DIR/$b" 0755 root root <"$src/$b")" || return 1
    [[ $r == unchanged ]] || changed+=("$b")
  done
  bin_license "$src" || return 1
  BIN_SOURCE_DIR="$src"
  bin_record || return 1
  if ((${#changed[@]} > 0)); then
    BIN_CHANGED=1
    log_changed "${changed[*]} $(bin_version) in $BIN_DIR"
  else
    log_skip "panel, agent, files $(bin_version) unchanged"
  fi
}
