// Command otel-pgx-qualification exercises the production pgx tracer closure
// against one explicitly owned database and a real loopback OTLP receiver.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
)

const ownedDatabase = "plan032_w10_callbacks"
const secretProbe = "synthetic-sensitive-bind-must-not-export"

type databaseReference struct {
	URL string `json:"database_url"`
}
type receipt struct {
	SchemaVersion      int            `json:"schemaVersion"`
	Status             string         `json:"status"`
	Database           string         `json:"database"`
	ServerVersionNum   int            `json:"serverVersionNum"`
	ServerMajor        int            `json:"serverMajor"`
	Callbacks          map[string]int `json:"actualOTelSpans"`
	Rows               int            `json:"committedRows"`
	ParameterRedaction bool           `json:"boundParameterAndCredentialAbsent"`
	TraceConnected     bool           `json:"spansShareRootTrace"`
	Scope              string         `json:"scope"`
}

type capture struct {
	mu       sync.Mutex
	requests []*collector.ExportTraceServiceRequest
}

func (c *capture) handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/traces" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	request := new(collector.ExportTraceServiceRequest)
	if proto.Unmarshal(body, request) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	c.requests = append(c.requests, request)
	c.mu.Unlock()
	w.Header().Set("Content-Type", "application/x-protobuf")
}

func readReference(path string) (string, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", errors.New("protected database reference unavailable")
	}
	f := os.NewFile(uintptr(fd), "database reference")
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 {
		return "", errors.New("database reference must be a private regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(data) > 16384 {
		return "", errors.New("database reference exceeds bound")
	}
	var ref databaseReference
	if json.Unmarshal(data, &ref) != nil {
		return "", errors.New("database reference is malformed")
	}
	parsed, err := url.Parse(ref.URL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" || parsed.Path != "/"+ownedDatabase || parsed.Fragment != "" {
		return "", errors.New("database reference must identify exact dedicated W10 database")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", errors.New("database connection options are malformed")
	}
	for key, values := range query {
		if len(values) != 1 {
			return "", errors.New("duplicate database connection options are forbidden")
		}
		switch key {
		case "sslmode", "sslrootcert", "connect_timeout", "application_name":
		default:
			return "", errors.New("database connection overrides are forbidden")
		}
	}
	return ref.URL, nil
}

func requireServerVersion(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n/10000 != 18 {
		return 0, errors.New("qualification requires actual PostgreSQL major 18")
	}
	return n, nil
}

func run(ctx context.Context, connection string) (*receipt, error) {
	captures := new(capture)
	server := httptest.NewServer(http.HandlerFunc(captures.handler))
	defer server.Close()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	shutdown, err := observability.InitTracing(ctx, logger, observability.TracingConfig{Endpoint: server.URL, SamplerRatio: 1, ServiceName: "w10-pgx-owned-qualification", Environment: "owned"})
	if err != nil {
		return nil, errors.New("tracing initialization failed")
	}
	defer func() {
		budget, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = shutdown(budget)
	}()
	ctx, rootSpan := otel.Tracer("owned-pgx").Start(ctx, "owned.database.callbacks")
	defer rootSpan.End()
	database, err := db.ConnectWithConfig(ctx, connection, db.PoolConfig{MaxConns: 1, MinConns: 1, HealthCheckPeriod: time.Hour})
	if err != nil {
		return nil, errors.New("dedicated database connection failed")
	}
	defer database.Close()
	var actual, version string
	if database.Pool().QueryRow(ctx, "SELECT current_database(), current_setting('server_version_num')").Scan(&actual, &version) != nil || actual != ownedDatabase {
		return nil, errors.New("database ownership fence failed")
	}
	versionNum, err := requireServerVersion(version)
	if err != nil {
		return nil, err
	}
	// Exercise Connect with the exact production ConnConfig/Tracer, without
	// claiming that every background pool connection inherits a sampled parent.
	direct, err := pgx.ConnectConfig(ctx, database.Pool().Config().ConnConfig.Copy())
	if err != nil {
		return nil, errors.New("production connection callback failed")
	}
	defer direct.Close(ctx)
	if _, err = database.Pool().Exec(ctx, "CREATE TABLE IF NOT EXISTS callback_rows (id integer PRIMARY KEY, value text NOT NULL)"); err != nil {
		return nil, errors.New("owned fixture table creation failed")
	}
	if _, err = database.Pool().Exec(ctx, "TRUNCATE callback_rows"); err != nil {
		return nil, errors.New("owned fixture reset failed")
	}
	conn, err := database.Pool().Acquire(ctx)
	if err != nil {
		return nil, errors.New("production pool acquire failed")
	}
	if _, err = conn.Conn().Prepare(ctx, "w10_callback_prepare", "SELECT $1::integer"); err != nil {
		conn.Release()
		return nil, errors.New("production prepare callback failed")
	}
	conn.Release()
	batch := new(pgx.Batch)
	batch.Queue("INSERT INTO callback_rows (id,value) VALUES ($1,$2)", 1, secretProbe)
	batch.Queue("INSERT INTO callback_rows (id,value) VALUES ($1,$2)", 2, secretProbe)
	result := database.Pool().SendBatch(ctx, batch)
	for range 2 {
		if _, err = result.Exec(); err != nil {
			_ = result.Close()
			return nil, errors.New("production batch query failed")
		}
	}
	if result.Close() != nil {
		return nil, errors.New("production batch completion failed")
	}
	var rows int
	if database.Pool().QueryRow(ctx, "SELECT count(*) FROM callback_rows").Scan(&rows) != nil || rows != 2 {
		return nil, errors.New("committed fixture rows differ")
	}
	rootSpan.End()
	budget, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = shutdown(budget); err != nil {
		return nil, errors.New("real OTLP shutdown flush failed")
	}
	return validateCapture(captures, trace.SpanContextFromContext(ctx).TraceID(), connection, rows, versionNum)
}

func validateCapture(c *capture, root trace.TraceID, connection string, rows, versionNum int) (*receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	counts := map[string]int{"connect": 0, "prepare": 0, "batch": 0, "batchQuery": 0, "acquire": 0, "query": 0}
	parsed, err := url.Parse(connection)
	if err != nil {
		return nil, errors.New("database connection is malformed")
	}
	var password string
	if parsed.User != nil {
		password, _ = parsed.User.Password()
	}
	connected := true
	batchIDs := map[string]bool{}
	for _, request := range c.requests {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.Name == "batch start" {
						batchIDs[string(span.SpanId)] = true
					}
				}
			}
		}
	}
	for _, request := range c.requests {
		raw, _ := proto.Marshal(request)
		if bytes.Contains(raw, []byte(secretProbe)) || bytes.Contains(raw, []byte(connection)) || (password != "" && bytes.Contains(raw, []byte(password))) {
			return nil, errors.New("export contains bound parameter or credential")
		}
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if !bytes.Equal(span.TraceId, root[:]) {
						connected = false
					}
					switch span.Name {
					case "connect":
						counts["connect"]++
					case "pool.acquire":
						counts["acquire"]++
					case "batch start":
						counts["batch"]++
					default:
						if scope.Scope.Name == "github.com/exaring/otelpgx" {
							counts["query"]++
						}
					}
					for _, attr := range span.Attributes {
						if attr.Key == "pgx.prepare_stmt.name" && attr.Value.GetStringValue() == "w10_callback_prepare" {
							counts["prepare"]++
						}
					}
					if span.Name == "INSERT" && batchIDs[string(span.ParentSpanId)] {
						counts["batchQuery"]++
					}
				}
			}
		}
	}
	for _, name := range []string{"connect", "prepare", "batch", "acquire", "query"} {
		if counts[name] == 0 {
			return nil, fmt.Errorf("actual %s callback span missing", name)
		}
	}
	if counts["batchQuery"] < 2 || !connected {
		return nil, errors.New("batch query or root trace linkage missing")
	}
	return &receipt{SchemaVersion: 1, Status: "pass", Database: ownedDatabase, ServerVersionNum: versionNum, ServerMajor: versionNum / 10000, Callbacks: counts, Rows: rows, ParameterRedaction: true, TraceConnected: true, Scope: "Real production pgx tracer closure on dedicated owned CNPG18 database and loopback OTLP protobuf receiver; not whole application/queue/tunnel/collector-stack qualification."}, nil
}

func main() {
	reference := flag.String("database-ref", "", "private database reference")
	out := flag.String("out", "", "new sanitized receipt path")
	flag.Parse()
	if *reference == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "database-ref and out are required")
		os.Exit(2)
	}
	connection, err := readReference(*reference)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	proof, err := run(ctx, connection)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	raw, err := json.MarshalIndent(proof, "", "  ")
	if err != nil {
		os.Exit(1)
	}
	fd, err := unix.Open(*out, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "new receipt path unavailable")
		os.Exit(1)
	}
	file := os.NewFile(uintptr(fd), "receipt")
	_, err = file.Write(append(raw, '\n'))
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		fmt.Fprintln(os.Stderr, "receipt write failed")
		os.Exit(1)
	}
	fmt.Println("Owned production pgx callback qualification passed; sanitized receipt written.")
}
