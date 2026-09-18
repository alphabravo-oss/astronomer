package redisconn

import (
	"crypto/tls"
	"testing"

	"github.com/hibiken/asynq"
)

func TestParseSentinelFullContract(t *testing.T) {
	opt, err := Parse("redis-sentinel://app:data-secret@s1:26379,s2:26379,s3:26379?master=astronomer&db=4&sentinel_username=sentinel&sentinel_password=sentinel-secret&tls=true&tls_server_name=valkey.internal")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := opt.(asynq.RedisFailoverClientOpt)
	if !ok {
		t.Fatalf("option type = %T, want RedisFailoverClientOpt", opt)
	}
	if got.MasterName != "astronomer" || got.DB != 4 || got.Username != "app" || got.Password != "data-secret" || got.SentinelUsername != "sentinel" || got.SentinelPassword != "sentinel-secret" {
		t.Fatalf("parsed Sentinel option = %#v", got)
	}
	if len(got.SentinelAddrs) != 3 || got.TLSConfig == nil || got.TLSConfig.MinVersion != tls.VersionTLS12 || got.TLSConfig.ServerName != "valkey.internal" {
		t.Fatalf("parsed Sentinel transport = %#v", got)
	}
}

func TestParseSentinelDefaultsSentinelPasswordToDataPassword(t *testing.T) {
	opt, err := Parse("redis-sentinel://:shared-secret@s1:26379,s2:26379,s3:26379?master=astronomer")
	if err != nil {
		t.Fatal(err)
	}
	got := opt.(asynq.RedisFailoverClientOpt)
	if got.Password != "shared-secret" || got.SentinelPassword != "shared-secret" {
		t.Fatalf("passwords = data %q Sentinel %q", got.Password, got.SentinelPassword)
	}
}

func TestParseSentinelLoadsURLUnsafePasswordFromEnvironment(t *testing.T) {
	t.Setenv("TEST_VALKEY_PASSWORD", "plus+/slash&colon:at@password")
	opt, err := Parse("redis-sentinel://s1:26379?master=astronomer&password_env=TEST_VALKEY_PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	got := opt.(asynq.RedisFailoverClientOpt)
	if got.Password != "plus+/slash&colon:at@password" || got.SentinelPassword != got.Password {
		t.Fatalf("passwords = data %q Sentinel %q", got.Password, got.SentinelPassword)
	}
}

func TestParseSentinelRejectsMisleadingLegacyAndIncompleteForms(t *testing.T) {
	for _, raw := range []string{
		"redis-sentinel://s1:26379/mymaster/0",
		"redis-sentinel://s1:26379?db=0",
		"redis-sentinel://?master=astronomer",
		"redis-sentinel://s1:26379?master=astronomer&db=-1",
	} {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("Parse(%q) succeeded, want error", raw)
		}
	}
}

func TestParsePreservesStandardAsynqURIs(t *testing.T) {
	if _, err := Parse("redis://localhost:6379/2"); err != nil {
		t.Fatal(err)
	}
}
