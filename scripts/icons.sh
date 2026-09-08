#!/usr/bin/env bash
# Regenerate committed icon exports from the two editable SVG sources.
# Requires rsvg-convert (librsvg) and ImageMagick; not part of the build.
set -euo pipefail
cd "$(dirname "$0")/.."
assets=internal/webui/static
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

for size in 16 32 48 128; do
  rsvg-convert -w "$size" -h "$size" "$assets/favicon.svg" -o "$work/$size.png"
done
cp "$work/128.png" "$assets/icon.png"
magick "$work/16.png" "$work/32.png" "$work/48.png" "$assets/favicon.ico"

sed 's/width="512" height="512" viewBox/width="192" height="192" viewBox/' \
  "$assets/icon.svg" > "$assets/offline/icon.svg"
cp "$assets/icon.svg" "$assets/offline/icon-512.svg"
for size in 192 512; do
  rsvg-convert -w "$size" -h "$size" "$assets/icon.svg" -o "$assets/offline/icon-$size.png"
done
# Full bleed background; all essential artwork lies inside the central
# 80% safe circle so launchers can apply their own circle or squircle mask.
sed -e 's/rx="112"/rx="0"/g' -e '/<rect x="9"/d' \
  "$assets/icon.svg" > "$work/maskable.svg"
rsvg-convert "$work/maskable.svg" -o "$assets/offline/icon-maskable-512.png"
rsvg-convert -w 180 -h 180 "$work/maskable.svg" -o "$assets/apple-touch-icon.png"
cp "$assets/apple-touch-icon.png" "$assets/offline/apple-touch-icon.png"
