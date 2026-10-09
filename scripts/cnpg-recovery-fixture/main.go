// cnpg-recovery-fixture seeds/reads only an owned migrated qualification DB.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db"
	"github.com/google/uuid"
)

type tenant struct {
	Cluster, Project, User, Repository, Connection, Source, Bundle, Version, Target, Deployment uuid.UUID
	CredentialDigest, TrustDigest                                                               string
	PublicTrust                                                                                 publicTrust
}

type manifest struct {
	SchemaVersion int       `json:"schemaVersion"`
	Owner         string    `json:"owner"`
	SourceCommit  string    `json:"sourceCommit"`
	RunID         uuid.UUID `json:"runID"`
	Migration     int       `json:"migration"`
	KeyRefDigest  string    `json:"keyRefDigest"`
	KeyIDs        []string  `json:"keyIDs"`
	Tenants       []tenant  `json:"tenants"`
	AuditDigests  []string  `json:"auditDigests"`
	Limits        []string  `json:"limits"`
}

var databaseName = regexp.MustCompile(`^plan032_w12_recovery[a-z0-9_]*$`)

func decodeClosed(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("invalid closed fixture document")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing fixture document")
	}
	return nil
}

func privateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("private regular reference required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("private reference unreadable")
	}
	return data, nil
}

func databaseURL(path string) (string, string, error) {
	data, err := privateFile(path)
	if err != nil {
		return "", "", err
	}
	var ref struct {
		URL string `json:"database_url"`
	}
	if decodeClosed(data, &ref) != nil {
		return "", "", errors.New("invalid DB reference")
	}
	u, err := url.Parse(ref.URL)
	if err != nil || u.Host == "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Fragment != "" || !strings.HasPrefix(u.Path, "/") || !databaseName.MatchString(strings.TrimPrefix(u.Path, "/")) {
		return "", "", errors.New("dedicated recovery database required")
	}
	for key := range u.Query() {
		switch key {
		case "sslmode", "sslrootcert", "connect_timeout", "application_name":
		default:
			return "", "", errors.New("connection override rejected")
		}
	}
	return ref.URL, strings.TrimPrefix(u.Path, "/"), nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "cnpg-recovery-fixture: failed owned identity, credential or data invariant")
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "", "seed or verify")
	ref := flag.String("database-ref", "", "0600 dedicated database reference")
	keyRef := flag.String("key-ref", "", "independently recoverable 0600 disposable key/token reference")
	manifestPath := flag.String("manifest", "", "nonsecret expected manifest file")
	commit := flag.String("source-commit", "", "reviewed full source commit")
	negative := flag.String("negative-key-mode", "", "missing-old or wrong; expected to fail verification")
	flag.Parse()
	if (*mode != "seed" && *mode != "verify") || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(*commit) || *manifestPath == "" || *keyRef == "" {
		return errors.New("invalid invocation")
	}
	if *negative != "" && (*mode != "verify" || (*negative != "missing-old" && *negative != "wrong")) {
		return errors.New("invalid negative mode")
	}
	url, name, err := databaseURL(*ref)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	connection, err := db.ConnectWithConfig(ctx, url, db.PoolConfig{MaxConns: 2, MinConns: 1})
	if err != nil {
		return errors.New("database connection failed")
	}
	defer connection.Close()
	var actual string
	if err := connection.Pool().QueryRow(ctx, "SELECT current_database()").Scan(&actual); err != nil || actual != name {
		return errors.New("dedicated DB mismatch")
	}
	if err := connection.SchemaHealth(ctx); err != nil {
		return errors.New("migration state unhealthy")
	}
	var schema int
	if err := connection.Pool().QueryRow(ctx, "SELECT version FROM schema_migrations WHERE NOT dirty").Scan(&schema); err != nil || schema != 72 {
		return errors.New("full schema72 is required")
	}
	if *mode == "seed" {
		file, err := os.OpenFile(*manifestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return errors.New("manifest must be new")
		}
		defer file.Close()
		m, err := seed(ctx, connection.Pool(), *keyRef, *commit)
		if err != nil {
			return err
		}
		data, _ := json.MarshalIndent(m, "", "  ")
		if _, err := file.Write(append(data, '\n')); err != nil {
			return errors.New("manifest write failed")
		}
		fmt.Println("owned recovery fixture seeded; keys and credentials remain in protected reference")
		return nil
	}
	data, err := os.ReadFile(*manifestPath)
	if err != nil {
		return errors.New("manifest unavailable")
	}
	var expected manifest
	if decodeClosed(data, &expected) != nil || expected.SchemaVersion != 1 || expected.Owner != "Plan032 W12" || expected.SourceCommit != *commit || expected.RunID == uuid.Nil || len(expected.Tenants) != 2 {
		return errors.New("unbound expected manifest")
	}
	if err := verify(ctx, connection.Pool(), *keyRef, *negative, expected); err != nil {
		return err
	}
	fmt.Println("restored credentials, Charlie TLS keys, agent scope, tenants, audit intents and delivery generations verified")
	return nil
}
