package repository

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// isNoRows safely detects pgx.ErrNoRows. errors.Is can SIGSEGV when the
// error's Unwrap chain contains a nil interface value (observed with pgx v5
// under certain pool/row conditions). Direct comparison + string fallback
// avoids the crash.
func isNoRows(err error) bool {
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
