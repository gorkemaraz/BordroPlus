package report

import "testing"

func TestCSVSafe(t *testing.T) {
	cases := map[string]string{
		// Formula triggers get a single-quote prefix.
		`=HYPERLINK("http://evil","x")`: `'=HYPERLINK("http://evil","x")`,
		"+1+2":                          "'+1+2",
		"@SUM(A1)":                      "'@SUM(A1)",
		"\tcmd":                         "'\tcmd",
		"\rfoo":                         "'\rfoo",
		"-cmd|'/c calc'!A1":             "'-cmd|'/c calc'!A1",
		// Genuine data is untouched.
		"İç Denetim":   "İç Denetim",
		"-7.10":        "-7.10",  // negative amount
		"+5.00":        "'+5.00", // + never appears in a real amount, so escape it
		"45000.50":     "45000.50",
		"6":            "6",
		"genel.ic_den": "genel.ic_den",
		"":             "",
	}
	for in, want := range cases {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
}
