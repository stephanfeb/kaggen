package memory

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseTemporalQuery extracts a date range from a natural language query.
// Returns zero times and false if no temporal expression is found.
func ParseTemporalQuery(query string, now time.Time) (start, end time.Time, found bool) {
	q := strings.ToLower(query)

	// "yesterday"
	if strings.Contains(q, "yesterday") {
		d := now.AddDate(0, 0, -1)
		return startOfDay(d), endOfDay(d), true
	}
	// "today"
	if strings.Contains(q, "today") {
		return startOfDay(now), endOfDay(now), true
	}

	// "last week"
	if strings.Contains(q, "last week") {
		return now.AddDate(0, 0, -7), now, true
	}
	// "last month"
	if strings.Contains(q, "last month") {
		return now.AddDate(0, -1, 0), now, true
	}
	// "last year"
	if strings.Contains(q, "last year") {
		return now.AddDate(-1, 0, 0), now, true
	}

	// "last N days/weeks/months"
	if m := lastNRe.FindStringSubmatch(q); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := m[2]
		switch {
		case strings.HasPrefix(unit, "day"):
			return now.AddDate(0, 0, -n), now, true
		case strings.HasPrefix(unit, "week"):
			return now.AddDate(0, 0, -n*7), now, true
		case strings.HasPrefix(unit, "month"):
			return now.AddDate(0, -n, 0), now, true
		case strings.HasPrefix(unit, "year"):
			return now.AddDate(-n, 0, 0), now, true
		}
	}

	// Seasons: "last summer", "last winter", etc.
	if m := lastSeasonRe.FindStringSubmatch(q); m != nil {
		year := now.Year()
		// "last" means the most recent past occurrence
		if now.Month() <= 3 {
			year-- // if early in year, "last summer" means previous year
		}
		s, e := seasonRange(m[1], year)
		if !s.IsZero() {
			return s, e, true
		}
	}

	// "in YYYY-MM-DD"
	if m := isoDateRe.FindStringSubmatch(q); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
		return t, endOfDay(t), true
	}

	// "in YYYY-MM"
	if m := isoMonthRe.FindStringSubmatch(q); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		s := time.Date(y, time.Month(mo), 1, 0, 0, 0, 0, time.UTC)
		e := s.AddDate(0, 1, -1)
		return s, endOfDay(e), true
	}

	// "in YYYY"
	if m := isoYearRe.FindStringSubmatch(q); m != nil {
		y, _ := strconv.Atoi(m[1])
		s := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
		e := time.Date(y, 12, 31, 23, 59, 59, 0, time.UTC)
		return s, e, true
	}

	return time.Time{}, time.Time{}, false
}

var (
	lastNRe      = regexp.MustCompile(`last\s+(\d+)\s+(day|week|month|year)s?`)
	lastSeasonRe = regexp.MustCompile(`last\s+(summer|winter|spring|fall|autumn)`)
	isoDateRe    = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)
	isoMonthRe   = regexp.MustCompile(`\b(\d{4})-(\d{2})\b`)
	isoYearRe    = regexp.MustCompile(`\bin\s+(\d{4})\b`)
)

func seasonRange(season string, year int) (start, end time.Time) {
	switch season {
	case "spring":
		return time.Date(year, 3, 1, 0, 0, 0, 0, time.UTC),
			time.Date(year, 5, 31, 23, 59, 59, 0, time.UTC)
	case "summer":
		return time.Date(year, 6, 1, 0, 0, 0, 0, time.UTC),
			time.Date(year, 8, 31, 23, 59, 59, 0, time.UTC)
	case "fall", "autumn":
		return time.Date(year, 9, 1, 0, 0, 0, 0, time.UTC),
			time.Date(year, 11, 30, 23, 59, 59, 0, time.UTC)
	case "winter":
		return time.Date(year, 12, 1, 0, 0, 0, 0, time.UTC),
			time.Date(year+1, 2, 28, 23, 59, 59, 0, time.UTC)
	}
	return time.Time{}, time.Time{}
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func endOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, t.Location())
}
