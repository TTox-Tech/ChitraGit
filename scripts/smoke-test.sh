#!/usr/bin/env bash
# End-to-end smoke test of a chitragit binary against real git, with local remotes only.
#
#   scripts/smoke-test.sh path/to/chitragit [expected-version]
#
# Builds a tiny archive in a temp folder and checks the promise that matters most
# (PRD G2, zero data loss): after a sync every repo is either updated or untouched,
# and uncommitted local work is still there. Runs on Linux, macOS and Windows (Git Bash).
set -euo pipefail

bin="${1:?usage: $0 <chitragit binary> [expected-version]}"
want_version="${2:-}"
case "$bin" in /* | [A-Za-z]:*) ;; *) bin="$PWD/$bin" ;; esac

fail() { printf 'smoke test FAILED: %s\n' "$*" >&2; exit 1; }
step() { printf '==> %s\n' "$*"; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
export GIT_CONFIG_NOSYSTEM=1 HOME="$work/home"
# Keep personal settings and caches inside the temp folder on every OS.
export CHITRAGIT_CONFIG_DIR="$work/config" CHITRAGIT_CACHE_DIR="$work/cache"
mkdir -p "$HOME"
git config --global user.name "Smoke Test"
git config --global user.email "smoke@example.com"
git config --global init.defaultBranch main
git config --global protocol.file.allow always

step "version"
out="$("$bin" version)"
echo "$out"
if [ -n "$want_version" ]; then
  case "$out" in *"${want_version#v}"*) ;; *) fail "version output does not mention $want_version" ;; esac
fi

step "set up remotes and clones"
remotes="$work/remotes" archive="$work/archive" upstream="$work/upstream"
mkdir -p "$remotes" "$archive/owner" "$upstream"
for name in clean dirty clash; do
  git init -q --bare "$remotes/$name.git"
  git clone -q "$remotes/$name.git" "$upstream/$name" 2>/dev/null
  (cd "$upstream/$name" && echo "one" > file.txt && echo "notes" > notes.txt &&
    git add . && git commit -qm "first" && git push -q origin main)
  git clone -q "$remotes/$name.git" "$archive/owner/$name"
done
# Local, uncommitted work in a file the upstream does not touch.
echo "my local note" >> "$archive/owner/dirty/notes.txt"
echo "untracked work" > "$archive/owner/dirty/mine.txt"
# Local, uncommitted work that clashes with the upstream change: must be left exactly as it was.
echo "local edit" >> "$archive/owner/clash/file.txt"
clash_head="$(git -C "$archive/owner/clash" rev-parse HEAD)"
clash_file="$(cat "$archive/owner/clash/file.txt")"
# New upstream commits for all three.
for name in clean dirty clash; do
  (cd "$upstream/$name" && echo "two" >> file.txt && git commit -qam "second" && git push -q origin main)
done
want_head="$(git -C "$upstream/clean" rev-parse HEAD)"
want_dirty_head="$(git -C "$upstream/dirty" rev-parse HEAD)"

step "init, scan, sync"
cd "$archive"
"$bin" --headless init . >/dev/null
"$bin" --headless scan
"$bin" --headless doctor || true # doctor may warn about the machine; it must not crash
# A clash may be reported as a partial run (exit 1); any other non-zero code is a failure.
code=0
"$bin" --headless --no-color sync || code=$?
[ "$code" = 0 ] || [ "$code" = 1 ] || fail "sync exited with $code"
"$bin" --headless --json status > "$work/status.json"

step "check results"
[ "$(git -C owner/clean rev-parse HEAD)" = "$want_head" ] || fail "clean repo was not updated"
[ "$(git -C owner/dirty rev-parse HEAD)" = "$want_dirty_head" ] || fail "dirty repo was not updated"
grep -q "my local note" owner/dirty/notes.txt || fail "uncommitted change in dirty repo was lost"
[ "$(cat owner/dirty/mine.txt)" = "untracked work" ] || fail "untracked file in dirty repo was lost"
[ "$(git -C owner/clash rev-parse HEAD)" = "$clash_head" ] || fail "clashing repo's branch was moved"
[ "$(cat owner/clash/file.txt)" = "$clash_file" ] || fail "clashing repo's local change was not kept exactly"
[ -z "$(git -C owner/clean status --porcelain)" ] || fail "clean repo has stray changes"
grep -q '"schema"' "$work/status.json" || fail "status --json did not print the JSON schema"

step "second sync keeps everything"
code=0
"$bin" --headless --no-color sync || code=$?
[ "$code" = 0 ] || [ "$code" = 1 ] || fail "second sync exited with $code"
[ "$(cat owner/clash/file.txt)" = "$clash_file" ] || fail "second sync changed the clashing repo"
grep -q "my local note" owner/dirty/notes.txt || fail "second sync lost the uncommitted change"

echo "smoke test passed"
