// Package dates centralises the application's calendar arithmetic.
//
// All business decisions (activation windows, the export schedule, "today")
// happen in Europe/Sofia, while stored timestamps are UTC. Keeping both in
// one place keeps that split from leaking into handlers.
package dates

import (
	"fmt"
	"time"
)

// ISO is the storage layout for calendar dates.
const ISO = "2006-01-02"

// Display is the layout shown to users.
const Display = "02.01.2006"

// Sofia is the business time zone. Loaded eagerly so a container missing
// tzdata fails at startup rather than at the first activation.
var Sofia = mustLoad("Europe/Sofia")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(fmt.Sprintf("load time zone %s (is time/tzdata imported?): %v", name, err))
	}
	return loc
}

// Now returns the current instant in the business time zone.
func Now() time.Time { return time.Now().In(Sofia) }

// Today returns today's date in the business time zone, as an ISO string.
func Today() string { return Now().Format(ISO) }

// ParseISO parses a stored "YYYY-MM-DD" date into midnight in Sofia.
func ParseISO(s string) (time.Time, error) {
	t, err := time.ParseInLocation(ISO, s, Sofia)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q, expected YYYY-MM-DD", s)
	}
	return t, nil
}

// FormatDisplay converts a stored ISO date to DD.MM.YYYY. Input that is not a
// valid date is returned unchanged so a template never renders an error.
func FormatDisplay(iso string) string {
	t, err := ParseISO(iso)
	if err != nil {
		return iso
	}
	return t.Format(Display)
}

// EndDate returns the inclusive last day of an activation that starts on
// startISO and runs for the given whole months:
//
//	end = start + months - 1 day
//
// Month arithmetic clamps to the end of the target month, so a 31 January
// start plus one month ends on 28 February (or 29 in a leap year) rather than
// rolling into March.
func EndDate(startISO string, months int) (string, error) {
	start, err := ParseISO(startISO)
	if err != nil {
		return "", err
	}
	if months < 1 {
		return "", fmt.Errorf("months must be at least 1, got %d", months)
	}
	return AddMonths(start, months).AddDate(0, 0, -1).Format(ISO), nil
}

// AddMonths adds whole months to t, clamping the day of month to the last day
// of the resulting month instead of overflowing into the next one.
func AddMonths(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	target := time.Date(y, m+time.Month(months), 1, t.Hour(), t.Minute(), t.Second(), 0, t.Location())
	if last := DaysInMonth(target.Year(), target.Month()); d > last {
		d = last
	}
	return time.Date(target.Year(), target.Month(), d, t.Hour(), t.Minute(), t.Second(), 0, t.Location())
}

// DaysInMonth returns the number of days in the given month.
func DaysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// NowUTC returns the current instant as a stored UTC timestamp.
func NowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// FormatUTC renders t as a stored UTC timestamp.
func FormatUTC(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ParseUTC parses a stored UTC timestamp.
func ParseUTC(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

// DisplayTimestamp renders a stored UTC timestamp as "DD.MM.YYYY HH:MM" in
// the business time zone. Unparsable input is returned unchanged.
func DisplayTimestamp(s string) string {
	t, err := ParseUTC(s)
	if err != nil {
		return s
	}
	return t.In(Sofia).Format("02.01.2006 15:04")
}
