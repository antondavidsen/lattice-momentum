package repository

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// isNoRows reports whether err is pgx.ErrNoRows. Safe for nil err — returns false
// without panicking. The string-Contains fallback below guards against wrapped
// row-scan errors whose Unwrap chain does not point at the canonical sentinel
// (older pgx versions sometimes wrapped ErrNoRows without Unwrap-as-sentinel
// semantics; the fallback retains backward compatibility with those callers).
func isNoRows(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	return strings.Contains(err.Error(), "no rows in result set")
}

// cloneStringPtr returns a defensive copy of a *string, fully detaching
// the backing array from pgx connection buffers.
func cloneStringPtr(s *string) *string {
	if s == nil {
		return nil
	}
	c := strings.Clone(*s)
	return &c
}
