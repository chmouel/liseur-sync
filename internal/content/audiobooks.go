//go:build unix

package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/chmouel/liseur-sync/internal/audiomap"
	"github.com/chmouel/liseur-sync/internal/calibre"
	"github.com/chmouel/liseur-sync/internal/epub"
	"github.com/chmouel/liseur-sync/internal/m4b"
	"github.com/chmouel/liseur-sync/internal/metadata"
	"github.com/chmouel/liseur-sync/internal/store"
)

// AudiobookCatalog is the store surface watched-folder audiobook
// reconciliation and auto-alignment need. Concrete store.Store backends
// implement it; unit tests that only exercise EPUB reconciliation may
// omit it.
type AudiobookCatalog interface {
	AudiobooksInFolder(ctx context.Context, folderID string) ([]store.KnownAudiobook, error)
	ReconcileFolderAudiobooks(
		ctx context.Context, folderID string, observed []store.ObservedAudiobook,
		complete bool, at time.Time,
	) (store.ReconcileResult, error)
	ActiveCatalogAudiobooks(ctx context.Context) ([]store.CatalogAudiobook, error)
	ListCatalogBooks(
		ctx context.Context, viewerID, folderID string,
		cursor *store.CatalogBookCursor, limit int,
	) ([]store.CatalogBook, error)
	CatalogAuthorsForBooks(ctx context.Context, viewerID string, bookIDs []string) (map[string][]string, error)
	CatalogBookIdentifiers(ctx context.Context, viewerID, bookID string) ([]store.BookIdentifier, error)
	ListFolders(ctx context.Context, viewerID, after string, limit int) ([]store.Folder, error)
	ListBookAudiobooks(ctx context.Context) ([]store.BookAudiobook, error)
	UpsertBookAudiobook(ctx context.Context, link store.BookAudiobook, at time.Time) error
	DeleteBookAudiobook(ctx context.Context, bookID string) error
}

func (r *Reconciler) reconcilePlainAudiobooks(
	ctx context.Context, folder store.Folder, report ScanReport, now time.Time,
) (store.ReconcileResult, error) {
	ac, ok := r.catalog.(AudiobookCatalog)
	if !ok {
		return store.ReconcileResult{}, nil
	}
	known, err := ac.AudiobooksInFolder(ctx, folder.ID)
	if err != nil {
		return store.ReconcileResult{}, err
	}
	byPath := make(map[string]store.KnownAudiobook, len(known))
	for _, ab := range known {
		byPath[ab.RelativePath] = ab
	}

	observed := make([]store.ObservedAudiobook, 0, len(report.Audiobooks))
	audioComplete := report.Complete

	for _, abFile := range report.Audiobooks {
		if err := ctx.Err(); err != nil {
			return store.ReconcileResult{}, err
		}
		prior, hadPrior := byPath[abFile.RelativePath]
		if hadPrior && audiobookUnchanged(prior, abFile) {
			observed = append(observed, store.ObservedAudiobook{
				RelativePath:  abFile.RelativePath,
				SizeBytes:     abFile.SizeBytes,
				MTime:         abFile.ModifiedAt,
				ContentSHA256: prior.ContentSHA256,
				CueMTime:      abFile.CueModifiedAt,
				CueSizeBytes:  abFile.CueSizeBytes,
				Unchanged:     true,
			})
			continue
		}
		var knownSHA string
		if hadPrior && m4bStatMatches(prior, abFile.SizeBytes, abFile.ModifiedAt) {
			knownSHA = prior.ContentSHA256
		}
		obs, err := r.readPlainAudiobook(ctx, folder.RootPath, abFile, knownSHA)
		if err != nil {
			r.log.Warn("skipping unreadable audiobook",
				"folder", folder.ID, "path", abFile.RelativePath, "error", err)
			audioComplete = false
			continue
		}
		obs.Replaces = hadPrior && prior.ContentSHA256 != obs.ContentSHA256
		observed = append(observed, obs)
	}

	res, err := ac.ReconcileFolderAudiobooks(ctx, folder.ID, observed, audioComplete, now)
	if err != nil {
		return store.ReconcileResult{}, err
	}
	if err := r.alignCatalogAudiobooks(ctx, ac, now); err != nil {
		r.log.Warn("aligning catalog audiobooks failed", "folder", folder.ID, "error", err)
	}
	return res, nil
}

func (r *Reconciler) reconcileCalibreAudiobooks(
	ctx context.Context, folder store.Folder, root *os.Root, books []calibre.Book, now time.Time,
) (store.ReconcileResult, error) {
	ac, ok := r.catalog.(AudiobookCatalog)
	if !ok {
		return store.ReconcileResult{}, nil
	}
	known, err := ac.AudiobooksInFolder(ctx, folder.ID)
	if err != nil {
		return store.ReconcileResult{}, err
	}
	byCalibreID := make(map[int64]store.KnownAudiobook, len(known))
	for _, ab := range known {
		if ab.CalibreID != nil {
			byCalibreID[*ab.CalibreID] = ab
		}
	}

	var observed []store.ObservedAudiobook
	audioComplete := true
	for _, book := range books {
		if err := ctx.Err(); err != nil {
			return store.ReconcileResult{}, err
		}
		formats := book.AudiobookFormats()
		if len(formats) == 0 {
			continue
		}
		prior, hadPrior := byCalibreID[book.ID]
		obs, err := r.readCalibreAudiobook(ctx, root, book, formats[0], prior, hadPrior)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrStageMissing) {
				calibreID := book.ID
				observed = append(observed, store.ObservedAudiobook{
					CalibreID:  &calibreID,
					Unservable: true,
				})
				continue
			}
			r.log.Warn("skipping unreadable calibre audiobook",
				"folder", folder.ID, "calibre_id", book.ID, "error", err)
			audioComplete = false
			continue
		}
		observed = append(observed, obs)
	}

	res, err := ac.ReconcileFolderAudiobooks(ctx, folder.ID, observed, audioComplete, now)
	if err != nil {
		return store.ReconcileResult{}, err
	}
	if err := r.alignCatalogAudiobooks(ctx, ac, now); err != nil {
		r.log.Warn("aligning catalog audiobooks failed", "folder", folder.ID, "error", err)
	}
	return res, nil
}

func m4bStatMatches(prior store.KnownAudiobook, size int64, mtime time.Time) bool {
	return prior.SizeBytes == size &&
		prior.MTime.UTC().Truncate(time.Second).Equal(mtime.UTC().Truncate(time.Second))
}

func audiobookUnchanged(prior store.KnownAudiobook, file ScannedAudiobook) bool {
	if !m4bStatMatches(prior, file.SizeBytes, file.ModifiedAt) {
		return false
	}
	if (prior.CueMTime == nil) != (file.CueModifiedAt == nil) {
		return false
	}
	if prior.CueMTime != nil {
		if prior.CueSizeBytes != file.CueSizeBytes {
			return false
		}
		if !prior.CueMTime.UTC().Truncate(time.Second).Equal(file.CueModifiedAt.UTC().Truncate(time.Second)) {
			return false
		}
	}
	return true
}

func (r *Reconciler) readPlainAudiobook(
	ctx context.Context, rootPath string, file ScannedAudiobook, knownSHA string,
) (store.ObservedAudiobook, error) {
	if err := ctx.Err(); err != nil {
		return store.ObservedAudiobook{}, err
	}
	opened, info, err := openUnderRoot(rootPath, file.RelativePath)
	if err != nil {
		return store.ObservedAudiobook{}, err
	}
	defer opened.Close()

	contentSHA := knownSHA
	if contentSHA == "" {
		digest := sha256.New()
		if _, err := io.Copy(digest, opened); err != nil {
			return store.ObservedAudiobook{}, err
		}
		contentSHA = hex.EncodeToString(digest.Sum(nil))
		if _, err := opened.Seek(0, io.SeekStart); err != nil {
			return store.ObservedAudiobook{}, err
		}
	}

	meta, err := m4b.ReadMetadata(ctx, m4b.SeekerRangeReader(opened, info.Size()), info.Size())
	if err != nil {
		return store.ObservedAudiobook{}, err
	}

	chapters := meta.Chapters
	if file.CueRelativePath != "" {
		cueBytes, err := readCueUnderRoot(rootPath, file.CueRelativePath)
		if err != nil {
			return store.ObservedAudiobook{}, err
		}
		if cueChaps := m4b.ParseCueChapters(cueBytes, meta.Duration); len(cueChaps) > 0 && len(cueChaps) >= len(chapters) {
			chapters = cueChaps
		}
	}

	title := strings.TrimSpace(meta.Title)
	authors := meta.Authors
	if title == "" || len(authors) == 0 {
		cand := metadata.ParsePath(file.RelativePath, metadata.DefaultPathPatterns())
		if title == "" {
			title = cand.Title
		}
		if title == "" {
			title = strings.TrimSuffix(path.Base(file.RelativePath), path.Ext(file.RelativePath))
		}
		if len(authors) == 0 && cand.Author != "" {
			authors = []string{cand.Author}
		}
	}

	authorsJSON := mustJSONSlice(authors)
	narratorsJSON := mustJSONSlice(meta.Narrators)
	chaptersJSON := mustJSONChapters(chapters)

	return store.ObservedAudiobook{
		RelativePath:    file.RelativePath,
		SizeBytes:       info.Size(),
		MTime:           info.ModTime().UTC(),
		ContentSHA256:   contentSHA,
		CueMTime:        file.CueModifiedAt,
		CueSizeBytes:    file.CueSizeBytes,
		Title:           title,
		AuthorsJSON:     authorsJSON,
		NarratorsJSON:   narratorsJSON,
		SeriesName:      meta.SeriesName,
		ASIN:            meta.ASIN,
		ISBN:            cleanISBN(meta.ISBN),
		DurationSeconds: meta.Duration,
		ChaptersJSON:    chaptersJSON,
	}, nil
}

func (r *Reconciler) readCalibreAudiobook(
	ctx context.Context, root *os.Root, book calibre.Book, format calibre.Format,
	prior store.KnownAudiobook, hadPrior bool,
) (store.ObservedAudiobook, error) {
	if err := ctx.Err(); err != nil {
		return store.ObservedAudiobook{}, err
	}
	relative := book.RelativePath(format)
	if !SafeRelativePath(relative) {
		return store.ObservedAudiobook{}, ErrUnsafePath
	}
	linfo, err := root.Lstat(relative)
	if err != nil {
		return store.ObservedAudiobook{}, err
	}
	if !linfo.Mode().IsRegular() {
		return store.ObservedAudiobook{}, ErrUnsafePath
	}
	opened, err := root.OpenFile(relative, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return store.ObservedAudiobook{}, err
	}
	defer opened.Close()
	info, err := opened.Stat()
	if err != nil {
		return store.ObservedAudiobook{}, err
	}
	if !info.Mode().IsRegular() {
		return store.ObservedAudiobook{}, ErrUnsafePath
	}

	var cueRel string
	var cueSize int64
	var cueMod *time.Time
	candCue := path.Join(book.Path, format.Name+cueExtension)
	if SafeRelativePath(candCue) {
		if cinfo, err := root.Lstat(candCue); err == nil && cinfo.Mode().IsRegular() {
			cueRel = candCue
			cueSize = cinfo.Size()
			cm := cinfo.ModTime().UTC()
			cueMod = &cm
		}
	}

	var contentSHA string
	if hadPrior && prior.RelativePath == relative && m4bStatMatches(prior, info.Size(), info.ModTime()) {
		contentSHA = prior.ContentSHA256
	}
	if contentSHA == "" {
		digest := sha256.New()
		if _, err := io.Copy(digest, opened); err != nil {
			return store.ObservedAudiobook{}, err
		}
		contentSHA = hex.EncodeToString(digest.Sum(nil))
		if _, err := opened.Seek(0, io.SeekStart); err != nil {
			return store.ObservedAudiobook{}, err
		}
	}

	meta, err := m4b.ReadMetadata(ctx, m4b.SeekerRangeReader(opened, info.Size()), info.Size())
	if err != nil {
		return store.ObservedAudiobook{}, err
	}
	chapters := meta.Chapters
	if cueRel != "" {
		cueBytes, err := readCueFromRootHandle(root, cueRel)
		if err != nil {
			return store.ObservedAudiobook{}, err
		}
		if cueChaps := m4b.ParseCueChapters(cueBytes, meta.Duration); len(cueChaps) > 0 && len(cueChaps) >= len(chapters) {
			chapters = cueChaps
		}
	}

	title := strings.TrimSpace(book.Title)
	if title == "" {
		title = strings.TrimSpace(meta.Title)
	}
	authors := book.Authors
	if len(authors) == 0 {
		authors = meta.Authors
	}
	seriesName := book.Series
	if seriesName == "" {
		seriesName = meta.SeriesName
	}
	isbn := cleanISBN(book.Identifiers["isbn"])
	if isbn == "" {
		isbn = cleanISBN(meta.ISBN)
	}
	asin := strings.TrimSpace(book.Identifiers["asin"])
	if asin == "" {
		asin = strings.TrimSpace(book.Identifiers["amazon"])
	}
	if asin == "" {
		asin = meta.ASIN
	}

	calibreID := book.ID
	return store.ObservedAudiobook{
		CalibreID:       &calibreID,
		RelativePath:    relative,
		SizeBytes:       info.Size(),
		MTime:           info.ModTime().UTC(),
		ContentSHA256:   contentSHA,
		CueMTime:        cueMod,
		CueSizeBytes:    cueSize,
		Title:           title,
		AuthorsJSON:     mustJSONSlice(authors),
		NarratorsJSON:   mustJSONSlice(meta.Narrators),
		SeriesName:      seriesName,
		ASIN:            asin,
		ISBN:            isbn,
		DurationSeconds: meta.Duration,
		ChaptersJSON:    mustJSONChapters(chapters),
	}, nil
}

const maxCueFileBytes = 512 << 10

func readCueUnderRoot(rootPath, relative string) ([]byte, error) {
	opened, info, err := openUnderRoot(rootPath, relative)
	if err != nil {
		return nil, err
	}
	defer opened.Close()
	if info.Size() > maxCueFileBytes {
		return nil, errors.New("content: cue file exceeds size limit")
	}
	return io.ReadAll(io.LimitReader(opened, maxCueFileBytes))
}

func readCueFromRootHandle(root *os.Root, relative string) ([]byte, error) {
	if !SafeRelativePath(relative) {
		return nil, ErrUnsafePath
	}
	linfo, err := root.Lstat(relative)
	if err != nil {
		return nil, err
	}
	if !linfo.Mode().IsRegular() {
		return nil, ErrUnsafePath
	}
	opened, err := root.OpenFile(relative, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer opened.Close()
	info, err := opened.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrUnsafePath
	}
	if info.Size() > maxCueFileBytes {
		return nil, errors.New("content: cue file exceeds size limit")
	}
	return io.ReadAll(io.LimitReader(opened, maxCueFileBytes))
}

func cleanISBN(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(s)) {
		if (r >= '0' && r <= '9') || r == 'X' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) != 10 && len(out) != 13 {
		return ""
	}
	return out
}

func mustJSONSlice(vals []string) []byte {
	if vals == nil {
		vals = []string{}
	}
	raw, err := json.Marshal(vals)
	if err != nil {
		return []byte("[]")
	}
	return raw
}

func mustJSONChapters(chaps []m4b.Chapter) []byte {
	if chaps == nil {
		chaps = []m4b.Chapter{}
	}
	raw, err := json.Marshal(chaps)
	if err != nil {
		return []byte("[]")
	}
	return raw
}

type matchedBookPair struct {
	book      store.CatalogBook
	audiobook store.CatalogAudiobook
	source    string
}

var (
	abTrailingBracket = regexp.MustCompile(`\s*[(\[][^()\[\]]*[)\]]\s*$`)
	abTrailingVolume  = regexp.MustCompile(`(?i)[\s,.\-–—]*\b(?:book|tome|volume|vol\.?|livre)\s+(?:\d+|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty|[ivxlcdm]{1,8})\s*$`)
	abLeadingSeries   = regexp.MustCompile(`^.+?\s+\d+(?:\.\d+)?\s+-\s+(.+)$`)
)

func abFold(s string) string {
	var b strings.Builder
	space := false
	for _, r := range store.FoldForSearch(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		} else {
			space = true
		}
	}
	return b.String()
}

func abTitleKey(title string) string {
	t := strings.TrimSpace(title)
	for {
		s := abTrailingBracket.ReplaceAllString(t, "")
		if s == t {
			break
		}
		t = s
	}
	if i := strings.IndexByte(t, ':'); i > 0 {
		t = t[:i]
	}
	t = abTrailingVolume.ReplaceAllString(t, "")
	return abFold(t)
}

func abCandidateTitleKeys(title, seriesName string) []string {
	primary := abTitleKey(title)
	var keys []string
	if primary != "" {
		keys = append(keys, primary)
	}
	if m := abLeadingSeries.FindStringSubmatch(strings.TrimSpace(title)); m != nil {
		if stripped := abTitleKey(m[1]); stripped != "" && stripped != primary {
			keys = append(keys, stripped)
		}
	} else if seriesName != "" {
		if prefix, rest, ok := strings.Cut(title, " - "); ok {
			if strings.HasPrefix(abFold(prefix), abFold(seriesName)) {
				if stripped := abTitleKey(rest); stripped != "" && stripped != primary {
					keys = append(keys, stripped)
				}
			}
		}
	}
	return keys
}

func (r *Reconciler) alignCatalogAudiobooks(
	ctx context.Context, ac AudiobookCatalog, now time.Time,
) error {
	audiobooks, err := ac.ActiveCatalogAudiobooks(ctx)
	if err != nil {
		return err
	}
	existingLinks, err := ac.ListBookAudiobooks(ctx)
	if err != nil {
		return err
	}
	existingByBook := make(map[string]store.BookAudiobook, len(existingLinks))
	for _, l := range existingLinks {
		existingByBook[l.BookID] = l
	}
	if len(audiobooks) == 0 {
		for _, l := range existingLinks {
			_ = ac.DeleteBookAudiobook(ctx, l.BookID)
		}
		return nil
	}

	folders, err := listAllFolders(ctx, ac)
	if err != nil {
		return err
	}
	folderRootByID := make(map[string]string, len(folders))
	for _, f := range folders {
		folderRootByID[f.ID] = f.RootPath
	}

	books, err := listAllActiveCatalogBooks(ctx, ac, folders)
	if err != nil {
		return err
	}
	bookIDs := make([]string, 0, len(books))
	for _, b := range books {
		bookIDs = append(bookIDs, b.ID)
	}
	authorsByBook, err := catalogAuthorsByBook(ctx, ac, bookIDs)
	if err != nil {
		return err
	}
	idsByBook := make(map[string][]store.BookIdentifier)
	needIDs := false
	for _, ab := range audiobooks {
		if ab.ISBN != "" || ab.ASIN != "" {
			needIDs = true
			break
		}
	}
	if needIDs {
		for _, b := range books {
			if ids, err := ac.CatalogBookIdentifiers(ctx, "", b.ID); err == nil && len(ids) > 0 {
				idsByBook[b.ID] = ids
			}
		}
	}

	pairs := matchCatalogAudiobooksToBooks(books, audiobooks, authorsByBook, idsByBook)
	matchedBookIDs := make(map[string]bool, len(pairs))

	for _, pair := range pairs {
		matchedBookIDs[pair.book.ID] = true
		mapKey := audiomap.CatalogMappingKey(pair.book, pair.audiobook)
		if prev, ok := existingByBook[pair.book.ID]; ok &&
			prev.AudiobookID == pair.audiobook.ID &&
			prev.MappingKey == mapKey {
			continue
		}

		var chapters []m4b.Chapter
		_ = json.Unmarshal(pair.audiobook.ChaptersJSON, &chapters)
		if len(chapters) == 0 || pair.audiobook.DurationSeconds <= 0 {
			_ = ac.UpsertBookAudiobook(ctx, store.BookAudiobook{
				BookID:       pair.book.ID,
				AudiobookID:  pair.audiobook.ID,
				EditionSHA:   pair.book.ContentSHA256,
				AudiobookSHA: pair.audiobook.ContentSHA256,
				Source:       pair.source,
				State:        store.BookAudiobookUnmapped,
				MappingKey:   mapKey,
				MappingJSON:  []byte("{}"),
			}, now)
			continue
		}

		rootPath := folderRootByID[pair.book.FolderID]
		mapping, deterministic, alignErr := r.computeAudiobookAlignment(ctx, rootPath, pair.book, pair.audiobook, chapters)
		if alignErr != nil && !deterministic {
			// Transient file/archive read error: preserve any existing link row
			// and do not cache the failure under mapKey so the next pass retries.
			r.log.Warn("transient error reading epub for audiobook alignment; will retry on next pass",
				"book_id", pair.book.ID, "audiobook_id", pair.audiobook.ID, "error", alignErr)
			continue
		}
		state := store.BookAudiobookUnmapped
		mappingJSON := []byte("{}")
		if alignErr == nil && len(mapping.Segments) > 0 {
			if raw, err := json.Marshal(mapping); err == nil {
				state = store.BookAudiobookActive
				mappingJSON = raw
			}
		} else if alignErr != nil {
			r.log.Debug("audiobook alignment unmapped",
				"book_id", pair.book.ID, "audiobook_id", pair.audiobook.ID, "error", alignErr)
		}

		if err := ac.UpsertBookAudiobook(ctx, store.BookAudiobook{
			BookID:       pair.book.ID,
			AudiobookID:  pair.audiobook.ID,
			EditionSHA:   pair.book.ContentSHA256,
			AudiobookSHA: pair.audiobook.ContentSHA256,
			Source:       pair.source,
			State:        state,
			MappingKey:   mapKey,
			MappingJSON:  mappingJSON,
		}, now); err != nil {
			return err
		}
	}

	for _, l := range existingLinks {
		if !matchedBookIDs[l.BookID] {
			_ = ac.DeleteBookAudiobook(ctx, l.BookID)
		}
	}
	return nil
}

// catalogAuthorBatchSize bounds CatalogAuthorsForBooks calls: that query spends
// one SQL parameter per book, and SQLite refuses more than 32,766 variables.
const catalogAuthorBatchSize = 500

// catalogAuthorsByBook reads author names for the whole catalog in bounded
// batches, so a large library cannot make an alignment pass fail outright.
func catalogAuthorsByBook(
	ctx context.Context, ac AudiobookCatalog, bookIDs []string,
) (map[string][]string, error) {
	authorsByBook := make(map[string][]string, len(bookIDs))
	for start := 0; start < len(bookIDs); start += catalogAuthorBatchSize {
		end := min(start+catalogAuthorBatchSize, len(bookIDs))
		batch, err := ac.CatalogAuthorsForBooks(ctx, "", bookIDs[start:end])
		if err != nil {
			return nil, err
		}
		for id, authors := range batch {
			authorsByBook[id] = authors
		}
	}
	return authorsByBook, nil
}

func (r *Reconciler) computeAudiobookAlignment(
	ctx context.Context, rootPath string, book store.CatalogBook, ab store.CatalogAudiobook, chapters []m4b.Chapter,
) (audiomap.Mapping, bool, error) {
	f, size, err := OpenBook(ctx, rootPath, book)
	if err != nil {
		return audiomap.Mapping{}, false, err
	}
	defer f.Close()

	pub, err := epub.OpenPublication(ctx, f, size, r.epub)
	if err != nil {
		return audiomap.Mapping{}, false, err
	}
	bookChaps, resources, err := audiomap.BookAnchors(pub.Index(ctx, 100000), func(href string) ([]byte, error) {
		return pub.Resource(ctx, href)
	})
	if err != nil {
		return audiomap.Mapping{}, false, err
	}
	mapping, err := audiomap.Align(bookChaps, resources, chapters, ab.DurationSeconds)
	return mapping, true, err
}

func matchCatalogAudiobooksToBooks(
	books []store.CatalogBook,
	audiobooks []store.CatalogAudiobook,
	authorsByBook map[string][]string,
	idsByBook map[string][]store.BookIdentifier,
) []matchedBookPair {
	usedBooks := make(map[string]bool)
	usedAudio := make(map[string]bool)
	var pairs []matchedBookPair

	// 1. Calibre ID match (same folder_id and same non-nil calibre_id).
	bookByFolderCalibre := make(map[string]store.CatalogBook)
	for _, b := range books {
		if b.CalibreID != nil {
			key := b.FolderID + ":" + strconvI64(*b.CalibreID)
			bookByFolderCalibre[key] = b
		}
	}
	for _, ab := range audiobooks {
		if ab.CalibreID == nil {
			continue
		}
		key := ab.FolderID + ":" + strconvI64(*ab.CalibreID)
		if b, ok := bookByFolderCalibre[key]; ok && !usedBooks[b.ID] && !usedAudio[ab.ID] {
			usedBooks[b.ID] = true
			usedAudio[ab.ID] = true
			pairs = append(pairs, matchedBookPair{book: b, audiobook: ab, source: "calibre"})
		}
	}

	// 2. Same name beside the book (ADR-0058): same folder_id, same
	// directory, same filename without its extension, one of each. This
	// is where an uploaded audiobook lands in a plain folder, and it is
	// what somebody copying one in by hand would most likely do.
	stemKey := func(folderID, relative string) string {
		return folderID + ":" + strings.TrimSuffix(relative, path.Ext(relative))
	}
	booksByStem := make(map[string][]store.CatalogBook)
	audioByStem := make(map[string][]store.CatalogAudiobook)
	for _, b := range books {
		if !usedBooks[b.ID] && b.RelativePath != "" {
			key := stemKey(b.FolderID, b.RelativePath)
			booksByStem[key] = append(booksByStem[key], b)
		}
	}
	for _, ab := range audiobooks {
		if !usedAudio[ab.ID] && ab.RelativePath != "" {
			key := stemKey(ab.FolderID, ab.RelativePath)
			audioByStem[key] = append(audioByStem[key], ab)
		}
	}
	for _, ab := range audiobooks {
		if usedAudio[ab.ID] || ab.RelativePath == "" {
			continue
		}
		key := stemKey(ab.FolderID, ab.RelativePath)
		stemBooks := booksByStem[key]
		if len(stemBooks) != 1 || len(audioByStem[key]) != 1 || usedBooks[stemBooks[0].ID] {
			continue
		}
		usedBooks[stemBooks[0].ID] = true
		usedAudio[ab.ID] = true
		pairs = append(pairs, matchedBookPair{book: stemBooks[0], audiobook: ab, source: "filename"})
	}

	// 3. Dedicated subdirectory match (same folder_id and same dir != "." with 1 book and 1 audiobook).
	booksByDir := make(map[string][]store.CatalogBook)
	audioByDir := make(map[string][]store.CatalogAudiobook)
	for _, b := range books {
		if usedBooks[b.ID] {
			continue
		}
		dir := path.Dir(b.RelativePath)
		if dir != "." && dir != "" {
			booksByDir[b.FolderID+":"+dir] = append(booksByDir[b.FolderID+":"+dir], b)
		}
	}
	for _, ab := range audiobooks {
		if usedAudio[ab.ID] {
			continue
		}
		dir := path.Dir(ab.RelativePath)
		if dir != "." && dir != "" {
			audioByDir[ab.FolderID+":"+dir] = append(audioByDir[ab.FolderID+":"+dir], ab)
		}
	}
	for dirKey, dirBooks := range booksByDir {
		dirAudios := audioByDir[dirKey]
		if len(dirBooks) == 1 && len(dirAudios) == 1 {
			b := dirBooks[0]
			ab := dirAudios[0]
			if !usedBooks[b.ID] && !usedAudio[ab.ID] {
				usedBooks[b.ID] = true
				usedAudio[ab.ID] = true
				pairs = append(pairs, matchedBookPair{book: b, audiobook: ab, source: "directory"})
			}
		}
	}

	// 4. Unique Identifier (ISBN / ASIN) match across remaining active books and audiobooks.
	bookIDKeys := func(b store.CatalogBook) []string {
		var keys []string
		seen := make(map[string]bool)
		for _, id := range idsByBook[b.ID] {
			var key string
			switch strings.ToLower(strings.TrimSpace(id.Scheme)) {
			case "isbn":
				if clean := cleanISBN(id.Value); clean != "" {
					key = "isbn:" + clean
				}
			case "asin", "amazon":
				if clean := strings.ToLower(strings.TrimSpace(id.Value)); clean != "" {
					key = "asin:" + clean
				}
			}
			if key != "" && !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
		return keys
	}
	audioIDKeys := func(ab store.CatalogAudiobook) []string {
		var keys []string
		if clean := cleanISBN(ab.ISBN); clean != "" {
			keys = append(keys, "isbn:"+clean)
		}
		if clean := strings.ToLower(strings.TrimSpace(ab.ASIN)); clean != "" {
			keys = append(keys, "asin:"+clean)
		}
		return keys
	}

	booksByIDKey := make(map[string][]store.CatalogBook)
	for _, b := range books {
		if usedBooks[b.ID] {
			continue
		}
		for _, key := range bookIDKeys(b) {
			booksByIDKey[key] = append(booksByIDKey[key], b)
		}
	}

	// A book-to-audiobook pair is accepted only when it is unambiguous from
	// both directions across every identifier key. Otherwise a book whose ISBN
	// names one audiobook and whose ASIN names another would pair with
	// whichever key this loop happened to visit first.
	for _, ab := range audiobooks {
		if usedAudio[ab.ID] {
			continue
		}
		var matchedBooks []store.CatalogBook
		seenBooks := make(map[string]bool)
		for _, key := range audioIDKeys(ab) {
			if idBooks := booksByIDKey[key]; len(idBooks) == 1 && !usedBooks[idBooks[0].ID] && !seenBooks[idBooks[0].ID] {
				seenBooks[idBooks[0].ID] = true
				matchedBooks = append(matchedBooks, idBooks[0])
			}
		}
		if len(matchedBooks) != 1 {
			continue
		}
		candidateBook := matchedBooks[0]

		// Verify reverse uniqueness: no other unmatched audiobook shares an
		// identifier key that uniquely names candidateBook.
		reverseMatches := 0
		for _, otherAB := range audiobooks {
			if usedAudio[otherAB.ID] {
				continue
			}
			for _, key := range audioIDKeys(otherAB) {
				if idBooks := booksByIDKey[key]; len(idBooks) == 1 && idBooks[0].ID == candidateBook.ID {
					reverseMatches++
					break
				}
			}
		}
		if reverseMatches != 1 {
			continue
		}
		usedBooks[candidateBook.ID] = true
		usedAudio[ab.ID] = true
		pairs = append(pairs, matchedBookPair{book: candidateBook, audiobook: ab, source: "identifier"})
	}

	// 5. Unique Normalized (Title, Author) match across remaining active books and audiobooks.
	for _, ab := range audiobooks {
		if usedAudio[ab.ID] {
			continue
		}
		var abAuthors []string
		_ = json.Unmarshal(ab.AuthorsJSON, &abAuthors)
		if len(abAuthors) == 0 {
			continue
		}
		abAuthorKey := abFold(abAuthors[0])
		if abAuthorKey == "" {
			continue
		}
		titleKeys := abCandidateTitleKeys(ab.Title, ab.SeriesName)
		if len(titleKeys) == 0 {
			continue
		}

		var matchedBooks []store.CatalogBook
		for _, b := range books {
			if usedBooks[b.ID] {
				continue
			}
			bTitleKey := abTitleKey(b.Title)
			if bTitleKey == "" {
				continue
			}
			titleMatch := false
			for _, tk := range titleKeys {
				if tk == bTitleKey {
					titleMatch = true
					break
				}
			}
			if !titleMatch {
				continue
			}
			authorMatch := false
			for _, a := range authorsByBook[b.ID] {
				if abFold(a) == abAuthorKey {
					authorMatch = true
					break
				}
			}
			if authorMatch {
				matchedBooks = append(matchedBooks, b)
			}
		}
		if len(matchedBooks) != 1 {
			continue
		}
		candidateBook := matchedBooks[0]

		// Verify reverse uniqueness: no other unmatched audiobook matches candidateBook.
		reverseMatches := 0
		bTitleKey := abTitleKey(candidateBook.Title)
		for _, otherAB := range audiobooks {
			if usedAudio[otherAB.ID] {
				continue
			}
			var otherAuthors []string
			_ = json.Unmarshal(otherAB.AuthorsJSON, &otherAuthors)
			if len(otherAuthors) == 0 {
				continue
			}
			otherAuthorKey := abFold(otherAuthors[0])
			authorOK := false
			for _, a := range authorsByBook[candidateBook.ID] {
				if abFold(a) == otherAuthorKey {
					authorOK = true
					break
				}
			}
			if !authorOK {
				continue
			}
			for _, tk := range abCandidateTitleKeys(otherAB.Title, otherAB.SeriesName) {
				if tk == bTitleKey {
					reverseMatches++
					break
				}
			}
		}
		if reverseMatches == 1 {
			usedBooks[candidateBook.ID] = true
			usedAudio[ab.ID] = true
			pairs = append(pairs, matchedBookPair{book: candidateBook, audiobook: ab, source: "title_author"})
		}
	}

	return pairs
}

func listAllActiveCatalogBooks(ctx context.Context, ac AudiobookCatalog, folders []store.Folder) ([]store.CatalogBook, error) {
	var all []store.CatalogBook
	for _, f := range folders {
		var cursor *store.CatalogBookCursor
		for {
			page, err := ac.ListCatalogBooks(ctx, "", f.ID, cursor, 200)
			if err != nil {
				return nil, err
			}
			for _, b := range page {
				if b.Status == store.BookActive {
					all = append(all, b)
				}
			}
			if len(page) < 200 {
				break
			}
			last := page[len(page)-1]
			cursor = &store.CatalogBookCursor{CreatedAt: last.CreatedAt, ID: last.ID}
		}
	}
	return all, nil
}

func listAllFolders(ctx context.Context, ac AudiobookCatalog) ([]store.Folder, error) {
	var all []store.Folder
	var after string
	for {
		page, err := ac.ListFolders(ctx, "", after, 100)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < 100 {
			break
		}
		after = store.FolderCursor(page[len(page)-1])
	}
	return all, nil
}

func strconvI64(v int64) string {
	var buf [24]byte
	i := len(buf)
	neg := v < 0
	u := uint64(v)
	if neg {
		u = -u
	}
	if u == 0 {
		return "0"
	}
	for u > 0 {
		i--
		buf[i] = byte('0' + u%10)
		u /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
