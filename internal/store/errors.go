package store

import (
	"errors"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// IsUniqueViolation reports whether err (or an error it wraps) is a SQLite UNIQUE or PRIMARY KEY
// constraint failure, decided by the extended result code rather than the message text.
func IsUniqueViolation(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	switch se.Code() {
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
		return true
	}
	return false
}
