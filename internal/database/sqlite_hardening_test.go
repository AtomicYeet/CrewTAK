package database

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

func TestSqliteDSN(t *testing.T) {
	cases := map[string]string{
		"donetick.db":                    "donetick.db?_pragma=busy_timeout(15000)",
		"/data/donetick.db?mode=rwc":     "/data/donetick.db?mode=rwc&_pragma=busy_timeout(15000)",
		"x.db?_pragma=busy_timeout(100)": "x.db?_pragma=busy_timeout(100)",
	}
	for in, want := range cases {
		if got := sqliteDSN(in); got != want {
			t.Errorf("sqliteDSN(%q) = %q, want %q", in, got, want)
		}
	}
}

type ctxKey struct{}

func TestDetachFromRequestCancelKeepsValues(t *testing.T) {
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "v"))
	db := &gorm.DB{Statement: &gorm.Statement{Context: parent}}
	detachFromRequestCancel(db)
	cancel()
	if err := db.Statement.Context.Err(); err != nil {
		t.Fatalf("detached context was cancelled: %v", err)
	}
	if db.Statement.Context.Value(ctxKey{}) != "v" {
		t.Fatal("detached context lost its values")
	}
}
