package money

import "testing"

func TestParseAndFormat(t *testing.T) {
	ok := map[string]Kurus{
		"0": 0, "45000": 4_500_000, "45000.5": 4_500_050, "45000.05": 4_500_005,
		" 12.34 ": 1234, "-7.10": -710, "1000000000000": MaxAmount,
	}
	for in, want := range ok {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", ".", "1.", ".5", "1.234", "1,5", "1e3", "abc", "--1", "1.-2", "9999999999999999"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) accepted", in)
		}
	}
	if _, err := Parse("1000000000001"); err == nil {
		t.Error("amount above MaxAmount accepted")
	}

	for k, want := range map[Kurus]string{0: "0.00", 5: "0.05", 4_500_050: "45000.50", -710: "-7.10"} {
		if got := k.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", k, got, want)
		}
	}
}
