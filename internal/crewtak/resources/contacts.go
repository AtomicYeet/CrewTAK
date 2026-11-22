package resources

// Contacts: the station phone book under Resources (name, phone, email).
// Anyone on the crew can view, add, edit and delete. Stored only in CrewTAK
// for now; not synced with any other address book.

import (
	"sort"
	"strconv"
	"strings"
	"time"

	auth "donetick.com/core/internal/auth"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Contact struct {
	ID        int       `json:"id" gorm:"primary_key"`
	CircleID  int       `json:"-" gorm:"column:circle_id;not null;index"`
	Name      string    `json:"name" gorm:"column:name;size:100;not null"`
	Phone     string    `json:"phone" gorm:"column:phone;size:40"`
	Email     string    `json:"email" gorm:"column:email;size:254"`
	CreatedBy int       `json:"createdBy" gorm:"column:created_by"`
	CreatedAt time.Time `json:"createdAt" gorm:"column:created_at"`
	UpdatedBy int       `json:"updatedBy" gorm:"column:updated_by"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"column:updated_at"`
}

func (Contact) TableName() string { return "crewtak_contacts" }

type Handler struct {
	db *gorm.DB
}

func NewHandler(db *gorm.DB) *Handler {
	return &Handler{db: db}
}

// sortContacts orders by name (ignoring case), then by id.
func sortContacts(list []Contact) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := strings.ToLower(list[i].Name), strings.ToLower(list[j].Name)
		if a != b {
			return a < b
		}
		return list[i].ID < list[j].ID
	})
}

func loadContacts(db *gorm.DB, circleID int) ([]Contact, error) {
	list := []Contact{}
	if err := db.Where("circle_id = ?", circleID).Find(&list).Error; err != nil {
		return nil, err
	}
	sortContacts(list)
	return list, nil
}

func (h *Handler) listContacts(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		c.JSON(401, gin.H{"error": "User not authenticated"})
		return
	}
	list, err := loadContacts(h.db.WithContext(c), user.CircleID)
	if err != nil {
		c.JSON(500, gin.H{"error": "Error loading contacts"})
		return
	}
	c.JSON(200, gin.H{"res": list})
}

func (h *Handler) createContact(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		c.JSON(401, gin.H{"error": "User not authenticated"})
		return
	}
	var req contactFields
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request"})
		return
	}
	fields, err := normalizeContact(req)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	now := time.Now().UTC()
	contact := Contact{
		CircleID:  user.CircleID,
		Name:      fields.Name,
		Phone:     fields.Phone,
		Email:     fields.Email,
		CreatedBy: user.ID,
		CreatedAt: now,
		UpdatedBy: user.ID,
		UpdatedAt: now,
	}
	if err := h.db.WithContext(c).Create(&contact).Error; err != nil {
		c.JSON(500, gin.H{"error": "Error saving contact"})
		return
	}
	c.JSON(200, gin.H{"res": contact})
}

func (h *Handler) updateContact(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		c.JSON(401, gin.H{"error": "User not authenticated"})
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid contact"})
		return
	}
	var req contactFields
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Invalid request"})
		return
	}
	fields, err := normalizeContact(req)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	db := h.db.WithContext(c)
	var contact Contact
	if err := db.Where("circle_id = ? AND id = ?", user.CircleID, id).First(&contact).Error; err != nil {
		c.JSON(404, gin.H{"error": "That contact is gone"})
		return
	}
	contact.Name = fields.Name
	contact.Phone = fields.Phone
	contact.Email = fields.Email
	contact.UpdatedBy = user.ID
	contact.UpdatedAt = time.Now().UTC()
	if err := db.Save(&contact).Error; err != nil {
		c.JSON(500, gin.H{"error": "Error saving contact"})
		return
	}
	c.JSON(200, gin.H{"res": contact})
}

func (h *Handler) deleteContact(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		c.JSON(401, gin.H{"error": "User not authenticated"})
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid contact"})
		return
	}
	res := h.db.WithContext(c).Where("circle_id = ? AND id = ?", user.CircleID, id).Delete(&Contact{})
	if res.Error != nil {
		c.JSON(500, gin.H{"error": "Error deleting contact"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(404, gin.H{"error": "That contact is already gone"})
		return
	}
	c.JSON(200, gin.H{"res": id})
}

func Routes(r *gin.Engine, h *Handler, multiAuthMiddleware *auth.MultiAuthMiddleware) {
	g := r.Group("api/v1/crewtak/contacts")
	g.Use(multiAuthMiddleware.MiddlewareFunc())
	{
		g.GET("", h.listContacts)
		g.POST("", h.createContact)
		g.PUT("/:id", h.updateContact)
		g.DELETE("/:id", h.deleteContact)
	}
}
