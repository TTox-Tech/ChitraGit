#!/usr/bin/env bash
# Builds the demo archive used by the VHS tapes in this folder: a dozen repos across a
# few owners, all with local file:// remotes, some with new upstream commits, one with
# local work and one whose local work clashes with the update.
#
#   docs/demo/make-archive.sh /tmp/chitragit-demo
set -euo pipefail

root="${1:?usage: $0 <folder>}"
rm -rf "$root"
mkdir -p "$root/remotes" "$root/upstream" "$root/archive"

repos="charmbracelet/bubbletea charmbracelet/lipgloss charmbracelet/vhs golang/go golang/tools
kubernetes/kubectl torvalds/linux ttox-tech/chitragit rust-lang/rustlings sindresorhus/awesome
public-apis/public-apis jgm/pandoc"

i=0
for full in $repos; do
  owner="${full%/*}" name="${full#*/}"
  bare="$root/remotes/$owner/$name.git" up="$root/upstream/$owner/$name"
  mkdir -p "$(dirname "$bare")" "$(dirname "$up")" "$root/archive/$owner"
  git init -q --bare -b main "$bare"
  git clone -q "$bare" "$up" 2>/dev/null
  (cd "$up" && printf '# %s\n\nDemo copy of %s.\n' "$name" "$full" > README.md &&
    echo "v1" > CHANGES && git add . && git -c user.name=Dev -c user.email=dev@example.com commit -qm "Initial import" &&
    git push -q origin main)
  git clone -q "$bare" "$root/archive/$owner/$name"
  i=$((i + 1))
done

# Local work: kept through the update.
echo "my notes on the event loop" > "$root/archive/charmbracelet/bubbletea/NOTES.md"
# Local work that clashes with the update: left exactly as it was.
echo "my local tweak" >> "$root/archive/golang/tools/CHANGES"

# New upstream commits for about half of the repos.
for full in charmbracelet/bubbletea charmbracelet/vhs golang/go golang/tools torvalds/linux \
  ttox-tech/chitragit rust-lang/rustlings; do
  up="$root/upstream/$full"
  for n in 1 2 3; do
    (cd "$up" && echo "change $n" >> CHANGES &&
      git -c user.name=Dev -c user.email=dev@example.com commit -qam "Update $n" && git push -q origin main)
  done
done

cd "$root/archive"
chitragit --headless init . >/dev/null
chitragit --headless scan >/dev/null
echo "$root/archive"
