package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const estateCompareReportLimit = 32 << 20

// The preliminary token pass rejects duplicate keys, oversized tokens and
// excessive nesting before typed decoding. Diagnostics never echo input text.
func decodeEstateStrict(raw []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	budget := 1000000
	if err := checkEstateJSONValue(decoder, 0, &budget); err != nil {
		return errors.New("invalid_json_structure")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing_json")
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return errors.New("invalid_report_schema")
	}
	return nil
}
func checkEstateJSONValue(d *json.Decoder, depth int, budget *int) error {
	*budget--
	if depth > 32 || *budget < 0 {
		return errors.New("json_bound")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	if s, ok := token.(string); ok && len(s) > 4096 {
		return errors.New("string_bound")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			token, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || len(key) > 1024 || seen[key] {
				return errors.New("duplicate_or_invalid_key")
			}
			seen[key] = true
			if err := checkEstateJSONValue(d, depth+1, budget); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := checkEstateJSONValue(d, depth+1, budget); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected_delimiter")
	}
	_, err = d.Token()
	return err
}
func readEstateBounded(path string, limit int64) ([]byte, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", errors.New("input_unreadable")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, "", errors.New("input_unreadable")
	}
	if int64(len(raw)) > limit {
		return nil, "", errors.New("input_too_large")
	}
	return raw, fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}
func readEstateComparisonReport(path string) (estateReport, string, error) {
	var r estateReport
	raw, digest, err := readEstateBounded(path, estateCompareReportLimit)
	if err != nil {
		return r, "", err
	}
	if err = decodeEstateStrict(raw, &r); err != nil {
		return r, "", err
	}
	if err = validateEstateComparisonReport(r); err != nil {
		return r, "", err
	}
	return r, digest, nil
}
