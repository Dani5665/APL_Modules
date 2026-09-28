package dates

import (
	"testing"
	"time"
)

func TestEndDate(t *testing.T) {
	tests := []struct {
		name   string
		start  string
		months int
		want   string
	}{
		{"one month", "2026-03-01", 1, "2026-03-31"},
		{"three months", "2026-01-15", 3, "2026-04-14"},
		{"full year", "2026-06-01", 12, "2027-05-31"},
		// 31 Jan + 1 month clamps to 28 Feb, so the inclusive end is 27 Feb.
		{"month-end clamp", "2026-01-31", 1, "2026-02-27"},
		{"month-end clamp leap year", "2028-01-31", 1, "2028-02-28"},
		{"clamp to 30-day month", "2026-05-31", 1, "2026-06-29"},
		{"across year boundary", "2026-12-15", 2, "2027-02-14"},
		{"first of month", "2026-02-01", 1, "2026-02-28"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EndDate(tt.start, tt.months)
			if err != nil {
				t.Fatalf("EndDate(%q, %d): %v", tt.start, tt.months, err)
			}
			if got != tt.want {
				t.Errorf("EndDate(%q, %d) = %q, want %q", tt.start, tt.months, got, tt.want)
			}
		})
	}
}

func TestEndDateRejectsBadInput(t *testing.T) {
	if _, err := EndDate("15.01.2026", 1); err == nil {
		t.Error("expected an error for a non-ISO start date")
	}
	if _, err := EndDate("2026-01-15", 0); err == nil {
		t.Error("expected an error for zero months")
	}
}

func TestDaysInMonth(t *testing.T) {
	tests := []struct {
		year  int
		month int
		want  int
	}{
		{2026, 1, 31}, {2026, 2, 28}, {2028, 2, 29}, {2026, 4, 30}, {2026, 12, 31},
	}
	for _, tt := range tests {
		if got := DaysInMonth(tt.year, monthOf(tt.month)); got != tt.want {
			t.Errorf("DaysInMonth(%d, %d) = %d, want %d", tt.year, tt.month, got, tt.want)
		}
	}
}

func TestFormatDisplay(t *testing.T) {
	if got := FormatDisplay("2026-01-05"); got != "05.01.2026" {
		t.Errorf("FormatDisplay = %q, want %q", got, "05.01.2026")
	}
	if got := FormatDisplay("nonsense"); got != "nonsense" {
		t.Errorf("FormatDisplay passed through = %q, want %q", got, "nonsense")
	}
}

func monthOf(m int) time.Month { return time.Month(m) }
