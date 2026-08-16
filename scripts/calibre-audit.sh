#!/usr/bin/env bash
# Audit a Calibre library against the disk underneath it.
#
# A Calibre folder is driven entirely by metadata.db, so this server
# shows exactly what that database says — including its lies. There are
# two kinds worth finding:
#
#   - a row with no file. Calibre lists a book, or one format of one,
#     that is not there. The book cannot be read, and until the row goes
#     it costs a warning on every folder pass.
#   - a file with no row. A book directory Calibre has forgotten. It is
#     invisible to Calibre and therefore invisible here, however plainly
#     it sits on the disk.
#
# The second kind is what a synchronised library produces: metadata.db
# is one SQLite file, and two machines editing it means one of the two
# edits silently loses, taking its books with it. The directories, being
# ordinary files, survive.
#
#   scripts/calibre-audit.sh ~/Documents/Calibre
#
# Read-only. It opens metadata.db immutable, so it cannot disturb a
# library Calibre has open and cannot replay a stale journal into one.
# Nothing here repairs anything: metadata.db belongs to Calibre, and
# Calibre is the only thing that should write to it.
#
#   calibredb --with-library=DIR remove ID              # a row with no files
#   calibredb --with-library=DIR remove_format ID FMT   # a format with no file
#   calibredb --with-library=DIR add DIR/BOOK -1        # a directory with no row
#
# Exits non-zero when it finds anything, so it can be run from cron.
set -euo pipefail

# comm requires both inputs sorted the same way; pin the collation so
# sort and comm cannot disagree over an accented title.
export LC_ALL=C

die() {
	echo "calibre-audit: $*" >&2
	exit 2
}

command -v sqlite3 >/dev/null || die "sqlite3 is required"

LIBRARY=${1:-}
[[ -n $LIBRARY ]] || die "usage: $(basename "$0") /path/to/calibre/library"
LIBRARY=${LIBRARY%/}
[[ -f $LIBRARY/metadata.db ]] || die "$LIBRARY/metadata.db does not exist (not a Calibre library?)"

query() { sqlite3 "file:$LIBRARY/metadata.db?immutable=1" "$@"; }
indent() { while IFS= read -r _l; do echo "  $_l"; done; }

# macOS keeps "Métro" on the disk decomposed (e + combining acute) while
# Calibre stores it composed in metadata.db, so the two sides of the
# sweep below would never compare equal without this. Both sides go
# through it; on Linux it is a no-op.
if command -v python3 >/dev/null; then
	nfc() { python3 -c 'import sys,unicodedata
for line in sys.stdin: sys.stdout.write(unicodedata.normalize("NFC", line))'; }
else
	nfc() { cat; }
fi

found=0

echo "library: $LIBRARY"
echo "books:   $(query 'SELECT COUNT(*) FROM books')"

echo
echo "== format rows whose file is missing"
n=0
while IFS='|' read -r id fmt rel; do
	[[ -n $id ]] || continue
	[[ -f $LIBRARY/$rel ]] && continue
	printf '  %-5s %-6s %s\n' "$id" "$fmt" "$rel"
	n=$((n + 1))
done < <(query "SELECT b.id, d.format, b.path || '/' || d.name || '.' || lower(d.format)
	FROM books b JOIN data d ON d.book = b.id ORDER BY b.id")
if ((n == 0)); then
	echo "  none"
else
	found=1
	echo "  -> calibredb --with-library='$LIBRARY' remove_format ID FMT"
fi

echo
echo "== books with no format rows at all"
if orphans=$(query "SELECT id || '  ' || title FROM books b
	WHERE NOT EXISTS (SELECT 1 FROM data d WHERE d.book = b.id)") && [[ -n $orphans ]]; then
	found=1
	indent <<<"$orphans"
	echo "  -> calibredb --with-library='$LIBRARY' remove ID"
else
	echo "  none"
fi

echo
echo "== books claiming a cover that is not on disk"
n=0
while IFS= read -r line; do
	[[ -n $line ]] || continue
	id=${line%%|*}
	rel=${line#*|}
	[[ -f $LIBRARY/$rel/cover.jpg ]] && continue
	printf '  %-5s %s\n' "$id" "$rel"
	n=$((n + 1))
done < <(query "SELECT id || '|' || path FROM books WHERE has_cover = 1")
if ((n == 0)); then
	echo "  none"
else
	found=1
	echo "  -> re-set the cover in Calibre, or clear has_cover"
fi

echo
echo "== book directories Calibre has forgotten"
known=$(mktemp)
ondisk=$(mktemp)
trap 'rm -f "$known" "$ondisk"' EXIT
query 'SELECT path FROM books' | nfc | sort >"$known"
# Calibre's layout is exactly <author>/<title> (<id>), so a book directory
# is always at depth two. Calibre's own dot-directories — .caltrash,
# .calnotes — are not books, and neither is Syncthing's .stfolder.
# No -printf here: BSD find, which is what macOS has, does not have it.
(cd "$LIBRARY" && find . -mindepth 2 -maxdepth 2 -type d -not -path './.*') | sed 's|^\./||' | nfc | sort >"$ondisk"
if forgotten=$(comm -13 "$known" "$ondisk") && [[ -n $forgotten ]]; then
	found=1
	indent <<<"$forgotten"
	echo "  -> calibredb --with-library='$LIBRARY' add 'DIR' -1"
	echo "     (then delete the directory Calibre copied the book out of)"
else
	echo "  none"
fi

echo
if ((found == 0)); then
	echo "metadata.db and the disk agree."
	exit 0
fi
echo "metadata.db and the disk disagree; see above."
exit 1
