package resources

// Field rules for Contacts (the Resources → Contacts phone book). Kept free
// of database and web imports so the rules are easy to test on their own.
// Keep in step with the frontend's src/crewtak/contacts.js.

import (
	"errors"
	"net/mail"
	"strings"
	"unicode"
)

const (
	maxContactName  = 100
	maxContactPhone = 40
	maxContactEmail = 254
	// Short internal extensions (e.g. 4 digits) are fine; anything with
	// fewer digits than this isn't a phone number.
	minPhoneDigits = 3
)

// contactFields is what a person types in for a contact.
type contactFields struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
	Email string `json:"email"`
}

// collapseSpaces trims and turns any run of whitespace into one space.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// phoneAllowed: digits, spaces and the punctuation people write phone
// numbers with, plus letters for "ext" / "x" extensions.
func phoneAllowed(r rune) bool {
	if unicode.IsDigit(r) || r == ' ' {
		return true
	}
	if strings.ContainsRune("+()-./#*,", r) {
		return true
	}
	return strings.ContainsRune("extEXT", r)
}

// normalizeContact tidies a contact and checks it. Name is required, plus at
// least one of phone or email. The phone is kept as typed (spaces tidied);
// the email is lowercased.
func normalizeContact(in contactFields) (contactFields, error) {
	out := contactFields{
		Name:  collapseSpaces(in.Name),
		Phone: collapseSpaces(in.Phone),
		Email: strings.ToLower(strings.TrimSpace(in.Email)),
	}
	switch {
	case out.Name == "":
		return out, errors.New("Enter a name")
	case len([]rune(out.Name)) > maxContactName:
		return out, errors.New("Name can be at most 100 characters")
	case out.Phone == "" && out.Email == "":
		return out, errors.New("Add a phone number or an email")
	}
	if out.Phone != "" {
		digits := 0
		for _, r := range out.Phone {
			if !phoneAllowed(r) {
				return out, errors.New("That phone number has characters it shouldn't")
			}
			if unicode.IsDigit(r) {
				digits++
			}
		}
		if digits < minPhoneDigits {
			return out, errors.New("That phone number is too short")
		}
		if len(out.Phone) > maxContactPhone {
			return out, errors.New("Phone number can be at most 40 characters")
		}
	}
	if out.Email != "" {
		addr, err := mail.ParseAddress(out.Email)
		domain := out.Email[strings.LastIndex(out.Email, "@")+1:]
		if err != nil || addr.Address != out.Email || !strings.Contains(domain, ".") {
			return out, errors.New("That email address doesn't look right")
		}
		if len(out.Email) > maxContactEmail {
			return out, errors.New("Email can be at most 254 characters")
		}
	}
	return out, nil
}
