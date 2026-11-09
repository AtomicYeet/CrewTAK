package shiftinfo

import (
	"testing"
	"time"
)

func TestTourStartAt(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
		want time.Time
	}{
		// C shift tour started Sat 7 Nov 2026 07:00.
		{"day 2 evening", chicago(t, 2026, 11, 8, 22, 42), chicago(t, 2026, 11, 7, 7, 0)},
		{"day 1 just after change", chicago(t, 2026, 11, 7, 7, 0), chicago(t, 2026, 11, 7, 7, 0)},
		{"0659 before change", chicago(t, 2026, 11, 9, 6, 59), chicago(t, 2026, 11, 7, 7, 0)},
		{"next tour", chicago(t, 2026, 11, 9, 7, 0), chicago(t, 2026, 11, 9, 7, 0)},
		{"UTC input", chicago(t, 2026, 11, 8, 22, 42).UTC(), chicago(t, 2026, 11, 7, 7, 0)},
	}
	for _, c := range cases {
		if got := tourStartAt(c.at); !got.Equal(c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestValidateAWS(t *testing.T) {
	now := chicago(t, 2026, 11, 8, 12, 0)
	at := func(d, h, m int) time.Time { return chicago(t, 2026, 11, d, h, m) }
	cases := []struct {
		name       string
		door       string
		start, end time.Time
		ok         bool
	}{
		{"good", "bc_east", at(8, 16, 15), at(8, 17, 15), true},
		{"overnight", "tsa", at(8, 23, 0), at(9, 1, 30), true},
		{"earlier this shift is fine", "delta", at(7, 10, 45), at(7, 11, 30), true},
		{"later tour", "allegiant", at(12, 10, 45), at(12, 11, 30), true},
		{"unknown door", "front_gate", at(8, 16, 0), at(8, 17, 0), false},
		{"end before start", "united", at(8, 17, 0), at(8, 16, 0), false},
		{"zero length", "united", at(8, 17, 0), at(8, 17, 0), false},
		{"too long", "united", at(8, 7, 0), at(9, 7, 1), false},
		{"ended last shift", "basement", at(6, 10, 0), at(6, 11, 0), false},
		{"missing times", "basement", time.Time{}, time.Time{}, false},
	}
	for _, c := range cases {
		err := validateAWS(c.door, c.start, c.end, now)
		if (err == nil) != c.ok {
			t.Errorf("%s: got err %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestLoadAWSClearsAtShiftChange(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&AWSEvent{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	add := func(circle int, door string, start, end time.Time) {
		t.Helper()
		e := AWSEvent{CircleID: circle, Door: door, StartAt: start.UTC(), EndAt: end.UTC()}
		if err := db.Create(&e).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	// Tour runs Sat 7 Nov 07:00 → Mon 9 Nov 07:00.
	add(1, "bc_east", chicago(t, 2026, 11, 8, 16, 15), chicago(t, 2026, 11, 8, 17, 15)) // done day 2
	add(1, "american", chicago(t, 2026, 11, 7, 13, 0), chicago(t, 2026, 11, 7, 13, 45)) // done day 1
	add(1, "tsa", chicago(t, 2026, 11, 9, 6, 0), chicago(t, 2026, 11, 9, 8, 0))         // across change
	add(1, "delta", chicago(t, 2026, 11, 10, 10, 0), chicago(t, 2026, 11, 10, 11, 0))   // next tour
	add(2, "united", chicago(t, 2026, 11, 7, 9, 0), chicago(t, 2026, 11, 7, 10, 0))     // other crew

	// During the tour nothing is cleared, and it's sorted by start.
	got, err := loadAWS(db, 1, chicago(t, 2026, 11, 8, 22, 0))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	doors := []string{}
	for _, e := range got {
		doors = append(doors, e.Door)
	}
	if want := "american,bc_east,tsa,delta"; join(doors) != want {
		t.Fatalf("during tour: got %s want %s", join(doors), want)
	}

	// After shift change the two completed ones are gone; the one running
	// across the change and the future one stay.
	got, err = loadAWS(db, 1, chicago(t, 2026, 11, 9, 7, 5))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	doors = doors[:0]
	for _, e := range got {
		doors = append(doors, e.Door)
	}
	if want := "tsa,delta"; join(doors) != want {
		t.Fatalf("after change: got %s want %s", join(doors), want)
	}
	var n int64
	db.Model(&AWSEvent{}).Where("circle_id = ?", 1).Count(&n)
	if n != 2 {
		t.Fatalf("rows left for crew 1: %d", n)
	}
	// The other crew's old event is only cleared when they load.
	db.Model(&AWSEvent{}).Where("circle_id = ?", 2).Count(&n)
	if n != 1 {
		t.Fatalf("crew 2 rows touched: %d", n)
	}
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
