package scanner

import (
	"errors"
	"math"
	"strings"

	"github.com/google/uuid"
)

type reportSummaryCounts struct {
	critical, high, medium, low, unknown int32
}

func checkedIngestReportSummary(ingester *Ingester, clusterID uuid.UUID, raw TrivyVulnerabilityReport) (reportSummaryCounts, error) {
	if ingester == nil {
		return reportSummaryCounts{}, errors.New("scanner: ingester is nil")
	}
	if clusterID == uuid.Nil {
		return reportSummaryCounts{}, errors.New("scanner: cluster_id is required")
	}
	if strings.TrimSpace(raw.Metadata.Name) == "" {
		return reportSummaryCounts{}, errors.New("scanner: report metadata.name is required")
	}
	return checkedReportSummary(raw.Report.Summary)
}

// Preserve the upstream aggregate even when individual CVE rows are omitted.
// Validate all counts before beginning a transaction or recording an outcome.
func checkedReportSummary(summary TrivySummary) (reportSummaryCounts, error) {
	values := [...]int{summary.CriticalCount, summary.HighCount, summary.MediumCount, summary.LowCount, summary.UnknownCount}
	var checked [5]int32
	for index, value := range values {
		if value < 0 || value > math.MaxInt32 {
			return reportSummaryCounts{}, errors.New("scanner: report summary counts must be nonnegative int32 values")
		}
		checked[index] = int32(value)
	}
	return reportSummaryCounts{checked[0], checked[1], checked[2], checked[3], checked[4]}, nil
}
