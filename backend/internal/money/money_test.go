package money

import (
	"encoding/json"
	"testing"
)

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

func TestJSONRoundTrip(t *testing.T) {
	type row struct {
		A Kurus  `json:"a"`
		B *Kurus `json:"b"`
	}
	b := Kurus(-710)
	out, err := json.Marshal(row{A: 4_500_050, B: &b})
	if err != nil || string(out) != `{"a":"45000.50","b":"-7.10"}` {
		t.Fatalf("marshal: %s %v", out, err)
	}
	var back row
	if err := json.Unmarshal(out, &back); err != nil || back.A != 4_500_050 || *back.B != -710 {
		t.Fatalf("unmarshal: %+v %v", back, err)
	}
	if err := json.Unmarshal([]byte(`{"a":4500050}`), &back); err == nil {
		t.Fatal("bare numbers must be rejected")
	}
}
