package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func estateComparisonRequested(c *config) bool {
	return c.compareBaseline != "" || c.compareCandidate != "" || c.compareImages != ""
}
func registerEstateComparisonFlags(c *config) {
	flag.StringVar(&c.compareBaseline, "compare-estate-baseline", "", "offline baseline estate report JSON")
	flag.StringVar(&c.compareCandidate, "compare-estate-candidate", "", "offline candidate estate report JSON")
	flag.StringVar(&c.compareImages, "compare-estate-images", "", "optional offline image inventory review JSON")
}
func validateEstateComparisonFlags(c *config, flags *flag.FlagSet, env []string) error {
	invalid := c.compareBaseline == "" || c.compareCandidate == "" || c.outPath == "" || flags.NArg() != 0
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "compare-estate-baseline", "compare-estate-candidate", "compare-estate-images", "out":
		default:
			invalid = true
		}
	})
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "LOADTEST_") && key != "LOADTEST_OUT" && value != "" {
			invalid = true
		}
	}
	if invalid {
		return errors.New("mixed_or_incomplete_comparison_options")
	}
	return nil
}
func runEstateComparison(c *config) error {
	a, ah, err := readEstateComparisonReport(c.compareBaseline)
	if err != nil {
		return err
	}
	b, bh, err := readEstateComparisonReport(c.compareCandidate)
	if err != nil {
		return err
	}
	provenance, err := compareEstateImages(c.compareImages, a, b)
	if err != nil {
		return err
	}
	result := compareEstateReports(a, b, ah, bh, provenance)
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return errors.New("comparison_encoding_failed")
	}
	raw = append(raw, '\n')
	md := estateComparisonMarkdown(result)
	outputs := []string{c.outPath, c.outPath + ".json", c.outPath + ".sha256"}
	inputs := []string{c.compareBaseline, c.compareCandidate, c.compareImages}
	if c.compareImages != "" {
		reviewRaw, _, err := readEstateBounded(c.compareImages, 64<<10)
		if err != nil {
			return err
		}
		var review estateImageReview
		if decodeEstateStrict(reviewRaw, &review) != nil {
			return errors.New("invalid_image_review")
		}
		for _, p := range []string{review.Baseline, review.Candidate} {
			if !filepath.IsAbs(p) {
				p = filepath.Join(filepath.Dir(c.compareImages), p)
			}
			inputs = append(inputs, p)
		}
	}
	for _, output := range outputs {
		for _, input := range inputs {
			if input != "" && estateSameFile(output, input) {
				return errors.New("output_conflicts_with_input")
			}
		}
	}
	sums := []byte(fmt.Sprintf("%x  comparison.md\n%x  comparison.json\n", sha256.Sum256(md), sha256.Sum256(raw)))
	for i, data := range [][]byte{md, raw, sums} {
		if os.WriteFile(outputs[i], data, 0600) != nil {
			return errors.New("comparison_output_failed")
		}
	}
	return nil
}
func estateSameFile(a, b string) bool {
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	if aa == bb {
		return true
	}
	ai, ae := os.Stat(a)
	bi, be := os.Stat(b)
	return ae == nil && be == nil && os.SameFile(ai, bi)
}
func estateComparisonMarkdown(r estateComparison) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Offline estate comparison\n\nQualified: false. Evidence: report consistency only. Optimization criteria eligible: %t.\n\nBaseline SHA256: %s\n\nCandidate SHA256: %s\n\n", r.OptimizationEligible, r.BaselineSHA256, r.CandidateSHA256)
	b.WriteString("Sampled rates use first-to-last successful scrape intervals. Image and attribution review is user-declared, not live provenance. All workload counts below describe the whole phase.\n\n")
	for _, row := range r.Rows {
		fmt.Fprintf(&b, "## Phase %d / member %d\n\nPhase SHA256: %s\n\nMember SHA256: %s\n\nEligible: %t. Blockers: %s.\n\n", row.PhaseOrdinal, row.MemberOrdinal, row.PhaseSHA256, row.MemberSHA256, row.Eligible, strings.Join(row.Blockers, ", "))
		fmt.Fprintf(&b, "Work success/failed: %d/%d → %d/%d.\n\n", row.BaselineWork.Success, row.BaselineWork.Failed, row.CandidateWork.Success, row.CandidateWork.Failed)
		b.WriteString("| Category | Baseline requests/s | Candidate requests/s | Percent change | Status |\n|---|---:|---:|---:|---|\n")
		for _, m := range row.Metrics {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", m.Category, estateComparisonNumber(m.Baseline.RPS), estateComparisonNumber(m.Candidate.RPS), estateComparisonNumber(m.PercentChange), m.PercentageStatus)
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
func estateComparisonNumber(v *float64) string {
	if v == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%.6g", *v)
}
