package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScrapeOnceCapturesProcessLeakMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`# TYPE go_goroutines gauge
go_goroutines 41
# TYPE go_memstats_alloc_bytes gauge
go_memstats_alloc_bytes 1048576
# TYPE process_open_fds gauge
process_open_fds 19
`))
	}))
	defer server.Close()

	recorder := newRecorder()
	if err := scrapeOnce(context.Background(), server.URL, "", recorder); err != nil {
		t.Fatal(err)
	}
	for metric, want := range map[string]float64{
		"server_goroutines": 41,
		"server_heap_bytes": 1048576,
		"server_open_fds":   19,
	} {
		if got := lastValue(recorder.scrapeSeries[metric]); got != want {
			t.Fatalf("%s = %v, want %v", metric, got, want)
		}
	}
}

func TestLeakWindowAveragesExcludeRampUp(t *testing.T) {
	series := points(1, 2, 10, 10, 10, 10, 11, 11)
	baseline, terminal, ok := leakWindowAverages(series)
	if !ok || baseline != 10 || terminal != 11 {
		t.Fatalf("baseline=%v terminal=%v ok=%t", baseline, terminal, ok)
	}
}

func points(values ...float64) []scrapePoint {
	out := make([]scrapePoint, 0, len(values))
	for _, value := range values {
		out = append(out, scrapePoint{value: value})
	}
	return out
}
