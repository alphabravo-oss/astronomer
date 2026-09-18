// Package redisconn owns the platform-wide Redis/Valkey connection contract.
// Every producer, consumer, cache, and coordination client must use this parser
// so Sentinel failover and authentication behave identically everywhere.
package redisconn

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/hibiken/asynq"
)

// Parse accepts Asynq's standard Redis URI forms and Astronomer's extended
// redis-sentinel form:
//
// redis-sentinel://[username:password@]s1:26379,s2:26379,s3:26379
//
//	?master=astronomer&db=0&sentinel_username=...&sentinel_password=...&tls=true
//
// Userinfo authenticates to the Valkey data nodes. Sentinel credentials may be
// supplied independently; when sentinel_password is absent it defaults to the
// data password, which is the chart-managed topology's secure default.
func Parse(raw string) (asynq.RedisConnOpt, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("parse Valkey connection URI: %w", err)
	}
	if u.Scheme != "redis-sentinel" {
		opt, parseErr := asynq.ParseRedisURI(raw)
		if parseErr != nil {
			return nil, fmt.Errorf("parse Valkey connection URI: %w", parseErr)
		}
		return opt, nil
	}
	if u.Host == "" {
		return nil, fmt.Errorf("parse Valkey Sentinel URI: at least one Sentinel address is required")
	}
	if strings.Trim(u.Path, "/") != "" {
		return nil, fmt.Errorf("parse Valkey Sentinel URI: use master and db query parameters, not path segments")
	}
	q := u.Query()
	master := strings.TrimSpace(q.Get("master"))
	if master == "" {
		return nil, fmt.Errorf("parse Valkey Sentinel URI: master query parameter is required")
	}
	db := 0
	if rawDB := q.Get("db"); rawDB != "" {
		db, err = strconv.Atoi(rawDB)
		if err != nil || db < 0 {
			return nil, fmt.Errorf("parse Valkey Sentinel URI: db must be a non-negative integer")
		}
	}
	addrs := strings.Split(u.Host, ",")
	for i := range addrs {
		addrs[i] = strings.TrimSpace(addrs[i])
		if addrs[i] == "" {
			return nil, fmt.Errorf("parse Valkey Sentinel URI: Sentinel addresses must not be empty")
		}
	}
	username := ""
	password := ""
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}
	if passwordEnv := strings.TrimSpace(q.Get("password_env")); passwordEnv != "" {
		if password != "" {
			return nil, fmt.Errorf("parse Valkey Sentinel URI: password_env cannot be combined with userinfo password")
		}
		var ok bool
		password, ok = os.LookupEnv(passwordEnv)
		if !ok || password == "" {
			return nil, fmt.Errorf("parse Valkey Sentinel URI: password environment variable %q is empty or unset", passwordEnv)
		}
	}
	sentinelPassword := q.Get("sentinel_password")
	if sentinelPasswordEnv := strings.TrimSpace(q.Get("sentinel_password_env")); sentinelPasswordEnv != "" {
		if sentinelPassword != "" {
			return nil, fmt.Errorf("parse Valkey Sentinel URI: sentinel_password_env cannot be combined with sentinel_password")
		}
		var ok bool
		sentinelPassword, ok = os.LookupEnv(sentinelPasswordEnv)
		if !ok || sentinelPassword == "" {
			return nil, fmt.Errorf("parse Valkey Sentinel URI: Sentinel password environment variable %q is empty or unset", sentinelPasswordEnv)
		}
	}
	if sentinelPassword == "" {
		sentinelPassword = password
	}
	opt := asynq.RedisFailoverClientOpt{
		MasterName:       master,
		SentinelAddrs:    addrs,
		SentinelUsername: q.Get("sentinel_username"),
		SentinelPassword: sentinelPassword,
		Username:         username,
		Password:         password,
		DB:               db,
	}
	if q.Get("tls") == "true" {
		serverName := strings.TrimSpace(q.Get("tls_server_name"))
		if serverName == "" {
			serverName, _, _ = net.SplitHostPort(addrs[0])
			if serverName == "" {
				serverName = addrs[0]
			}
		}
		opt.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	} else if rawTLS := q.Get("tls"); rawTLS != "" && rawTLS != "false" {
		return nil, fmt.Errorf("parse Valkey Sentinel URI: tls must be true or false")
	}
	return opt, nil
}
