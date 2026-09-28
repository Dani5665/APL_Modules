// Package scheduler runs the monthly Excel export from a single in-process
// goroutine. There is one instance of this application, so a cron library and
// a distributed lock would both be more machinery than the job needs.
package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"haynesproform/internal/dates"
)

// ParseHHMM parses a "HH:MM" time of day.
func ParseHHMM(s string) (hour, minute int, err error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, 0, fmt.Errorf("невалиден час %q, очаква се ЧЧ:ММ", s)
	}
	hour, err = strconv.Atoi(h)
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("невалиден час %q, очаква се ЧЧ:ММ", s)
	}
	minute, err = strconv.Atoi(m)
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("невалиден час %q, очаква се ЧЧ:ММ", s)
	}
	return hour, minute, nil
}

// NextRun returns the first scheduled moment strictly after from.
//
// The schedule is monthly on dayOfMonth at hh:mm in Europe/Sofia. A month
// shorter than dayOfMonth runs on its last day instead, so a 31st schedule
// still fires in February.
func NextRun(from time.Time, dayOfMonth int, hhmm string) (time.Time, error) {
	if dayOfMonth < 1 || dayOfMonth > 31 {
		return time.Time{}, fmt.Errorf("невалиден ден от месеца: %d", dayOfMonth)
	}
	hour, minute, err := ParseHHMM(hhmm)
	if err != nil {
		return time.Time{}, err
	}

	from = from.In(dates.Sofia)
	year, month := from.Year(), from.Month()

	// Try this month, then the following ones. Two iterations always suffice,
	// but the loop keeps the intent obvious.
	for i := 0; i < 3; i++ {
		candidate := monthlyRun(year, month, dayOfMonth, hour, minute)
		if candidate.After(from) {
			return candidate, nil
		}
		month++
		if month > time.December {
			month = time.January
			year++
		}
	}
	return time.Time{}, fmt.Errorf("не може да се изчисли следващо изпълнение")
}

// monthlyRun builds the run time in the given month, clamping the day to the
// month's length.
func monthlyRun(year int, month time.Month, dayOfMonth, hour, minute int) time.Time {
	// Normalise a month that overflowed past December.
	t := time.Date(year, month, 1, 0, 0, 0, 0, dates.Sofia)
	year, month = t.Year(), t.Month()

	day := dayOfMonth
	if last := dates.DaysInMonth(year, month); day > last {
		day = last
	}
	return time.Date(year, month, day, hour, minute, 0, 0, dates.Sofia)
}
