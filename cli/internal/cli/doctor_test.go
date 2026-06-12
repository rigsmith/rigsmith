package cli

import "testing"

func TestSdkSatisfies(t *testing.T) {
	tests := []struct {
		name      string
		installed string
		pinned    string
		want      bool
	}{
		{"no pin", "8.0.100", "", true},
		{"exact major", "8.0.100", "8.0.100", true},
		{"newer major ok", "9.0.100", "8.0.400", true},
		{"older major fails", "7.0.400", "8.0.100", false},
		{"unparseable installed defers to ok", "preview", "8.0.100", true},
		{"unparseable pin defers to ok", "8.0.100", "latest", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sdkSatisfies(tt.installed, tt.pinned); got != tt.want {
				t.Fatalf("sdkSatisfies(%q, %q) = %v, want %v", tt.installed, tt.pinned, got, tt.want)
			}
		})
	}
}

func TestMajorOf(t *testing.T) {
	tests := []struct {
		in   string
		want int
		ok   bool
	}{
		{"8.0.100", 8, true},
		{"10", 10, true},
		{" 9.0 ", 9, true},
		{"preview", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := majorOf(tt.in)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("major = %d, want %d", got, tt.want)
			}
		})
	}
}
