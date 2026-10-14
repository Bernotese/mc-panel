# shellcheck shell=bash
# binaries.sh - installer spec §4.4: the release tarball and checksums.txt of the same release,
# SHA-256 check, extraction into $WORK, atomic install of panel, agent and files into BIN_DIR.
# Nothing is written to the system before the download is verified. With --from-dir the local
# build is used as is (no network). Sets BIN_SOURCE_DIR (render_template then takes deploy/
# from there) and BIN_CHANGED (units.sh restarts the services when a binary changed).

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
# verified and extracted release tarball. Runs in $(...): every failure dies explicitly.
bin_source() {
  if [[ -n $FROM_DIR ]]; then
    echo "$FROM_DIR"
    return 0
  fi
  local v tarball dir
  v="$(bin_version)"
  [[ $v != dev ]] || die "this install.sh has no release version; use --version or --from-dir"
  tarball="mc-panel_${v}_linux_${ARCH}.tar.gz"
  fetch "$(bin_url "$tarball")" "$WORK/$tarball" || die "download of $tarball failed"
  fetch "$(bin_url checksums.txt)" "$WORK/checksums.txt" || die "download of checksums.txt failed"
  sha256_verify "$WORK/$tarball" "$WORK/checksums.txt" "$tarball" || die "checksum of $tarball does not match"
  bin_tar_safe "$WORK/$tarball" || die "$tarball contains unsafe entries (absolute paths, .., links)"
  dir="$WORK/release"
  rm -rf -- "$dir"
  mkdir -m 0700 -- "$dir" || die "cannot create $dir"
  tar -xzf "$WORK/$tarball" -C "$dir" --no-same-owner --no-same-permissions || die "cannot extract $tarball"
  echo "$dir"
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
  BIN_SOURCE_DIR="$src"
  if ((${#changed[@]} > 0)); then
    BIN_CHANGED=1
    log_changed "${changed[*]} $(bin_version) in $BIN_DIR"
  else
    log_skip "panel, agent, files $(bin_version) unchanged"
  fi
}
