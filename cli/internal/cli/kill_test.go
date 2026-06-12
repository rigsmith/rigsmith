package cli

import (
	"reflect"
	"testing"

	"github.com/rigsmith/cli/internal/config"
)

func TestParsePids(t *testing.T) {
	const self = 999
	tests := []struct {
		name  string
		input string
		want  []int
	}{
		{"empty", "", nil},
		{"single", "1234", []int{1234}},
		{"newline separated", "1234\n5678\n", []int{1234, 5678}},
		{"space separated", "12 34 56", []int{12, 34, 56}},
		{"dedup and sort", "30\n10\n20\n10\n", []int{10, 20, 30}},
		{"drops self", "100\n999\n200", []int{100, 200}},
		{"drops non-positive and junk", "0\n-3\nfoo\n42", []int{42}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parsePids(tt.input, self); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parsePids(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseNetstatPids(t *testing.T) {
	const self = 999
	// Real-ish `netstat -ano -p tcp` rows: Proto, Local, Foreign, State, PID.
	output := "" +
		"  Proto  Local Address          Foreign Address        State           PID\n" +
		"  TCP    0.0.0.0:3000           0.0.0.0:0              LISTENING       4321\n" +
		"  TCP    127.0.0.1:3000         0.0.0.0:0              LISTENING       4321\n" +
		"  TCP    0.0.0.0:8080           0.0.0.0:0              LISTENING       5555\n" +
		"  TCP    127.0.0.1:3000         127.0.0.1:51000        ESTABLISHED     6000\n" +
		"  TCP    0.0.0.0:9999           0.0.0.0:0              LISTENING       999\n"

	if got := parseNetstatPids(output, 3000, self); !reflect.DeepEqual(got, []int{4321}) {
		t.Fatalf("port 3000 = %v, want [4321] (dedup, only LISTENING on :3000)", got)
	}
	if got := parseNetstatPids(output, 8080, self); !reflect.DeepEqual(got, []int{5555}) {
		t.Fatalf("port 8080 = %v, want [5555]", got)
	}
	// Port 9999 is owned by self, so it must be dropped.
	if got := parseNetstatPids(output, 9999, self); got != nil {
		t.Fatalf("port 9999 = %v, want nil (self filtered)", got)
	}
	// A port nobody listens on.
	if got := parseNetstatPids(output, 1234, self); got != nil {
		t.Fatalf("port 1234 = %v, want nil", got)
	}
}

func TestMatchProjectNames(t *testing.T) {
	names := []string{"Buoy.Web.Api", "Buoy.Web.Worker", "Buoy.Core"}
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"exact wins over substring", "Buoy.Core", []string{"Buoy.Core"}},
		{"exact case-insensitive", "buoy.core", []string{"Buoy.Core"}},
		{"substring fallback", "Web", []string{"Buoy.Web.Api", "Buoy.Web.Worker"}},
		{"no match", "nope", nil},
		{"empty query", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchProjectNames(names, tt.query); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("matchProjectNames(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestDirBase(t *testing.T) {
	tests := map[string]string{
		"/Users/john/Git/net-changesets":  "net-changesets",
		"/Users/john/Git/net-changesets/": "net-changesets",
		"net-changesets":                  "net-changesets",
		`C:\Users\john\repo`:              "repo",
		"/":                               "",
	}
	for in, want := range tests {
		if got := dirBase(in); got != want {
			t.Fatalf("dirBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinInts(t *testing.T) {
	if got := joinInts([]int{3000, 8080}, ", "); got != "3000, 8080" {
		t.Fatalf("joinInts = %q, want %q", got, "3000, 8080")
	}
	if got := joinInts(nil, ", "); got != "" {
		t.Fatalf("joinInts(nil) = %q, want empty", got)
	}
}

func TestResolveKillPatterns_ConfigMatchWins(t *testing.T) {
	// With no name arg and an explicit kill.match, the config wins outright and
	// no discovery happens. Use a temp dir with no manifests as root.
	cfg := config.Config{Kill: config.Kill{Match: []string{"dotnet watch", "vite"}}}
	got := resolveKillPatterns(cfg, t.TempDir(), "")
	if !reflect.DeepEqual(got, []string{"dotnet watch", "vite"}) {
		t.Fatalf("resolveKillPatterns = %v, want config match", got)
	}
}
