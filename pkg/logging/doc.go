// Package logging is the single structured-logging boundary for every
// Astronomer binary (Technology Selection Guide ch.05).
//
// Every binary constructs one zerolog.Logger through New (stderr) or
// NewWithWriter, installs it with SetDefault, and from then on:
//
//   - zerolog call sites log through the returned Logger (or Default());
//   - log/slog call sites keep compiling and route into the same zerolog
//     encoder through SlogHandler, which SetDefault installs as the slog
//     default handler — there is exactly one encoder and one redaction
//     boundary per process;
//   - request-scoped code derives a logger with Ctx(ctx, base), which adds
//     correlation_id (and trace_id when a span is active).
//
// Output is one JSON object per line (format "json", the production default)
// or zerolog.ConsoleWriter text for local development (format "text").
// Redaction is applied to the encoded JSON line before it reaches the sink,
// so it covers zerolog and slog call sites alike: sensitive keys are replaced
// with "[REDACTED]" (whole subtree) and credential-shaped substrings in every
// string value are scrubbed.
package logging
