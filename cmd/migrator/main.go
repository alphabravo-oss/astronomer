// Command migrator applies Astronomer database migrations while holding a
// database-scoped PostgreSQL session advisory lock. The outer lock is acquired
// before golang-migrate creates or updates schema_migrations, which prevents two
// release installers from deadlocking around non-transactional PostgreSQL DDL.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepostgres "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

const (
	defaultLockTimeout = 15 * time.Second
	tryLockSQL         = `SELECT pg_try_advisory_lock(hashtextextended(current_database(), 781930472366215))`
	unlockSQL          = `SELECT pg_advisory_unlock(hashtextextended(current_database(), 781930472366215))`
	lockPollInterval   = 100 * time.Millisecond
)

var buildVersion = "development"

type options struct {
	databaseURL string
	sourceURL   string
	command     string
	args        []string
	prefetch    uint
	lockTimeout time.Duration
	verbose     bool
}

type migrationLogger struct {
	w       io.Writer
	verbose bool
}

func (l migrationLogger) Printf(format string, args ...any) {
	_, _ = fmt.Fprintf(l.w, format, args...)
}

func (l migrationLogger) Verbose() bool { return l.verbose }

type lockedDatabase struct {
	db   *sql.DB
	conn *sql.Conn
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	opts, done, err := parseOptions(args, stdout, stderr)
	if err != nil || done {
		return err
	}

	if opts.databaseURL == "" {
		return errors.New("-database is required")
	}
	if opts.sourceURL == "" {
		return errors.New("-path or -source is required")
	}

	err = runMigration(ctx, opts, stdin, stdout, stderr)
	if err == nil {
		return nil
	}
	return errors.New(redactDatabaseCredentials(err.Error(), opts.databaseURL))
}

func parseOptions(args []string, stdout, stderr io.Writer) (options, bool, error) {
	var opts options
	var path string
	var lockTimeoutSeconds uint
	var help, version bool

	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.databaseURL, "database", "", "PostgreSQL migration database URL")
	flags.StringVar(&opts.sourceURL, "source", "", "migration source URL")
	flags.StringVar(&path, "path", "", "shorthand for -source=file://path")
	flags.UintVar(&opts.prefetch, "prefetch", 10, "number of migrations to prefetch")
	flags.UintVar(&lockTimeoutSeconds, "lock-timeout", uint(defaultLockTimeout/time.Second), "seconds to wait for migration locks")
	flags.BoolVar(&opts.verbose, "verbose", false, "enable verbose migration logs")
	flags.BoolVar(&help, "help", false, "print usage")
	flags.BoolVar(&version, "version", false, "print binary version")
	flags.Usage = func() { printUsage(stderr) }

	if err := flags.Parse(args); err != nil {
		return options{}, false, err
	}
	if help {
		printUsage(stdout)
		return options{}, true, nil
	}
	if version {
		_, _ = fmt.Fprintln(stdout, buildVersion)
		return options{}, true, nil
	}
	if opts.sourceURL != "" && path != "" {
		return options{}, false, errors.New("-source and -path are mutually exclusive")
	}
	if path != "" {
		opts.sourceURL = "file://" + path
	}
	if lockTimeoutSeconds > uint(math.MaxInt64/int64(time.Second)) {
		return options{}, false, errors.New("-lock-timeout is too large")
	}
	opts.lockTimeout = time.Duration(lockTimeoutSeconds) * time.Second

	remaining := flags.Args()
	if len(remaining) == 0 {
		printUsage(stderr)
		return options{}, false, errors.New("a migration command is required")
	}
	opts.command = remaining[0]
	opts.args = remaining[1:]
	return opts, false, nil
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage: migrate OPTIONS COMMAND [arg...]

Options:
  -source URL       Location of migrations
  -path PATH        Shorthand for -source=file://PATH
  -database URL     PostgreSQL database URL
  -prefetch N       Number of migrations to prefetch (default 10)
  -lock-timeout N   Seconds to wait for migration locks (default 15)
  -verbose          Enable verbose migration logs
  -version          Print binary version
  -help             Print usage

Commands:
  goto V            Migrate to version V
  up [N]            Apply all or N up migrations
  down N            Apply N down migrations
  down -all         Apply all down migrations after confirmation
  force V           Set version V without applying a migration
  version           Print current migration version
`)
}

func runMigration(ctx context.Context, opts options, stdin io.Reader, stdout, stderr io.Writer) error {
	postgresConfig, err := parsePostgresConfig(opts.databaseURL)
	if err != nil {
		return err
	}
	locked, err := openLockedDatabase(ctx, opts.databaseURL, opts.lockTimeout)
	if err != nil {
		return err
	}
	databaseDriver, err := migratepostgres.WithConnection(ctx, locked.conn, postgresConfig)
	if err != nil {
		return errors.Join(
			fmt.Errorf("initialize PostgreSQL migration driver: %w", err),
			cleanupLockedDatabase(locked, true),
		)
	}
	migrator, err := migrate.NewWithDatabaseInstance(opts.sourceURL, "postgres", databaseDriver)
	if err != nil {
		return errors.Join(
			fmt.Errorf("initialize migration engine: %w", err),
			cleanupMigrationDatabase(locked, databaseDriver.Close),
		)
	}
	migrator.Log = migrationLogger{w: stderr, verbose: opts.verbose}
	migrator.PrefetchMigrations = opts.prefetch
	migrator.LockTimeout = opts.lockTimeout

	stopGracefully := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			select {
			case migrator.GracefulStop <- true:
			case <-stopGracefully:
			}
		case <-stopGracefully:
		}
	}()

	runErr := executeCommand(migrator, opts.command, opts.args, stdin, stdout)
	close(stopGracefully)
	releaseErr := releaseSessionLock(locked.conn)
	sourceErr, databaseErr := migrator.Close()
	cleanupErr := errors.Join(
		releaseErr,
		sourceErr,
		databaseErr,
		locked.db.Close(),
	)
	return errors.Join(runErr, cleanupErr)
}

func openLockedDatabase(ctx context.Context, databaseURL string, timeout time.Duration) (*lockedDatabase, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse migration database URL: %w", err)
	}
	db, err := sql.Open("postgres", migrate.FilterCustomQuery(parsed).String())
	if err != nil {
		return nil, fmt.Errorf("open migration database: %w", err)
	}
	// WithConnection below makes this exact session the migration engine's
	// session. Keeping one physical connection means the outer advisory lock
	// cannot disappear before an interrupted migration transaction terminates.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	lockCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		lockCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	conn, err := db.Conn(lockCtx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to migration database: %w", err)
	}
	for {
		var acquired bool
		if err := conn.QueryRowContext(lockCtx, tryLockSQL).Scan(&acquired); err != nil {
			_ = conn.Close()
			_ = db.Close()
			return nil, fmt.Errorf("try migration session lock: %w", err)
		}
		if acquired {
			return &lockedDatabase{db: db, conn: conn}, nil
		}
		if timeout == 0 {
			_ = conn.Close()
			_ = db.Close()
			return nil, errors.New("migration session lock is already held")
		}

		timer := time.NewTimer(lockPollInterval)
		select {
		case <-lockCtx.Done():
			timer.Stop()
			_ = conn.Close()
			_ = db.Close()
			return nil, fmt.Errorf("acquire migration session lock within %s: %w", timeout, lockCtx.Err())
		case <-timer.C:
		}
	}
}

func releaseSessionLock(conn *sql.Conn) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var released bool
	if err := conn.QueryRowContext(ctx, unlockSQL).Scan(&released); err != nil {
		return fmt.Errorf("release migration session lock: %w", err)
	}
	if !released {
		return errors.New("migration session lock was not held during release")
	}
	return nil
}

func cleanupLockedDatabase(locked *lockedDatabase, unlock bool) error {
	var unlockErr error
	if unlock {
		unlockErr = releaseSessionLock(locked.conn)
	}
	return errors.Join(unlockErr, locked.conn.Close(), locked.db.Close())
}

func cleanupMigrationDatabase(locked *lockedDatabase, closeDriver func() error) error {
	return errors.Join(releaseSessionLock(locked.conn), closeDriver(), locked.db.Close())
}

func parsePostgresConfig(databaseURL string) (*migratepostgres.Config, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse migration database URL: %w", err)
	}
	query := parsed.Query()
	config := &migratepostgres.Config{
		MigrationsTable:       query.Get("x-migrations-table"),
		MultiStatementMaxSize: migratepostgres.DefaultMultiStatementMaxSize,
	}
	if value := query.Get("x-migrations-table-quoted"); value != "" {
		config.MigrationsTableQuoted, err = strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("parse x-migrations-table-quoted: %w", err)
		}
	}
	if config.MigrationsTableQuoted && (len(config.MigrationsTable) < 2 || config.MigrationsTable[0] != '"' || config.MigrationsTable[len(config.MigrationsTable)-1] != '"') {
		return nil, errors.New("x-migrations-table must be quoted when x-migrations-table-quoted is enabled")
	}
	if value := query.Get("x-statement-timeout"); value != "" {
		milliseconds, parseErr := strconv.Atoi(value)
		if parseErr != nil || milliseconds < 0 {
			return nil, errors.New("x-statement-timeout must be a non-negative integer in milliseconds")
		}
		config.StatementTimeout = time.Duration(milliseconds) * time.Millisecond
	}
	if value := query.Get("x-multi-statement"); value != "" {
		config.MultiStatementEnabled, err = strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("parse x-multi-statement: %w", err)
		}
	}
	if value := query.Get("x-multi-statement-max-size"); value != "" {
		config.MultiStatementMaxSize, err = strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("parse x-multi-statement-max-size: %w", err)
		}
		if config.MultiStatementMaxSize <= 0 {
			config.MultiStatementMaxSize = migratepostgres.DefaultMultiStatementMaxSize
		}
	}
	return config, nil
}

func executeCommand(migrator *migrate.Migrate, command string, args []string, stdin io.Reader, stdout io.Writer) error {
	var err error
	switch command {
	case "up":
		n, limited, parseErr := parseOptionalCount(args)
		if parseErr != nil {
			return fmt.Errorf("up: %w", parseErr)
		}
		if limited {
			err = migrator.Steps(n)
		} else {
			err = migrator.Up()
		}
	case "down":
		n, parseErr := parseDown(args, stdin, stdout)
		if parseErr != nil {
			return fmt.Errorf("down: %w", parseErr)
		}
		if n < 0 {
			err = migrator.Down()
		} else {
			err = migrator.Steps(-n)
		}
	case "goto":
		version, parseErr := parseUintArg("goto", args)
		if parseErr != nil {
			return parseErr
		}
		err = migrator.Migrate(uint(version))
	case "force":
		version, parseErr := parseForceVersion(args)
		if parseErr != nil {
			return parseErr
		}
		err = migrator.Force(version)
	case "version":
		if len(args) != 0 {
			return errors.New("version does not accept arguments")
		}
		version, dirty, versionErr := migrator.Version()
		if versionErr != nil {
			return versionErr
		}
		if dirty {
			_, err = fmt.Fprintf(stdout, "%d (dirty)\n", version)
		} else {
			_, err = fmt.Fprintln(stdout, version)
		}
	default:
		return fmt.Errorf("unsupported migration command %q", command)
	}
	if errors.Is(err, migrate.ErrNoChange) {
		_, _ = fmt.Fprintln(stdout, migrate.ErrNoChange)
		return nil
	}
	return err
}

func parseOptionalCount(args []string) (int, bool, error) {
	if len(args) == 0 {
		return 0, false, nil
	}
	if len(args) != 1 {
		return 0, false, errors.New("expected at most one migration count")
	}
	n, err := strconv.ParseInt(args[0], 10, 32)
	if err != nil || n < 0 {
		return 0, false, errors.New("migration count must be a non-negative integer")
	}
	return int(n), true, nil
}

func parseDown(args []string, stdin io.Reader, stdout io.Writer) (int, error) {
	if len(args) == 1 && args[0] == "-all" {
		_, _ = fmt.Fprint(stdout, "Apply all down migrations? [y/N] ")
		answer, _ := bufio.NewReader(stdin).ReadString('\n')
		if strings.ToLower(strings.TrimSpace(answer)) != "y" {
			return 0, errors.New("all down migrations were not confirmed")
		}
		return -1, nil
	}
	n, limited, err := parseOptionalCount(args)
	if err != nil {
		return 0, err
	}
	if !limited {
		return 0, errors.New("a count or explicit -all confirmation is required")
	}
	return n, nil
}

func parseUintArg(command string, args []string) (uint64, error) {
	if len(args) != 1 {
		return 0, fmt.Errorf("%s requires exactly one version", command)
	}
	version, err := strconv.ParseUint(args[0], 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s version must be a non-negative 32-bit integer", command)
	}
	return version, nil
}

func parseForceVersion(args []string) (int, error) {
	if len(args) != 1 {
		return 0, errors.New("force requires exactly one version")
	}
	version, err := strconv.ParseInt(args[0], 10, 32)
	if err != nil || version < -1 {
		return 0, errors.New("force version must be an integer greater than or equal to -1")
	}
	return int(version), nil
}

func redactDatabaseCredentials(message, databaseURL string) string {
	redacted := strings.ReplaceAll(message, databaseURL, "[redacted database URL]")
	parsed, err := url.Parse(databaseURL)
	if err != nil || parsed.User == nil {
		return redacted
	}
	if password, ok := parsed.User.Password(); ok && password != "" {
		redacted = strings.ReplaceAll(redacted, password, "[redacted]")
		redacted = strings.ReplaceAll(redacted, url.QueryEscape(password), "[redacted]")
	}
	return redacted
}
