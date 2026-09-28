package scheduler

import (
	"testing"
	"time"

	"haynesproform/internal/dates"
)

func sofia(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, dates.Sofia)
}

func TestNextRun(t *testing.T) {
	tests := []struct {
		name string
		from time.Time
		day  int
		hhmm string
		want time.Time
	}{
		{
			name: "later the same month",
			from: sofia(2026, time.September, 5, 9, 0),
			day:  15, hhmm: "08:00",
			want: sofia(2026, time.September, 15, 8, 0),
		},
		{
			name: "already past this month, so next month",
			from: sofia(2026, time.September, 20, 9, 0),
			day:  15, hhmm: "08:00",
			want: sofia(2026, time.October, 15, 8, 0),
		},
		{
			name: "same day but earlier in the day",
			from: sofia(2026, time.September, 15, 6, 0),
			day:  15, hhmm: "08:00",
			want: sofia(2026, time.September, 15, 8, 0),
		},
		{
			name: "same day and exactly at the scheduled time moves on",
			from: sofia(2026, time.September, 15, 8, 0),
			day:  15, hhmm: "08:00",
			want: sofia(2026, time.October, 15, 8, 0),
		},
		{
			name: "day 31 in a 30-day month runs on the 30th",
			from: sofia(2026, time.September, 1, 0, 0),
			day:  31, hhmm: "23:30",
			want: sofia(2026, time.September, 30, 23, 30),
		},
		{
			name: "day 31 in February runs on the 28th",
			from: sofia(2026, time.February, 1, 0, 0),
			day:  31, hhmm: "08:00",
			want: sofia(2026, time.February, 28, 8, 0),
		},
		{
			name: "day 30 in a leap February runs on the 29th",
			from: sofia(2028, time.February, 1, 0, 0),
			day:  30, hhmm: "08:00",
			want: sofia(2028, time.February, 29, 8, 0),
		},
		{
			name: "rolls over the year boundary",
			from: sofia(2026, time.December, 20, 0, 0),
			day:  5, hhmm: "07:15",
			want: sofia(2027, time.January, 5, 7, 15),
		},
		{
			name: "first of the month",
			from: sofia(2026, time.January, 31, 23, 59),
			day:  1, hhmm: "00:30",
			want: sofia(2026, time.February, 1, 0, 30),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextRun(tt.from, tt.day, tt.hhmm)
			if err != nil {
				t.Fatalf("NextRun: %v", err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("NextRun = %s, want %s", got.Format(time.RFC3339), tt.want.Format(time.RFC3339))
			}
		})
	}
}

func TestNextRunIsAlwaysInTheFuture(t *testing.T) {
	// Walk a full year of start points against a 31st schedule, which is the
	// one that clamps most often.
	from := sofia(2026, time.January, 1, 0, 0)
	for i := 0; i < 400; i++ {
		got, err := NextRun(from, 31, "08:00")
		if err != nil {
			t.Fatalf("NextRun at %s: %v", from, err)
		}
		if !got.After(from) {
			t.Fatalf("NextRun(%s) = %s, which is not in the future", from, got)
		}
		from = from.Add(24 * time.Hour)
	}
}

func TestNextRunRejectsBadInput(t *testing.T) {
	now := sofia(2026, time.September, 1, 0, 0)
	for _, tt := range []struct {
		day  int
		hhmm string
	}{
		{0, "08:00"}, {32, "08:00"}, {15, "25:00"}, {15, "08:60"},
		{15, "0800"}, {15, ""}, {15, "aa:bb"},
	} {
		if _, err := NextRun(now, tt.day, tt.hhmm); err == nil {
			t.Errorf("NextRun(day=%d, hhmm=%q) accepted invalid input", tt.day, tt.hhmm)
		}
	}
}

func TestParseHHMM(t *testing.T) {
	h, m, err := ParseHHMM(" 07:05 ")
	if err != nil || h != 7 || m != 5 {
		t.Errorf("ParseHHMM = %d:%d, %v; want 7:5", h, m, err)
	}
	if _, _, err := ParseHHMM("24:00"); err == nil {
		t.Error("ParseHHMM accepted hour 24")
	}
}
