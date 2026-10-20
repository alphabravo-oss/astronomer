package logging

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"github.com/rs/zerolog"
)

// SlogHandler returns a slog.Handler that encodes through logger, so slog
// call sites share the zerolog encoder and redaction boundary.
//
// Groups flatten to dotted keys ("group.key"). Structured values without a
// field policy are omitted (errors, times, durations, levels, fmt.Stringers
// and []string are kept), a sensitive group name redacts its whole group,
// and the slog context propagates correlation_id/trace_id.
func SlogHandler(logger zerolog.Logger) slog.Handler {
	return &slogHandler{logger: logger}
}

type slogHandler struct {
	logger zerolog.Logger
	// prefix is the dotted WithGroup path applied to later attrs.
	prefix string
	// attrs were attached by WithAttrs; keys already carry their prefix.
	attrs []slog.Attr
	// redactAll is set once a WithGroup name is sensitive.
	redactAll bool
}

func (h *slogHandler) Enabled(_ context.Context, level slog.Level) bool {
	zl := zerologLevel(level)
	return zl >= h.logger.GetLevel() && zl >= zerolog.GlobalLevel()
}

func (h *slogHandler) Handle(ctx context.Context, record slog.Record) error {
	event := h.logger.WithLevel(zerologLevel(record.Level))
	if event == nil {
		return nil
	}
	if ctx != nil {
		event = event.Ctx(ctx)
	}
	for _, attr := range h.attrs {
		appendAttr(event, attr, "")
	}
	record.Attrs(func(attr slog.Attr) bool {
		appendAttr(event, h.sanitize(attr), h.prefix)
		return true
	})
	event.Msg(record.Message)
	return nil
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	clone := *h
	clone.attrs = make([]slog.Attr, len(h.attrs), len(h.attrs)+len(attrs))
	copy(clone.attrs, h.attrs)
	for _, attr := range attrs {
		attr = h.sanitize(attr)
		attr.Key = joinKey(h.prefix, attr.Key)
		clone.attrs = append(clone.attrs, attr)
	}
	return &clone
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.prefix = joinKey(h.prefix, name)
	clone.redactAll = h.redactAll || SensitiveKey(name)
	return &clone
}

func (h *slogHandler) sanitize(attr slog.Attr) slog.Attr {
	if h.redactAll {
		return slog.String(attr.Key, Redacted)
	}
	return sanitizeAttr(attr)
}

func zerologLevel(level slog.Level) zerolog.Level {
	switch {
	case level < slog.LevelDebug:
		return zerolog.TraceLevel
	case level < slog.LevelInfo:
		return zerolog.DebugLevel
	case level < slog.LevelWarn:
		return zerolog.InfoLevel
	case level < slog.LevelError:
		return zerolog.WarnLevel
	default:
		return zerolog.ErrorLevel
	}
}

func joinKey(prefix, key string) string {
	switch {
	case prefix == "":
		return key
	case key == "":
		return prefix
	default:
		return prefix + "." + key
	}
}

func appendAttr(event *zerolog.Event, attr slog.Attr, prefix string) {
	value := attr.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		groupPrefix := joinKey(prefix, attr.Key)
		for _, member := range value.Group() {
			appendAttr(event, member, groupPrefix)
		}
		return
	}
	if attr.Key == "" {
		return
	}
	key := joinKey(prefix, attr.Key)
	switch value.Kind() {
	case slog.KindString:
		event.Str(key, value.String())
	case slog.KindInt64:
		event.Int64(key, value.Int64())
	case slog.KindUint64:
		event.Uint64(key, value.Uint64())
	case slog.KindFloat64:
		event.Float64(key, value.Float64())
	case slog.KindBool:
		event.Bool(key, value.Bool())
	case slog.KindDuration:
		event.Dur(key, value.Duration())
	case slog.KindTime:
		event.Time(key, value.Time())
	default:
		appendAny(event, key, value.Any())
	}
}

func appendAny(event *zerolog.Event, key string, value any) {
	switch typed := value.(type) {
	case error:
		event.AnErr(key, typed)
	case time.Duration:
		event.Dur(key, typed)
	case time.Time:
		event.Time(key, typed)
	case []string:
		event.Strs(key, typed)
	default:
		event.Str(key, omittedStructured)
	}
}

func sanitizeAttr(attr slog.Attr) slog.Attr {
	attr.Value = attr.Value.Resolve()
	if SensitiveKey(attr.Key) {
		return slog.String(attr.Key, Redacted)
	}
	switch attr.Value.Kind() {
	case slog.KindGroup:
		group := attr.Value.Group()
		clean := make([]slog.Attr, len(group))
		for i, member := range group {
			clean[i] = sanitizeAttr(member)
		}
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(clean...)}
	case slog.KindAny:
		return slog.Attr{Key: attr.Key, Value: sanitizeAny(attr.Value.Any())}
	default:
		return attr
	}
}

func sanitizeAny(value any) slog.Value {
	switch typed := value.(type) {
	case nil:
		return slog.StringValue("")
	case error:
		if isNilPointer(typed) {
			return slog.StringValue("")
		}
		return slog.AnyValue(typed)
	case slog.Level:
		return slog.StringValue(typed.String())
	case zerolog.Level:
		return slog.StringValue(typed.String())
	case time.Time, time.Duration, []string:
		return slog.AnyValue(typed)
	case fmt.Stringer:
		if isNilPointer(typed) {
			return slog.StringValue("")
		}
		return slog.StringValue(typed.String())
	default:
		return slog.StringValue(omittedStructured)
	}
}

func isNilPointer(value any) bool {
	reflected := reflect.ValueOf(value)
	return reflected.Kind() == reflect.Pointer && reflected.IsNil()
}
