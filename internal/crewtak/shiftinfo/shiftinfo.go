// Package shiftinfo stores CrewTAK's per-shift assignments: who carries each
// station phone and who has each overnight sleep block.
//
// Every assignment belongs to one crew and one 48-hour tour (identified by the
// tour's start date, YYYY-MM-DD, as computed by the frontend's shift rotation).
// A new tour therefore starts with every slot empty.
package shiftinfo

import (
	"regexp"
	"time"

	auth "donetick.com/core/internal/auth"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Slot is one assignable position for one tour.
type Slot struct {
	ID        int       `json:"-" gorm:"primary_key"`
	CircleID  int       `json:"-" gorm:"column:circle_id;not null;uniqueIndex:idx_crewtak_shift_slot"`
	Tour      string    `json:"tour" gorm:"column:tour;size:10;not null;uniqueIndex:idx_crewtak_shift_slot"`
	Slot      string    `json:"slot" gorm:"column:slot;size:32;not null;uniqueIndex:idx_crewtak_shift_slot"`
	UserID    *int      `json:"userId" gorm:"column:user_id"`
	UpdatedBy int       `json:"updatedBy" gorm:"column:updated_by"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"column:updated_at"`
}

func (Slot) TableName() string { return "crewtak_shift_slots" }

// Slots the page knows about. Anything else is rejected.
var validSlots = map[string]bool{
	"phone.captain": true,
	"phone.ff1":     true,
	"phone.ff2":     true,
	"sleep.1.0":     true,
	"sleep.1.1":     true,
	"sleep.1.2":     true,
	"sleep.2.0":     true,
	"sleep.2.1":     true,
	"sleep.2.2":     true,
}

var tourPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

type Handler struct {
	db *gorm.DB
}

func NewHandler(db *gorm.DB) *Handler {
	return &Handler{db: db}
}

func (h *Handler) get(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		c.JSON(401, gin.H{"error": "User not authenticated"})
		return
	}
	tour := c.Query("tour")
	if !tourPattern.MatchString(tour) {
		c.JSON(400, gin.H{"error": "tour must be YYYY-MM-DD"})
		return
	}
	slots := []Slot{}
	if err := h.db.WithContext(c).
		Where("circle_id = ? AND tour = ?", user.CircleID, tour).
		Find(&slots).Error; err != nil {
		c.JSON(500, gin.H{"error": "Error loading shift info"})
		return
	}
	c.JSON(200, gin.H{"res": slots})
}

type setReq struct {
	Tour   string `json:"tour"`
	Slot   string `json:"slot"`
	UserID *int   `json:"userId"`
}

func (h *Handler) set(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		c.JSON(401, gin.H{"error": "User not authenticated"})
		return
	}
	var req setReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request"})
		return
	}
	if !tourPattern.MatchString(req.Tour) || !validSlots[req.Slot] {
		c.JSON(400, gin.H{"error": "Unknown tour or slot"})
		return
	}
	if req.UserID != nil {
		// Only members of the caller's crew can be assigned.
		var n int64
		err := h.db.WithContext(c).Table("user_circles").
			Where("circle_id = ? AND user_id = ?", user.CircleID, *req.UserID).
			Count(&n).Error
		if err != nil {
			c.JSON(500, gin.H{"error": "Error checking crew member"})
			return
		}
		if n == 0 {
			c.JSON(400, gin.H{"error": "That person isn't in this crew"})
			return
		}
	}

	slot := Slot{
		CircleID:  user.CircleID,
		Tour:      req.Tour,
		Slot:      req.Slot,
		UserID:    req.UserID,
		UpdatedBy: user.ID,
		UpdatedAt: time.Now().UTC(),
	}
	err := h.db.WithContext(c).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "circle_id"}, {Name: "tour"}, {Name: "slot"}},
		DoUpdates: clause.AssignmentColumns([]string{"user_id", "updated_by", "updated_at"}),
	}).Create(&slot).Error
	if err != nil {
		c.JSON(500, gin.H{"error": "Error saving shift info"})
		return
	}
	c.JSON(200, gin.H{"res": slot})
}

func Routes(r *gin.Engine, h *Handler, multiAuthMiddleware *auth.MultiAuthMiddleware) {
	g := r.Group("api/v1/crewtak/shift-info")
	g.Use(multiAuthMiddleware.MiddlewareFunc())
	{
		g.GET("", h.get)
		g.PUT("", h.set)
	}
}
