package shiftinfo

// Automatic captain-phone hand-off at each watch change.
//
// When a watch block starts (20:00, 23:30, 03:00 station time), whoever has
// that watch takes the Captain's phone, and the phone they were carrying goes
// to whoever is handing the Captain's phone over. Example: Michael has the
// 1130p–3a watch and FF1, Steven has Captain's. At 23:30 Michael gets
// Captain's and Steven gets FF1.
//
//   - Nobody on the watch: nothing happens.
//   - The person on watch already has Captain's: nothing to do.
//   - They had no phone: they take Captain's and the old holder is left
//     without one (one phone per person still holds).
//
// Each hand-off happens once per watch block. A marker row
// (swap.<day>.<block>, never sent to the page) records it, so if someone
// changes phones by hand during the watch it is left alone. If the watch is
// filled in partway through the block, the hand-off happens then. Shift change
// at 07:00 still starts a new tour with every slot empty.

import (
	"context"
	"fmt"
	"log"
	"time"
	_ "time/tzdata" // station time zone even if the image has no zoneinfo

	"go.uber.org/fx"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	stationTZ      = "America/Chicago"
	shiftHours     = 48
	shiftChangeHr  = 7
	captainSlot    = "phone.captain"
	swapPrefix     = "swap."
	swapCheckEvery = 30 * time.Second
)

// A Shift came on at 07:00 on Friday 16 October 2026 (mirrors the
// frontend's src/crewtak/shifts.js).
var anchorDate = time.Date(2026, time.October, 16, 0, 0, 0, 0, time.UTC)

// Watch blocks: index, start hour/minute, and whether the start falls on the
// next calendar morning (after midnight) of the shift day.
var watchStarts = []struct {
	index        int
	hour, minute int
	nextMorning  bool
}{
	{0, 20, 0, false},
	{1, 23, 30, false},
	{2, 3, 0, true},
}

var phoneSlots = []string{"phone.captain", "phone.ff1", "phone.ff2", "phone.ops"}

func stationLocation() *time.Location {
	if loc, err := time.LoadLocation(stationTZ); err == nil {
		return loc
	}
	return time.Local
}

// civilDays counts calendar days from the anchor date to d's local date.
func civilDays(d time.Time) int {
	day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	return int(day.Sub(anchorDate).Hours() / 24)
}

func floorMod(a, b int) int { return ((a % b) + b) % b }

// tourAndDay returns the tour key (start date, YYYY-MM-DD), the shift day
// (1 or 2) and the calendar date that shift day started on, for station-local
// time t. Shift days change at 07:00.
func tourAndDay(t time.Time) (tour string, shiftDay int, dayStart time.Time) {
	d := civilDays(t)
	if t.Hour() < shiftChangeHr {
		d-- // before 07:00 still belongs to yesterday's shift day
	}
	daysPerTour := shiftHours / 24
	into := floorMod(d, daysPerTour)
	start := anchorDate.AddDate(0, 0, d-into)
	dayStart = time.Date(start.Year(), start.Month(), start.Day()+into, 0, 0, 0, 0, t.Location())
	return start.Format("2006-01-02"), into + 1, dayStart
}

// currentWatch returns the watch block running at station-local time t, or
// -1 when no watch is running (07:00–20:00).
func currentWatch(t time.Time) (tour string, shiftDay, block int) {
	tour, shiftDay, dayStart := tourAndDay(t)
	block = -1
	for _, w := range watchStarts {
		start := time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), w.hour, w.minute, 0, 0, t.Location())
		if w.nextMorning {
			start = start.AddDate(0, 0, 1)
		}
		if !t.Before(start) {
			block = w.index
		}
	}
	return tour, shiftDay, block
}

func slotUser(slots map[string]*int, key string) *int {
	if v, ok := slots[key]; ok {
		return v
	}
	return nil
}

func sameUser(a, b *int) bool {
	return a != nil && b != nil && *a == *b
}

// swapCrew does the hand-off for one crew if it is due. Returns true when it
// changed the phones.
func swapCrew(tx *gorm.DB, circleID int, tour string, shiftDay, block int, now time.Time) (bool, error) {
	marker := fmt.Sprintf("%s%d.%d", swapPrefix, shiftDay, block)
	watchSlot := fmt.Sprintf("sleep.%d.%d", shiftDay, block)

	var rows []Slot
	if err := tx.Where("circle_id = ? AND tour = ?", circleID, tour).Find(&rows).Error; err != nil {
		return false, err
	}
	slots := map[string]*int{}
	for _, r := range rows {
		slots[r.Slot] = r.UserID
	}
	if _, done := slots[marker]; done {
		return false, nil
	}
	awake := slotUser(slots, watchSlot)
	if awake == nil {
		return false, nil // nobody on this watch (yet)
	}

	captain := slotUser(slots, captainSlot)
	changed := false
	if !sameUser(awake, captain) {
		// The phone the person coming on watch carries now, if any.
		oldPhone := ""
		for _, p := range phoneSlots {
			if p != captainSlot && sameUser(slotUser(slots, p), awake) {
				oldPhone = p
				break
			}
		}
		if err := upsertSlot(tx, circleID, tour, captainSlot, awake, now); err != nil {
			return false, err
		}
		if oldPhone != "" {
			if err := upsertSlot(tx, circleID, tour, oldPhone, captain, now); err != nil {
				return false, err
			}
		}
		changed = true
	}
	return changed, upsertSlot(tx, circleID, tour, marker, awake, now)
}

func upsertSlot(tx *gorm.DB, circleID int, tour, slot string, userID *int, now time.Time) error {
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "circle_id"}, {Name: "tour"}, {Name: "slot"}},
		DoUpdates: clause.AssignmentColumns([]string{"user_id", "updated_by", "updated_at"}),
	}).Create(&Slot{CircleID: circleID, Tour: tour, Slot: slot, UserID: userID, UpdatedAt: now}).Error
}

// RunPhoneSwaps hands the Captain's phone over for every crew whose watch
// change is due at station-local time now.
func RunPhoneSwaps(db *gorm.DB, now time.Time) (int, error) {
	tour, shiftDay, block := currentWatch(now)
	if block < 0 {
		return 0, nil
	}
	watchSlot := fmt.Sprintf("sleep.%d.%d", shiftDay, block)
	var circles []int
	if err := db.Model(&Slot{}).
		Where("tour = ? AND slot = ? AND user_id IS NOT NULL", tour, watchSlot).
		Distinct().Pluck("circle_id", &circles).Error; err != nil {
		return 0, err
	}
	swapped := 0
	for _, circleID := range circles {
		err := db.Transaction(func(tx *gorm.DB) error {
			changed, err := swapCrew(tx, circleID, tour, shiftDay, block, now.UTC())
			if changed {
				swapped++
			}
			return err
		})
		if err != nil {
			return swapped, err
		}
	}
	return swapped, nil
}

// StartPhoneSwaps checks every 30 seconds whether a watch change is due.
func StartPhoneSwaps(lc fx.Lifecycle, h *Handler) {
	ctx, cancel := context.WithCancel(context.Background())
	loc := stationLocation()
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				ticker := time.NewTicker(swapCheckEvery)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						n, err := RunPhoneSwaps(h.db.WithContext(ctx), time.Now().UTC().In(loc))
						if err != nil {
							log.Printf("crewtak: captain phone hand-off failed: %v", err)
						} else if n > 0 {
							log.Printf("crewtak: handed the Captain's phone over for %d crew(s)", n)
						}
					}
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			return nil
		},
	})
}
