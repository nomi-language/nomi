package regexgroups

import (
	"reflect"
	"regexp"
	"testing"
)

// shape renders groups as one letter each, S for a group every match fills
// and M for one a match may leave out, with a name after `=`.
func shape(groups []Group) []string {
	out := make([]string, len(groups))
	for i, g := range groups {
		s := "S"
		if g.Optional {
			s = "M"
		}
		if g.Name != "" {
			s += "=" + g.Name
		}
		out[i] = s
	}
	return out
}

func TestGroups(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		want    []string
	}{
		{`\d+`, []string{}},
		{`(\d+)`, []string{"S"}},
		{`(\d+)-(\w+)?`, []string{"S", "M"}},
		{`(a)|(b)`, []string{"M", "M"}},
		{`(a)?`, []string{"M"}},
		{`(a)*`, []string{"M"}},
		{`(a)+`, []string{"S"}},
		{`(a){2,3}`, []string{"S"}},
		{`(a){0,3}`, []string{"M"}},
		{`(a){0}`, []string{"M"}},
		{`(?:(a))?`, []string{"M"}},
		{`((a)?)`, []string{"S", "M"}},
		{`((a))?`, []string{"M", "M"}},
		{`((a)|b)`, []string{"S", "M"}},
		{`x(a)|x(b)`, []string{"M", "M"}},
		{`(a*)`, []string{"S"}},
		{`()`, []string{"S"}},
		{`(?:a|(b))c`, []string{"M"}},
		{`(?P<year>\d{4})-(?<month>\d\d)?`, []string{"S=year", "M=month"}},
		{`(?P<key>\w+)=(\w+)`, []string{"S=key", "S"}},
		{`(?i)(a)(?:b(c))+`, []string{"S", "S"}},
	} {
		groups, err := Groups(tc.pattern)
		if err != nil {
			t.Errorf("%s: %v", tc.pattern, err)
			continue
		}
		if got := shape(groups); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: groups %v, want %v", tc.pattern, got, tc.want)
		}
		// Group count and names agree with the compiled regexp's.
		re := regexp.MustCompile(tc.pattern)
		if re.NumSubexp() != len(groups) {
			t.Errorf("%s: %d groups, regexp has %d", tc.pattern, len(groups), re.NumSubexp())
		}
		for i, name := range re.SubexpNames()[1:] {
			if i < len(groups) && groups[i].Name != name {
				t.Errorf("%s: group %d named %q, regexp names it %q", tc.pattern, i+1, groups[i].Name, name)
			}
		}
	}
}

// TestGroupsOptionalAgreesWithMatches holds Optional to what matching does:
// a group the analysis calls required takes part in every match of these
// inputs.
func TestGroupsOptionalAgreesWithMatches(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		inputs  []string
	}{
		{`(\d+)-(\w+)?`, []string{"1-", "12-ab"}},
		{`((a)?)b`, []string{"b", "ab"}},
		{`(a)+(b)*`, []string{"a", "aab", "abb"}},
		{`(x)|(y)`, []string{"x", "y"}},
	} {
		groups, err := Groups(tc.pattern)
		if err != nil {
			t.Fatal(err)
		}
		re := regexp.MustCompile(tc.pattern)
		for _, in := range tc.inputs {
			loc := re.FindStringSubmatchIndex(in)
			if loc == nil {
				t.Fatalf("%s does not match %q", tc.pattern, in)
			}
			for i, g := range groups {
				if !g.Optional && loc[2*(i+1)] < 0 {
					t.Errorf("%s on %q: group %d is required but did not take part", tc.pattern, in, i+1)
				}
			}
		}
	}
}

func TestGroupsRefusesAnInvalidPattern(t *testing.T) {
	if _, err := Groups(`(`); err == nil {
		t.Fatal("Groups(`(`) answered no error")
	}
}
