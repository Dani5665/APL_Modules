// Command app runs the module activation request portal: the salesperson
// part, the admin part, the outbox worker, the export scheduler and the
// nightly SQLite backup, all in one process.
package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	// The distroless runtime image carries no tzdata, so the zone database
	// is compiled into the binary. Europe/Sofia drives every business date.
	_ "time/tzdata"

	"haynesproform/internal/audit"
	"haynesproform/internal/auth"
	"haynesproform/internal/config"
	"haynesproform/internal/demo"
	"haynesproform/internal/email"
	"haynesproform/internal/entrylink"
	"haynesproform/internal/export"
	"haynesproform/internal/external"
	mockdir "haynesproform/internal/external/mock"
	mssqldir "haynesproform/internal/external/mssql"
	apphttp "haynesproform/internal/http"
	"haynesproform/internal/requests"
	"haynesproform/internal/scheduler"
	"haynesproform/internal/store"
)

func main() {
	// The health check subcommand exists because the distroless image has no
	// shell or curl for Docker's HEALTHCHECK to call.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}
	if len(os.Args) > 1 && os.Args[1] == "sign-link" {
		os.Exit(runSignLink(os.Args[2:]))
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)
	log.Info("starting",
		"addr", cfg.Addr,
		"external_mode", cfg.ExternalMode,
		"data_dir", cfg.DataDir,
		"cookie_secure", cfg.CookieSecure,
		"admin_2fa_enabled", cfg.Admin2FAEnabled)
	if !cfg.Admin2FAEnabled {
		log.Warn("admin 2FA is DISABLED (ADMIN_2FA_ENABLED=false); " +
			"this is only permitted with EXTERNAL_DB_MODE=mock and must never be used in production")
	}

	// ctx is cancelled on SIGTERM/SIGINT and drives every background worker.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	log.Info("database ready", "path", db.Path())

	if err := email.SeedTemplates(ctx, db); err != nil {
		return err
	}

	enc, err := auth.NewEncrypter(cfg.EncryptionKey)
	if err != nil {
		return err
	}

	if err := bootstrapAdmin(ctx, db, cfg, log); err != nil {
		return err
	}

	// Demo data exists only alongside the mock directory, so a real
	// deployment can never pick it up.
	if cfg.ExternalMode == config.ExternalMock {
		if _, err := demo.Seed(ctx, db, log); err != nil {
			return fmt.Errorf("seed demo data: %w", err)
		}
	}

	directory, err := openDirectory(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer directory.Close()

	auditLog := audit.New(db, log)
	composer := email.NewComposer(db, cfg.BaseURL)
	outbox := email.NewWorker(db, enc, log)
	generator := export.NewGenerator(db)
	sched := scheduler.New(db, generator, composer, auditLog, log, outbox.Notify)

	app := &apphttp.App{
		Cfg:       cfg,
		DB:        db,
		Directory: directory,
		Sessions:  auth.NewManager(db, cfg.SessionIdleTimeout, cfg.SessionAbsoluteTimeout, cfg.CookieSecure),
		Throttle:  auth.NewThrottle(db),
		Enc:       enc,
		Audit:     auditLog,
		Requests:  requests.NewService(db),
		Composer:  composer,
		Outbox:    outbox,
		Export:    generator,
		Scheduler: sched,
		EntryLink: entryLinkParser(cfg),
		Log:       log,
	}

	handler, err := apphttp.NewHandler(app)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: handler,
		// TLS is terminated by the reverse proxy; these bound a slow client.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	var wg sync.WaitGroup
	start := func(name string, fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
		log.Debug("background worker started", "worker", name)
	}

	start("outbox", func() { outbox.Run(ctx) })
	start("scheduler", func() { sched.Run(ctx) })
	start("housekeeping", func() { runHousekeeping(ctx, db, cfg, log) })

	serverErr := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	// Stop accepting new requests and let in-flight ones finish before the
	// workers are torn down.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "error", err)
	}

	wg.Wait()
	log.Info("stopped")
	return nil
}

// openDirectory builds the external directory, wrapped in its cache.
//
// A failure to reach MSSQL at startup is not fatal: the admin part and every
// local page work without it, and the customer part shows a friendly error.
func openDirectory(ctx context.Context, cfg config.Config, log *slog.Logger) (external.Directory, error) {
	switch cfg.ExternalMode {
	case config.ExternalMock:
		log.Warn("using the mock external directory; client data is not real")
		return external.NewCached(mockdir.New(), cfg.ClientListCacheTTL), nil

	case config.ExternalMSSQL:
		dir, err := mssqldir.Open(ctx, cfg.MSSQLDSN)
		if err != nil {
			return nil, fmt.Errorf("external database: %w", err)
		}
		log.Info("connected to the external database")
		return external.NewCached(dir, cfg.ClientListCacheTTL), nil
	}
	return nil, fmt.Errorf("unsupported EXTERNAL_DB_MODE %q", cfg.ExternalMode)
}

// entryLinkParser selects the entry-link parser per ENTRY_LINK_SIGNING_ENABLED
// (see config.Config and README.md, "Entry link format"). config.Load already
// refuses to start with signing off against EXTERNAL_DB_MODE=mssql.
func entryLinkParser(cfg config.Config) entrylink.EntryLinkParser {
	if !cfg.EntryLinkSigningEnabled {
		return entrylink.PathParser{}
	}
	return entrylink.SignedParser{Secret: cfg.EntryLinkSecret}
}

// bootstrapAdmin creates the first administrator from the environment when no
// admin exists yet.
func bootstrapAdmin(ctx context.Context, db *store.DB, cfg config.Config, log *slog.Logger) error {
	n, err := db.CountActiveAdmins(ctx)
	if err != nil {
		return err
	}

	if n > 0 {
		if cfg.BootstrapAdminEmail != "" {
			log.Warn("BOOTSTRAP_ADMIN_EMAIL and BOOTSTRAP_ADMIN_PASSWORD are still set "+
				"although an administrator already exists; unset them",
				"admins", n)
		}
		return nil
	}

	if cfg.BootstrapAdminEmail == "" {
		log.Error("no administrator exists and BOOTSTRAP_ADMIN_EMAIL is not set; " +
			"set BOOTSTRAP_ADMIN_EMAIL and BOOTSTRAP_ADMIN_PASSWORD and restart")
		return nil
	}

	emailAddr, err := auth.NormalizeEmail(cfg.BootstrapAdminEmail)
	if err != nil {
		return fmt.Errorf("BOOTSTRAP_ADMIN_EMAIL: %w", err)
	}
	hash, err := auth.HashPassword(cfg.BootstrapAdminPassword)
	if err != nil {
		return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD: %w", err)
	}

	// The bootstrap password comes from an env var an operator typed, so it is
	// treated as provisional: the admin must set their own before doing
	// anything else, in addition to the always-mandatory 2FA enrollment.
	id, err := db.CreateAdmin(ctx, emailAddr, hash, true)
	if err != nil {
		return fmt.Errorf("create the bootstrap administrator: %w", err)
	}
	log.Info("bootstrap administrator created; password change and 2FA enrollment required at first login",
		"email", emailAddr, "id", id)
	return nil
}

// Housekeeping intervals.
const (
	purgeInterval  = 15 * time.Minute
	backupInterval = 24 * time.Hour
	backupsKept    = 14
)

// runHousekeeping purges expired sessions and takes the daily online backup.
func runHousekeeping(ctx context.Context, db *store.DB, cfg config.Config, log *slog.Logger) {
	purge := time.NewTicker(purgeInterval)
	defer purge.Stop()
	backup := time.NewTicker(backupInterval)
	defer backup.Stop()

	idleCutoff := func() time.Time { return time.Now().Add(-cfg.SessionIdleTimeout) }
	doPurge := func() {
		if err := db.PurgeExpired(ctx, idleCutoff()); err != nil {
			log.Error("expired sessions could not be purged", "error", err)
		}
	}
	doBackup := func() { takeBackup(ctx, db, cfg, log) }

	safeCall(log, "housekeeping", doPurge)
	safeCall(log, "housekeeping", doBackup)

	for {
		select {
		case <-ctx.Done():
			log.Info("housekeeping stopped")
			return
		case <-purge.C:
			safeCall(log, "housekeeping", doPurge)
		case <-backup.C:
			safeCall(log, "housekeeping", doBackup)
		}
	}
}

// safeCall runs fn with panic recovery. Background goroutines have no HTTP
// middleware to catch a panic for them, and one unrecovered panic in any
// goroutine takes the whole process down, not just that job.
func safeCall(log *slog.Logger, worker string, fn func()) {
	defer func() {
		if p := recover(); p != nil {
			log.Error("panic recovered in background worker",
				"worker", worker, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
		}
	}()
	fn()
}

// takeBackup writes an online copy of the database and prunes old ones.
func takeBackup(ctx context.Context, db *store.DB, cfg config.Config, log *slog.Logger) {
	dir := cfg.BackupDir()
	name := fmt.Sprintf("app-%s.db", time.Now().UTC().Format("2006-01-02T150405"))
	dest := dir + "/" + name

	if err := db.Backup(ctx, dest); err != nil {
		log.Error("backup failed", "path", dest, "error", err)
		return
	}
	log.Info("backup written", "path", dest)

	if err := pruneBackups(dir, backupsKept); err != nil {
		log.Error("old backups could not be pruned", "dir", dir, "error", err)
	}
}

// pruneBackups keeps the newest keep files and removes the rest.
func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 4 && e.Name()[:4] == "app-" {
			names = append(names, e.Name())
		}
	}
	// The timestamp in the name sorts chronologically, so a lexical sort is
	// a chronological one.
	if len(names) <= keep {
		return nil
	}
	sortStrings(names)

	for _, name := range names[:len(names)-keep] {
		if err := os.Remove(dir + "/" + name); err != nil {
			return err
		}
	}
	return nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// runHealthcheck is the `app healthcheck` subcommand used by Docker.
func runHealthcheck() int {
	addr := os.Getenv("APP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

// runSignLink is the `app sign-link` subcommand: given a client code and a
// salesperson login, it prints a valid, signed entry link path for manual
// testing, using ENTRY_LINK_SECRET and APP_BASE_URL from the environment
// exactly as the running server would validate them. The parent application
// is expected to sign links the same way - see README.md, "Entry link
// format" - this command exists only so a signed link can be produced
// without writing that code twice.
func runSignLink(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: app sign-link <9-digit-client-code> <saler-login> [ttl, e.g. 5m; default 5m]")
		return 2
	}
	code, login := args[0], args[1]

	ttl := 5 * time.Minute
	if len(args) >= 3 {
		d, err := time.ParseDuration(args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "sign-link: invalid ttl %q: %v\n", args[2], err)
			return 2
		}
		ttl = d
	}

	raw := strings.TrimSpace(os.Getenv("ENTRY_LINK_SECRET"))
	if raw == "" {
		fmt.Fprintln(os.Stderr, "sign-link: ENTRY_LINK_SECRET is not set in the environment")
		return 2
	}
	secret, err := decodeConfigBase64(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sign-link: ENTRY_LINK_SECRET is not valid base64: %v\n", err)
		return 2
	}

	if _, err := entrylink.Validate(code, login); err != nil {
		fmt.Fprintf(os.Stderr, "sign-link: %v\n", err)
		return 2
	}

	exp := strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	sig := hex.EncodeToString(entrylink.Sign(secret, code, login, exp))

	base := strings.TrimRight(strings.TrimSpace(os.Getenv("APP_BASE_URL")), "/")
	fmt.Printf("%s/r/%s/%s?exp=%s&sig=%s\n", base, code, login, exp, sig)
	fmt.Fprintf(os.Stderr, "(expires in %s)\n", ttl)
	return 0
}

// decodeConfigBase64 mirrors config.decodeBase64 (unexported there): it
// accepts standard or URL-safe base64, padded or not.
func decodeConfigBase64(s string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	}
	var err error
	for _, enc := range encodings {
		var b []byte
		if b, err = enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, err
}
