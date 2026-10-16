package migrations

import (
	"context"

	"gorm.io/gorm"
)

// CrewTAK: crews are created as "<name>'s crew". Rename crews that were
// created before the fork as "<name>'s circle" so the old wording disappears.
type CrewTAKRenameCircles20261016 struct{}

func (m CrewTAKRenameCircles20261016) ID() string {
	return "20261016_crewtak_rename_circles_to_crews"
}

func (m CrewTAKRenameCircles20261016) Description() string {
	return `CrewTAK: rename default "<name>'s circle" crew names to "<name>'s crew".`
}

func (m CrewTAKRenameCircles20261016) Down(ctx context.Context, db *gorm.DB) error {
	return nil
}

func (m CrewTAKRenameCircles20261016) Up(ctx context.Context, db *gorm.DB) error {
	if !db.Migrator().HasTable("circles") {
		return nil
	}
	// substr/length/|| behave the same in SQLite and Postgres.
	// len("'s circle") == 9
	return db.WithContext(ctx).Exec(
		`UPDATE circles SET name = substr(name, 1, length(name) - 9) || ? WHERE name LIKE ?`,
		"'s crew", "%'s circle",
	).Error
}

func init() {
	Register(CrewTAKRenameCircles20261016{})
}
