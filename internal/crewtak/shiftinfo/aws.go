package shiftinfo

// AWS events: a door held for a set window (start and end time). Added and
// deleted from the AWS page; the wall display shows the ones that fall in the
// current 48-hour tour, struck through once they've ended.
//
//   - One door per event; any number of events can overlap, even on the
//     same door.
//   - Separate from tasks: nothing here touches chores or the schedule.
//   - At shift change (07:00 at the start of a new tour) events that have
//     already ended are deleted. Events still to come, including ones loaded
//     for later tours, stay until they've ended and a shift change passes.
//
// Times are stored in UTC and compared in Go, not SQL, so SQLite's text
// timestamps can't trip us up.

import (
	"errors"
	"sort"
	"strconv"
	"time"

	auth "donetick.com/core/internal/auth"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AWSDoors are the doors an event can be for, in the order the page lists
// them. Keep in step with the frontend's src/crewtak/aws.js.
var AWSDoors = []string{
	"bc_east",
	"bc_west",
	"tsa",
	"ff_door",
	"united",
	"delta",
	"american",
	"allegiant",
	"salt_room",
	"basement",
}

// Longest event accepted.
const maxAWSDuration = 24 * time.Hour

type AWSEvent struct {
	ID        int       `json:"id" gorm:"primary_key"`
	CircleID  int       `json:"-" gorm:"column:circle_id;not null;index"`
	Door      string    `json:"door" gorm:"column:door;size:32;not null"`
	StartAt   time.Time `json:"startAt" gorm:"column:start_at;not null"`
	EndAt     time.Time `json:"endAt" gorm:"column:end_at;not null"`
	CreatedBy int       `json:"createdBy" gorm:"column:created_by"`
	CreatedAt time.Time `json:"createdAt" gorm:"column:created_at"`
}

func (AWSEvent) TableName() string { return "crewtak_aws_events" }

func knownDoor(door string) bool {
	for _, d := range AWSDoors {
		if d == door {
			return true
		}
	}
	return false
}

// tourStartAt is 07:00 station time on the first day of the tour on duty
// at t.
func tourStartAt(t time.Time) time.Time {
	loc := stationLocation()
	tour, _, _ := tourAndDay(t.In(loc))
	d, err := time.ParseInLocation("2006-01-02", tour, loc)
	if err != nil {
		return t
	}
	return time.Date(d.Year(), d.Month(), d.Day(), shiftChangeHr, 0, 0, 0, loc)
}

// validateAWS checks a new event at time now.
func validateAWS(door string, start, end, now time.Time) error {
	switch {
	case !knownDoor(door):
		return errors.New("Pick a door")
	case start.IsZero() || end.IsZero():
		return errors.New("Set a start and end time")
	case !end.After(start):
		return errors.New("End time must be after the start time")
	case end.Sub(start) > maxAWSDuration:
		return errors.New("An AWS event can be at most 24 hours long")
	case !end.After(tourStartAt(now)):
		return errors.New("That event ended before this shift started")
	}
	return nil
}

// clearedAWS returns the events that have ended before the current tour
// started, i.e. completed events from a previous shift.
func clearedAWS(events []AWSEvent, now time.Time) []int {
	start := tourStartAt(now)
	ids := []int{}
	for _, e := range events {
		if !e.EndAt.After(start) {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// loadAWS returns the crew's events, deleting completed ones from earlier
// shifts first.
func loadAWS(db *gorm.DB, circleID int, now time.Time) ([]AWSEvent, error) {
	var all []AWSEvent
	if err := db.Where("circle_id = ?", circleID).Find(&all).Error; err != nil {
		return nil, err
	}
	old := clearedAWS(all, now)
	if len(old) > 0 {
		if err := db.Where("circle_id = ? AND id IN ?", circleID, old).Delete(&AWSEvent{}).Error; err != nil {
			return nil, err
		}
	}
	gone := map[int]bool{}
	for _, id := range old {
		gone[id] = true
	}
	events := []AWSEvent{}
	for _, e := range all {
		if !gone[e.ID] {
			e.StartAt = e.StartAt.UTC()
			e.EndAt = e.EndAt.UTC()
			events = append(events, e)
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].StartAt.Equal(events[j].StartAt) {
			return events[i].StartAt.Before(events[j].StartAt)
		}
		return events[i].ID < events[j].ID
	})
	return events, nil
}

func (h *Handler) listAWS(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		c.JSON(401, gin.H{"error": "User not authenticated"})
		return
	}
	events, err := loadAWS(h.db.WithContext(c), user.CircleID, time.Now())
	if err != nil {
		c.JSON(500, gin.H{"error": "Error loading AWS events"})
		return
	}
	c.JSON(200, gin.H{"res": events})
}

type awsReq struct {
	Door    string    `json:"door"`
	StartAt time.Time `json:"startAt"`
	EndAt   time.Time `json:"endAt"`
}

func (h *Handler) createAWS(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		c.JSON(401, gin.H{"error": "User not authenticated"})
		return
	}
	var req awsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request"})
		return
	}
	now := time.Now()
	if err := validateAWS(req.Door, req.StartAt, req.EndAt, now); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	event := AWSEvent{
		CircleID:  user.CircleID,
		Door:      req.Door,
		StartAt:   req.StartAt.UTC(),
		EndAt:     req.EndAt.UTC(),
		CreatedBy: user.ID,
		CreatedAt: now.UTC(),
	}
	if err := h.db.WithContext(c).Create(&event).Error; err != nil {
		c.JSON(500, gin.H{"error": "Error saving AWS event"})
		return
	}
	c.JSON(200, gin.H{"res": event})
}

func (h *Handler) deleteAWS(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		c.JSON(401, gin.H{"error": "User not authenticated"})
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid event"})
		return
	}
	res := h.db.WithContext(c).Where("circle_id = ? AND id = ?", user.CircleID, id).Delete(&AWSEvent{})
	if res.Error != nil {
		c.JSON(500, gin.H{"error": "Error deleting AWS event"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(404, gin.H{"error": "That event is already gone"})
		return
	}
	c.JSON(200, gin.H{"res": id})
}

func AWSRoutes(r *gin.Engine, h *Handler, multiAuthMiddleware *auth.MultiAuthMiddleware) {
	g := r.Group("api/v1/crewtak/aws")
	g.Use(multiAuthMiddleware.MiddlewareFunc())
	{
		g.GET("", h.listAWS)
		g.POST("", h.createAWS)
		g.DELETE("/:id", h.deleteAWS)
	}
}
