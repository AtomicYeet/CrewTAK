package chore

// CrewTAK: "Clear all overdue tasks".
//
// Skips every overdue task in the caller's crew in one go. Each task gets one
// "skipped" entry in its history, credited to the person who pressed the
// button, and moves to its first occurrence that isn't overdue any more.
//
// A plain skip only advances a repeating task by one cycle, so a daily task
// that is six days late would still be five days late afterwards. Here the
// schedule is stepped forward (with the same scheduler a normal skip uses)
// until the next due date is in the future. One-time tasks are closed, the
// same as a normal skip.

import (
	"time"

	auth "donetick.com/core/internal/auth"
	chModel "donetick.com/core/internal/chore/model"
	"donetick.com/core/logging"
	"github.com/gin-gonic/gin"
)

// Safety stop for very frequent schedules (e.g. hourly) left overdue for a
// long time.
const clearOverdueMaxSteps = 5000

type clearOverdueResult struct {
	ID          int        `json:"id"`
	Name        string     `json:"name"`
	NextDueDate *time.Time `json:"nextDueDate"`
}

// nextNotOverdue steps a task's schedule forward from its current due date
// until it lands after `now`. A nil result means the task does not repeat.
func nextNotOverdue(c *gin.Context, chore *chModel.Chore, now time.Time) (*time.Time, error) {
	work := *chore
	for i := 0; i < clearOverdueMaxSteps; i++ {
		completed := now
		if work.NextDueDate != nil {
			completed = work.NextDueDate.UTC()
		}
		next, err := scheduleNextDueDate(c, &work, completed)
		if err != nil || next == nil {
			return next, err
		}
		if next.After(now) {
			return next, nil
		}
		// No progress (e.g. adaptive): stop rather than loop forever.
		if work.NextDueDate != nil && !next.After(*work.NextDueDate) {
			return next, nil
		}
		work.NextDueDate = next
	}
	return work.NextDueDate, nil
}

// ClearOverdue skips every overdue task in the crew.
func (h *Handler) ClearOverdue(c *gin.Context) {
	logger := logging.FromContext(c)
	actualUser, impersonatedUser, hasImpersonation := auth.CurrentUserWithImpersonation(c)
	if actualUser == nil {
		c.JSON(401, gin.H{"error": "Authentication failed"})
		return
	}
	user := actualUser
	if hasImpersonation {
		user = impersonatedUser
	}

	chores, err := h.choreRepo.GetChores(c, actualUser.CircleID, user.ID, false, nil, false)
	if err != nil {
		logger.Error("clear overdue: failed to load chores", "error", err)
		c.JSON(500, gin.H{"error": "Failed to load tasks"})
		return
	}

	now := time.Now().UTC()
	skipped := []clearOverdueResult{}
	failed := []clearOverdueResult{}
	for _, chore := range chores {
		if chore == nil || !chore.IsActive || chore.NextDueDate == nil || !chore.NextDueDate.Before(now) {
			continue
		}
		// Waiting on a manager's approval: leave it for them.
		if chore.Status == chModel.ChoreStatusPendingApproval {
			continue
		}
		// Event-driven schedules have no "next" date to move to.
		if chore.FrequencyType == chModel.FrequencyTypeTrigger || chore.FrequencyType == chModel.FrequencyTypeAlways {
			continue
		}

		next, err := nextNotOverdue(c, chore, now)
		if err != nil {
			logger.Error("clear overdue: scheduling failed", "choreID", chore.ID, "error", err)
			failed = append(failed, clearOverdueResult{ID: chore.ID, Name: chore.Name})
			continue
		}
		if err := h.choreRepo.SkipChore(c, chore, user.ID, next, chore.AssignedTo, nil); err != nil {
			logger.Error("clear overdue: skip failed", "choreID", chore.ID, "error", err)
			failed = append(failed, clearOverdueResult{ID: chore.ID, Name: chore.Name})
			continue
		}
		skipped = append(skipped, clearOverdueResult{ID: chore.ID, Name: chore.Name, NextDueDate: next})

		updated, err := h.choreRepo.GetChore(c, chore.ID, user.ID, actualUser.CircleID)
		if err != nil {
			continue
		}
		h.eventProducer.ChoreSkipped(c, user.WebhookURL, updated, &user.User)
		if h.realTimeService != nil {
			var entry *chModel.ChoreHistory
			if history, _ := h.choreRepo.GetChoreHistoryWithLimit(c, chore.ID, 1); len(history) > 0 {
				entry = history[0]
			}
			h.realTimeService.GetEventBroadcaster().BroadcastChoreSkipped(updated, &user.User, entry, nil)
		}
	}

	logger.Info("clear overdue", "userID", user.ID, "skipped", len(skipped), "failed", len(failed))
	c.JSON(200, gin.H{"res": gin.H{"skipped": skipped, "failed": failed}})
}

// CrewtakRoutes registers CrewTAK's extra task endpoints under their own
// prefix so they never collide with the upstream /chores/:id routes.
func CrewtakRoutes(router *gin.Engine, h *Handler, multiAuthMiddleware *auth.MultiAuthMiddleware) {
	g := router.Group("api/v1/crewtak/chores")
	g.Use(multiAuthMiddleware.MiddlewareFunc())
	g.Use(auth.ImpersonationMiddleware(h.uRepo, h.circleRepo))
	{
		g.POST("/clear-overdue", h.ClearOverdue)
	}
}
