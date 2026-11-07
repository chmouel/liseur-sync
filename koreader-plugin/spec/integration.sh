#!/usr/bin/env bash
# Runs the plugin's modules against a throwaway Liseur Sync server,
# inside an unpacked KOReader tree. Needs go, python3 and curl.
#
#   KOREADER_DIR=/path/to/koreader koreader-plugin/spec/integration.sh
#
# KOREADER_DIR is the directory holding reader.lua and luajit, e.g. the
# lib/koreader directory of a koreader-linux-x86_64 release.
set -euo pipefail

die() { echo "integration: $*" >&2; exit 1; }
[ -n "${KOREADER_DIR:-}" ] && [ -x "$KOREADER_DIR/luajit" ] || die "set KOREADER_DIR to an unpacked KOReader"

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
WORK=$(mktemp -d)
PORT=${PORT:-18431}
BASE="http://127.0.0.1:$PORT"
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
cache_dir = "$WORK/cache"
EOF

(cd "$ROOT" && go tool templ generate ./internal/webui/ >/dev/null && go build -o "$WORK/liseur-sync" ./cmd/liseur-sync)

mkdir -p "$WORK/books"
python3 - "$WORK/books/small.epub" <<'EOF'
import sys, zipfile
z = zipfile.ZipFile(sys.argv[1], "w")
z.writestr(zipfile.ZipInfo("mimetype"), "application/epub+zip")
z.writestr("META-INF/container.xml", """<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>""")
z.writestr("OEBPS/content.opf", """<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:identifier id="id">urn:uuid:7d0b3c1e-0000-4000-8000-000000000001</dc:identifier>
<dc:title>A Small Test Book</dc:title><dc:creator>Ann Author</dc:creator><dc:language>en</dc:language>
<meta property="dcterms:modified">2026-01-01T00:00:00Z</meta>
</metadata>
<manifest>
<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
""" + "".join('<item id="c%d" href="c%d.xhtml" media-type="application/xhtml+xml"/>\n' % (i, i) for i in range(1, 5)) + """</manifest>
<spine>""" + "".join('<itemref idref="c%d"/>' % i for i in range(1, 5)) + """</spine>
</package>""")
z.writestr("OEBPS/nav.xhtml", """<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head>
<body><nav epub:type="toc"><ol>""" + "".join('<li><a href="c%d.xhtml">Chapter %d</a></li>' % (i, i) for i in range(1, 5)) + """</ol></nav></body></html>""")
for i in range(1, 5):
    paras = "".join("<p>Chapter %d, paragraph %d. %s</p>" % (i, j, "Words to read. " * 40) for j in range(1, 30))
    z.writestr("OEBPS/c%d.xhtml" % i, """<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter %d</title></head><body><h1>Chapter %d</h1>%s</body></html>""" % (i, i, paras))
z.close()
EOF

admin() { "$WORK/liseur-sync" admin -config "$WORK/config.toml" "$@"; }
printf 'a-password-for-a-throwaway-server\na-password-for-a-throwaway-server\n' | admin create-user reader >/dev/null
admin add-folder -assign reader "Shelf" "$WORK/books" >/dev/null
mint() { admin mint-token -scope "$1" reader "$2" | sed -n 's/^secret (shown once): //p'; }
TOKEN=$(mint sync,library-read,read-insights "KOReader one")
TOKEN2=$(mint sync,library-read "KOReader two")
TOKEN3=$(mint sync "Sync only")
TOKEN4=$(mint sync,library-read "KOReader reader")
KOSYNC_CODE=$(admin pairing-code reader | sed -n '2p' | tr -d ' ')
[ -n "$KOSYNC_CODE" ] && [ -n "$TOKEN" ] && [ -n "$TOKEN2" ] && [ -n "$TOKEN3" ] && [ -n "$TOKEN4" ] || die "could not mint tokens"

"$WORK/liseur-sync" serve -config "$WORK/config.toml" >>"$WORK/server.log" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 100); do
	curl -fsS "$BASE/healthz" >/dev/null 2>&1 && break
	sleep 0.2
done
for _ in $(seq 1 100); do
	n=$(curl -fsS -H "Authorization: Bearer $TOKEN" "$BASE/v1/folders" |
		python3 -c 'import json,sys; print(json.load(sys.stdin)["folders"][0]["folder_id"])' 2>/dev/null |
		xargs -I{} curl -fsS -H "Authorization: Bearer $TOKEN" "$BASE/v1/folders/{}/books" |
		python3 -c 'import json,sys; print(len(json.load(sys.stdin)["books"]))' 2>/dev/null || echo 0)
	[ "$n" = 1 ] && break
	sleep 0.2
done
[ "$n" = 1 ] || { cat "$WORK/server.log" >&2; die "the book was never catalogued"; }

mkdir -p "$WORK/ko" "$WORK/ko-reader"
cd "$KOREADER_DIR"
export SDL_VIDEODRIVER=dummy LISEUR_URL="$BASE" WORK_DIR="$WORK"
KO_HOME="$WORK/ko" KOSYNC_CODE="$KOSYNC_CODE" LISEUR_TOKEN="$TOKEN" LISEUR_TOKEN2="$TOKEN2" LISEUR_TOKEN3="$TOKEN3" \
	PLUGIN_DIR="$ROOT/koreader-plugin/liseur.koplugin" \
	./luajit "$HERE/koreader_integration.lua"
KO_HOME="$WORK/ko-reader" LISEUR_TOKEN4="$TOKEN4" LISEUR_TOKEN2="$TOKEN2" \
	PLUGIN_PARENT="$ROOT/koreader-plugin" \
	./luajit "$HERE/koreader_reader.lua"
