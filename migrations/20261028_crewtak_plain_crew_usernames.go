package migrations

import (
	"context"
	"strings"

	"gorm.io/gorm"
)

// CrewTAK: crew logins used to be stored as "<owner>_<name>" and signed in
// through a separate "sub account" form. Login is now a single username box,
// so strip the owner prefix and let everyone sign in with just "<name>".
// Passwords are untouched. A name that would collide with an existing
// username is left as-is.
type CrewTAKPlainCrewUsernames20261028 struct{}

func (m CrewTAKPlainCrewUsernames20261028) ID() string {
	return "20261028_crewtak_plain_crew_usernames"
}

func (m CrewTAKPlainCrewUsernames20261028) Description() string {
	return `CrewTAK: rename "<owner>_<name>" crew logins to plain "<name>".`
}

func (m CrewTAKPlainCrewUsernames20261028) Down(ctx context.Context, db *gorm.DB) error {
	return nil
}

func (m CrewTAKPlainCrewUsernames20261028) Up(ctx context.Context, db *gorm.DB) error {
	if !db.Migrator().HasTable("users") || !db.Migrator().HasColumn("users", "parent_user_id") {
		return nil
	}

	type row struct {
		ID             int
		Username       string
		ParentUsername string
	}
	var rows []row
	if err := db.WithContext(ctx).Raw(`
		SELECT u.id AS id, u.username AS username, p.username AS parent_username
		FROM users u JOIN users p ON p.id = u.parent_user_id
		WHERE u.parent_user_id IS NOT NULL`).Scan(&rows).Error; err != nil {
		return err
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, r := range rows {
			prefix := r.ParentUsername + "_"
			if r.ParentUsername == "" || !strings.HasPrefix(r.Username, prefix) {
				continue
			}
			plain := strings.ToLower(strings.TrimPrefix(r.Username, prefix))
			if plain == "" {
				continue
			}
			var taken int64
			if err := tx.Table("users").
				Where("LOWER(username) = ? AND id <> ?", plain, r.ID).
				Count(&taken).Error; err != nil {
				return err
			}
			if taken > 0 {
				continue
			}
			if err := tx.Exec(`UPDATE users SET username = ? WHERE id = ?`, plain, r.ID).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func init() {
	Register(CrewTAKPlainCrewUsernames20261028{})
}
