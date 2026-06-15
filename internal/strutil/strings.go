package strutil

import "strings"

// FirstNonBlank returns the first value whose trimmed form is not empty,
// preserving the original value.
func FirstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// FirstNonBlankTrimmed returns the trimmed form of the first non-blank value.
func FirstNonBlankTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
