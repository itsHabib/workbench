#!/usr/bin/env bash
# Build Fleet and copy its example role assets. Harness setup belongs to `fleet role`.
set -euo pipefail
mode="${1:-dry}"
if [ "$#" -gt 1 ] || { [ "$mode" != dry ] && [ "$mode" != --apply ]; }; then
  echo 'usage: bash cmd/fleet/install.sh [--apply]' >&2
  exit 2
fi
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
install_home="${FLEET_HOME:-$HOME/.fleet}"
# Refuse to replace edited cards. Custom lanes can live separately via FLEET_LANES.
for source in "$here"/examples/lanes/*/*; do
  target="$install_home/lanes/${source#"$here/examples/lanes/"}"
  if [ -e "$target" ] && ! cmp -s "$source" "$target"; then
    echo "refusing to replace different asset: $target; preserve it separately before installing" >&2
    if [ "$mode" = --apply ]; then exit 1; fi
  fi
done
printf 'Build fleet in %s/bin; copy example lanes into %s/lanes\n' "$install_home" "$install_home"
if [ "$mode" = dry ]; then
  echo 'Dry run. Use --apply to install; then use fleet role to configure a checkout.'
  exit 0
fi
mkdir -p "$install_home/bin" "$install_home/lanes"
# Windows execs a binary by extension: an extensionless fleet runs from Git Bash but
# not from Console, and every projected hook command silently fails (#322).
exe=""
case "$(cd "$root" && go env GOOS)" in windows) exe=".exe" ;; esac
(cd "$root" && go build -o "$install_home/bin/fleet$exe" ./cmd/fleet)
for source in "$here"/examples/lanes/*/*; do
  target="$install_home/lanes/${source#"$here/examples/lanes/"}"
  mkdir -p "$(dirname "$target")"
  cp "$source" "$target"
done
printf 'Installed. Add %s/bin to PATH. See cmd/fleet/docs/install.md for role setup.\n' "$install_home"
