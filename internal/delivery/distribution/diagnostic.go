package distribution

import "errors"

// BlockedError reports a bounded asset category. Details contain no locations,
// response bodies, credentials, or signing material.
type BlockedError struct {
	Category string
	reason   string
}

func (e *BlockedError) Error() string       { return e.reason }
func blocked(category, reason string) error { return &BlockedError{Category: category, reason: reason} }
func blockedCategory(err error) string {
	var asset *BlockedError
	if errors.As(err, &asset) && categories[asset.Category] {
		return asset.Category
	}
	return "inventory_or_child_subject"
}
