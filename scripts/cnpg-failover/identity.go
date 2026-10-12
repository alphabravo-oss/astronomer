package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var databasePattern = regexp.MustCompile(`^plan032_w12_[a-z0-9_]+$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var uidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func validUID(value string) bool    { return uidPattern.MatchString(value) }
func validCommit(value string) bool { return commitPattern.MatchString(value) }

func readDatabaseRef(path string) (string, string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", "", errors.New("database reference must be a private regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", errors.New("cannot read database reference")
	}
	var ref struct {
		URL string `json:"database_url"`
	}
	if json.Unmarshal(data, &ref) != nil {
		return "", "", errors.New("invalid database reference")
	}
	parsed, err := url.Parse(ref.URL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" || parsed.Fragment != "" {
		return "", "", errors.New("invalid dedicated database URL")
	}
	name := strings.TrimPrefix(parsed.Path, "/")
	if !databasePattern.MatchString(name) {
		return "", "", errors.New("database URL must name a dedicated W12 database without search-path overrides")
	}
	for key := range parsed.Query() {
		switch key {
		case "sslmode", "sslrootcert", "connect_timeout", "application_name":
		default:
			return "", "", errors.New("database URL contains an unsupported connection override")
		}
	}
	return ref.URL, name, nil
}

type faultRecord struct {
	ClusterUID string    `json:"clusterUID"`
	PodUID     string    `json:"podUID"`
	Fault      string    `json:"fault"`
	At         time.Time `json:"at"`
}

func readFault(path, clusterUID string, started time.Time) (*faultRecord, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("fault marker unreadable")
	}
	var marker faultRecord
	if json.Unmarshal(data, &marker) != nil || marker.ClusterUID != clusterUID || !validUID(marker.PodUID) ||
		marker.Fault != "owned-primary-pod-deletion" || marker.At.Before(started) || marker.At.After(time.Now().Add(time.Second)) {
		return nil, errors.New("fault marker does not bind this owned cluster and run")
	}
	return &marker, nil
}
