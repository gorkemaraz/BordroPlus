// Package sim generates the demo company: 1.500 employees in a realistic
// organisation, two years of clock punches (with deliberate missing and
// double punches) and leave history, and keeps producing today's punches
// in live mode.
//
// Everything is derived from one seed through per-entity random streams
// (seed, employee index, date), never from map iteration or wall-clock
// order, so the same seed always produces the same data, and a day
// generated live equals the same day generated in bulk.
package sim

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"time"

	"bordroplus/internal/leave"
	"bordroplus/internal/money"
)

// UnitPlan is an org unit the simulator staffs. Existing units (created by
// the demo seed) have an empty Parent.
type UnitPlan struct {
	Code, Parent, Name string
	Weight             int
	Night              bool        // night shift 22:00-06:00
	MinPay, MaxPay     money.Kurus // 2026 monthly gross range
}

// Units: staffing weights roughly follow a services company.
var Units = []UnitPlan{
	{"yazilim", "", "", 120, false, 6000000, 18000000},
	{"altyapi", "bilgi_teknolojileri", "Altyapı ve Ağ", 60, false, 5500000, 15000000},
	{"veri", "bilgi_teknolojileri", "Veri ve Analitik", 50, false, 6000000, 17000000},
	{"operasyon", "genel_mudurluk", "Operasyon", 4, false, 6000000, 12000000},
	{"depo", "operasyon", "Depo", 180, false, 3303000, 5500000},
	{"lojistik", "operasyon", "Lojistik", 150, false, 3303000, 6000000},
	{"gece_vardiyasi", "operasyon", "Gece Vardiyası", 90, true, 3600000, 6000000},
	{"satis", "genel_mudurluk", "Satış", 4, false, 7000000, 14000000},
	{"kurumsal_satis", "satis", "Kurumsal Satış", 110, false, 5000000, 12000000},
	{"bayi_satis", "satis", "Bayi Satış", 120, false, 4500000, 10000000},
	{"musteri_hizmetleri", "genel_mudurluk", "Müşteri Hizmetleri", 4, false, 5000000, 9000000},
	{"cagri_merkezi", "musteri_hizmetleri", "Çağrı Merkezi", 220, false, 3303000, 4500000},
	{"pazarlama", "genel_mudurluk", "Pazarlama", 70, false, 5000000, 11000000},
	{"hazine", "finans", "Hazine", 30, false, 6000000, 13000000},
	{"bordro", "", "", 25, false, 5000000, 9000000},
	{"muhasebe", "", "", 40, false, 5000000, 10000000},
	{"ise_alim", "insan_kaynaklari", "İşe Alım", 35, false, 4500000, 9000000},
	{"insan_kaynaklari", "", "", 25, false, 5000000, 10000000},
	{"ic_denetim", "", "", 15, false, 6000000, 12000000},
}

var firstNames = []string{
	"Ahmet", "Mehmet", "Mustafa", "Ali", "Hüseyin", "Hasan", "İbrahim", "Murat", "Emre", "Burak",
	"Oğuz", "Serkan", "Kerem", "Tolga", "Cem", "Onur", "Barış", "Umut", "Deniz", "Can",
	"Ayşe", "Fatma", "Emine", "Hatice", "Zeynep", "Elif", "Merve", "Büşra", "Esra", "Selin",
	"Gizem", "Ebru", "Derya", "Sibel", "Özge", "Pınar", "Tuğba", "Gamze", "İrem", "Cansu",
}

var lastNames = []string{
	"Yılmaz", "Kaya", "Demir", "Şahin", "Çelik", "Yıldız", "Yıldırım", "Öztürk", "Aydın", "Özdemir",
	"Arslan", "Doğan", "Kılıç", "Aslan", "Çetin", "Kara", "Koç", "Kurt", "Özkan", "Şimşek",
	"Polat", "Korkmaz", "Erdoğan", "Güneş", "Tekin", "Aksoy", "Bulut", "Kaplan", "Avcı", "Uçar",
	"Taş", "Ateş", "Bozkurt", "Güler", "Ekinci", "Duman", "Sarı", "Acar", "Turan", "Keskin",
}

// Person is one simulated employee.
type Person struct {
	Index      int
	No         string
	First      string
	Last       string
	Unit       string
	Night      bool
	Hire       time.Time
	Birth      time.Time
	Term       *time.Time
	Salary2025 money.Kurus
	Salary2026 money.Kurus
	Device     string
}

func stream(seed uint64, a, b uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed^0x9E3779B97F4A7C15, a<<32^b))
}

func dayNumber(d time.Time) uint64 { return uint64(d.Unix() / 86400) }

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func roundTo(k money.Kurus, step money.Kurus) money.Kurus { return (k + step/2) / step * step }

// People generates n employees. historyFrom is the start of the simulated
// history; about 15% are hired during it, 3% leave during it.
func People(seed uint64, n int, historyFrom, historyTo time.Time) []Person {
	r := stream(seed, 1, 0)
	total := 0
	for _, u := range Units {
		total += u.Weight
	}
	people := make([]Person, n)
	for i := range people {
		pick := r.IntN(total)
		var u UnitPlan
		for _, cand := range Units {
			if pick < cand.Weight {
				u = cand
				break
			}
			pick -= cand.Weight
		}
		p := Person{
			Index: i, No: fmt.Sprintf("S%05d", i+1), Unit: u.Code, Night: u.Night,
			First: firstNames[r.IntN(len(firstNames))], Last: lastNames[r.IntN(len(lastNames))],
			Device: fmt.Sprintf("T%d", i%6+1),
		}
		if r.Float64() < 0.15 {
			span := int(historyTo.Sub(historyFrom).Hours() / 24)
			p.Hire = historyFrom.AddDate(0, 0, r.IntN(span-30))
		} else {
			start := date(2008, 1, 1)
			span := int(historyFrom.Sub(start).Hours() / 24)
			p.Hire = start.AddDate(0, 0, r.IntN(span))
		}
		ageAtHire := 19 + r.IntN(27)
		p.Birth = p.Hire.AddDate(-ageAtHire, 0, -r.IntN(365))
		if r.Float64() < 0.03 {
			first := p.Hire.AddDate(0, 2, 0)
			if first.Before(date(2025, 1, 1)) {
				first = date(2025, 1, 1)
			}
			if span := int(historyTo.Sub(first).Hours() / 24); span > 0 {
				t := first.AddDate(0, 0, r.IntN(span))
				p.Term = &t
			}
		}
		// Pay skews towards the lower end of the unit's range.
		x := math.Pow(r.Float64(), 1.8)
		pay := u.MinPay + money.Kurus(x*float64(u.MaxPay-u.MinPay))
		p.Salary2026 = max(roundTo(pay, 10000), 3303000)
		p.Salary2025 = max(roundTo(p.Salary2026*100/128, 10000), 2600550)
		people[i] = p
	}
	return people
}

// Event is one punch.
type Event struct {
	At  time.Time
	Dir string
}

var istanbul = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		panic(err)
	}
	return loc
}()

func at(d time.Time, minutes float64) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, istanbul).Add(time.Duration(minutes * float64(time.Minute))).Truncate(time.Second)
}

// Day returns a person's punches on day d (shifts starting that day), with
// the deliberate faults: about 0.6% of shifts lose the out punch, 0.4% the
// in punch, and 1.6% get a double punch.
func Day(seed uint64, p Person, d time.Time, h leave.Holidays) []Event {
	r := stream(seed, uint64(p.Index)+1000, dayNumber(d))
	wd := d.Weekday()
	afternoonOff, holiday := h[d]
	var in, out time.Time
	switch {
	case wd == time.Sunday:
		return nil
	case holiday && !afternoonOff:
		// Warehouse and night teams sometimes work public holidays.
		if !(p.Night || p.Unit == "depo") || r.Float64() > 0.15 {
			return nil
		}
		in = at(d, 8*60+r.NormFloat64()*8)
		out = in.Add(8*time.Hour + time.Duration(r.NormFloat64()*10)*time.Minute)
	case wd == time.Saturday:
		if p.Night || r.Float64() > 0.06 {
			return nil
		}
		in = at(d, 9*60+r.NormFloat64()*10)
		out = in.Add(5*time.Hour + time.Duration(r.NormFloat64()*15)*time.Minute)
	case p.Night:
		in = at(d, 22*60+r.NormFloat64()*8)
		out = at(d.AddDate(0, 0, 1), 6*60+r.NormFloat64()*8)
	default:
		in = at(d, 8*60+30+r.NormFloat64()*10)
		dur := 9*time.Hour + 15*time.Minute + time.Duration(r.NormFloat64()*15)*time.Minute
		if r.Float64() < 0.04 {
			dur += 2 * time.Hour // overtime
		}
		if afternoonOff {
			dur = at(d, 13*60).Sub(in) + time.Duration(r.IntN(10))*time.Minute
		}
		out = in.Add(dur)
	}
	ev := []Event{{in, "in"}, {out, "out"}}
	switch f := r.Float64(); {
	case f < 0.006:
		ev = ev[:1]
	case f < 0.010:
		ev = ev[1:]
	case f < 0.018:
		ev = []Event{ev[0], {in.Add(time.Duration(1+r.IntN(3)) * time.Minute), "in"}, ev[1]}
	case f < 0.026:
		ev = append(ev, Event{out.Add(time.Duration(1+r.IntN(3)) * time.Minute), "out"})
	}
	return ev
}

// Absence is a planned, approved leave.
type Absence struct {
	Type       string
	Start, End time.Time
	HalfDays   int
}

func (a Absence) covers(d time.Time) bool { return !d.Before(a.Start) && !d.After(a.End) }

// Absences plans a person's leave in [from, to]: two or three annual leaves
// a year (only while the balance allows), some sick reports and rare unpaid
// leave, never overlapping.
func Absences(seed uint64, p Person, from, to time.Time, h leave.Holidays) []Absence {
	r := stream(seed, uint64(p.Index)+500000, 0)
	start := from
	if p.Hire.After(start) {
		start = p.Hire
	}
	end := to
	if p.Term != nil && p.Term.Before(end) {
		end = *p.Term
	}
	if !start.Before(end) {
		return nil
	}
	span := int(end.Sub(start).Hours() / 24)
	years := max(1, span/365)
	var cands []Absence
	add := func(typ string, days int) {
		for range 5 { // a few tries to find a free slot
			s := start.AddDate(0, 0, r.IntN(max(1, span-10)))
			for s.Weekday() == time.Saturday || s.Weekday() == time.Sunday {
				s = s.AddDate(0, 0, 1)
			}
			e := s
			for n := 1; n < days; {
				e = e.AddDate(0, 0, 1)
				if e.Weekday() != time.Saturday && e.Weekday() != time.Sunday {
					n++
				}
			}
			if e.After(end) {
				continue
			}
			clash := slices.ContainsFunc(cands, func(a Absence) bool { return !e.Before(a.Start) && !s.After(a.End) })
			if clash {
				continue
			}
			cost, _ := leave.WorkingHalfDays(s, e, leave.FullDay, h)
			if cost == 0 {
				continue
			}
			cands = append(cands, Absence{Type: typ, Start: s, End: e, HalfDays: cost})
			return
		}
	}
	for range years * (2 + r.IntN(2)) {
		add(leave.TypeAnnual, 1+r.IntN(7))
	}
	for range r.IntN(years + 2) {
		add(leave.TypeSick, 1+r.IntN(3))
	}
	if r.Float64() < 0.1 {
		add(leave.TypeUnpaid, 1+r.IntN(2))
	}
	slices.SortFunc(cands, func(a, b Absence) int { return a.Start.Compare(b.Start) })

	// Keep annual leave only where the balance at its start allows it.
	acc := leave.Accruals(p.Hire, &p.Birth, p.Term, to)
	used := 0
	out := cands[:0]
	for _, a := range cands {
		if a.Type == leave.TypeAnnual {
			earned := 0
			for _, x := range acc {
				if !x.Date.After(a.Start) {
					earned += x.HalfDays
				}
			}
			if earned-used < a.HalfDays {
				continue
			}
			used += a.HalfDays
		}
		out = append(out, a)
	}
	return out
}
