package timesheet

import (
	"strings"
	"testing"
	"time"

	"bordroplus/internal/leave"
)

// at parses an Istanbul local time "2006-01-02 15:04".
func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, Istanbul)
	if err != nil {
		panic(err)
	}
	return t
}

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// punches builds punches from "in 2026-11-02 08:00" style specs, ids 1..n.
func punches(spec ...string) []Punch {
	out := make([]Punch, len(spec))
	for i, s := range spec {
		dir, when, _ := strings.Cut(s, " ")
		out[i] = Punch{ID: int64(i + 1), At: at(when), Dir: Direction(dir)}
	}
	return out
}

var holidays = leave.Holidays{
	day("2026-10-28"): true,  // Wednesday, Cumhuriyet Bayramı arifesi: afternoon off
	day("2026-10-29"): false, // Thursday, Cumhuriyet Bayramı
}

var asOf = at("2026-12-31 23:59")

func finalised(t *testing.T, spec ...string) []Session {
	t.Helper()
	s, a := Pair(punches(spec...), asOf, DefaultRules)
	if len(a) != 0 {
		t.Fatalf("unexpected anomalies: %+v", a)
	}
	return Finalise(s, holidays, DefaultRules)
}

func segs(s Session) map[string]int {
	m := map[string]int{}
	for _, g := range s.Segments {
		m[g.Date.Format("01-02")+" "+string(g.Kind)] = g.Minutes
	}
	return m
}

func TestRequiredBreak(t *testing.T) {
	for gross, want := range map[int]int{0: 0, 10: 10, 240: 15, 241: 30, 450: 30, 451: 60, 720: 60} {
		if got := RequiredBreak(gross); got != want {
			t.Errorf("RequiredBreak(%d) = %d, want %d", gross, got, want)
		}
	}
}

func TestOrdinaryDayAndLunchGap(t *testing.T) {
	s := finalised(t, "in 2026-11-02 08:30", "out 2026-11-02 18:00")
	if s[0].Gross != 570 || s[0].Break != 60 || s[0].Net != 510 {
		t.Fatalf("one session: %+v", s[0])
	}
	// Lunch taken outside (45 min gap): only the missing 15 minutes are deducted.
	s = finalised(t, "in 2026-11-02 08:00", "out 2026-11-02 12:00", "in 2026-11-02 12:45", "out 2026-11-02 17:30")
	if s[0].Net+s[1].Net != 510 || s[1].Break != 15 {
		t.Fatalf("with lunch gap: %+v", s)
	}
}

func TestNightShiftCrossesMidnight(t *testing.T) {
	// Friday 22:00 → Saturday 06:00: both ordinary days; break taken around 02:00.
	s := finalised(t, "in 2026-11-06 22:00", "out 2026-11-07 06:00")[0]
	if got := segs(s); got["11-06 normal"] != 120 || got["11-07 normal"] != 300 || s.WorkDate != day("2026-11-06") {
		t.Fatalf("Fri night: %v work date %s", got, s.WorkDate)
	}
	// Saturday 22:00 → Sunday 06:00: the Sunday part is weekly rest day work.
	s = finalised(t, "in 2026-11-07 22:00", "out 2026-11-08 06:00")[0]
	if got := segs(s); got["11-07 normal"] != 120 || got["11-08 rest_day"] != 300 {
		t.Fatalf("Sat night: %v", got)
	}
}

func TestHolidayClassification(t *testing.T) {
	// Arife: ordinary until 13:00, holiday afterwards; the break falls at 13:30.
	s := finalised(t, "in 2026-10-28 09:00", "out 2026-10-28 18:00")[0]
	if got := segs(s); got["10-28 normal"] != 240 || got["10-28 holiday"] != 240 {
		t.Fatalf("arife: %v", got)
	}
	// Night into the holiday: Wednesday 22:00 is already holiday afternoon.
	s = finalised(t, "in 2026-10-28 22:00", "out 2026-10-29 06:00")[0]
	if got := segs(s); got["10-28 holiday"] != 120 || got["10-29 holiday"] != 300 {
		t.Fatalf("night into holiday: %v", got)
	}
}

func TestDuplicatePunches(t *testing.T) {
	s, a := Pair(punches("in 2026-11-02 08:00", "in 2026-11-02 08:02", "out 2026-11-02 17:00", "out 2026-11-02 17:01"), asOf, DefaultRules)
	if len(s) != 1 || s[0].In != at("2026-11-02 08:00") || s[0].Out != at("2026-11-02 17:00") {
		t.Fatalf("sessions: %+v", s)
	}
	if len(a) != 2 || a[0].Key != "drop:2" || a[1].Key != "drop:4" || a[0].SuggestedAt != nil {
		t.Fatalf("anomalies: %+v", a)
	}
}

func TestMissingPunchesAndSuggestions(t *testing.T) {
	// Monday's out is missing: Monday's time is not counted, Tuesday's is.
	s, a := Pair(punches("in 2026-11-02 08:00", "in 2026-11-03 08:00", "out 2026-11-03 17:00"), asOf, DefaultRules)
	if len(s) != 1 || len(a) != 1 || a[0].Kind != MissingOut || a[0].Key != "out:1" {
		t.Fatalf("missing out: %+v %+v", s, a)
	}
	if !a[0].SuggestedAt.Equal(at("2026-11-02 17:00")) { // default 9 h shift
		t.Fatalf("suggested out = %s", a[0].SuggestedAt)
	}

	// An out with no in: suggest an in one typical shift earlier.
	_, a = Pair(punches("out 2026-11-02 17:30"), asOf, DefaultRules)
	if len(a) != 1 || a[0].Kind != MissingIn || !a[0].SuggestedAt.Equal(at("2026-11-02 08:30")) {
		t.Fatalf("missing in: %+v", a)
	}

	// Forgot to punch out Monday and in Tuesday: a 33-hour "session" is two anomalies.
	s, a = Pair(punches("in 2026-11-02 08:00", "out 2026-11-03 17:00"), asOf, DefaultRules)
	if len(s) != 0 || len(a) != 2 || a[0].Key != "out:1" || a[1].Key != "in:2" {
		t.Fatalf("long gap: %+v %+v", s, a)
	}
}

func TestSuggestionsLearnTypicalShift(t *testing.T) {
	// Three 8-hour days establish the typical shift; the suggestion follows it.
	_, a := Pair(punches(
		"in 2026-11-02 09:00", "out 2026-11-02 17:00",
		"in 2026-11-03 09:00", "out 2026-11-03 17:00",
		"in 2026-11-04 09:00", "out 2026-11-04 17:00",
		"in 2026-11-05 09:00",
		"in 2026-11-06 09:00", "out 2026-11-06 17:00",
	), asOf, DefaultRules)
	if len(a) != 1 || !a[0].SuggestedAt.Equal(at("2026-11-05 17:00")) {
		t.Fatalf("anomalies: %+v", a)
	}
	// Never after the next punch.
	_, a = Pair(punches("in 2026-11-02 09:00", "in 2026-11-02 15:00", "out 2026-11-02 18:00"), asOf, DefaultRules)
	if !a[0].SuggestedAt.Equal(at("2026-11-02 14:59")) {
		t.Fatalf("clamped suggestion: %s", a[0].SuggestedAt)
	}
}

func TestOpenShiftIsNotAnAnomalyYet(t *testing.T) {
	ps := punches("in 2026-11-02 08:00")
	if _, a := Pair(ps, at("2026-11-02 15:00"), DefaultRules); len(a) != 0 {
		t.Fatalf("ongoing shift flagged: %+v", a)
	}
	if _, a := Pair(ps, at("2026-11-03 08:01"), DefaultRules); len(a) != 1 || a[0].Kind != MissingOut {
		t.Fatalf("stale open shift: %+v", a)
	}
}

func TestWeeklyOvertimeRestDayAndHoliday(t *testing.T) {
	var spec []string
	for _, d := range []string{"02", "03", "04", "05", "06", "07"} { // Mon-Sat, 9.5 h gross, 8.5 h net
		spec = append(spec, "in 2026-11-"+d+" 08:00", "out 2026-11-"+d+" 17:30")
	}
	spec = append(spec, "in 2026-11-08 10:00", "out 2026-11-08 14:00") // Sunday 4 h
	weeks := Weekly(finalised(t, spec...), DefaultRules)
	if len(weeks) != 1 {
		t.Fatalf("weeks: %+v", weeks)
	}
	w := weeks[0]
	// 6 × 510 = 3060 ordinary minutes: 2700 normal + 360 overtime; Sunday 240 - 15 break.
	if w.NormalMinutes != 2700 || w.OvertimeMinutes != 360 || w.RestDayMinutes != 225 || w.WorkedDays != 7 {
		t.Fatalf("week: %+v", w)
	}

	// Holiday week: holiday minutes do not count towards 45 hours.
	weeks = Weekly(finalised(t,
		"in 2026-10-26 08:00", "out 2026-10-26 20:00",
		"in 2026-10-27 08:00", "out 2026-10-27 20:00",
		"in 2026-10-28 08:00", "out 2026-10-28 20:00",
		"in 2026-10-29 08:00", "out 2026-10-29 20:00",
		"in 2026-10-30 08:00", "out 2026-10-30 20:00",
	), DefaultRules)
	w = weeks[0]
	// Mon, Tue, Fri 660 each; arife 300 ordinary (08-13) + 420 holiday - 60 break
	// at 14:00; the 29th 660 holiday. Ordinary 2280 < 2700: no overtime, although
	// 3300 minutes were worked — holiday time is paid separately, not as overtime.
	if w.NormalMinutes != 2280 || w.OvertimeMinutes != 0 || w.HolidayMinutes != 360+660 {
		t.Fatalf("holiday week: %+v", w)
	}
}

func TestMonthRange(t *testing.T) {
	cases := map[string][2]string{
		"2026-10-01": {"2026-09-28", "2026-10-25"}, // 1 Oct is a Thursday, 31 Oct a Saturday
		"2026-11-01": {"2026-10-26", "2026-11-29"}, // starts on a Sunday
		"2026-02-01": {"2026-01-26", "2026-02-22"},
		"2026-05-01": {"2026-04-27", "2026-05-31"}, // ends on a Sunday
	}
	for m, want := range cases {
		f, l := MonthRange(day(m))
		if f.Format("2006-01-02") != want[0] || l.Format("2006-01-02") != want[1] || f.Weekday() != time.Monday || l.Weekday() != time.Sunday {
			t.Errorf("MonthRange(%s) = %s..%s, want %s..%s", m, f.Format("2006-01-02"), l.Format("2006-01-02"), want[0], want[1])
		}
	}
	// Consecutive months tile the calendar without gaps or overlaps.
	_, octLast := MonthRange(day("2026-10-01"))
	novFirst, _ := MonthRange(day("2026-11-01"))
	if !octLast.AddDate(0, 0, 1).Equal(novFirst) {
		t.Fatal("months do not tile")
	}
}
