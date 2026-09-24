package plugin

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestHelperPlugin is not a test: run as a subprocess with
// RIGSMITH_HELPER_PLUGIN set, it is a plugin that answers every method with
// that variable's value.
func TestHelperPlugin(t *testing.T) {
	answer, ok := os.LookupEnv("RIGSMITH_HELPER_PLUGIN")
	if !ok {
		return
	}
	fmt.Print(answer)
	os.Exit(0)
}

// helperEcosystem is a subprocess ecosystem whose plugin answers with answer.
func helperEcosystem(t *testing.T, answer string) *SubprocessEcosystem {
	t.Helper()
	t.Setenv("RIGSMITH_HELPER_PLUGIN", answer)
	return &SubprocessEcosystem{
		host: &Host{Path: os.Args[0], BaseArgs: []string{"-test.run=^TestHelperPlugin$", "--"}},
		info: EcosystemInfo{ID: "helper", Capabilities: []string{MethodPublished}},
	}
}

func TestSubprocessPublishedInsistsOnAnAnswer(t *testing.T) {
	req := PublishedRequest{Package: Package{Name: "lib", Version: "1.0.0"}}
	for _, tc := range []struct {
		answer     string
		published  bool
		noRegistry bool
		wantErr    string
	}{
		{`{"published": true}`, true, false, ""},
		{`{"published": false}`, false, false, ""},
		{`{"noRegistry": true}`, false, true, ""},
		{`{}`, false, false, "no published answer"},
		{`null`, false, false, "no published answer"},
		{`{"published": true, "noRegistry": true}`, false, false, "published and noRegistry"},
	} {
		resp, err := helperEcosystem(t, tc.answer).Published(context.Background(), req)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: err = %v, want %q", tc.answer, err, tc.wantErr)
			}
			continue
		}
		if err != nil || resp.Published != tc.published || resp.NoRegistry != tc.noRegistry {
			t.Errorf("%s: resp = %+v, err = %v", tc.answer, resp, err)
		}
	}
}

// A plugin that doesn't advertise the method gets a clear error, not an
// unknown-method failure.
func TestSubprocessPublishedNeedsTheCapability(t *testing.T) {
	eco := helperEcosystem(t, `{"published": true}`)
	eco.info.Capabilities = []string{MethodDiscover, MethodPublish}
	_, err := eco.Published(context.Background(), PublishedRequest{Package: Package{Name: "lib", Version: "1.0.0"}})
	if err == nil || !strings.Contains(err.Error(), "doesn't support") {
		t.Errorf("err = %v, want the missing-capability error", err)
	}
}
