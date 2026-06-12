package cli

import "testing"

func TestParseLinePct(t *testing.T) {
	tests := []struct {
		name string
		json string
		want float64
		ok   bool
	}{
		{"valid", `{"total":{"lines":{"pct":83.5}}}`, 83.5, true},
		{"zero", `{"total":{"lines":{"pct":0}}}`, 0, true},
		{"hundred", `{"total":{"lines":{"pct":100}}}`, 100, true},
		{"missing pct", `{"total":{"lines":{}}}`, 0, false},
		{"missing lines", `{"total":{}}`, 0, false},
		{"missing total", `{}`, 0, false},
		{"garbage", `not json`, 0, false},
		{"empty", ``, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseLinePct([]byte(tt.json))
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("pct = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseGoCoverage(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   float64
		ok     bool
	}{
		{
			"single",
			"ok  \tgithub.com/x/y\t0.012s\tcoverage: 76.4% of statements\n",
			76.4, true,
		},
		{
			"takes max across packages",
			"coverage: 50.0% of statements\ncoverage: 91.2% of statements\ncoverage: 0.0% of statements\n",
			91.2, true,
		},
		{"integer pct", "coverage: 100% of statements\n", 100, true},
		{"no coverage line", "ok\tgithub.com/x/y\t0.01s\n", 0, false},
		{"empty", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseGoCoverage(tt.output)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("pct = %v, want %v", got, tt.want)
			}
		})
	}
}
