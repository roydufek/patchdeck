package models

import "testing"

func TestHasNewUpdates(t *testing.T) {
	pkg := func(name, ver string) PackageInfo { return PackageInfo{Name: name, NewVersion: ver} }
	cases := []struct {
		name       string
		prev, curr []PackageInfo
		want       bool
	}{
		{"nothing to nothing", nil, nil, false},
		{"first updates appear (0 -> N)", nil, []PackageInfo{pkg("curl", "8.1")}, true},
		{"same pending set (no new)", []PackageInfo{pkg("curl", "8.1"), pkg("vim", "9.0")}, []PackageInfo{pkg("curl", "8.1"), pkg("vim", "9.0")}, false},
		{"a new package appeared", []PackageInfo{pkg("curl", "8.1")}, []PackageInfo{pkg("curl", "8.1"), pkg("vim", "9.0")}, true},
		{"an update's version bumped", []PackageInfo{pkg("curl", "8.1")}, []PackageInfo{pkg("curl", "8.2")}, true},
		{"set only shrank (some applied)", []PackageInfo{pkg("curl", "8.1"), pkg("vim", "9.0")}, []PackageInfo{pkg("vim", "9.0")}, false},
		{"all applied (N -> 0)", []PackageInfo{pkg("curl", "8.1")}, nil, false},
	}
	for _, c := range cases {
		if got := HasNewUpdates(c.prev, c.curr); got != c.want {
			t.Errorf("%s: HasNewUpdates = %v, want %v", c.name, got, c.want)
		}
	}
}
