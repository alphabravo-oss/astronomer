package userpreferences

import (
	"fmt"
	"regexp"
)

const MaxStarredTypes = 20

var starredTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*/[a-z0-9][a-z0-9-]*$`)

func validateStarredTypes(types []string) error {
	if len(types) > MaxStarredTypes {
		return fmt.Errorf("starred_types may contain at most %d resource types", MaxStarredTypes)
	}
	seen := make(map[string]struct{}, len(types))
	for _, value := range types {
		if len(value) > 317 || !starredTypePattern.MatchString(value) {
			return fmt.Errorf("starred resource type %q must be group/plural (core/plural for core resources)", value)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("starred resource type %q is duplicated", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}
