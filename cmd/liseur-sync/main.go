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

// cacheDirFor resolves the configured cache directory. A relative path
// is taken as relative to a SQLite database's directory, so that a
// config naming both by relative path still works whatever directory the
// server is started from.
func cacheDirFor(cfg config.Config) string {
	dir := cfg.Content.CacheDir
	if !filepath.IsAbs(dir) &&
		cfg.Database.Driver == "sqlite" &&
		filepath.IsAbs(cfg.Database.URL) {
		return filepath.Join(filepath.Dir(cfg.Database.URL), dir)
	}
	return dir
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
  admin [-config <file>]   manage users, tokens, and folders;
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
	cache, err := content.OpenCache(cacheDirFor(cfg))
	if err != nil {
		return fmt.Errorf("open cover cache: %w", err)
	}
	defer func() { _ = cache.Close() }()

	// One limiter for every path that verifies a password, so the web
	// form and /v1/login share a per-IP budget instead of each
	// offering an unthrottled way around the other.
	loginLimiter := auth.NewRateLimiter(10, time.Minute)

	// The reconciler is built before the API server because an upload
	// hands its bytes straight to a pass (ADR-0023), and the watcher
	// wraps the same reconciler rather than a second one.
	reconciler := content.NewReconciler(st, content.ScanLimits{
		MaxFiles: cfg.Content.ScanMaxFiles,
		MaxDepth: cfg.Content.ScanMaxDepth,
	}, cfg.EPUBLimits(), slog.Default())

	apiSrv := &api.Server{
		St:           st,
		Auth:         auth.NewService(st),
		Cfg:          cfg,
		LoginLimiter: loginLimiter,
		Files:        content.NewFiles(st),
		Covers:       cache,
		Ingest:       content.NewIngester(reconciler),
		Kosync: &kosync.Server{
			St:          st,
			OpenReg:     cfg.OpenRegistration,
			PairingTTL:  time.Duration(cfg.PairingCodeTTLMin) * time.Minute,
			AuthRateLim: auth.NewRateLimiter(10, time.Minute),
		},
		Koplugin: &koplugin.Server{St: st},
	}
	// One background worker, not five. The watcher runs a pass over
	// every folder at startup, whenever a tree changes, and on a slow
	// safety timer; a pass is idempotent and holds no state, so there is
	// no queue to drain and nothing to recover on restart (ADR-0017).
	watcher := content.NewWatcher(st, reconciler, slog.Default())
	// The UI delegates downloads, covers and uploads back to the API
	// server, so the two surfaces share one implementation of the rules
	// about what a stored file may claim to be, and one about what may
	// be written into a watched folder.
	apiSrv.WebUI = &webui.Server{
		St: st, Auth: auth.NewService(st), Cfg: cfg,
		LoginLimiter: loginLimiter,
		Downloads:    apiSrv,
		Covers:       apiSrv,
		Uploads:      apiSrv,
		Watching:     watcher,
	}
	mux := apiSrv.Handler()

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
	errCh := make(chan error, 2)
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		if err := watcher.Run(bgCtx); err != nil {
			errCh <- fmt.Errorf("folder watcher: %w", err)
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
	<-watcherDone
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
	if err := admin.Run(st, rest); err != nil {
		var usage admin.UsageError
		if errors.As(err, &usage) {
			return usageExit{text: usage.Error(), code: usage.ExitCode}
		}
		return err
	}
	return nil
}
