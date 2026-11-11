#!/usr/bin/env bash
# Take the screenshots the setup guide (docs/site) shows.
#
# Like scripts/screenshots.sh this runs the real thing: a real server with
# real books, the real "Download KOReader setup" button in a real browser,
# and then a real KOReader that is given the downloaded setup file and
# photographed. Nothing here is a test and nothing runs in CI.
#
#   KOREADER_DIR=/path/to/koreader/lib/koreader scripts/guide-shots.sh
#
# KOREADER_DIR is an unpacked desktop KOReader release (the directory that
# holds reader.lua). It also needs Xvfb, python3 with python-xlib,
# ImageMagick's `import`, node, curl, jq and Chrome (LISEUR_CHROME).
#
# KEEP=1 leaves the server running and prints where, for iterating on the
# KOReader half with scripts/guide-shots/koreader.py.
set -euo pipefail
cd "$(dirname "$0")/.."

die() { echo "guide-shots: $*" >&2; exit 1; }

ROOT=$PWD
OUT=${OUT:-docs/site/img}
CACHE=${CACHE:-tmp/screenshots/books}
PORT=${PORT:-8598}
BASE="http://127.0.0.1:$PORT"
PASSWORD="a-password-for-a-throwaway-server"
READER_NAME=${READER_NAME:-Kobo Libra}
KOREADER_DIR=${KOREADER_DIR:-}

for tool in go node curl jq python3 xvfb-run import; do
	command -v "$tool" >/dev/null || die "$tool is required"
done
[ -f "$KOREADER_DIR/reader.lua" ] || die "set KOREADER_DIR to an unpacked KOReader (the directory holding reader.lua)"

CHROME=${LISEUR_CHROME:-}
if [ -z "$CHROME" ]; then
	for c in google-chrome-stable google-chrome chromium chromium-browser; do
		if command -v "$c" >/dev/null; then CHROME=$(command -v "$c"); break; fi
	done
fi
[ -n "$CHROME" ] || die "no chromium found; set LISEUR_CHROME"

# The same eight Standard Ebooks editions as scripts/screenshots.sh, so a
# cache from one run serves the other.
BOOKS=(
	"https://standardebooks.org/ebooks/herman-melville/moby-dick/downloads/herman-melville_moby-dick.epub"
	"https://standardebooks.org/ebooks/mary-shelley/frankenstein/downloads/mary-shelley_frankenstein.epub"
	"https://standardebooks.org/ebooks/jane-austen/pride-and-prejudice/downloads/jane-austen_pride-and-prejudice.epub"
	"https://standardebooks.org/ebooks/arthur-conan-doyle/the-hound-of-the-baskervilles/downloads/arthur-conan-doyle_the-hound-of-the-baskervilles.epub"
	"https://standardebooks.org/ebooks/h-g-wells/the-war-of-the-worlds/downloads/h-g-wells_the-war-of-the-worlds.epub"
	"https://standardebooks.org/ebooks/bram-stoker/dracula/downloads/bram-stoker_dracula.epub"
	"https://standardebooks.org/ebooks/oscar-wilde/the-picture-of-dorian-gray/downloads/oscar-wilde_the-picture-of-dorian-gray.epub"
	"https://standardebooks.org/ebooks/robert-louis-stevenson/treasure-island/downloads/robert-louis-stevenson_treasure-island.epub"
)
mkdir -p "$CACHE" "$OUT"
for url in "${BOOKS[@]}"; do
	name=${url##*/}
	if [ ! -s "$CACHE/$name" ]; then
		echo "fetching $name"
		curl -fsSL --retry 3 -o "$CACHE/$name" "$url?source=download" || die "could not fetch $url"
		head -c 4 "$CACHE/$name" | grep -q '^PK' || { rm -f "$CACHE/$name"; die "$name is not a zip"; }
	fi
done

WORK=$(mktemp -d)
SERVER_PID=""
cleanup() {
	[ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
	[ "${KEEP:-}" = 1 ] || { sleep 1; rm -rf "$WORK" || true; }
}
trap cleanup EXIT

cat >"$WORK/config.toml" <<EOF
listen_addr = "127.0.0.1:$PORT"
insecure_http = true

[database]
driver = "sqlite"
url = "$WORK/liseur-sync.db"

[content]
cache_dir = "$WORK/cache"
EOF

go tool templ generate ./internal/webui/ >/dev/null
go build -o "$WORK/liseur-sync" ./cmd/liseur-sync

start_server() {
	"$WORK/liseur-sync" serve -config "$WORK/config.toml" >>"$WORK/server.log" 2>&1 &
	SERVER_PID=$!
	for _ in $(seq 1 100); do
		curl -fsS "$BASE/healthz" >/dev/null 2>&1 && return
		sleep 0.2
	done
	cat "$WORK/server.log" >&2
	die "the server never came up"
}
stop_server() { kill "$SERVER_PID" 2>/dev/null || true; wait "$SERVER_PID" 2>/dev/null || true; SERVER_PID=""; }

start_server
curl -fsS -c "$WORK/cookies" -o /dev/null \
	--data-urlencode "username=alice" --data-urlencode "password=$PASSWORD" \
	--data-urlencode "repeat=$PASSWORD" "$BASE/ui/setup"
SESSION=$(awk '/session/ {print $6 "=" $7}' "$WORK/cookies" | head -1)
[ -n "$SESSION" ] || die "setup did not hand back a session"

stop_server
admin() { "$WORK/liseur-sync" admin -config "$WORK/config.toml" "$@"; }
SHELF="$WORK/books"
mkdir -p "$SHELF"
cp "$CACHE"/*.epub "$SHELF/"
ADD_OUTPUT=$(admin add-folder "Alice's Books" "$SHELF") || die "could not watch the shelf"
FOLDER_ID=$(printf '%s\n' "$ADD_OUTPUT" | sed -n 's/.*(id \([^)]*\)).*/\1/p')
[ -n "$FOLDER_ID" ] || die "could not read the folder id"
admin assign-folder alice "$FOLDER_ID" >/dev/null || die "could not grant alice the folder"
start_server

# Wait for the first pass to put every book in the catalog.
WANT=$(ls "$SHELF"/*.epub | wc -l)
for _ in $(seq 1 120); do
	HAVE=$({ curl -fsS -H "Cookie: $SESSION" "$BASE/ui/library" | grep -o 'books/[0-9a-f]\{32\}"' || true; } | sort -u | wc -l)
	[ "$HAVE" -ge "$WANT" ] && break
	sleep 1
done
[ "$HAVE" -ge "$WANT" ] || die "the library only has $HAVE of $WANT books"

ZIP_DIR="$WORK/zip"
echo "web UI"
SHOT_CHROME=$CHROME SHOT_URL=$BASE SHOT_COOKIE=$SESSION IMG_DIR=$OUT ZIP_DIR=$ZIP_DIR READER_NAME=$READER_NAME \
	node scripts/guide-shots/web.mjs

echo "KOReader"
KO_STATE=$WORK/koreader
mkdir -p "$KO_STATE"
unzip -q -o "$ZIP_DIR/setup.zip" -d "$KO_STATE/unzipped"
[ "${KEEP:-}" = 1 ] && echo "kept: WORK=$WORK BASE=$BASE"
# The download dialog prints the books folder. On a Kobo that is
# /mnt/onboard/Books, so give KOReader a private mount with that path when
# the system allows it, rather than photographing a temporary directory.
WRAP=()
KO_BOOKS=""
if unshare -Urm true 2>/dev/null; then
	WRAP=(unshare -Urm sh -c 'mount -t tmpfs tmpfs /mnt && mkdir -p /mnt/onboard/Books && exec "$@"' sh)
	KO_BOOKS=/mnt/onboard/Books
fi
PYTHONDONTWRITEBYTECODE=1 KOREADER_DIR=$KOREADER_DIR KO_STATE=$KO_STATE UNZIPPED=$KO_STATE/unzipped \
	KO_BOOKS=$KO_BOOKS IMG_DIR=$OUT "${WRAP[@]}" python3 scripts/guide-shots/koreader.py
BOOK=$(cat "$KO_STATE/book-title")
[ -n "$BOOK" ] || die "KOReader did not download a book"

echo "checking the server received the position"
SHOT_CHROME=$CHROME SHOT_URL=$BASE SHOT_COOKIE=$SESSION IMG_DIR=$OUT ZIP_DIR=$ZIP_DIR BOOK=$BOOK \
	node scripts/guide-shots/web-after.mjs

echo "done: $OUT"
