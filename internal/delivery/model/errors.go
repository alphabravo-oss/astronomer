package model

import (
	"fmt"
	"strings"
)

// ValidationCode is a stable machine-readable category for invalid domain
// input. Callers may map it to an API error without parsing Error().
type ValidationCode string

const (
	CodeRequired         ValidationCode = "required"
	CodeInvalid          ValidationCode = "invalid"
	CodeUnsupported      ValidationCode = "unsupported"
	CodeConflict         ValidationCode = "conflict"
	CodeLimitExceeded    ValidationCode = "limit_exceeded"
	CodeNotImmutable     ValidationCode = "not_immutable"
	CodeSecretNotAllowed ValidationCode = "secret_not_allowed"
)

// Violation describes one invalid field. Messages are deliberately safe for
// API responses: validators must not copy credentials or arbitrary content
// into them.
type Violation struct {
	Field   string         `json:"field"`
	Code    ValidationCode `json:"code"`
	Message string         `json:"message"`
}

// ValidationError aggregates violations in deterministic validation order.
type ValidationError struct {
	Violations []Violation `json:"violations"`
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return "delivery model validation failed"
	}
	parts := make([]string, 0, len(e.Violations))
	for _, violation := range e.Violations {
		parts = append(parts, fmt.Sprintf("%s: %s", violation.Field, violation.Message))
	}
	return "delivery model validation failed: " + strings.Join(parts, "; ")
}

// HasCode reports whether validation produced a violation with code.
func (e *ValidationError) HasCode(code ValidationCode) bool {
	if e == nil {
		return false
	}
	for _, violation := range e.Violations {
		if violation.Code == code {
			return true
		}
	}
	return false
}

type validationCollector struct {
	violations []Violation
}

func (c *validationCollector) add(field string, code ValidationCode, message string) {
	c.violations = append(c.violations, Violation{Field: field, Code: code, Message: message})
}

func (c *validationCollector) append(prefix string, err error) {
	if err == nil {
		return
	}
	if validation, ok := err.(*ValidationError); ok {
		for _, violation := range validation.Violations {
			field := violation.Field
			if prefix != "" {
				if field == "" {
					field = prefix
				} else {
					field = prefix + "." + field
				}
			}
			c.add(field, violation.Code, violation.Message)
		}
		return
	}
	c.add(prefix, CodeInvalid, "is invalid")
}

func (c *validationCollector) err() error {
	if len(c.violations) == 0 {
		return nil
	}
	return &ValidationError{Violations: append([]Violation(nil), c.violations...)}
}
