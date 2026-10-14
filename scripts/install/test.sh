#!/usr/bin/env bash
# shellcheck shell=bash
# test.sh [FILTER] - runs the installer's Bash tests (scripts/install/tests/*.sh) without a
# framework. Every test_* function runs in its own subshell with set -e, a fresh ROOT and WORK
# and dry-run mode, so nothing touches the system. TAP-like output; exit 1 if a test failed.
set -euo pipefail

umask 022 # lib.sh refuses to write below group-writable directories
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export MCPANEL_INSTALL_NO_MAIN=1 MCPANEL_INSTALL_DRYRUN=1
# shellcheck source=main.sh
. "$here/main.sh"
# A second definition of a test_* name would silently replace the first one.
_dups="$(grep -hoE '^[[:space:]]*test_[A-Za-z0-9_]+[[:space:]]*\(\)' "$here"/tests/*.sh | tr -d ' \t()' | LC_ALL=C sort | uniq -d)"
if [[ -n $_dups ]]; then
  echo "# duplicate test names: $(paste -sd' ' - <<<"$_dups")" >&2
  exit 1
fi
for _f in "$here"/tests/*.sh; do
  # shellcheck disable=SC1090 # test files are found at run time
  . "$_f"
done

# --- assertions (exit the test's subshell on failure) ----------------------------------------
fail() {
  printf '%s\n' "$@" >&2
  exit 1
}
assert_eq() {
  [[ $1 == "$2" ]] || fail "expected: $1" "actual:   $2" "${3:-}"
}
assert_contains() {
  [[ $1 == *"$2"* ]] || fail "missing: $2" "in: $1"
}
assert_not_contains() {
  [[ $1 != *"$2"* ]] || fail "unexpected: $2" "in: $1"
}
assert_file_contains() {
  [[ -f $1 ]] || fail "missing file: $1"
  grep -qF -- "$2" "$1" || fail "missing in $1: $2" "$(cat -- "$1")"
}
# assert_file_eq FILE EXP: the content of FILE (without its last newline) is EXP.
assert_file_eq() {
  [[ -f $1 ]] || fail "missing file: $1"
  assert_eq "$2" "$(cat -- "$1")" "content of $1"
}
# _run_errexit CMD...: runs CMD in a subshell with errexit on (output in $WORK/out) and leaves
# its exit code in _rc. The subshell must not run in a conditional context: `(cmd) || s=$?`
# would switch errexit off inside it, even for an explicit set -e (main.sh relies on it).
_run_errexit() {
  local e=0
  if [[ $- == *e* ]]; then e=1; fi
  set +e
  (
    set -e
    "$@"
  ) >"$WORK/out" 2>&1
  _rc=$?
  if [[ $e == 1 ]]; then set -e; fi
  return 0
}
# assert_exit CODE CMD...: runs CMD in a subshell (output in $WORK/out) and expects exit CODE.
assert_exit() {
  local want="$1"
  shift
  _run_errexit "$@"
  [[ $_rc == "$want" ]] || fail "exit $_rc, want $want: $*" "$(cat -- "$WORK/out")"
}
# stub NAME BODY: defines function NAME { BODY } for the current test (functions win over
# commands, so this replaces queries like id, ss, getent, sshd -T or curl --unix-socket).
# Sourcing a generated definition keeps install.sh itself free of eval.
stub() {
  # shellcheck disable=SC1090 # the definition is generated here
  . <(printf '%s() {\n%s\n}\n' "$1" "$2")
}
assert_called() {
  grep -qF -- "$1" "$WORK/calls.log" 2>/dev/null || fail "not called: $1" "$(cat -- "$WORK/calls.log" 2>/dev/null)"
}
assert_not_called() {
  if grep -qF -- "$1" "$WORK/calls.log" 2>/dev/null; then fail "unexpectedly called: $1"; fi
}
# assert_fails CMD...: CMD exits with any code other than 0 (output in $WORK/out).
assert_fails() {
  _run_errexit "$@"
  [[ $_rc -ne 0 ]] || fail "expected failure: $*" "$(cat -- "$WORK/out")"
}
assert_file() {
  [[ -e $1 || -L $1 ]] || fail "missing: $1"
}
assert_no_file() {
  [[ ! -e $1 && ! -L $1 ]] || fail "unexpected: $1"
}
# fake_exec: replaces the exec builtin for the current test; the call is recorded in
# calls.log as "exec <args>" and the caller returns 0 instead of being replaced.
fake_exec() {
  # shellcheck disable=SC2016 # the body is code for the stub, expanded when it runs
  stub exec 'printf "exec %s\n" "$*" >>"$WORK/calls.log"'
}

# --- runner ----------------------------------------------------------------------------------
# test.sh [FILE|PATTERN]: an argument with a "/" or ending in ".sh" selects the tests defined
# in that file (path relative to scripts/install or absolute), anything else filters by name.
filter="${1:-}"
only=""
if [[ $filter == */* || $filter == *.sh ]]; then
  [[ $filter == /* ]] || filter="$here/$filter"
  [[ -f $filter ]] || { echo "# no such test file: $filter" >&2; exit 1; }
  only=" $(grep -oE '^test_[A-Za-z0-9_]+' "$filter" | tr '\n' ' ')"
  filter=""
fi
n=0
failed=0
for t in $(declare -F | awk '{ print $3 }' | grep '^test_' | LC_ALL=C sort); do
  if [[ -n $only && $only != *" $t "* ]]; then
    continue
  fi
  if [[ -n $filter && $t != *"$filter"* ]]; then
    continue
  fi
  n=$((n + 1))
  out="$(mktemp)"
  set +e
  (
    set -e
    ROOT="$(mktemp -d)"
    WORK="$ROOT/.work"
    mkdir -p "$WORK"
    : >"$WORK/calls.log"
    DRYRUN=1
    TTY="$WORK/no-tty" # never the real terminal; tests that need answers write this file
    trap 'rm -rf -- "$ROOT"' EXIT
    "$t"
  ) >"$out" 2>&1
  s=$?
  set -e
  if [[ $s -eq 0 ]]; then
    echo "ok $n - $t"
  else
    echo "not ok $n - $t"
    sed 's/^/#   /' "$out"
    failed=$((failed + 1))
  fi
  rm -f -- "$out"
done
echo "1..$n"
if [[ $n -eq 0 ]]; then
  echo "# no tests matched '${filter}'" >&2
  exit 1
fi
if [[ $failed -ne 0 ]]; then
  echo "# $failed of $n tests failed" >&2
  exit 1
fi
