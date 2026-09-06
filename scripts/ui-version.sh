#!/bin/sh
# The window's version, read from the module that declares it.
#
# One place, because the release needs it twice — once for the Windows build
# GoReleaser makes and once for the macOS app packaged after it — and two copies
# of "where the version comes from" is how the same window ends up shipping under
# two different numbers.
set -eu

mod="${1:-ui/go.mod}"
v=$(sed -n 's|^module .*rigsmith:version \(.*\)$|\1|p' "$mod" | tr -d ' ')
if [ -z "$v" ]; then
  echo "$mod has no // rigsmith:version comment, so nothing decides what the window reports" >&2
  exit 1
fi
printf '%s\n' "$v"
