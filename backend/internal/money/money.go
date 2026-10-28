// Package money represents Turkish lira amounts as integer kuruş. Floats are
// never used: amounts cross the API as decimal strings ("45000.50") and are
// parsed exactly.
package money

import (
	"errors"
	"strconv"
	"strings"
)

// Kurus is an amount in kuruş (1/100 TRY).
type Kurus int64

// MaxAmount bounds parsed amounts (one trillion lira) far below int64 overflow.
const MaxAmount Kurus = 100_000_000_000_000

var ErrInvalid = errors.New("invalid amount: use digits with at most two decimals, e.g. 45000.50")

// Parse reads "1234", "1234.5" or "1234.56" (optionally negative). More than
// two decimals is an error rather than a silent rounding.
func Parse(s string) (Kurus, error) {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, hasDot := strings.Cut(s, ".")
	if whole == "" || len(whole) > 15 || !digits(whole) || (hasDot && (len(frac) == 0 || len(frac) > 2 || !digits(frac))) {
		return 0, ErrInvalid
	}
	w, _ := strconv.ParseInt(whole, 10, 64)
	frac = (frac + "00")[:2]
	f, _ := strconv.ParseInt(frac, 10, 64)
	k := Kurus(w*100 + f)
	if k > MaxAmount {
		return 0, ErrInvalid
	}
	if neg {
		k = -k
	}
	return k, nil
}

// String formats as "1234.56".
func (k Kurus) String() string {
	sign := ""
	if k < 0 {
		sign = "-"
		k = -k
	}
	return sign + strconv.FormatInt(int64(k/100), 10) + "." + pad2(int64(k%100))
}

func pad2(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}

func digits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// MarshalJSON writes the amount as a decimal string ("45000.50") so clients
// never handle kuruş integers or binary floats.
func (k Kurus) MarshalJSON() ([]byte, error) { return []byte(`"` + k.String() + `"`), nil }

// UnmarshalJSON accepts the decimal string form.
func (k *Kurus) UnmarshalJSON(b []byte) error {
	if len(b) < 2 || b[0] != '"' || b[len(b)-1] != '"' {
		return ErrInvalid
	}
	v, err := Parse(string(b[1 : len(b)-1]))
	if err != nil {
		return err
	}
	*k = v
	return nil
}
