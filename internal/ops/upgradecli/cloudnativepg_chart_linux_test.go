//go:build linux

package upgradecli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCNPGChartImageRequiresLiteralSameRepositoryDigestAndStrictSingleValues(t *testing.T) {
	image := "ghcr.io/cloudnative-pg/postgresql:17.11-202610050823-standard-trixie@sha256:" + strings.Repeat("a", 64)
	values := func(value string) []byte {
		return []byte("postgres:\n  cloudNativePG:\n    imageName: " + value + "\n")
	}
	got, major, err := cnpgChartImage(context.Background(), values(image))
	if err != nil || got != image || major != 17 {
		t.Fatal("literal authenticated-chart operand parser rejected", err)
	}
	for _, bad := range []string{strings.Replace(image, "cloudnative-pg", "foreign", 1), strings.Split(image, "@")[0], strings.Replace(image, "sha256:", "sha512:", 1), strings.Replace(image, "17.11", "017.11", 1), strings.Replace(image, "standard-trixie", "minimal-trixie", 1)} {
		if _, _, err := cnpgChartImage(context.Background(), values(bad)); err == nil {
			t.Fatal("foreign/ambiguous operand admitted")
		}
	}
	for _, bad := range [][]byte{append(values(image), []byte("---\npostgres: {}\n")...), []byte("postgres: &p {}\nother: *p\n"), []byte("postgres: {}\npostgres: {}\n"), values("17.11"), make([]byte, (1<<20)+1)} {
		if _, _, err := cnpgChartImage(context.Background(), bad); err == nil {
			t.Fatal("ambiguous/unbounded chart values admitted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := cnpgChartImage(ctx, values(image)); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled chart parse lost identity", err)
	}
}
