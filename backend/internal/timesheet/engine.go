// Package timesheet turns clock punches into worked time. This file is the
// pure engine: pairing punches into sessions, detecting inconsistent
// punches with a suggested fix, statutory breaks, splitting time by
// calendar day and classifying it as normal, weekly rest day or public
// holiday, and the weekly 45-hour overtime rule.
//
// All arithmetic is in whole minutes; punch times are truncated to the minute.
package timesheet

import (
	"slices"
	"strconv"
	"time"

	"bordroplus/internal/leave"
)

// Rules is one versioned rule set. Pay multipliers are percentages of the
// normal hourly wage.
type Rules struct {
	Version           string
	DuplicateWindow   time.Duration // a second punch in the same direction within this window is a double punch
	MaxSession        time.Duration // longer "sessions" are a missing out followed by a missing in
	DefaultShift      time.Duration // used for suggestions until there is enough history
	WeeklyLimit       time.Duration // Labour Law art. 63: 45 hours
	AnnualOvertimeCap time.Duration // art. 41: 270 hours a year
	OvertimePct       int           // art. 41: +50%
	RestDayPct        int           // weekly rest day worked
	HolidayPct        int           // art. 47: public holiday worked, one extra day's wage
	HalfHolidayFrom   int           // minute of day when a half holiday starts (13:00)
}

// DefaultRules is the rule set in force.
var DefaultRules = Rules{
	Version:           "TS-2026.1",
	DuplicateWindow:   5 * time.Minute,
	MaxSession:        16 * time.Hour,
	DefaultShift:      9 * time.Hour,
	WeeklyLimit:       45 * time.Hour,
	AnnualOvertimeCap: 270 * time.Hour,
	OvertimePct:       150,
	RestDayPct:        150,
	HolidayPct:        200,
	HalfHolidayFrom:   13 * 60,
}

// Istanbul is the zone in which days, weeks and holidays are decided.
var Istanbul = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		panic(err)
	}
	return loc
}()

type Direction string

const (
	In  Direction = "in"
	Out Direction = "out"
)

// Punch is one effective clock event (raw punches minus approved drops plus
// approved additions), sorted by time.
type Punch struct {
	ID  int64
	At  time.Time
	Dir Direction
}

// DayKind classifies worked minutes for pay.
type DayKind string

const (
	Normal  DayKind = "normal"
	RestDay DayKind = "rest_day" // Sunday
	Holiday DayKind = "holiday"
)

// Segment is the part of a session falling on one calendar day and kind.
type Segment struct {
	Date    time.Time `json:"date"` // civil date, UTC midnight
	Kind    DayKind   `json:"kind"`
	Minutes int       `json:"minutes"` // after break deduction
}

// Session is a matched in/out pair.
type Session struct {
	InID     int64     `json:"in_punch_id"`
	OutID    int64     `json:"out_punch_id"`
	In       time.Time `json:"in"`
	Out      time.Time `json:"out"`
	WorkDate time.Time `json:"work_date"` // local date of the in punch
	Gross    int       `json:"gross_minutes"`
	Break    int       `json:"break_minutes"`
	Net      int       `json:"net_minutes"`
	Segments []Segment `json:"segments"`
}

type AnomalyKind string

const (
	Duplicate  AnomalyKind = "drop_duplicate"
	MissingOut AnomalyKind = "add_missing_out"
	MissingIn  AnomalyKind = "add_missing_in"
)

// Anomaly is an inconsistent punch and the fix the system proposes.
type Anomaly struct {
	Kind     AnomalyKind `json:"kind"`
	Key      string      `json:"suggestion_key"`
	PunchID  int64       `json:"punch_id"`
	PunchAt  time.Time   `json:"punch_at"`
	WorkDate time.Time   `json:"work_date"`
	// SuggestedAt is when the missing punch is assumed to have happened
	// (nil for duplicates, which are dropped).
	SuggestedAt *time.Time `json:"suggested_at"`
	Reason      string     `json:"reason"`
}

// CivilDate is the Istanbul calendar date of t as UTC midnight.
func CivilDate(t time.Time) time.Time {
	y, m, d := t.In(Istanbul).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Pair matches punches into sessions and reports every punch it cannot
// explain. Unexplained punches never produce worked time: the affected
// time only counts once a correction is approved. An in punch still open
// at asOf is an ongoing shift, not an anomaly, until MaxSession passes.
func Pair(ps []Punch, asOf time.Time, r Rules) ([]Session, []Anomaly) {
	var (
		sessions  []Session
		anomalies []Anomaly
		openIdx   = -1
		lastOut   = -1 // index of the last out that closed a session
		needsTime []int
	)
	missing := func(kind AnomalyKind, i int, reason string) {
		anomalies = append(anomalies, Anomaly{Kind: kind, PunchID: ps[i].ID, PunchAt: ps[i].At, WorkDate: CivilDate(ps[i].At), Reason: reason})
		needsTime = append(needsTime, len(anomalies)-1)
	}
	dup := func(i int, reason string) {
		anomalies = append(anomalies, Anomaly{Kind: Duplicate, PunchID: ps[i].ID, PunchAt: ps[i].At, WorkDate: CivilDate(ps[i].At), Reason: reason})
	}

	for i, p := range ps {
		switch p.Dir {
		case In:
			if openIdx >= 0 {
				if p.At.Sub(ps[openIdx].At) <= r.DuplicateWindow {
					dup(i, "aynı yönde art arda giriş basımı (çift basım)")
					continue
				}
				missing(MissingOut, openIdx, "girişin çıkışı yok")
			}
			openIdx = i
		case Out:
			if openIdx >= 0 {
				if p.At.Sub(ps[openIdx].At) > r.MaxSession {
					missing(MissingOut, openIdx, "giriş ile sonraki çıkış arası "+hours(r.MaxSession)+" saati aşıyor")
					missing(MissingIn, i, "çıkışın girişi yok")
					openIdx = -1
					continue
				}
				sessions = append(sessions, newSession(ps[openIdx], p))
				openIdx, lastOut = -1, i
				continue
			}
			if lastOut >= 0 && p.At.Sub(ps[lastOut].At) <= r.DuplicateWindow {
				dup(i, "aynı yönde art arda çıkış basımı (çift basım)")
				continue
			}
			missing(MissingIn, i, "çıkışın girişi yok")
		}
	}
	if openIdx >= 0 && asOf.Sub(ps[openIdx].At) > r.MaxSession {
		missing(MissingOut, openIdx, "girişin çıkışı yok")
	}

	typical := typicalShift(sessions, r.DefaultShift)
	for _, ai := range needsTime {
		a := &anomalies[ai]
		idx := slices.IndexFunc(ps, func(p Punch) bool { return p.ID == a.PunchID })
		var at time.Time
		if a.Kind == MissingOut {
			at = a.PunchAt.Add(typical)
			if idx+1 < len(ps) && !at.Before(ps[idx+1].At) {
				at = ps[idx+1].At.Add(-time.Minute)
			}
			if at.After(asOf) {
				at = asOf
			}
		} else {
			at = a.PunchAt.Add(-typical)
			if idx > 0 && !at.After(ps[idx-1].At) {
				at = ps[idx-1].At.Add(time.Minute)
			}
		}
		at = at.Truncate(time.Minute)
		a.SuggestedAt = &at
	}
	for i := range anomalies {
		a := &anomalies[i]
		prefix := map[AnomalyKind]string{Duplicate: "drop:", MissingOut: "out:", MissingIn: "in:"}[a.Kind]
		a.Key = prefix + strconv.FormatInt(a.PunchID, 10)
	}
	return sessions, anomalies
}

// typicalShift is the median gross length of the clean sessions (at least
// three), else def.
func typicalShift(sessions []Session, def time.Duration) time.Duration {
	if len(sessions) < 3 {
		return def
	}
	ds := make([]int, len(sessions))
	for i, s := range sessions {
		ds[i] = s.Gross
	}
	slices.Sort(ds)
	return time.Duration(ds[len(ds)/2]) * time.Minute
}

func hours(d time.Duration) string { return strconv.Itoa(int(d.Hours())) }

func newSession(in, out Punch) Session {
	i, o := in.At.Truncate(time.Minute), out.At.Truncate(time.Minute)
	return Session{InID: in.ID, OutID: out.ID, In: i, Out: o, WorkDate: CivilDate(i), Gross: int(o.Sub(i) / time.Minute)}
}

// RequiredBreak is the statutory break for a day's work (Labour Law art. 68):
// up to 4 hours 15 minutes, up to 7.5 hours 30 minutes, beyond that 1 hour.
func RequiredBreak(grossMinutes int) int {
	switch {
	case grossMinutes <= 0:
		return 0
	case grossMinutes <= 4*60:
		return min(15, grossMinutes)
	case grossMinutes <= 7*60+30:
		return 30
	default:
		return 60
	}
}

// Finalise computes breaks and day segments. The break is per work day:
// the statutory break for the day's total minus the gaps already taken
// between that day's sessions, deducted from the day's longest session at
// its midpoint.
func Finalise(sessions []Session, h leave.Holidays, r Rules) []Session {
	out := slices.Clone(sessions)
	byDay := map[time.Time][]int{}
	for i := range out {
		out[i].Segments = split(out[i].In, out[i].Out, h, r)
		byDay[out[i].WorkDate] = append(byDay[out[i].WorkDate], i)
	}
	for _, idx := range byDay {
		gross, gaps, longest := 0, 0, idx[0]
		for k, i := range idx {
			gross += out[i].Gross
			if k > 0 {
				gaps += int(out[i].In.Sub(out[idx[k-1]].Out) / time.Minute)
			}
			if out[i].Gross > out[longest].Gross {
				longest = i
			}
		}
		deduct := max(0, RequiredBreak(gross)-gaps)
		deduct = min(deduct, out[longest].Gross)
		out[longest].Break = deduct
		deductAtMidpoint(&out[longest], deduct)
	}
	for i := range out {
		out[i].Net = out[i].Gross - out[i].Break
	}
	return out
}

func deductAtMidpoint(s *Session, minutes int) {
	if minutes == 0 {
		return
	}
	mid := s.In.Add(s.Out.Sub(s.In) / 2)
	// Find the segment containing the midpoint, then take the rest in order.
	order := []int{}
	t := s.In
	for i, seg := range s.Segments {
		end := t.Add(time.Duration(seg.Minutes) * time.Minute)
		if !mid.Before(t) && mid.Before(end) {
			order = append([]int{i}, order...)
		} else {
			order = append(order, i)
		}
		t = end
	}
	for _, i := range order {
		take := min(minutes, s.Segments[i].Minutes)
		s.Segments[i].Minutes -= take
		minutes -= take
		if minutes == 0 {
			break
		}
	}
}

// split cuts [in, out) at local midnights and at the start of half holidays.
func split(in, out time.Time, h leave.Holidays, r Rules) []Segment {
	var segs []Segment
	for t := in; t.Before(out); {
		local := t.In(Istanbul)
		day := CivilDate(t)
		next := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, Istanbul)
		kind := Normal
		if local.Weekday() == time.Sunday {
			kind = RestDay
		}
		if afternoonOnly, ok := h[day]; ok {
			cut := time.Date(local.Year(), local.Month(), local.Day(), 0, r.HalfHolidayFrom, 0, 0, Istanbul)
			switch {
			case !afternoonOnly || !t.Before(cut):
				kind = Holiday
			case cut.Before(next):
				next = cut // morning of a half holiday: ordinary time until 13:00
			}
		}
		if out.Before(next) {
			next = out
		}
		m := int(next.Sub(t) / time.Minute)
		if n := len(segs); n > 0 && segs[n-1].Date.Equal(day) && segs[n-1].Kind == kind {
			segs[n-1].Minutes += m
		} else {
			segs = append(segs, Segment{Date: day, Kind: kind, Minutes: m})
		}
		t = next
	}
	return segs
}

// Totals are minutes by pay class.
type Totals struct {
	NormalMinutes   int `json:"normal_minutes"`
	OvertimeMinutes int `json:"overtime_minutes"`
	RestDayMinutes  int `json:"rest_day_minutes"`
	HolidayMinutes  int `json:"holiday_minutes"`
	WorkedDays      int `json:"worked_days"`
}

func (t *Totals) add(o Totals) {
	t.NormalMinutes += o.NormalMinutes
	t.OvertimeMinutes += o.OvertimeMinutes
	t.RestDayMinutes += o.RestDayMinutes
	t.HolidayMinutes += o.HolidayMinutes
	t.WorkedDays += o.WorkedDays
}

// Week is one Monday-to-Sunday week.
type Week struct {
	Start time.Time `json:"week_start"`
	End   time.Time `json:"week_end"`
	Totals
}

// WeekStart is the Monday of d's week.
func WeekStart(d time.Time) time.Time {
	off := (int(d.Weekday()) + 6) % 7
	return d.AddDate(0, 0, -off)
}

// Weekly sums finalised sessions per week. Minutes on ordinary days (Monday
// to Saturday) above WeeklyLimit are overtime; Sunday and holiday minutes
// are paid at their own rate and do not count towards the 45 hours.
func Weekly(sessions []Session, r Rules) []Week {
	weeks := map[time.Time]*Week{}
	days := map[time.Time]bool{}
	for _, s := range sessions {
		for _, seg := range s.Segments {
			ws := WeekStart(seg.Date)
			w := weeks[ws]
			if w == nil {
				w = &Week{Start: ws, End: ws.AddDate(0, 0, 6)}
				weeks[ws] = w
			}
			switch seg.Kind {
			case Normal:
				w.NormalMinutes += seg.Minutes
			case RestDay:
				w.RestDayMinutes += seg.Minutes
			case Holiday:
				w.HolidayMinutes += seg.Minutes
			}
			if seg.Minutes > 0 && !days[seg.Date] {
				days[seg.Date] = true
				w.WorkedDays++
			}
		}
	}
	limit := int(r.WeeklyLimit / time.Minute)
	out := make([]Week, 0, len(weeks))
	for _, w := range weeks {
		if w.NormalMinutes > limit {
			w.OvertimeMinutes = w.NormalMinutes - limit
			w.NormalMinutes = limit
		}
		out = append(out, *w)
	}
	slices.SortFunc(out, func(a, b Week) int { return a.Start.Compare(b.Start) })
	return out
}

// MonthRange returns the days covered by month (its first day): the weeks
// whose Sunday falls in the month, so every week belongs to exactly one
// month. first is a Monday, last a Sunday.
func MonthRange(month time.Time) (first, last time.Time) {
	firstSunday := month.AddDate(0, 0, (7-int(month.Weekday()))%7)
	end := month.AddDate(0, 1, -1)
	lastSunday := end.AddDate(0, 0, -int(end.Weekday()))
	return firstSunday.AddDate(0, 0, -6), lastSunday
}

// Sum adds the weeks up.
func Sum(weeks []Week) Totals {
	var t Totals
	for _, w := range weeks {
		t.add(w.Totals)
	}
	return t
}
