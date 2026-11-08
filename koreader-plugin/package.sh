#!/usr/bin/env bash
# Builds a reproducible liseur.koplugin ZIP: fixed file order, fixed
# timestamps, no extra attributes, and nothing but the plugin and its
# licence. Credentials and state live in KOReader's settings directory,
# never in the package.
#
#   koreader-plugin/package.sh [output.zip]
#
# VERSION stamps a version into the packaged _meta.lua. The plugin does not
# carry one in the repository: an installed copy works without it, and only
# the KOReader plugin stores compare it against a release tag to offer an
# update.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
OUT=$(realpath -m "${1:-$HERE/liseur.koplugin.zip}")
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT

mkdir -p "$(dirname "$OUT")"

mkdir "$STAGE/liseur.koplugin"
cp "$HERE"/liseur.koplugin/*.lua "$STAGE/liseur.koplugin/"
cp "$HERE/../LICENSE" "$STAGE/liseur.koplugin/LICENSE"

if [ -n "${VERSION:-}" ]; then
  awk -v v="$VERSION" '
    /^return \{$/ { print; print "    version = \"" v "\","; next }
    { print }
  ' "$HERE/liseur.koplugin/_meta.lua" > "$STAGE/liseur.koplugin/_meta.lua"
fi
find "$STAGE" -exec touch -h -d "1980-01-01 00:00:00 UTC" {} +
find "$STAGE" -type f -exec chmod 0644 {} +
find "$STAGE" -type d -exec chmod 0755 {} +

rm -f "$OUT"
(cd "$STAGE" && find liseur.koplugin -print | LC_ALL=C sort | TZ=UTC zip -X -q -D "$OUT" -@)
echo "$OUT"
