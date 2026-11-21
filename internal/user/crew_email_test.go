package user

import "testing"

// CrewTAK: optional email on crew logins (Settings → Crew Users).
func TestNormalizeCrewEmail(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", true},
		{"   ", "", true},
		{"Steven@Example.com", "steven@example.com", true},
		{"  ff1@katw.example.org ", "ff1@katw.example.org", true},
		{"not-an-email", "", false},
		{"Steven <steven@example.com>", "", false},
		{"a@b.com, c@d.com", "", false},
	}
	for _, c := range cases {
		got, ok := normalizeCrewEmail(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("normalizeCrewEmail(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
