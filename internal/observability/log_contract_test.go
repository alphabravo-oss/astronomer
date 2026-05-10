package observability

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var forbiddenPrintPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bfmt\.Print(f|ln)?\s*\(`),
	regexp.MustCompile(`\blog\.Print(f|ln)?\s*\(`),
}

func TestNoLegacyPrintCallsInBusinessCode(t *testing.T) {
	roots := []string{"../../cmd", "../../internal"}

	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".go" || regexp.MustCompile(`_test\.go$`).MatchString(path) {
				return nil
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, pattern := range forbiddenPrintPatterns {
				if pattern.Match(data) {
					t.Errorf("%s contains forbidden print call matching %s", path, pattern.String())
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
}
