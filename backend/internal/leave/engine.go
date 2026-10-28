// Package leave implements annual leave: the entitlement engine (pure
// functions in this file), the balance ledger and the request workflow.
//
// All amounts are integer half-days: 1 day = 2. Dates are civil dates held
// as time.Time at UTC midnight.
package leave

import (
	"errors"
	"time"
)

// Entitlement bands of Labour Law 4857 art. 53:
//
//	service 1–5 years (5 included)          14 days
//	more than 5 and less than 15 years      20 days
//	15 years and more                       26 days
//
// Workers aged 18 or younger, or 50 or older, get at least 20 days.
const (
	daysJunior       = 14
	daysMid          = 20
	daysSenior       = 26
	daysAgeMinimum   = 20
	youngAgeMax      = 18
	seniorAgeMin     = 50
	juniorMaxYears   = 5
	seniorMinYears   = 15
	maxAccrualsYears = 60
)

// EntitlementDays returns the days earned at the anniversary that completes
// serviceYears (>= 1), for a worker of the given age on that day (age < 0 =
// unknown).
func EntitlementDays(serviceYears, age int) int {
	if serviceYears < 1 {
		return 0
	}
	days := daysJunior
	switch {
	case serviceYears >= seniorMinYears:
		days = daysSenior
	case serviceYears > juniorMaxYears:
		days = daysMid
	}
	if age >= 0 && (age <= youngAgeMax || age >= seniorAgeMin) && days < daysAgeMinimum {
		days = daysAgeMinimum
	}
	return days
}

// Accrual is the entitlement earned on one work anniversary.
type Accrual struct {
	Date         time.Time
	ServiceYears int
	Age          int // -1 when the birth date is unknown
	HalfDays     int
}

// Anniversary returns the n-th work anniversary. A 29 February hire date
// falls on 1 March in non-leap years (time.AddDate normalisation).
func Anniversary(hire time.Time, n int) time.Time { return hire.AddDate(n, 0, 0) }

// AgeOn returns the age in completed years on day d.
func AgeOn(birth, d time.Time) int {
	age := d.Year() - birth.Year()
	if d.Month() < birth.Month() || (d.Month() == birth.Month() && d.Day() < birth.Day()) {
		age--
	}
	return age
}

// Accruals lists every anniversary entitlement from hire up to and including
// asOf. Nothing accrues on or after the termination date: the anniversary
// has to be completed while still employed.
func Accruals(hire time.Time, birth, termination *time.Time, asOf time.Time) []Accrual {
	var out []Accrual
	for n := 1; n <= maxAccrualsYears; n++ {
		d := Anniversary(hire, n)
		if d.After(asOf) || (termination != nil && d.After(*termination)) {
			break
		}
		age := -1
		if birth != nil {
			age = AgeOn(*birth, d)
		}
		out = append(out, Accrual{Date: d, ServiceYears: n, Age: age, HalfDays: 2 * EntitlementDays(n, age)})
	}
	return out
}

// HalfDay selects half of a single day.
type HalfDay string

const (
	FullDay   HalfDay = ""
	Morning   HalfDay = "morning"
	Afternoon HalfDay = "afternoon"
)

// Holidays maps a date to whether only its afternoon is off (true) or the
// whole day (false).
type Holidays map[time.Time]bool

var (
	ErrRange   = errors.New("end date before start date")
	ErrHalfDay = errors.New("half-day leave must be a single day")
)

// dayValue is the working time of one day in half-days: 0 at weekends and
// full holidays, 1 on half holidays (morning only), 2 otherwise. The
// working week is Monday to Friday.
func dayValue(d time.Time, h Holidays) int {
	if wd := d.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return 0
	}
	if afternoonOnly, ok := h[d]; ok {
		if afternoonOnly {
			return 1
		}
		return 0
	}
	return 2
}

// WorkingHalfDays is the leave cost of [start, end]: only working time is
// deducted, so weekends and holidays inside a leave are free. For a half-day
// request the cost is that half if it is a working half: morning leave on
// an arife costs 1, afternoon leave on an arife costs 0.
func WorkingHalfDays(start, end time.Time, half HalfDay, h Holidays) (int, error) {
	if end.Before(start) {
		return 0, ErrRange
	}
	if half != FullDay {
		if !start.Equal(end) {
			return 0, ErrHalfDay
		}
		v := dayValue(start, h)
		switch {
		case v == 0:
			return 0, nil
		case half == Morning:
			return 1, nil
		case v == 2: // afternoon of a normal day
			return 1, nil
		default: // afternoon of a half holiday is already off
			return 0, nil
		}
	}
	total := 0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		total += dayValue(d, h)
	}
	return total, nil
}

// Overlap returns the intersection of two inclusive date ranges.
func Overlap(aStart, aEnd, bStart, bEnd time.Time) (start, end time.Time, ok bool) {
	start, end = aStart, aEnd
	if bStart.After(start) {
		start = bStart
	}
	if bEnd.Before(end) {
		end = bEnd
	}
	return start, end, !end.Before(start)
}

// SickRefund is how many half-days of an approved annual leave a sick
// report gives back: the working time of the overlap, never more than the
// annual leave cost. A half-day annual leave keeps its half.
func SickRefund(annualStart, annualEnd time.Time, annualHalf HalfDay, annualCost int,
	sickStart, sickEnd time.Time, h Holidays) int {
	s, e, ok := Overlap(annualStart, annualEnd, sickStart, sickEnd)
	if !ok {
		return 0
	}
	half := FullDay
	if annualHalf != FullDay {
		half = annualHalf
	}
	v, err := WorkingHalfDays(s, e, half, h)
	if err != nil {
		return 0
	}
	return min(v, annualCost)
}

// Days converts half-days to a JSON-friendly day count (exact for .5).
func Days(halfDays int) float64 { return float64(halfDays) / 2 }
