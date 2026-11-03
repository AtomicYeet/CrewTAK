package shiftinfo

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func chicago(t *testing.T, y int, m time.Month, d, hh, mm int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(stationTZ)
	if err != nil {
		t.Fatalf("load tz: %v", err)
	}
	return time.Date(y, m, d, hh, mm, 0, 0, loc)
}

func TestCurrentWatch(t *testing.T) {
	cases := []struct {
		name       string
		at         time.Time
		tour       string
		day, block int
	}{
		// Tour started Fri 16 Oct 2026 07:00 (A shift).
		{"day 1 afternoon", chicago(t, 2026, 10, 16, 15, 0), "2026-10-16", 1, -1},
		{"day 1 8p block", chicago(t, 2026, 10, 16, 20, 0), "2026-10-16", 1, 0},
		{"day 1 1130p block", chicago(t, 2026, 10, 16, 23, 30), "2026-10-16", 1, 1},
		{"day 1 3a block (next morning)", chicago(t, 2026, 10, 17, 3, 0), "2026-10-16", 1, 2},
		{"day 1 0659", chicago(t, 2026, 10, 17, 6, 59), "2026-10-16", 1, 2},
		{"day 2 starts 0700", chicago(t, 2026, 10, 17, 7, 0), "2026-10-16", 2, -1},
		{"day 2 1130p block", chicago(t, 2026, 10, 17, 23, 45), "2026-10-16", 2, 1},
		{"next tour", chicago(t, 2026, 10, 18, 7, 0), "2026-10-18", 1, -1},
		{"after DST ends", chicago(t, 2026, 11, 2, 23, 30), "2026-11-01", 2, 1},
		{"before anchor", chicago(t, 2026, 10, 15, 21, 0), "2026-10-14", 2, 0},
	}
	for _, c := range cases {
		tour, day, block := currentWatch(c.at)
		if tour != c.tour || day != c.day || block != c.block {
			t.Errorf("%s: got tour=%s day=%d block=%d, want %s %d %d", c.name, tour, day, block, c.tour, c.day, c.block)
		}
	}
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "swap.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&Slot{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func put(t *testing.T, db *gorm.DB, tour, slot string, user int) {
	t.Helper()
	u := user
	if err := upsertSlot(db, 1, tour, slot, &u, time.Now().UTC()); err != nil {
		t.Fatalf("put %s: %v", slot, err)
	}
}

func phones(t *testing.T, db *gorm.DB, tour string) map[string]int {
	t.Helper()
	var rows []Slot
	db.Where("circle_id = 1 AND tour = ? AND slot LIKE 'phone.%'", tour).Find(&rows)
	out := map[string]int{}
	for _, r := range rows {
		if r.UserID != nil {
			out[r.Slot] = *r.UserID
		}
	}
	return out
}

const (
	steven  = 10
	michael = 11
	alex    = 12
)

func TestPhoneSwapAtWatchChange(t *testing.T) {
	db := testDB(t)
	tour := "2026-10-16"
	put(t, db, tour, "phone.captain", steven)
	put(t, db, tour, "phone.ff1", michael)
	put(t, db, tour, "phone.ff2", alex)
	put(t, db, tour, "sleep.1.1", michael)

	// Before 23:30 nothing happens.
	if n, err := RunPhoneSwaps(db, chicago(t, 2026, 10, 16, 23, 0)); err != nil || n != 0 {
		t.Fatalf("early run: n=%d err=%v", n, err)
	}
	// 8p block is empty: still nothing.
	if p := phones(t, db, tour); p["phone.captain"] != steven {
		t.Fatalf("captain moved early: %v", p)
	}

	// 23:30: Michael takes Captain's, Steven gets FF1.
	if n, err := RunPhoneSwaps(db, chicago(t, 2026, 10, 16, 23, 30)); err != nil || n != 1 {
		t.Fatalf("swap run: n=%d err=%v", n, err)
	}
	p := phones(t, db, tour)
	if p["phone.captain"] != michael || p["phone.ff1"] != steven || p["phone.ff2"] != alex {
		t.Fatalf("after swap: %v", p)
	}

	// A manual change during the block is left alone.
	put(t, db, tour, "phone.captain", alex)
	if n, _ := RunPhoneSwaps(db, chicago(t, 2026, 10, 16, 23, 45)); n != 0 {
		t.Fatalf("swapped twice in one block")
	}
	if p := phones(t, db, tour); p["phone.captain"] != alex {
		t.Fatalf("manual change overwritten: %v", p)
	}
}

func TestPhoneSwapWithoutPhone(t *testing.T) {
	db := testDB(t)
	tour := "2026-10-16"
	put(t, db, tour, "phone.captain", steven)
	put(t, db, tour, "sleep.1.2", michael) // Michael has no phone
	if n, err := RunPhoneSwaps(db, chicago(t, 2026, 10, 17, 3, 5)); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	p := phones(t, db, tour)
	if p["phone.captain"] != michael || len(p) != 1 {
		t.Fatalf("got %v", p)
	}
}

func TestPhoneSwapAlreadyCaptain(t *testing.T) {
	db := testDB(t)
	tour := "2026-10-16"
	put(t, db, tour, "phone.captain", steven)
	put(t, db, tour, "phone.ff1", michael)
	put(t, db, tour, "sleep.2.0", steven)
	if n, err := RunPhoneSwaps(db, chicago(t, 2026, 10, 17, 20, 1)); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if p := phones(t, db, tour); p["phone.captain"] != steven || p["phone.ff1"] != michael {
		t.Fatalf("got %v", p)
	}
}
