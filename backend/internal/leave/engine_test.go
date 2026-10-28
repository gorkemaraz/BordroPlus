package leave

import (
	"testing"
	"time"
)

func d(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr(t time.Time) *time.Time { return &t }

func TestEntitlementBands(t *testing.T) {
	cases := []struct {
		years, age, want int
	}{
		{0, 30, 0},
		{1, 30, 14},
		{4, 30, 14},
		{5, 30, 14}, // "beş yıl dahil"
		{6, 30, 20}, // more than five
		{14, 30, 20},
		{15, 30, 26}, // fifteen included
		{30, 45, 26},
		// Age floor of 20 days.
		{1, 18, 20},
		{1, 19, 14},
		{3, 50, 20},
		{3, 49, 14},
		{20, 55, 26}, // floor never lowers a higher band
		{2, -1, 14},  // unknown age: no floor
	}
	for _, c := range cases {
		if got := EntitlementDays(c.years, c.age); got != c.want {
			t.Errorf("EntitlementDays(%d years, age %d) = %d, want %d", c.years, c.age, got, c.want)
		}
	}
}

func TestAccrualsFollowAnniversaries(t *testing.T) {
	hire := d("2010-01-04")
	acc := Accruals(hire, nil, nil, d("2026-10-28"))
	if len(acc) != 16 {
		t.Fatalf("accruals = %d, want 16", len(acc))
	}
	if !acc[0].Date.Equal(d("2011-01-04")) || acc[0].HalfDays != 28 {
		t.Errorf("first accrual = %+v", acc[0])
	}
	if acc[5].ServiceYears != 6 || acc[5].HalfDays != 40 {
		t.Errorf("6th accrual = %+v, want 20 days", acc[5])
	}
	if acc[14].ServiceYears != 15 || acc[14].HalfDays != 52 {
		t.Errorf("15th accrual = %+v, want 26 days", acc[14])
	}

	// Exactly on the anniversary counts; the day before does not.
	if n := len(Accruals(hire, nil, nil, d("2011-01-04"))); n != 1 {
		t.Errorf("on anniversary: %d accruals", n)
	}
	if n := len(Accruals(hire, nil, nil, d("2011-01-03"))); n != 0 {
		t.Errorf("day before anniversary: %d accruals", n)
	}
}

func TestAccrualsAgeFloorChangesYearByYear(t *testing.T) {
	// Hired at 17: 18 at the first anniversary (20 days), 19 at the second (14).
	acc := Accruals(d("2024-03-01"), ptr(d("2006-09-01")), nil, d("2026-10-28"))
	if len(acc) != 2 || acc[0].Age != 18 || acc[0].HalfDays != 40 || acc[1].Age != 19 || acc[1].HalfDays != 28 {
		t.Fatalf("accruals = %+v", acc)
	}
	// Turns 50 just before the 4th anniversary.
	acc = Accruals(d("2021-06-01"), ptr(d("1975-05-20")), nil, d("2026-10-28"))
	if acc[3].Age != 50 || acc[3].HalfDays != 40 || acc[2].HalfDays != 28 {
		t.Fatalf("accruals = %+v", acc)
	}
}

func TestAccrualsStopAtTermination(t *testing.T) {
	hire := d("2020-05-10")
	if n := len(Accruals(hire, nil, ptr(d("2023-05-09")), d("2026-01-01"))); n != 2 {
		t.Errorf("left the day before 3rd anniversary: %d accruals, want 2", n)
	}
	if n := len(Accruals(hire, nil, ptr(d("2023-05-10")), d("2026-01-01"))); n != 3 {
		t.Errorf("left on 3rd anniversary: %d accruals, want 3", n)
	}
}

func TestLeapDayHire(t *testing.T) {
	if got := Anniversary(d("2024-02-29"), 1); !got.Equal(d("2025-03-01")) {
		t.Errorf("anniversary of 29 Feb in 2025 = %s", got)
	}
	if got := Anniversary(d("2024-02-29"), 4); !got.Equal(d("2028-02-29")) {
		t.Errorf("anniversary of 29 Feb in 2028 = %s", got)
	}
	if got := AgeOn(d("2008-02-29"), d("2026-02-28")); got != 17 {
		t.Errorf("AgeOn leap birthday = %d", got)
	}
}

// 2026 holidays used by the calendar tests.
var h2026 = Holidays{
	d("2026-05-19"): false, // Tuesday
	d("2026-05-26"): true,  // Tuesday, Kurban Bayramı arifesi (afternoon off)
	d("2026-05-27"): false, d("2026-05-28"): false, d("2026-05-29"): false, d("2026-05-30"): false,
	d("2026-10-28"): true, d("2026-10-29"): false,
}

func TestWorkingHalfDays(t *testing.T) {
	cases := []struct {
		name       string
		start, end string
		half       HalfDay
		want       int
	}{
		{"one Monday", "2026-05-04", "2026-05-04", FullDay, 2},
		{"Mon-Fri", "2026-05-04", "2026-05-08", FullDay, 10},
		{"Mon-Mon spans a weekend", "2026-05-04", "2026-05-11", FullDay, 12},
		{"weekend only", "2026-05-09", "2026-05-10", FullDay, 0},
		{"week with 19 May", "2026-05-18", "2026-05-22", FullDay, 8},
		// Kurban Bayramı week: Mon full, Tue arife half, Wed-Fri holiday.
		{"bayram week", "2026-05-25", "2026-05-29", FullDay, 3},
		{"morning on a normal day", "2026-05-04", "2026-05-04", Morning, 1},
		{"afternoon on a normal day", "2026-05-04", "2026-05-04", Afternoon, 1},
		{"morning on arife", "2026-05-26", "2026-05-26", Morning, 1},
		{"afternoon on arife is already off", "2026-05-26", "2026-05-26", Afternoon, 0},
		{"full day on arife", "2026-05-26", "2026-05-26", FullDay, 1},
		{"half day on Saturday", "2026-05-09", "2026-05-09", Morning, 0},
		{"28-29 October", "2026-10-28", "2026-10-30", FullDay, 3},
		{"month of December", "2026-12-01", "2026-12-31", FullDay, 46},
	}
	for _, c := range cases {
		got, err := WorkingHalfDays(d(c.start), d(c.end), c.half, h2026)
		if err != nil || got != c.want {
			t.Errorf("%s: %d, %v; want %d", c.name, got, err, c.want)
		}
	}

	if _, err := WorkingHalfDays(d("2026-05-05"), d("2026-05-04"), FullDay, nil); err != ErrRange {
		t.Errorf("reversed range: %v", err)
	}
	if _, err := WorkingHalfDays(d("2026-05-04"), d("2026-05-05"), Morning, nil); err != ErrHalfDay {
		t.Errorf("multi-day half: %v", err)
	}
}

func TestSickRefund(t *testing.T) {
	// Annual leave 4-15 May 2026 (10 working days = 20 half-days).
	aS, aE := d("2026-05-04"), d("2026-05-15")
	cases := []struct {
		name   string
		sStart string
		sEnd   string
		want   int
	}{
		{"report inside leave, Wed-Fri", "2026-05-06", "2026-05-08", 6},
		{"report over the weekend only", "2026-05-09", "2026-05-10", 0},
		{"report starts before leave", "2026-04-30", "2026-05-05", 4},
		{"report ends after leave", "2026-05-14", "2026-05-20", 4},
		{"report covers whole leave", "2026-05-01", "2026-05-31", 20},
		{"no overlap", "2026-06-01", "2026-06-05", 0},
	}
	for _, c := range cases {
		if got := SickRefund(aS, aE, FullDay, 20, d(c.sStart), d(c.sEnd), h2026); got != c.want {
			t.Errorf("%s: refund %d, want %d", c.name, got, c.want)
		}
	}
	// Half-day annual leave refunds at most its half.
	if got := SickRefund(d("2026-05-04"), d("2026-05-04"), Morning, 1, d("2026-05-01"), d("2026-05-08"), h2026); got != 1 {
		t.Errorf("half-day refund = %d, want 1", got)
	}
	// Never more than what was charged.
	if got := SickRefund(aS, aE, FullDay, 5, d("2026-05-01"), d("2026-05-31"), h2026); got != 5 {
		t.Errorf("capped refund = %d, want 5", got)
	}
}

func TestDays(t *testing.T) {
	if Days(29) != 14.5 || Days(0) != 0 {
		t.Fatal("Days conversion")
	}
}
