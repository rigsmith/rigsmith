package hooks

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRoutingSettingsFileLimits(t *testing.T) {
	for _, tc := range []string{"boundary", "oversized", "directory", "symlink", "invalid", "null", "empty"} {
		t.Run(tc, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			data := "{}"
			switch tc {
			case "boundary":
				data += strings.Repeat(" ", routingSettingsLimit-len(data))
			case "oversized":
				data += strings.Repeat(" ", routingSettingsLimit+1-len(data))
			case "invalid":
				data = "{"
			case "null":
				data = "null"
			case "empty":
				data = ""
			}
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if tc == "directory" {
				path = filepath.Dir(path)
			}
			if tc == "symlink" {
				link := path + ".link"
				if err := os.Symlink(path, link); err != nil {
					t.Skip("symlink unavailable", err)
				}
				path = link
			}
			_, err := loadRoutingSettings(t.Context(), path)
			if (err == nil) != (tc == "boundary") {
				t.Fatal(tc, err)
			}
		})
	}
}

type routingReadFunc func([]byte) (int, error)

func (f routingReadFunc) Read(p []byte) (int, error) { return f(p) }

func TestRoutingSettingsCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		var ctx context.Context
		var cancel context.CancelFunc
		want := context.Canceled
		if deadline {
			ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			want = context.DeadlineExceeded
		} else {
			ctx, cancel = context.WithCancel(t.Context())
			cancel()
		}
		// Cancellation wins even over a nonexistent path; no settings I/O begins.
		if err := CheckSyncRouting(ctx, filepath.Join(t.TempDir(), "missing")); !errors.Is(err, want) {
			t.Fatal(err)
		}
		cancel()
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reads := 0
	_, err := readRoutingSettings(ctx, routingReadFunc(func(p []byte) (int, error) {
		reads++
		cancel()
		return copy(p, "{}"), io.EOF
	}))
	if !errors.Is(err, context.Canceled) || reads != 1 {
		t.Fatal("cancellation during read accepted data or read again", reads, err)
	}
}

func TestRoutingSettingsGrowingInputIsBounded(t *testing.T) {
	bytesRead := 0
	_, err := readRoutingSettings(t.Context(), routingReadFunc(func(p []byte) (int, error) {
		if len(p) > 32<<10 {
			t.Fatal("unbounded read chunk", len(p))
		}
		for i := range p {
			p[i] = ' '
		}
		bytesRead += len(p)
		return len(p), nil
	}))
	if err == nil || bytesRead != routingSettingsLimit+1 {
		t.Fatal("input exceeded read budget", bytesRead, err)
	}
}
