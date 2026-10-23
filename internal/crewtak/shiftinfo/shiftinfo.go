// Package shiftinfo stores CrewTAK's per-shift assignments: who carries each
// station phone and who has each overnight sleep block.
//
// Every assignment belongs to one crew and one 48-hour tour (identified by the
// tour's start date, YYYY-MM-DD, as computed by the frontend's shift rotation).
// A new tour therefore starts with every slot empty. Watch times (stored as
// sleep.<day>.<block>) are also keyed by shift day, so they start empty again
// when Day 1 rolls into Day 2.
//
// Within each group a person can hold only one slot: one phone, one watch time
// per day. Putting someone in a slot clears them from the other slots of the
// same group in the same tour.
//
// Rig (company) assignments are many-to-many: a rig can have several people
// and a person can be on several rigs. Each pairing is its own row,
// company.<rig>.u<userId>, set to that user (on) or null (off). Like
// everything else here they belong to the tour, so they reset at shift change.
package shiftinfo

import (
	"regexp"
	"strconv"
	"strings"
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
	"phone.ops":     true,
	"sleep.1.0":     true,
	"sleep.1.1":     true,
	"sleep.1.2":     true,
	"sleep.2.0":     true,
	"sleep.2.1":     true,
	"sleep.2.2":     true,
}

// exclusiveGroup is the prefix of the slots a person may hold only one of:
// "phone.", "company.", or "sleep.<day>." (one watch time per shift day).
func exclusiveGroup(slot string) string {
	switch {
	case strings.HasPrefix(slot, "phone."):
		return "phone."
	case strings.HasPrefix(slot, "sleep."):
		if i := strings.LastIndex(slot, "."); i > 0 {
			return slot[:i+1]
		}
	}
	return ""
}

var rigPattern = regexp.MustCompile(`^company\.(5181|5182|5184|5132)\.u(\d+)$`)

// validSlot reports whether slot is known and, for a rig pairing, that the
// user being set matches the user named in the slot (or is being cleared).
func validSlot(slot string, userID *int) bool {
	if validSlots[slot] {
		return true
	}
	m := rigPattern.FindStringSubmatch(slot)
	if m == nil {
		return false
	}
	return userID == nil || strconv.Itoa(*userID) == m[2]
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
	if !tourPattern.MatchString(req.Tour) || !validSlot(req.Slot, req.UserID) {
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

	now := time.Now().UTC()
	slot := Slot{
		CircleID:  user.CircleID,
		Tour:      req.Tour,
		Slot:      req.Slot,
		UserID:    req.UserID,
		UpdatedBy: user.ID,
		UpdatedAt: now,
	}
	cleared := []string{}
	err := h.db.WithContext(c).Transaction(func(tx *gorm.DB) error {
		// One slot per person per group: the newest pick wins and their old
		// slot is left empty for someone else.
		if group := exclusiveGroup(req.Slot); req.UserID != nil && group != "" {
			var old []Slot
			if err := tx.Where("circle_id = ? AND tour = ? AND user_id = ? AND slot LIKE ? AND slot <> ?",
				user.CircleID, req.Tour, *req.UserID, group+"%", req.Slot).
				Find(&old).Error; err != nil {
				return err
			}
			for _, o := range old {
				if err := tx.Model(&Slot{}).Where("id = ?", o.ID).Updates(map[string]interface{}{
					"user_id":    nil,
					"updated_by": user.ID,
					"updated_at": now,
				}).Error; err != nil {
					return err
				}
				cleared = append(cleared, o.Slot)
			}
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "circle_id"}, {Name: "tour"}, {Name: "slot"}},
			DoUpdates: clause.AssignmentColumns([]string{"user_id", "updated_by", "updated_at"}),
		}).Create(&slot).Error
	})
	if err != nil {
		c.JSON(500, gin.H{"error": "Error saving shift info"})
		return
	}
	c.JSON(200, gin.H{"res": slot, "cleared": cleared})
}

func Routes(r *gin.Engine, h *Handler, multiAuthMiddleware *auth.MultiAuthMiddleware) {
	g := r.Group("api/v1/crewtak/shift-info")
	g.Use(multiAuthMiddleware.MiddlewareFunc())
	{
		g.GET("", h.get)
		g.PUT("", h.set)
	}
}
