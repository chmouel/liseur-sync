#!/usr/bin/env bash
# Take the screenshots the README shows.
#
# A README screenshot has one job: look like the thing you would get if
# you ran this. So this script runs the thing. It builds the binary,
# starts a server in a temporary directory, uploads eight real books
# from Standard Ebooks, reads a bit of two of them, photographs four
# pages in a headless browser and throws the server away.
#
#   scripts/screenshots.sh            # writes docs/screenshots/*.png
#   OUT=/tmp/shots scripts/screenshots.sh
#
# Nothing here runs in CI, and nothing here is a test: the network is a
# dependency, and the whole point is to look at the output. The books
# are cached under tmp/ after the first run.
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT=$PWD
OUT=${OUT:-docs/screenshots}
CACHE=${CACHE:-tmp/screenshots/books}
PORT=${PORT:-8599}
BASE="http://127.0.0.1:$PORT"
PASSWORD="a-password-for-a-throwaway-server"

die() { echo "screenshots: $*" >&2; exit 1; }

for tool in go node curl jq; do
	command -v "$tool" >/dev/null || die "$tool is required"
done

find_chrome() {
	if [ -n "${LISEUR_CHROME:-}" ]; then echo "$LISEUR_CHROME"; return; fi
	for name in chromium chromium-browser google-chrome google-chrome-stable chrome; do
		if command -v "$name" >/dev/null; then command -v "$name"; return; fi
	done
	# Playwright's download, which a developer working on the web UI is
	# likely to have already.
	local found
	found=$(find "$HOME/.cache/ms-playwright" -maxdepth 3 -name chrome -type f 2>/dev/null |
		sort | tail -1)
	[ -n "$found" ] && echo "$found"
}
CHROME=$(find_chrome)
[ -n "$CHROME" ] || die "no chromium found; set LISEUR_CHROME"

# ------------------------------------------------------------- the books
#
# Standard Ebooks editions: public domain, carefully made, and with
# covers that are actually covers. Enough of them to fill a shelf, and
# varied enough that the grid does not look like a wallpaper.
#
# ?source=download is not decoration: without it the site answers with
# the "your download has started" page, and you end up with eight
# identical 10 KB HTML files that the server correctly refuses as
# invalid EPUBs.
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

mkdir -p "$CACHE"
for url in "${BOOKS[@]}"; do
	name=${url##*/}
	if [ ! -s "$CACHE/$name" ]; then
		echo "fetching $name"
		curl -fsSL --retry 3 -o "$CACHE/$name" "$url?source=download" || die "could not fetch $url"
		head -c 4 "$CACHE/$name" | grep -q '^PK' || {
			rm -f "$CACHE/$name"
			die "$name came back as something other than a zip"
		}
	fi
done

# ------------------------------------------------------------ the server
WORK=$(mktemp -d)
SERVER_PID=""
cleanup() {
	[ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
	rm -rf "$WORK"
}
trap cleanup EXIT

cat >"$WORK/config.toml" <<EOF
listen_addr = "127.0.0.1:$PORT"
insecure_http = true

[database]
driver = "sqlite"
url = "$WORK/liseur-sync.db"

[content]
root = "$WORK/content"
EOF

go tool templ generate ./internal/webui/ >/dev/null
go build -o "$WORK/liseur-sync" ./cmd/liseur-sync

start_server() {
	"$WORK/liseur-sync" serve -config "$WORK/config.toml" >>"$WORK/server.log" 2>&1 &
	SERVER_PID=$!
	for _ in $(seq 1 100); do
		if curl -fsS "$BASE/healthz" >/dev/null 2>&1; then return; fi
		sleep 0.2
	done
	cat "$WORK/server.log" >&2
	die "the server never came up"
}
stop_server() {
	kill "$SERVER_PID" 2>/dev/null || true
	wait "$SERVER_PID" 2>/dev/null || true
	SERVER_PID=""
}

start_server

# The first account is made through the first-run page, because that is
# the only way to make one without a terminal to type a password into —
# and it is the flow a new operator actually sees.
curl -fsS -c "$WORK/cookies" -o /dev/null \
	--data-urlencode "username=alice" \
	--data-urlencode "password=$PASSWORD" \
	--data-urlencode "repeat=$PASSWORD" \
	"$BASE/ui/setup"
SESSION=$(awk '/session/ {print $6 "=" $7}' "$WORK/cookies" | head -1)
[ -n "$SESSION" ] || die "setup did not hand back a session"

# The CLI wants the database to itself, so it gets it.
stop_server
admin() { "$WORK/liseur-sync" admin -config "$WORK/config.toml" "$@"; }
LIBRARY=$(admin create-library alice "Alice's Books" | sed -n 's/.*(id \([^)]*\)).*/\1/p')
[ -n "$LIBRARY" ] || die "no library id"
TOKEN=$(admin mint-token -scope sync,library-read,library-manage alice "Screenshots" |
	sed -n 's/^secret (shown once): //p')
[ -n "$TOKEN" ] || die "no token"
start_server

# ------------------------------------------------------------- the books
api() { curl -fsS -H "Authorization: Bearer $TOKEN" "$@"; }

BOOK_IDS=()
for file in "$CACHE"/*.epub; do
	name=$(basename "$file")
	job=$(api -X POST -H "Idempotency-Key: shot-$name" \
		-F "file=@$file" "$BASE/v1/libraries/$LIBRARY/upload" | jq -r .job_id)
	[ -n "$job" ] || die "upload of $name returned no job"
	book=""
	for _ in $(seq 1 120); do
		state=$(api "$BASE/v1/ingest/jobs/$job")
		case $(jq -r .state <<<"$state") in
		promoted)
			book=$(jq -r .book_id <<<"$state")
			break
			;;
		failed | quarantined) die "$name did not ingest: $(jq -r '.error_code // "?"' <<<"$state")" ;;
		esac
		sleep 1
	done
	[ -n "$book" ] || die "$name never finished ingesting"
	BOOK_IDS+=("$book")
	echo "ingested $name"
done

# A shelf where nothing has been read is a file listing. Two books get a
# position, pushed the way a real client pushes one.
read_to() {
	local book=$1 fraction=$2 op=$3
	local work
	work=$(api -X POST -H 'Content-Type: application/json' -d '{"confirmed":true}' \
		"$BASE/v1/books/$book/resolve" | jq -r .work_id)
	[ -n "$work" ] || die "no work for $book"
	api -X POST -H 'Content-Type: application/json' -o /dev/null \
		-d "$(jq -nc --arg w "$work" --arg id "$op" --argjson p "$fraction" \
			--arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
			'{ops:[{op_id:$id, work_id:$w, client_ts:$ts, progression:$p}]}')" \
		"$BASE/v1/ops"
}
read_to "${BOOK_IDS[0]}" 0.37 "018e6f1a-0000-7000-8000-0000000000a1"
read_to "${BOOK_IDS[2]}" 0.68 "018e6f1a-0000-7000-8000-0000000000a2"

# A panel with one account in it does not show what a panel is for.
csrf_from() {
	curl -fsS -b "$WORK/cookies" "$BASE$1" |
		sed -n 's/.*name="csrf" value="\([^"]*\)".*/\1/p' | head -1
}
for mate in bob carol; do
	curl -fsS -b "$WORK/cookies" -o /dev/null \
		--data-urlencode "csrf=$(csrf_from /ui/admin/users)" \
		--data-urlencode "name=$mate" \
		--data-urlencode "password=$PASSWORD" \
		--data-urlencode "repeat=$PASSWORD" \
		"$BASE/ui/admin/users"
done

# ---------------------------------------------------------- the pictures
mkdir -p "$OUT"
BOOK=${BOOK_IDS[1]}
READER=${BOOK_IDS[0]}

# The reader unpacks its book in the browser, so it is photographed only
# once the engine has a rendered chapter to show — in the dark theme,
# which is a browser-local preference rather than something the server
# knows, and clipped to the window, because a full-page capture resizes
# the frame the book is in and gets a white rectangle. Everything else
# is ready when the page is.
WAIT=$'\n'"document.querySelector('foliate-view')?.renderer?.getContents?.()[0]?.doc"$'\n\n'
EVAL=$'\n'"(() => { const r = document.querySelector('#reader-settings-form input[name=\"theme\"][value=\"dark\"]'); r.checked = true; r.dispatchEvent(new Event('input', { bubbles: true })); return 'dark' })()"$'\n\n'

cd internal/webui
SHOT_CHROME="$CHROME" \
	SHOT_URL="$BASE" \
	SHOT_COOKIE="$SESSION" \
	SHOT_DIR="$ROOT/$OUT" \
	SHOT_WIDTHS=1440 \
	SHOT_PATHS="/ui/library,/ui/books/$READER/read,/ui/books/$BOOK,/ui/admin/users" \
	SHOT_NAMES="library,reader,book,admin" \
	SHOT_WAIT="$WAIT" \
	SHOT_EVAL="$EVAL" \
	SHOT_CLIP=",viewport,," \
	node testdata/uishots.mjs
cd "$ROOT"
rm -rf "$OUT/profile"

# Chrome writes bigger PNGs than it needs to, and a screenshot in a
# README should not be the largest file in the repository. Two hundred
# and fifty-six colours is plenty for a dark UI and a few cover
# paintings, and cuts these to a third; without ImageMagick, lossless
# squeezing is still better than nothing.
if command -v magick >/dev/null; then
	for png in "$OUT"/*.png; do magick "$png" -colors 256 -depth 8 "$png"; done
fi
if command -v oxipng >/dev/null; then
	oxipng -q -o 4 --strip safe "$OUT"/*.png
elif command -v optipng >/dev/null; then
	optipng -quiet -o2 "$OUT"/*.png
fi

echo
echo "wrote:"
for png in "$OUT"/*.png; do
	echo "  $png ($(du -h "$png" | cut -f1))"
done
