package scheduler

import (
	"testing"
	"time"
)

// utc builds a UTC time and asserts its weekday matches wantWD, so the cron
// assertions below can't silently test the wrong day if the date arithmetic is off.
func utc(t *testing.T, y int, mo time.Month, d, h, mi int, wantWD time.Weekday) time.Time {
	t.Helper()
	tm := time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
	if tm.Weekday() != wantWD {
		t.Fatalf("test setup: %s is %s, expected %s", tm.Format("2006-01-02"), tm.Weekday(), wantWD)
	}
	return tm
}

func TestCronMatches(t *testing.T) {
	// Anchor dates in June 2026: the 1st is a Monday.
	mon := utc(t, 2026, time.June, 1, 0, 0, time.Monday)        // 2026-06-01 Monday
	fri := utc(t, 2026, time.June, 5, 0, 0, time.Friday)        // 2026-06-05 Friday (not the 13th)
	sun := utc(t, 2026, time.June, 7, 0, 0, time.Sunday)        // 2026-06-07 Sunday
	sat13 := utc(t, 2026, time.June, 13, 0, 0, time.Saturday)   // 2026-06-13 Saturday (the 13th)
	satNo13 := utc(t, 2026, time.June, 6, 0, 0, time.Saturday)  // 2026-06-06 Saturday (neither Fri nor 13th)

	cases := []struct {
		name string
		expr string
		when time.Time
		want bool
	}{
		{"daily at 0:00 hits midnight", "0 0 * * *", mon, true},
		{"daily at 3:00 misses midnight", "0 3 * * *", mon, false},
		{"step minutes match :00", "*/15 0 * * *", utc(t, 2026, time.June, 1, 0, 0, time.Monday), true},
		{"step minutes miss :07", "*/15 * * * *", utc(t, 2026, time.June, 1, 9, 7, time.Monday), false},

		// weekday 7 == Sunday (the bug fix)
		{"weekday 7 fires on Sunday", "0 0 * * 7", sun, true},
		{"weekday 0 fires on Sunday", "0 0 * * 0", sun, true},
		{"weekday 7 does not fire on Monday", "0 0 * * 7", mon, false},
		{"weekday range 5-7 fires on Sunday", "0 0 * * 5-7", sun, true},
		{"weekday name sun fires on Sunday", "0 0 * * sun", sun, true},

		// DoM + DoW OR semantics when BOTH restricted: "13th OR Friday"
		{"DoM/DoW OR: Friday matches via DoW", "0 0 13 * 5", fri, true},
		{"DoM/DoW OR: 13th matches via DoM", "0 0 13 * 5", sat13, true},
		{"DoM/DoW OR: neither Friday nor 13th misses", "0 0 13 * 5", satNo13, false},

		// AND semantics when only one is restricted
		{"DoW only restricted: Monday matches", "0 0 * * 1", mon, true},
		{"DoW only restricted: Friday misses Monday rule", "0 0 * * 1", fri, false},
		{"DoM only restricted: 13th matches", "0 0 13 * *", sat13, true},
		{"DoM only restricted: 5th misses", "0 0 13 * *", fri, false},

		// macros
		{"@daily fires at midnight", "@daily", mon, true},
		{"@weekly fires Sunday midnight", "@weekly", sun, true},
		{"@weekly misses Monday", "@weekly", mon, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cronMatches(tc.expr, tc.when); got != tc.want {
				t.Errorf("cronMatches(%q, %s)=%v, want %v", tc.expr, tc.when.Format("2006-01-02 15:04 Mon"), got, tc.want)
			}
		})
	}
}

func TestNextRun(t *testing.T) {
	from := utc(t, 2026, time.June, 1, 4, 0, time.Monday) // Monday 04:00
	got := NextRun("0 3 * * *", from)
	if got == nil {
		t.Fatal("NextRun returned nil for a daily expression")
	}
	want := time.Date(2026, time.June, 2, 3, 0, 0, 0, time.UTC) // next 03:00 is the following day
	if !got.Equal(want) {
		t.Errorf("NextRun=%s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}

	// A Sunday-only (weekday 7) schedule must produce a real next time, not nil.
	if NextRun("0 0 * * 7", from) == nil {
		t.Error("NextRun returned nil for weekday-7 (Sunday) schedule — should find the next Sunday")
	}

	// Garbage expression yields nil rather than scanning forever.
	if NextRun("not a cron", from) != nil {
		t.Error("NextRun should return nil for an invalid expression")
	}
}
