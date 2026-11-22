package resources

import (
	"strings"
	"testing"
)

func TestNormalizeContact(t *testing.T) {
	cases := []struct {
		name string
		in   contactFields
		want contactFields
		err  string // part of the error message; "" = ok
	}{
		{"phone only", contactFields{Name: "  Airport   Ops ", Phone: " (920) 555-0142 "}, contactFields{Name: "Airport Ops", Phone: "(920) 555-0142"}, ""},
		{"email only, lowercased", contactFields{Name: "Pam", Email: " Pam.Sanders@Example.COM "}, contactFields{Name: "Pam", Email: "pam.sanders@example.com"}, ""},
		{"both", contactFields{Name: "TSA", Phone: "920-555-0100 ext 12", Email: "tsa@example.gov"}, contactFields{Name: "TSA", Phone: "920-555-0100 ext 12", Email: "tsa@example.gov"}, ""},
		{"internal extension", contactFields{Name: "Tower", Phone: "4567"}, contactFields{Name: "Tower", Phone: "4567"}, ""},
		{"international", contactFields{Name: "Vendor", Phone: "+1 920.555.0199 x3"}, contactFields{Name: "Vendor", Phone: "+1 920.555.0199 x3"}, ""},
		{"no name", contactFields{Name: "   ", Phone: "9205550142"}, contactFields{}, "name"},
		{"name too long", contactFields{Name: strings.Repeat("a", 101), Phone: "9205550142"}, contactFields{}, "100"},
		{"nothing to call", contactFields{Name: "Ghost"}, contactFields{}, "phone number or an email"},
		{"phone too short", contactFields{Name: "Short", Phone: "12"}, contactFields{}, "too short"},
		{"phone letters", contactFields{Name: "Letters", Phone: "call me maybe 555"}, contactFields{}, "characters"},
		{"phone too long", contactFields{Name: "Long", Phone: strings.Repeat("9", 41)}, contactFields{}, "40"},
		{"bad email", contactFields{Name: "Bad", Email: "not-an-email"}, contactFields{}, "email"},
		{"email with name", contactFields{Name: "Bad", Email: "Pam <pam@example.com>"}, contactFields{}, "email"},
		{"email no dot", contactFields{Name: "Bad", Email: "pam@localhost"}, contactFields{}, "email"},
	}
	for _, c := range cases {
		got, err := normalizeContact(c.in)
		if c.err == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			} else if got != c.want {
				t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: got err %v, want one mentioning %q", c.name, err, c.err)
		}
	}
}
