package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	_ "time/tzdata" // IANA timezones in FROM scratch images

	"github.com/chmouel/liseur-sync/internal/adapter/koplugin"
	"github.com/chmouel/liseur-sync/internal/adapter/kosync"
	"github.com/chmouel/liseur-sync/internal/admin"
	"github.com/chmouel/liseur-sync/internal/api"
	"github.com/chmouel/liseur-sync/internal/auth"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/content"
	"github.com/chmouel/liseur-sync/internal/metadata/provider"
	"github.com/chmouel/liseur-sync/internal/store"
	"github.com/chmouel/liseur-sync/internal/store/postgres"
	"github.com/chmouel/liseur-sync/internal/store/sqlite"
	"github.com/chmouel/liseur-sync/internal/webui"
)

func openStore(cfg config.Config) (store.Store, error) {
	switch cfg.Database.Driver {
	case "sqlite":
		return sqlite.Open(cfg.Database.URL)
	case "postgres":
		return postgres.Open(cfg.Database.URL)
	}
	return nil, fmt.Errorf("unknown database driver %q", cfg.Database.Driver)
}

type contentStartupReport struct {
	Abandoned      content.AbandonedUploadReport
	Ingest         content.IngestRecoveryReport
	Reconciliation content.BlobReconciliationReport
	Availability   content.CatalogAvailabilityReport
	GC             content.BlobGCReport
}

// contentRootFor resolves the configured content directory. A relative
// root is taken as relative to a SQLite database's directory, so that a
// config naming both by relative path still works whatever directory the
// server is started from.
func contentRootFor(cfg config.Config) string {
	root := cfg.Content.Root
	if !filepath.IsAbs(root) &&
		cfg.Database.Driver == "sqlite" &&
		filepath.IsAbs(cfg.Database.URL) {
		return filepath.Join(filepath.Dir(cfg.Database.URL), root)
	}
	return root
}

func openContentAndRecover(
	ctx context.Context,
	st store.Store,
	cfg config.Config,
	now time.Time,
) (*content.CAS, contentStartupReport, error) {
	var report contentStartupReport
	cas, err := content.Open(contentRootFor(cfg))
	if err != nil {
		return nil, report, fmt.Errorf("open content store: %w", err)
	}
	cas.SetStagingCap(cfg.Content.MaxStagingBytes)
	// Before anything else, and before the listener opens: an upload
	// interrupted by a crash can only be told apart from one in flight by
	// the fact that nothing is in flight yet.
	report.Abandoned, err = content.SweepAbandonedUploads(
		ctx, st, cas, now,
		time.Duration(cfg.Content.FailureRetentionHours)*time.Hour,
		cfg.Content.RecoveryBatchSize,
	)
	if err != nil {
		cas.Close()
		return nil, report, fmt.Errorf("sweep abandoned uploads: %w", err)
	}
	report.Ingest, err = content.RecoverIngest(
		ctx, st, cas, now, now,
		time.Duration(cfg.Content.FailureRetentionHours)*time.Hour,
		cfg.Content.RecoveryBatchSize,
	)
	if err != nil {
		cas.Close()
		return nil, report, fmt.Errorf("recover content ingestion: %w", err)
	}
	report.Reconciliation, err = content.ReconcileBlobInventory(
		ctx, st, cas, now, cfg.Content.RecoveryBatchSize)
	if err != nil {
		cas.Close()
		return nil, report, fmt.Errorf("reconcile content blobs: %w", err)
	}
	// The catalog follows the inventory: a book whose blob was just found
	// missing must stop being offered before the server starts serving.
	report.Availability, err = content.ReconcileCatalogAvailability(
		ctx, st, now, cfg.Content.RecoveryBatchSize)
	if err != nil {
		cas.Close()
		return nil, report, fmt.Errorf("reconcile catalog availability: %w", err)
	}
	report.GC, err = content.SweepOrphanedBlobs(
		ctx, st, cas,
		now.Add(-time.Duration(cfg.Content.OrphanGraceHours)*time.Hour),
		cfg.Content.RecoveryBatchSize)
	if err != nil {
		cas.Close()
		return nil, report, fmt.Errorf("sweep orphan content blobs: %w", err)
	}
	return cas, report, nil
}

func runIngestValidationWorker(
	ctx context.Context,
	st store.Store,
	cas *content.CAS,
	cfg config.Config,
) error {
	interval := time.Duration(cfg.Content.IngestWorkerInterval) * time.Second
	for {
		report, err := content.RunIngestValidationPass(
			ctx, st, cas, time.Now,
			time.Duration(cfg.Content.FailureRetentionHours)*time.Hour,
			cfg.EPUBLimits(), cfg.Content.RecoveryBatchSize)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if fatalWorkerError(err) {
				return err
			}
			slog.Error("ingest pass failed, retrying next tick", "err", err)
		}
		if report.Validated != 0 || report.Quarantined != 0 ||
			report.Skipped != 0 {
			slog.Info("ingest validation pass complete",
				"validated", report.Validated,
				"quarantined", report.Quarantined,
				"skipped", report.Skipped)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func runIngestMetadataExtractionWorker(
	ctx context.Context,
	st store.Store,
	cas *content.CAS,
	cfg config.Config,
) error {
	interval := time.Duration(cfg.Content.IngestWorkerInterval) * time.Second
	for {
		report, err := content.RunIngestMetadataExtractionPass(
			ctx, st, cas, time.Now,
			time.Duration(cfg.Content.FailureRetentionHours)*time.Hour,
			cfg.EPUBLimits(), cfg.Content.RecoveryBatchSize)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if fatalWorkerError(err) {
				return err
			}
			slog.Error("ingest pass failed, retrying next tick", "err", err)
		}
		if report.Extracted != 0 || report.Quarantined != 0 ||
			report.Skipped != 0 {
			slog.Info("ingest metadata extraction pass complete",
				"extracted", report.Extracted,
				"quarantined", report.Quarantined,
				"skipped", report.Skipped)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

// fatalWorkerError reports whether an ingest pass failure should take the
// server down with it.
//
// Almost none should. A pass writes to disk, so a full or failing volume makes
// it fail every tick — and returning that error stops the process, taking the
// sync API, the adapters and the web UI offline because one blob could not be
// published. It also persists across a restart, so the supervisor gets a crash
// loop rather than a server that still serves reads.
//
// A broken invariant is different: it means the code or the data is wrong in a
// way the next tick cannot fix, and continuing would keep acting on it.
func fatalWorkerError(err error) bool {
	return errors.Is(err, store.ErrInvariantViolation) ||
		errors.Is(err, store.ErrInvalidTransition)
}

func runIngestPromotionWorker(
	ctx context.Context,
	st store.Store,
	cas *content.CAS,
	cfg config.Config,
) error {
	interval := time.Duration(cfg.Content.IngestWorkerInterval) * time.Second
	for {
		report, err := content.RunIngestPromotionPass(
			ctx, st, cas, content.NewLibraryPatterns(st), time.Now,
			time.Duration(cfg.Content.FailureRetentionHours)*time.Hour,
			cfg.Content.RecoveryBatchSize)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if fatalWorkerError(err) {
				return err
			}
			slog.Error("ingest pass failed, retrying next tick", "err", err)
		}
		if report.Promoted != 0 || report.Quarantined != 0 ||
			report.Skipped != 0 || report.Replayed != 0 ||
			report.Undescribed != 0 || report.Misconfigured != 0 {
			slog.Info("ingest promotion pass complete",
				"promoted", report.Promoted,
				"quarantined", report.Quarantined,
				"skipped", report.Skipped,
				"replayed", report.Replayed,
				"undescribed", report.Undescribed,
				"misconfigured", report.Misconfigured)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

// runLibraryRefreshWorker refreshes each library on its own schedule.
//
// The tick is not the schedule: it is how often the server asks whether
// anything is due. What is due is a per-library question — an interval
// that library carries, or an administrator who pressed the button — and
// the claim that answers it is also the lock, so two ticks cannot end up
// sweeping the same root at once.
//
// There is no filesystem-notification path. Only a completed full sweep
// may conclude that a book is gone (ADR-0002), so a notification could
// only ever reduce latency for additions, and a server that acts on them
// has two code paths where one of them is not allowed to reach the
// conclusion that matters.
func runLibraryRefreshWorker(
	ctx context.Context,
	st store.Store,
	cas *content.CAS,
	cfg config.Config,
) error {
	interval := time.Duration(cfg.Content.RefreshTick) * time.Second
	if interval <= 0 {
		return nil
	}
	opts := content.WatchedSyncOptions{
		Scan: content.ScanLimits{
			MaxFiles: cfg.Content.WatchedMaxFiles,
			MaxDepth: cfg.Content.WatchedMaxDepth,
		},
		MaxFileBytes:    cfg.Content.MaxUploadBytes,
		QuotaLimitBytes: watchedQuotaLimit(cfg),
		// An in-place library's books are published by the sweep itself,
		// so it needs the layouts the promotion worker would otherwise
		// have resolved.
		Patterns:         content.NewLibraryPatterns(st),
		FailureRetention: time.Duration(cfg.Content.FailureRetentionHours) * time.Hour,
	}
	for {
		report, err := content.RunRefreshPass(ctx, st, cas, opts, time.Now)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if fatalWorkerError(err) {
				return err
			}
			slog.Error("library refresh failed, retrying next tick", "err", err)
		}
		if report.Changed() {
			slog.Info("library refresh pass complete",
				"libraries", report.Libraries,
				"swept", report.Swept,
				"unavailable", report.Unavailable,
				"errored", report.Errored,
				"ingested", report.Ingested,
				"unchanged", report.Unchanged,
				"rehashed", report.Rehashed,
				"review", report.Review,
				"marked_absent", report.MarkedAbsent,
				"unavailable_files", report.FilesUnavailable,
				"restored_files", report.FilesRestored,
				"failed", report.Failed)
		}
		mapIngestedBooks(ctx, st, report.IngestedOwners)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

// mapIngestedBooks joins newly ingested catalog books to their owner's
// sync works. Without it a book is mapped the first time a client
// resolves it, which means a freshly filled library shows its owner a
// shelf of text tiles — no cover, no Read — until every book has been
// opened once. That surprised the only operator who has ever run this,
// so a sweep that ingested something now finishes the job.
//
// Only the owner is mapped. Backfill is per reader, so doing it for
// every account a library is shared with would cost readers × books on
// each sweep to save them a click they may never need; they keep the
// panel's button. It is safe to repeat — already-mapped books are
// skipped, and a book that matches an existing work on title and author
// alone is counted rather than guessed at, because a wrong guess merges
// two reading histories.
//
// Failure is logged, never fatal: a pass that swept correctly has done
// the part that cannot be redone, and the next tick tries again.
func mapIngestedBooks(ctx context.Context, st store.Store, owners []string) {
	for _, owner := range owners {
		if ctx.Err() != nil {
			return
		}
		report, err := admin.BackfillWorks(ctx, st, owner)
		if err != nil {
			slog.Error("mapping new books to works failed",
				"user", owner, "err", err)
			continue
		}
		if report.Created == 0 && report.Linked == 0 &&
			report.Fuzzy == 0 && report.Conflicted == 0 {
			continue
		}
		slog.Info("mapped new books to works",
			"user", owner,
			"books", report.Books,
			"created", report.Created,
			"linked", report.Linked,
			"needs_confirmation", report.Fuzzy,
			"conflicted", report.Conflicted,
			"skipped", report.Skipped)
	}
}

// watchedQuotaLimit is the same ceiling uploads are charged against. A
// watched snapshot costs its quota principal exactly what an upload of the
// same file would (ADR-0002).
func watchedQuotaLimit(cfg config.Config) *int64 {
	if cfg.Content.QuotaBytes <= 0 {
		return nil
	}
	limit := cfg.Content.QuotaBytes
	return &limit
}

// trashPurgeInterval is how often expired trash is collected. Deletion is
// not urgent — retention is measured in weeks — and each tick is bounded,
// so an hour keeps the work small without letting the bytes linger.
const trashPurgeInterval = time.Hour

// runTrashPurgeWorker permanently deletes books whose retention window
// has closed. It is deliberately the only caller of the purge: an
// operator who wants something gone sooner shortens retention, rather
// than reaching past the window.
func runTrashPurgeWorker(
	ctx context.Context,
	st store.Store,
	cfg config.Config,
) error {
	retention := time.Duration(cfg.Content.TrashRetentionHours) * time.Hour
	for {
		report, err := content.PurgeExpiredTrash(
			ctx, st, time.Now().UTC(), cfg.Content.RecoveryBatchSize)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if fatalWorkerError(err) {
				return err
			}
			slog.Error("trash purge failed, retrying next tick", "err", err)
		}
		if len(report.BookIDs) != 0 {
			slog.Info("trash purge complete",
				"books", len(report.BookIDs),
				"files", report.FilesPurged,
				"quota_released", report.ReservationsReleased,
				"blobs_orphaned", report.BlobsOrphaned,
				"retention", retention)
		}
		timer := time.NewTimer(trashPurgeInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func main() {
	if code := runMain(os.Args[1:], os.Stderr); code != 0 {
		os.Exit(code)
	}
}

type usageExit struct {
	text string
	code int
}

func (e usageExit) Error() string {
	return e.text
}

func runMain(args []string, stderr io.Writer) int {
	if err := run(args); err != nil {
		var usage usageExit
		if errors.As(err, &usage) {
			fmt.Fprint(stderr, usage.text)
			return usage.code
		}
		slog.Error("fatal", "err", err)
		return 1
	}
	return 0
}

const topUsage = `usage: liseur-sync <command> [flags]

  serve [-config <file>]   run the sync and content server
  admin [-config <file>]   manage users, tokens, and libraries;
                           run "liseur-sync admin help" for subcommands
`

func run(args []string) error {
	if len(args) == 0 {
		return usageExit{text: topUsage, code: 1}
	}
	switch args[0] {
	case "help", "-h", "--help":
		return usageExit{text: topUsage, code: 0}
	case "serve":
		return cmdServe(args[1:])
	case "admin":
		return cmdAdmin(args[1:])
	default:
		return usageExit{
			text: fmt.Sprintf("unknown subcommand %q (want serve|admin)\n%s",
				args[0], topUsage),
			code: 1,
		}
	}
}

// defaultConfigPath is the -config flag default: LISEUR_CONFIG if set,
// else the conventional file name in the working directory.
func defaultConfigPath() string {
	if p := os.Getenv("LISEUR_CONFIG"); p != "" {
		return p
	}
	return "liseur-sync.toml"
}

func flagUsage(fs *flag.FlagSet) string {
	var buf bytes.Buffer
	old := fs.Output()
	fs.SetOutput(&buf)
	fs.Usage()
	fs.SetOutput(old)
	return buf.String()
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", defaultConfigPath(),
		"path to TOML config file (default liseur-sync.toml, override with LISEUR_CONFIG)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return usageExit{text: flagUsage(fs), code: 0}
		}
		return usageExit{text: err.Error() + "\n" + flagUsage(fs), code: 1}
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	st, err := openStore(cfg)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()
	// Migration failure = refuse to start; never run against a
	// partially migrated schema.
	if err := st.Migrate(context.Background()); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	recoveryNow := time.Now().UTC()
	cas, recovery, err := openContentAndRecover(
		context.Background(), st, cfg, recoveryNow)
	if err != nil {
		return err
	}
	defer cas.Close()
	slog.Info("content recovery complete",
		"ready", len(recovery.Ingest.Ready),
		"failed", recovery.Ingest.Failed,
		"quarantined", recovery.Ingest.Quarantined,
		"cleaned", recovery.Ingest.Cleaned,
		"skipped", recovery.Ingest.Skipped,
		"physical_blobs", recovery.Reconciliation.PhysicalBlobs,
		"database_blobs", recovery.Reconciliation.DatabaseBlobs,
		"inserted_orphans", recovery.Reconciliation.InsertedOrphans,
		"orphans_marked", recovery.Reconciliation.OrphansMarked,
		"orphans_cleared", recovery.Reconciliation.OrphansCleared,
		"missing_marked", recovery.Reconciliation.MissingMarked,
		"missing_cleared", recovery.Reconciliation.MissingCleared,
		"unchanged_blobs", recovery.Reconciliation.Unchanged,
		"files_hidden", recovery.Availability.FilesMarkedMissing,
		"files_restored", recovery.Availability.FilesMarkedAvailable,
		"books_missing", recovery.Availability.BooksMarkedMissing,
		"books_restored", recovery.Availability.BooksMarkedActive,
		"gc_records_purged", recovery.GC.RecordsPurged,
		"gc_files_removed", recovery.GC.FilesRemoved,
		"gc_files_missing", recovery.GC.FilesMissing,
		"abandoned_uploads_failed", recovery.Abandoned.Failed,
		"abandoned_uploads_skipped", recovery.Abandoned.Skipped)

	// One limiter for every path that verifies a password, so the web
	// form and /v1/login share a per-IP budget instead of each
	// offering an unthrottled way around the other.
	loginLimiter := auth.NewRateLimiter(10, time.Minute)

	// External metadata lookup, off unless an operator listed providers.
	// Validate already refused an unknown name, so this cannot fail
	// here; it is checked anyway rather than ignored.
	providers, err := provider.New(cfg.Metadata.Providers, cfg.MetadataLimits())
	if err != nil {
		return err
	}
	if providers.Enabled() {
		slog.Info("external metadata lookup enabled",
			"providers", strings.Join(providers.Names(), ","))
	}

	apiSrv := &api.Server{
		St:           st,
		Auth:         auth.NewService(st),
		Cfg:          cfg,
		LoginLimiter: loginLimiter,
		// One budget per user across the whole server, so the web UI and
		// the API cannot each spend it: the limit exists to be a good
		// neighbour to a free service, and two of them is not a limit.
		Providers:     providers,
		LookupLimiter: auth.NewRateLimiter(20, time.Minute),
		Content:       cas,
		Blobs:         cas,
		Covers:        cas,
		Kosync: &kosync.Server{
			St:          st,
			OpenReg:     cfg.OpenRegistration,
			PairingTTL:  time.Duration(cfg.PairingCodeTTLMin) * time.Minute,
			AuthRateLim: auth.NewRateLimiter(10, time.Minute),
		},
		Koplugin: &koplugin.Server{St: st},
	}
	// The UI delegates uploads and downloads back to the API server, so
	// the two surfaces share one implementation of the rules about
	// staged bytes and what a stored file may claim to be.
	apiSrv.WebUI = &webui.Server{
		St: st, Auth: auth.NewService(st), Cfg: cfg,
		LoginLimiter: loginLimiter,
		Lookup:       apiSrv,
		Uploads:      apiSrv,
		Downloads:    apiSrv,
		Covers:       apiSrv,
		Backups:      &backupVerifier{st: st, cas: cas},
	}
	mux := apiSrv.Handler()

	// Inferred-session materializer (idempotent; safe to always run).
	bgCtx, bgStop := context.WithCancel(context.Background())
	materializerDone := make(chan struct{})
	go func() {
		defer close(materializerDone)
		apiSrv.RunMaterializer(bgCtx)
	}()

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.LogServerErrors(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Each producer sends at most one terminal error. Buffer all producers so
	// no goroutine can block reporting while coordinated shutdown waits for it.
	errCh := make(chan error, 6)
	validationDone := make(chan struct{})
	go func() {
		defer close(validationDone)
		if err := runIngestValidationWorker(bgCtx, st, cas, cfg); err != nil {
			errCh <- fmt.Errorf("ingest validation worker: %w", err)
		}
	}()
	extractionDone := make(chan struct{})
	go func() {
		defer close(extractionDone)
		if err := runIngestMetadataExtractionWorker(
			bgCtx, st, cas, cfg); err != nil {
			errCh <- fmt.Errorf("ingest metadata extraction worker: %w", err)
		}
	}()
	promotionDone := make(chan struct{})
	go func() {
		defer close(promotionDone)
		if err := runIngestPromotionWorker(bgCtx, st, cas, cfg); err != nil {
			errCh <- fmt.Errorf("ingest promotion worker: %w", err)
		}
	}()
	trashDone := make(chan struct{})
	go func() {
		defer close(trashDone)
		if err := runTrashPurgeWorker(bgCtx, st, cfg); err != nil {
			errCh <- fmt.Errorf("trash purge worker: %w", err)
		}
	}()
	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		if err := runLibraryRefreshWorker(bgCtx, st, cas, cfg); err != nil {
			errCh <- fmt.Errorf("library refresh worker: %w", err)
		}
	}()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		slog.Info("listening", "addr", cfg.ListenAddr, "driver", cfg.Database.Driver)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("serve HTTP: %w", err)
		}
	}()

	var runErr error
	select {
	case err := <-errCh:
		runErr = err
	case <-ctx.Done():
	}

	bgStop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	slog.Info("shutting down")
	shutdownErr := srv.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, srv.Close())
	}
	<-serverDone
	<-validationDone
	<-extractionDone
	<-promotionDone
	<-trashDone
	<-refreshDone
	<-materializerDone
	return errors.Join(runErr, shutdownErr)
}

func cmdAdmin(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			return usageExit{text: admin.Usage, code: 0}
		}
	}
	fs := flag.NewFlagSet("admin", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", defaultConfigPath(),
		"path to TOML config file (default liseur-sync.toml, override with LISEUR_CONFIG)")
	// Standard flag parsing: flags must precede the subcommand.
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return usageExit{text: admin.Usage, code: 0}
		}
		return usageExit{text: err.Error() + "\n" + admin.Usage, code: 1}
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return usageExit{text: admin.Usage, code: 1}
	}
	// Help must not need a working database: an operator who cannot
	// connect still has to be able to find out what the commands are.
	switch rest[0] {
	case "help", "-h", "--help":
		return usageExit{text: admin.Usage, code: 0}
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	st, err := openStore(cfg)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	if err := admin.Run(st, contentRootFor(cfg), rest); err != nil {
		var usage admin.UsageError
		if errors.As(err, &usage) {
			return usageExit{text: usage.Error(), code: usage.ExitCode}
		}
		return err
	}
	return nil
}
