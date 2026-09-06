// Command layout measures the sessions window in a real browser engine.
//
// The unit tests and the DOM harness check structure: that a row exists, that
// it is not hidden, that it carries the class it should. Neither can say how
// wide anything ends up, because jsdom does not lay anything out — which is how
// a checkbox inheriting input{min-width:280px} pushed its whole row off the
// panel while every assertion passed.
//
// This builds the page with the bridge stubbed by real data from this machine,
// loads it in headless Chrome, and reads the geometry back.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/rigsmith/rigsmith/ui/bridge"
)

func main() {
	chrome := findChrome()
	if chrome == "" {
		fmt.Println("layout: no Chrome found — skipping (this check is local, not CI)")
		return
	}
	dir, err := os.MkdirTemp("", "clauderig-layout-")
	if err != nil {
		fail(err)
	}
	defer os.RemoveAll(dir)

	page, err := buildPreview(dir)
	if err != nil {
		fail(err)
	}
	out, err := exec.Command(chrome, "--headless=new", "--disable-gpu",
		"--window-size=1180,820", "--virtual-time-budget=5000",
		"--dump-dom", "file://"+page).Output()
	if err != nil {
		fail(err)
	}
	m := regexp.MustCompile(`PROBE (\{[^<]*\})`).FindSubmatch(out)
	if m == nil {
		fail(fmt.Errorf("the page did not report its geometry — it may have failed to load"))
	}
	var got map[string]any
	if err := json.Unmarshal(m[1], &got); err != nil {
		fail(err)
	}

	// What has actually gone wrong here before, stated as what must be true.
	checks := []struct {
		name string
		ok   bool
	}{
		{"the filter is wide enough to type in", num(got["filter"]) > 100},
		{"the deleted checkbox is a checkbox, not a text field", num(got["checkboxW"]) < 40},
		{"the toggle sits inside its panel", got["toggleFitsPanel"] == true},
		{"the contents panel does not scroll sideways", num(got["plistOverflow"]) == 0},
		{"the places panel does not scroll sideways", num(got["storesOverflow"]) == 0},
		{"the aside about other accounts stays on one line", got["noteOneLine"] != false},
		{"sessions are listed", num(got["sessions"]) > 0},
	}
	bad := 0
	for _, c := range checks {
		mark := "ok  "
		if !c.ok {
			mark, bad = "FAIL", bad+1
		}
		fmt.Printf("  %s  %s\n", mark, c.name)
	}
	fmt.Printf("\n  measured: %s\n", strings.TrimSpace(string(m[1])))
	if bad > 0 {
		os.Exit(1)
	}
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "layout:", err)
	os.Exit(1)
}

// findChrome looks where Chrome installs itself, and nowhere else: this is a
// convenience for the person editing the stylesheet, not a portable driver.
func findChrome() string {
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
	case "linux":
		candidates = []string{"google-chrome", "chromium", "chromium-browser"}
	case "windows":
		candidates = []string{`C:\Program Files\Google\Chrome\Application\chrome.exe`}
	}
	for _, c := range candidates {
		if filepath.IsAbs(c) {
			if _, err := os.Stat(c); err == nil {
				return c
			}
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

// buildPreview writes the real page with the bridge answered from this
// machine's own stores, plus a script that reports the geometry in the title.
func buildPreview(dir string) (string, error) {
	_, self, _, _ := runtime.Caller(0)
	distDir := filepath.Join(filepath.Dir(self), "..", "dist")
	html, err := os.ReadFile(filepath.Join(distDir, "sessions.html"))
	if err != nil {
		return "", err
	}
	mono, err := os.ReadFile(filepath.Join(distDir, "monogram.js"))
	if err != nil {
		return "", err
	}

	p, ctx := bridge.NewPlaces(), context.Background()
	stores, _ := p.Stores(ctx)
	contents := map[string]any{}
	for _, st := range stores.Stores {
		c, _ := p.Contents(ctx, st.ID)
		contents[st.ID] = c
	}
	data, err := json.Marshal(map[string]any{"stores": stores, "contents": contents})
	if err != nil {
		return "", err
	}

	page := string(html)
	page = strings.Replace(page, `<script type="module">`,
		"<script>window.__data = "+string(data)+";</script>\n<script>", 1)
	page = strings.Replace(page,
		"import { Call } from '/wails/runtime.js';\nimport { monogramFor, paintMono } from './monogram.js';",
		stubCall+strings.ReplaceAll(string(mono), "export function", "function"), 1)
	page = strings.Replace(page, "</body>", probeScript+"</body>", 1)

	out := filepath.Join(dir, "preview.html")
	return out, os.WriteFile(out, []byte(page), 0o644)
}

const stubCall = `const Call = { async ByName(name, ...args) {
  const D = window.__data;
  if (name.endsWith('Places.Stores')) return D.stores;
  if (name.endsWith('Places.Contents')) return D.contents[args[0]] ?? {folders: []};
  if (name.endsWith('Library.List')) return {sessions: [], machine: 'preview', accounts: []};
  if (name.endsWith('Library.TakeHandOff')) return '';
  if (name.endsWith('Library.Detail')) return {session: {id: args[0], title: 'Preview session',
    when: new Date().toISOString(), cwd: '/Users/x/Git', sources: [], client: 'cli'}, prompts: 0};
  return {};
} };
`

const probeScript = `<script>
(async () => {
  const $ = id => document.getElementById(id);
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  try {
    await sleep(300); $('mode-places').click(); await sleep(500);
    const store = [...document.querySelectorAll('.store')].find(n => n.dataset.id === 'live:desktop')
      || document.querySelector('.store');
    store.click(); await sleep(800);
    const first = document.querySelector('.sessionrow:not([hidden])');
    if (first) { first.click(); await sleep(500); }
    const r = n => n.getBoundingClientRect();
    const panel = r($('plist')), f = r(document.querySelector('.pcontrols .pfilter'));
    const t = r(document.querySelector('.ptoggle'));
    const note = document.querySelector('.pnote'), noteSpan = note && note.querySelector('span');
    document.title = 'PROBE ' + JSON.stringify({
      filter: Math.round(f.width),
      toggleW: Math.round(t.width),
      toggleFitsPanel: t.right <= panel.right + 1,
      checkboxW: Math.round(r(document.querySelector('.ptoggle input')).width),
      noteOneLine: noteSpan ? Math.round(r(noteSpan).height) < 22 : null,
      plistOverflow: $('plist').scrollWidth - $('plist').clientWidth,
      storesOverflow: $('pstores').scrollWidth - $('pstores').clientWidth,
      detailInPane: !!document.querySelector('#pitems .drawer.inpane'),
      sessions: document.querySelectorAll('.sessionrow:not([hidden])').length,
    });
  } catch (e) { document.title = 'PROBE {"error":"' + String(e).replace(/"/g, "'") + '"}'; }
})();
</script>
`
