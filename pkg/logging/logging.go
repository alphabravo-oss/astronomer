package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// Output formats accepted by Config.Format.
const (
	FormatJSON = "json"
	FormatText = "text"
)

// Config is the logger construction contract. Zero values select the
// production defaults: info level, JSON output.
type Config struct {
	// Level is a zerolog level name (trace, debug, info, warn, error, fatal,
	// panic, disabled). Empty means info.
	Level string
	// Format is "json" (default; required in production) or "text" (also
	// accepted as "console"), which renders through zerolog.ConsoleWriter for
	// local development.
	Format string
	// Component labels every line ("component" field), e.g. "server".
	Component string
}

func init() {
	// Process-wide encoder policy shared by every logger this package builds.
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.TimestampFunc = func() time.Time { return time.Now().UTC() }
	zerolog.DurationFieldUnit = time.Millisecond
	zerolog.ErrorMarshalFunc = marshalError
	fallback, _ := New(Config{})
	defaultLogger.Store(&fallback)
}

// New builds the process logger writing to stderr. Stdout stays reserved for
// tool output.
func New(cfg Config) (zerolog.Logger, error) {
	return NewWithWriter(os.Stderr, cfg)
}

// NewWithWriter builds a logger writing to w (tests capture output this way).
// The sink is wrapped in a zerolog.MultiLevelWriter behind the redaction
// filter; never pass a Logger itself as the io.Writer.
func NewWithWriter(w io.Writer, cfg Config) (zerolog.Logger, error) {
	if w == nil {
		return zerolog.Nop(), fmt.Errorf("logging: nil writer")
	}
	level, err := ParseLevel(cfg.Level)
	if err != nil {
		return zerolog.Nop(), err
	}
	sink := w
	switch format := strings.ToLower(strings.TrimSpace(cfg.Format)); format {
	case "", FormatJSON:
	case FormatText, "console":
		sink = zerolog.ConsoleWriter{Out: w, TimeFormat: time.RFC3339}
	default:
		return zerolog.Nop(), fmt.Errorf("logging: unsupported format %q (want json or text)", cfg.Format)
	}
	writer := zerolog.MultiLevelWriter(&redactingWriter{next: sink})
	builder := zerolog.New(writer).Level(level).Hook(contextHook{}).With().Timestamp()
	if component := strings.TrimSpace(cfg.Component); component != "" {
		builder = builder.Str(ComponentField, component)
	}
	return builder.Logger(), nil
}

// ParseLevel parses a level name; empty means info.
func ParseLevel(raw string) (zerolog.Level, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return zerolog.InfoLevel, nil
	}
	if raw == "warning" {
		raw = "warn"
	}
	level, err := zerolog.ParseLevel(raw)
	if err != nil {
		return zerolog.NoLevel, fmt.Errorf("logging: invalid level %q: %w", raw, err)
	}
	if level == zerolog.NoLevel {
		return zerolog.NoLevel, fmt.Errorf("logging: invalid level %q", raw)
	}
	return level, nil
}

var defaultLogger atomic.Pointer[zerolog.Logger]

// SetDefault installs logger as the process logger and routes the log/slog
// default (and therefore the standard "log" package) into it, so the process
// has exactly one encoder.
func SetDefault(logger zerolog.Logger) {
	defaultLogger.Store(&logger)
	slog.SetDefault(slog.New(SlogHandler(logger)))
}

// Default returns the process logger installed by SetDefault (a JSON stderr
// logger at info before that). The pointer allows Default().Info() chaining;
// callers must not mutate the shared logger.
func Default() *zerolog.Logger { return defaultLogger.Load() }

// Slog adapts a zerolog logger for APIs that still accept *slog.Logger.
func Slog(logger zerolog.Logger) *slog.Logger { return slog.New(SlogHandler(logger)) }

// Environment variables read by ConfigFromEnv.
const (
	LevelEnv  = "LOG_LEVEL"
	FormatEnv = "LOG_FORMAT"
)

// ConfigFromEnv reads LOG_LEVEL and LOG_FORMAT for binaries that do not load
// internal/config (agent, CLI subcommands). Unset values keep the defaults.
func ConfigFromEnv(component string) Config {
	return Config{Level: os.Getenv(LevelEnv), Format: os.Getenv(FormatEnv), Component: component}
}
