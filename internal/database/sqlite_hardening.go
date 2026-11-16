package database

import (
	"context"
	"strings"
	"time"

	"donetick.com/core/logging"
	"gorm.io/gorm"
)

// CrewTAK: keep the SQLite database from getting stuck locked.
//
// What happened (15 Nov 2026): after a hard shutdown the server was slow,
// browsers gave up on requests, and the request context was cancelled while
// SQLite was part-way through a write. The driver interrupts the statement
// on cancel (SQLITE_INTERRUPT), which can leave that connection holding an
// open transaction. Every other query then waited on the lock until it gave
// up ("database is locked"), so every login failed until a restart.
//
// Three guards:
//   - Database work no longer stops when the browser gives up on the request.
//     Each query runs to the end, so SQLite is never interrupted mid-write.
//   - Connections are recycled every few minutes. Closing a connection rolls
//     back anything it left open, so even if a lock does get stuck it clears
//     itself instead of needing a restart.
//   - A longer busy timeout, so a slow moment waits rather than fails.

const sqliteBusyTimeoutMs = "15000"

// sqliteDSN adds the busy timeout to the database path.
func sqliteDSN(path string) string {
	if strings.Contains(path, "busy_timeout") {
		return path
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "_pragma=busy_timeout(" + sqliteBusyTimeoutMs + ")"
}

// detachFromRequestCancel keeps the request's values (tracing, logging) but
// drops its cancellation, so a closed browser tab can't interrupt SQLite.
func detachFromRequestCancel(db *gorm.DB) {
	if db == nil || db.Statement == nil || db.Statement.Context == nil {
		return
	}
	db.Statement.Context = context.WithoutCancel(db.Statement.Context)
}

func hardenSQLite(db *gorm.DB) {
	logger := logging.DefaultLogger()

	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetConnMaxLifetime(5 * time.Minute)
		sqlDB.SetConnMaxIdleTime(1 * time.Minute)
	} else {
		logger.Warnf("sqlite: could not configure connection pool: %v", err)
	}

	const name = "crewtak:detach_request_cancel"
	cb := db.Callback()
	register := []struct {
		what string
		err  error
	}{
		{"create", cb.Create().Before("gorm:begin_transaction").Register(name, detachFromRequestCancel)},
		{"update", cb.Update().Before("gorm:begin_transaction").Register(name, detachFromRequestCancel)},
		{"delete", cb.Delete().Before("gorm:begin_transaction").Register(name, detachFromRequestCancel)},
		{"query", cb.Query().Before("gorm:query").Register(name, detachFromRequestCancel)},
		{"row", cb.Row().Before("gorm:row").Register(name, detachFromRequestCancel)},
		{"raw", cb.Raw().Before("gorm:raw").Register(name, detachFromRequestCancel)},
	}
	for _, r := range register {
		if r.err != nil {
			logger.Warnf("sqlite: could not register %s callback: %v", r.what, r.err)
		}
	}
}
