package user

import (
	"reflect"
	"testing"
	"time"
)

// CrewTAK: rank, qualifications and hire date on crew logins
// (Settings → Crew Users).

func TestNormalizeCrewRank(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", true},
		{"  ", "", true},
		{"captain", "CAPTAIN", true},
		{" Driver_Operator ", "DRIVER_OPERATOR", true},
		{"OPS_LEAD", "OPS_LEAD", true},
		{"LIEUTENANT", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeCrewRank(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("NormalizeCrewRank(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeCrewQuals(t *testing.T) {
	cases := []struct {
		in   []string
		want string
		ok   bool
	}{
		{nil, "", true},
		{[]string{}, "", true},
		{[]string{"", " "}, "", true},
		// Canonical order, duplicates dropped, case-insensitive.
		{[]string{"emt", "ACTING_OFFICER", "arff_ff", "EMT"}, "ARFF_FF,ACTING_OFFICER,EMT", true},
		{[]string{"DO_PUMPER", "DO_ARFF"}, "DO_ARFF,DO_PUMPER", true},
		{[]string{"ARFF_FF", "PARAMEDIC"}, "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeCrewQuals(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("NormalizeCrewQuals(%v) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestSplitCrewQuals(t *testing.T) {
	if got := SplitCrewQuals(""); got == nil || len(got) != 0 {
		t.Errorf("SplitCrewQuals(\"\") = %#v; want empty non-nil slice", got)
	}
	want := []string{"ARFF_FF", "EMT"}
	if got := SplitCrewQuals("ARFF_FF,EMT"); !reflect.DeepEqual(got, want) {
		t.Errorf("SplitCrewQuals = %v; want %v", got, want)
	}
}

func TestNormalizeHireDate(t *testing.T) {
	nextYear := time.Now().AddDate(2, 0, 0).Format("2006-01-02")
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", true},
		{" 2014-06-02 ", "2014-06-02", true},
		{"2014-6-2", "", false},
		{"06/02/2014", "", false},
		{"2014-02-30", "", false},
		{"1890-01-01", "", false},
		{nextYear, "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeHireDate(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("NormalizeHireDate(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
