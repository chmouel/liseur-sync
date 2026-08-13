package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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

func openContentAndRecover(
	ctx context.Context,
	st store.Store,
	cfg config.Config,
	now time.Time,
) (*content.CAS, content.IngestRecoveryReport, error) {
	contentRoot := cfg.Content.Root
	if !filepath.IsAbs(contentRoot) &&
		cfg.Database.Driver == "sqlite" &&
		filepath.IsAbs(cfg.Database.URL) {
		contentRoot = filepath.Join(filepath.Dir(cfg.Database.URL), contentRoot)
	}
	cas, err := content.Open(contentRoot)
	if err != nil {
		return nil, content.IngestRecoveryReport{}, fmt.Errorf(
			"open content store: %w", err)
	}
	recovery, err := content.RecoverIngest(
		ctx, st, cas, now, now,
		time.Duration(cfg.Content.FailureRetentionHours)*time.Hour,
		cfg.Content.RecoveryBatchSize,
	)
	if err != nil {
		cas.Close()
		return nil, recovery, fmt.Errorf("recover content ingestion: %w", err)
	}
	return cas, recovery, nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: liseur-sync <serve|admin> [flags]")
	}
	switch args[0] {
	case "serve":
		return cmdServe(args[1:])
	case "admin":
		return cmdAdmin(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q (want serve|admin)", args[0])
	}
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fs.String("config", "liseur-sync.toml", "path to TOML config file")
	if err := fs.Parse(args); err != nil {
		return err
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
		"ready", len(recovery.Ready),
		"failed", recovery.Failed,
		"quarantined", recovery.Quarantined,
		"cleaned", recovery.Cleaned,
		"skipped", recovery.Skipped)

	// One limiter for every path that verifies a password, so the web
	// form and /v1/login share a per-IP budget instead of each
	// offering an unthrottled way around the other.
	loginLimiter := auth.NewRateLimiter(10, time.Minute)

	apiSrv := &api.Server{
		St:           st,
		Auth:         auth.NewService(st),
		Cfg:          cfg,
		LoginLimiter: loginLimiter,
		Kosync: &kosync.Server{
			St:          st,
			OpenReg:     cfg.OpenRegistration,
			PairingTTL:  time.Duration(cfg.PairingCodeTTLMin) * time.Minute,
			AuthRateLim: auth.NewRateLimiter(10, time.Minute),
		},
		Koplugin: &koplugin.Server{St: st},
		WebUI: &webui.Server{
			St: st, Auth: auth.NewService(st), Cfg: cfg,
			LoginLimiter: loginLimiter,
		},
	}
	mux := apiSrv.Routes()

	// Inferred-session materializer (idempotent; safe to always run).
	bgCtx, bgStop := context.WithCancel(context.Background())
	defer bgStop()
	go apiSrv.RunMaterializer(bgCtx)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.LogServerErrors(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.ListenAddr, "driver", cfg.Database.Driver)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	slog.Info("shutting down")
	return srv.Shutdown(shutdownCtx)
}

func cmdAdmin(args []string) error {
	fs := flag.NewFlagSet("admin", flag.ContinueOnError)
	cfgPath := fs.String("config", "liseur-sync.toml", "path to TOML config file")
	// Standard flag parsing: flags must precede the subcommand.
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return errors.New("usage: liseur-sync admin [-config f] <create-user|mint-token|list-tokens|revoke-token>")
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
	return admin.Run(st, rest)
}
