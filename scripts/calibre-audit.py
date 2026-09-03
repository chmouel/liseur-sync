#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["psycopg[binary]"]
# ///
"""Audit a Calibre library against its own metadata.db, and optionally
against the liseur-sync catalog, then offer to repair what it found.

  calibre-audit.py LIBRARY                    report; offer repair on a TTY
  calibre-audit.py LIBRARY --apply            report and repair
  calibre-audit.py LIBRARY --liseur-db PATH_OR_URL [--folder NAME]

Environment: CALIBREDB (binary or command, default "calibredb"),
LISEUR_DATABASE_URL (default for --liseur-db), LISEUR_SYNC (command that
runs the server binary, e.g. "docker exec liseur-sync liseur-sync"; when
set, a pass is run after repairs, otherwise the command is printed).

Exit 0 when clean, 1 when findings remain, 2 on a usage or tool error.

metadata.db is only ever read (immutable) here and only ever written by
calibredb. The liseur-sync database is only ever read: its single write
path is a reconcile pass, which is what `admin scan-folder` runs.
"""

from __future__ import annotations

import argparse
import filecmp
import os
import re
import shlex
import shutil
import sqlite3
import subprocess
import sys
import time
import unicodedata
from dataclasses import dataclass, field
from pathlib import Path

CONFLICT_RE = re.compile(
    r"^(?P<stem>.*)\.sync-conflict-\d{8}-\d{6}-[A-Z0-9]+(?P<ext>\.[^.]+)?$"
)
STAGING = ".audit-staging"
CONFLICTS = ".audit-conflicts"
SNAPSHOT = ".audit-snapshots"


def resolved_name(p: Path) -> str | None:
    """Name a Syncthing conflict copy stands in for: X.sync-conflict-D-DEV.ext -> X.ext."""
    m = CONFLICT_RE.match(p.name)
    return m["stem"] + (m["ext"] or "") if m else None


def nfc(s: str) -> str:
    return unicodedata.normalize("NFC", s)


def die(msg: str, code: int = 2) -> None:
    print(f"error: {msg}", file=sys.stderr)
    sys.exit(code)


@dataclass
class Findings:
    missing_formats: list[tuple[int, str, str]] = field(
        default_factory=list
    )  # id, fmt, relpath
    ghosts: list[tuple[int, str, str]] = field(default_factory=list)  # id, title, path
    no_data: list[tuple[int, str, str]] = field(default_factory=list)  # id, title, path
    covers: list[tuple[int, str]] = field(default_factory=list)  # id, path
    forgotten: list[str] = field(default_factory=list)  # raw relpath (author/title)
    conflicts: list[Path] = field(default_factory=list)  # absolute
    db_conflicts: list[Path] = field(default_factory=list)
    unscanned: list[tuple[int, str]] = field(default_factory=list)  # calibre id, title
    unpurged: list[int] = field(default_factory=list)  # calibre ids only in liseur
    staged: list[str] = field(
        default_factory=list
    )  # left in .audit-staging by an earlier run

    def any(self) -> bool:
        return any(getattr(self, f) for f in self.__dataclass_fields__)


# ---------------------------------------------------------------- audit


def open_metadata(library: Path) -> sqlite3.Connection:
    db = library / "metadata.db"
    if not db.is_file():
        die(f"{db} not found; is {library} a Calibre library?")
    return sqlite3.connect(f"file:{db}?immutable=1", uri=True)


def audit(library: Path, con: sqlite3.Connection) -> Findings:
    f = Findings()
    books = {
        bid: (title, path)
        for bid, title, path in con.execute("SELECT id, title, path FROM books")
    }
    # Every declared format must be a file. A book whose every format is
    # gone is a ghost: a row describing nothing.
    per_book_missing: dict[int, int] = {}
    per_book_total: dict[int, int] = {}
    for bid, fmt, name in con.execute("SELECT book, format, name FROM data"):
        per_book_total[bid] = per_book_total.get(bid, 0) + 1
        rel = f"{books[bid][1]}/{name}.{fmt.lower()}"
        if not (library / rel).is_file():
            per_book_missing[bid] = per_book_missing.get(bid, 0) + 1
            f.missing_formats.append((bid, fmt, rel))
    for bid, n in per_book_missing.items():
        if n == per_book_total[bid]:
            f.ghosts.append((bid, *books[bid]))
    f.missing_formats = [
        m for m in f.missing_formats if m[0] not in {g[0] for g in f.ghosts}
    ]
    for bid, title, path in con.execute(
        "SELECT id, title, path FROM books b WHERE NOT EXISTS (SELECT 1 FROM data d WHERE d.book = b.id)"
    ):
        f.no_data.append((bid, title, path))
    for bid, path in con.execute("SELECT id, path FROM books WHERE has_cover = 1"):
        if not (library / path / "cover.jpg").is_file():
            f.covers.append((bid, path))

    # Directories at depth 2 (author/title) that no row names. Compare
    # NFC-normalised (Syncthing and macOS disagree with Linux about
    # encoding), but keep the raw name for anything we do on disk.
    known = {nfc(p) for _, p in books.values()}
    for author in sorted(library.iterdir()):
        if not author.is_dir() or author.is_symlink() or author.name.startswith("."):
            continue
        for title in sorted(author.iterdir()):
            if not title.is_dir() or title.is_symlink() or title.name.startswith("."):
                continue
            rel = f"{author.name}/{title.name}"
            if nfc(rel) not in known:
                f.forgotten.append(rel)

    staging = library / STAGING
    if staging.is_dir():
        f.staged = sorted(x.name for x in staging.iterdir())

    for p in sorted(library.rglob("*.sync-conflict-*")):
        if any(part.startswith(".") for part in p.relative_to(library).parts[:-1]):
            continue
        if not p.is_file() or p.is_symlink():
            continue
        if resolved_name(p) == "metadata.db" and p.parent == library:
            f.db_conflicts.append(p)
        else:
            f.conflicts.append(p)
    return f


# ------------------------------------------------------- liseur compare


def liseur_calibre_ids(dsn: str, folder: str | None) -> tuple[str, str, set[int]]:
    """Return (folder name, folder id, calibre ids liseur knows) for the Calibre folder."""
    if dsn.startswith(("postgres://", "postgresql://")):
        import psycopg  # declared inline above; uv fetches it

        con = psycopg.connect(dsn)
        ph = "%s"
    else:
        if not Path(dsn).is_file():
            die(f"liseur database {dsn} not found")
        con = sqlite3.connect(f"file:{dsn}?mode=ro", uri=True)
        ph = "?"
    with con:
        cur = con.cursor()
        cur.execute("SELECT id, name FROM folders WHERE kind = 'calibre' ORDER BY name")
        folders = cur.fetchall()
        if folder:
            by_id = [r for r in folders if r[0] == folder]
            folders = by_id or [r for r in folders if r[1] == folder]
        if not folders:
            die(
                "no Calibre folder in the liseur database"
                + (f" named {folder!r}" if folder else "")
            )
        if len(folders) > 1:
            die(
                "several Calibre folders; pick one with --folder <id>: "
                + ", ".join(f"{n} ({i})" for i, n in folders)
            )
        fid, name = folders[0]
        cur.execute(
            f"SELECT calibre_id FROM books WHERE folder_id = {ph} AND calibre_id IS NOT NULL",
            (fid,),
        )
        ids = {int(r[0]) for r in cur.fetchall()}
    con.close()
    return name, fid, ids


def compare(con: sqlite3.Connection, f: Findings, liseur_ids: set[int]) -> None:
    servable = {}
    for bid, title in con.execute(
        "SELECT b.id, b.title FROM books b WHERE EXISTS (SELECT 1 FROM data d WHERE d.book = b.id)"
    ):
        servable[bid] = title
    ghost_ids = {g[0] for g in f.ghosts} | {n[0] for n in f.no_data}
    for bid, title in sorted(servable.items()):
        if bid not in liseur_ids and bid not in ghost_ids:
            f.unscanned.append((bid, title))
    calibre_ids = {r[0] for r in con.execute("SELECT id FROM books")}
    f.unpurged = sorted(liseur_ids - calibre_ids)


# --------------------------------------------------------------- report


def report(
    library: Path, con: sqlite3.Connection, f: Findings, liseur_folder: str | None
) -> None:
    (n_books,) = con.execute("SELECT COUNT(*) FROM books").fetchone()
    print(f"library: {library}\nbooks:   {n_books}\n")

    def section(title: str, rows: list[str], hint: str = "") -> None:
        print(f"== {title}: {len(rows)}")
        for r in rows:
            print(f"  {r}")
        if rows and hint:
            print(f"  -> {hint}")
        print()

    section(
        "formats listed but missing on disk",
        [f"{b}  {fmt}  {rel}" for b, fmt, rel in f.missing_formats],
        "remove_format, or restore the file",
    )
    section(
        "ghost rows (every format missing)",
        [f"{b}  {t}  [{p}]" for b, t, p in f.ghosts],
        "liseur-sync marks these missing, never catalogs them",
    )
    section("rows with no format at all", [f"{b}  {t}  [{p}]" for b, t, p in f.no_data])
    section(
        "has_cover set but no cover.jpg",
        [f"{b}  {p}" for b, p in f.covers],
        "re-set the cover in Calibre (report only)",
    )
    section(
        "directories on disk unknown to metadata.db",
        f.forgotten,
        "Syncthing or a copy left these; metadata.db never saw them",
    )
    section(
        "Syncthing conflict files", [str(p.relative_to(library)) for p in f.conflicts]
    )
    section(
        "metadata.db conflict copies",
        [str(p.relative_to(library)) for p in f.db_conflicts],
        "another machine still writes this library; never swapped in, the losing copy is where forgotten rows went",
    )
    section(
        f"left in {STAGING} by an earlier run",
        f.staged,
        "what calibredb declined or did not import; add or delete by hand",
    )
    if liseur_folder is not None:
        section(
            f"in Calibre with a file, not in liseur folder {liseur_folder!r}",
            [f"{b}  {t}" for b, t in f.unscanned],
            "a pass has not seen them yet",
        )
        section(
            f"in liseur folder {liseur_folder!r}, gone from Calibre",
            [str(b) for b in f.unpurged],
            "a complete pass purges them (ADR-0022)",
        )


# --------------------------------------------------------------- repair


class Calibredb:
    def __init__(self, library: Path, cmd: str) -> None:
        self.library = library
        self.cmd = shlex.split(cmd)
        if not self.cmd or shutil.which(self.cmd[0]) is None:
            die(f"calibredb not found ({cmd!r}); set CALIBREDB")

    def run(self, *args: str) -> subprocess.CompletedProcess[str]:
        argv = [*self.cmd, *args, f"--with-library={self.library}"]
        print(f"  $ {shlex.join(argv)}")
        p = subprocess.run(argv, text=True, capture_output=True, check=False)
        if p.returncode != 0:
            print(p.stderr.strip(), file=sys.stderr)
            die(f"calibredb exited {p.returncode}")
        return p


def snapshot(library: Path) -> Path:
    """Consistent copy of metadata.db via SQLite's backup API, so a
    database Calibre has open is still copied whole."""
    if (library / SNAPSHOT).is_symlink():
        die(f"{library / SNAPSHOT} is a symlink")
    dest = library / SNAPSHOT / time.strftime("%Y%m%d-%H%M%S")
    dest.mkdir(parents=True)
    src = sqlite3.connect(f"file:{library / 'metadata.db'}?mode=ro", uri=True)
    dst = sqlite3.connect(dest / "metadata.db")
    with dst:
        src.backup(dst)
    src.close()
    dst.close()
    print(f"snapshot: {dest / 'metadata.db'}")
    return dest


def inside(library: Path, *paths: Path) -> None:
    """Refuse to touch anything that resolves outside the library: the
    audit only ever moves files it found by walking this tree, and a
    symlink must not make that walk reach somewhere else."""
    for p in paths:
        if p.is_symlink() or not p.resolve().is_relative_to(library):
            die(f"{p} is a symlink or resolves outside {library}; not touching it")


def park(library: Path, p: Path) -> None:
    """Move a losing copy under .audit-conflicts, never over an earlier one."""
    if (library / CONFLICTS).is_symlink():
        die(f"{library / CONFLICTS} is a symlink")
    dest = library / CONFLICTS / p.relative_to(library)
    dest.parent.mkdir(parents=True, exist_ok=True)
    if dest.exists():
        dest = dest.with_name(f"{dest.name}.{int(p.stat().st_mtime)}")
    n = 0
    while dest.exists():
        n += 1
        dest = dest.with_name(f"{dest.name}.{n}")
    inside(library, p, dest.parent)
    shutil.move(str(p), str(dest))
    print(f"  parked   {p.relative_to(library)} -> {dest.relative_to(library)}")


def resolve_conflicts(library: Path, f: Findings) -> bool:
    changed = False
    for p in f.db_conflicts:
        park(library, p)
        changed = True
    for p in f.conflicts:
        name = resolved_name(p)
        if name is None:
            print(f"  skip     {p.relative_to(library)} (unrecognised name)")
            continue
        real = p.with_name(name)
        inside(library, p)
        if real.exists():
            inside(library, real)
        if not real.exists():
            p.rename(real)
            print(f"  renamed  {p.relative_to(library)} -> {real.name}")
        elif filecmp.cmp(p, real, shallow=False):
            p.unlink()
            print(f"  deleted  {p.relative_to(library)} (identical to {real.name})")
        elif p.stat().st_mtime > real.stat().st_mtime:
            park(library, real)
            p.rename(real)
            print(f"  newer    {p.relative_to(library)} -> {real.name}")
        else:
            park(library, p)
        changed = True
    return changed


def book_dir(library: Path, path: str) -> Path | None:
    """LIBRARY/<books.path>, or None when that value cannot be trusted:
    absolute, climbing out, or crossing a symlink. calibredb remove
    deletes this directory, so a row is never acted on without it."""
    parts = Path(path).parts
    if Path(path).is_absolute() or ".." in parts or not parts:
        return None
    d = library
    for part in parts:
        d = d / part
        if d.is_symlink():
            return None
    return d


def only_sidecars(d: Path) -> bool:
    if not d.exists():
        return True
    return all(x.name in ("cover.jpg", "metadata.opf") for x in d.iterdir())


def added_dirs(library: Path, added: list[int]) -> list[str]:
    con = open_metadata(library)
    q = f"SELECT path FROM books WHERE id IN ({','.join('?' * len(added))})"
    dirs = [r[0] for r in con.execute(q, added)]
    con.close()
    return dirs


def imported_files(library: Path, added: list[int]) -> list[Path]:
    """The format files calibredb now holds for the books it added."""
    con = open_metadata(library)
    q = (
        "SELECT b.path, d.name, lower(d.format) FROM data d JOIN books b ON b.id = d.book"
        f" WHERE d.book IN ({','.join('?' * len(added))})"
    )
    files = [
        library / path / f"{name}.{fmt}" for path, name, fmt in con.execute(q, added)
    ]
    con.close()
    return [f for f in files if f.is_file()]


def add_forgotten(library: Path, rel: str, cdb: Calibredb, left: list[str]) -> bool:
    """Stage one forgotten directory and let calibredb import it. Only the
    files calibredb reports having taken are removed from the staging
    copy; anything else stays there and is reported."""
    src = library / rel
    if any(CONFLICT_RE.match(x.name) for x in src.iterdir()):
        left.append(f"forgotten {rel}: still has conflict files; rerun")
        return False
    if not any(
        x.is_file() and x.suffix.lower() not in (".jpg", ".opf") for x in src.iterdir()
    ):
        left.append(f"forgotten {rel}: no book file inside; remove by hand")
        return False
    if (library / STAGING).is_symlink():
        die(f"{library / STAGING} is a symlink")
    staged = library / STAGING / rel.replace("/", "__")
    staged.parent.mkdir(parents=True, exist_ok=True)
    inside(library, src, staged.parent)
    if staged.exists():
        left.append(f"forgotten {rel}: {staged} already exists from an earlier run")
        return False
    shutil.move(str(src), str(staged))
    parent = src.parent
    if parent.is_dir() and not any(parent.iterdir()):
        parent.rmdir()
    p = cdb.run("add", "--one-book-per-directory", str(staged))
    m = re.search(r"Added book ids:\s*([\d,\s]+)", p.stdout)
    if not m:
        # Most often a title+author duplicate: calibredb declines with
        # exit 0. The copy is the only one, so it stays.
        left.append(
            f"forgotten {rel}: not added ({p.stderr.strip() or p.stdout.strip() or 'no output'}); kept at {staged}"
        )
        return False
    added = [int(x) for x in re.findall(r"\d+", m.group(1))]
    print(f"  added {added}  <- {rel}")
    # A staged file goes only when Calibre now holds a byte-identical
    # copy of it; two EPUBs in one directory yield one book, and the one
    # it did not keep must not be lost.
    held = imported_files(library, added)
    held += [
        c
        for c in (library / d / "cover.jpg" for d in added_dirs(library, added))
        if c.is_file()
    ]
    for x in list(staged.iterdir()):
        if not x.is_file() or x.is_symlink():
            continue
        # metadata.opf is the sidecar Calibre itself wrote and rewrites
        # on import; it carries nothing the new row lacks.
        if x.name == "metadata.opf" or any(
            filecmp.cmp(x, h, shallow=False) for h in held
        ):
            inside(library, x)
            x.unlink()
    rest = sorted(x.name for x in staged.iterdir())
    if rest:
        left.append(f"forgotten {rel}: calibredb did not take {rest}; kept at {staged}")
    else:
        staged.rmdir()
    return True


def repair(library: Path, f: Findings, cdb: Calibredb) -> tuple[bool, list[str]]:
    """Apply repairs. Returns (anything changed, leftovers to report)."""
    changed = False
    left: list[str] = []
    snapshot(library)

    if f.conflicts or f.db_conflicts:
        print("\n-- Syncthing conflicts")
        changed |= resolve_conflicts(library, f)
        # A resolved conflict may have put a "missing" format back or
        # emptied a ghost's directory: look again before touching rows.
        con = open_metadata(library)
        f = audit(library, con)
        con.close()

    # A ghost row may point at a directory Syncthing filled with files
    # under other names. `remove` sends the directory to .caltrash, so
    # it only runs when there is nothing there we would regret.
    ghosts = [*f.ghosts, *f.no_data]
    if ghosts:
        print("\n-- ghost rows")
    for bid, title, path in ghosts:
        d = book_dir(library, path)
        if d is None:
            left.append(
                f"ghost {bid} {title!r}: path {path!r} is not a plain path inside the library"
            )
        elif only_sidecars(d):
            cdb.run("remove", str(bid))
            changed = True
        else:
            left.append(
                f"ghost {bid} {title!r}: {path} holds files metadata.db does not list; resolve by hand"
            )

    if f.missing_formats:
        print("\n-- stale formats")
    for bid, fmt, rel in f.missing_formats:
        if (library / rel).is_file():
            continue
        cdb.run("remove_format", str(bid), fmt)
        changed = True

    if f.forgotten:
        print("\n-- forgotten directories")
    for rel in f.forgotten:
        changed |= add_forgotten(library, rel, cdb, left)
    staging = library / STAGING
    if staging.is_dir() and not any(staging.iterdir()):
        staging.rmdir()
    return changed, left


def run_pass(folder: str | None) -> bool:
    """Run the liseur-sync pass over the folder (by id when the liseur
    database told us one, since names need not be unique). Without
    LISEUR_SYNC or a folder the command is printed for the operator; a
    pass that ran and failed is a tool error. Returns whether it ran."""
    cmd = os.environ.get("LISEUR_SYNC", "")
    if not cmd or not folder:
        words = ["admin", "scan-folder", folder or "<FOLDER-ID>"]
        why = "set LISEUR_SYNC" if not cmd else "pass --folder or --liseur-db"
        print(
            f"\nnow run a pass ({why} to have it run here): liseur-sync {shlex.join(words)}"
        )
        return False
    try:
        argv = [*shlex.split(cmd), "admin", "scan-folder", folder]
        print(f"\n$ {shlex.join(argv)}")
        p = subprocess.run(argv, check=False)
    except (ValueError, OSError) as e:
        die(f"cannot run LISEUR_SYNC: {e}")
    if p.returncode != 0:
        die(f"pass failed (exit {p.returncode})")
    return True


# ----------------------------------------------------------------- main


def full_audit(
    library: Path, liseur_db: str | None, folder: str | None
) -> tuple[Findings, str | None, str | None]:
    """Audit, compare when a liseur database is given, report. Returns the
    findings and the liseur folder's name and id (None without a db)."""
    con = open_metadata(library)
    f = audit(library, con)
    name = fid = None
    if liseur_db:
        name, fid, ids = liseur_calibre_ids(liseur_db, folder)
        compare(con, f, ids)
    report(library, con, f, name)
    con.close()
    return f, name, fid


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("library", type=Path)
    ap.add_argument("--apply", action="store_true", help="repair without asking")
    ap.add_argument(
        "--liseur-db",
        default=os.environ.get("LISEUR_DATABASE_URL"),
        help="liseur-sync sqlite path or postgres:// URL (default $LISEUR_DATABASE_URL)",
    )
    ap.add_argument(
        "--folder",
        help="liseur folder name or id (to pick one of several, or to name the folder scan-folder gets)",
    )
    a = ap.parse_args()
    sys.stdout.reconfigure(line_buffering=True)  # keep order with calibredb output

    library = a.library.resolve()
    f, _, fid = full_audit(library, a.liseur_db, a.folder)
    folder = fid or a.folder

    repairable = (
        f.missing_formats
        or f.ghosts
        or f.no_data
        or f.forgotten
        or f.conflicts
        or f.db_conflicts
    )
    drift = bool(f.unscanned or f.unpurged)
    if not f.any():
        print("clean")
        return 0
    if not repairable and not drift:
        print("nothing to repair automatically")
        return 1

    # Nothing below runs unasked: a cron audit only reports, including
    # the pass, which rewrites the liseur catalog.
    if not a.apply:
        if not sys.stdin.isatty():
            print("findings above; rerun with --apply to repair")
            return 1
        if input("Apply repairs? [y/N] ").strip().lower() not in ("y", "yes"):
            return 1

    changed = False
    left: list[str] = []
    if repairable:
        cdb = Calibredb(library, os.environ.get("CALIBREDB", "calibredb"))
        changed, left = repair(library, f, cdb)
    pass_pending = (changed or drift) and not run_pass(folder)

    # Exit status comes from what is actually left, not from bookkeeping.
    print("\n== after repair")
    f, _, _ = full_audit(library, a.liseur_db, a.folder)
    for line in left:
        print(f"  {line}")
    if pass_pending:
        print("  the liseur-sync pass above has not run yet")
    if f.any() or left or pass_pending:
        return 1
    print("clean")
    return 0


if __name__ == "__main__":
    sys.exit(main())
