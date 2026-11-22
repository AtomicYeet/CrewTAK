package resources

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestLoadContactsSortedAndScopedToCrew(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "contacts.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&Contact{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, c := range []Contact{
		{CircleID: 1, Name: "tsa", Phone: "9205550100"},
		{CircleID: 1, Name: "Airport Ops", Phone: "9205550142"},
		{CircleID: 2, Name: "Another crew", Phone: "9205550000"},
		{CircleID: 1, Name: "Pam Sanders", Email: "pam@example.com"},
	} {
		c := c
		if err := db.Create(&c).Error; err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	list, err := loadContacts(db, 1)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := []string{}
	for _, c := range list {
		got = append(got, c.Name)
	}
	want := []string{"Airport Ops", "Pam Sanders", "tsa"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
