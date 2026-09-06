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
	// Two widths, because the faults this catches are all faults of not having
	// enough room: a checkbox with a 280px floor fits a wide window and shoves
	// its row off a narrow one. Measuring only the comfortable size is how the
	// same bug reached the same person twice.
	bad := 0
	for _, w := range []int{1180, 900, 720} {
		if !measure(chrome, page, w) {
			bad++
		}
	}
	if bad > 0 {
		os.Exit(1)
	}
}

func measure(chrome, page string, width int) bool {
	out, err := exec.Command(chrome, "--headless=new", "--disable-gpu",
		fmt.Sprintf("--window-size=%d,820", width), "--virtual-time-budget=5000",
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
	fmt.Printf("\n  at %dpx wide\n", width)

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
		{"the detail scrolls inside the panel", got["detailScrolls"] != false},
		{"the actions bar stays inside the panel", got["actsInsidePanel"] != false},
		{"the panel itself does not also scroll", num(got["detailOverflow"]) == 0},
		{"a shortened conversation offers to open out", got["hadOpenOut"] == true},
		{"opening it out shows every turn", num(got["turnsAfter"]) > num(got["turnsBefore"])},
		{"and the whole conversation still fits the panel", got["actsInsidePanel"] != false},
		{"prompts sit on the right of the answers", got["saidOnRight"] == true},
		{"and are coloured differently from them", got["rolesDiffer"] == true},
		{"the conversation has a search box", got["hasSearch"] == true},
		{"searching it narrows the turns", num(got["searchHits"]) > 0 && num(got["searchHits"]) < num(got["turnsAfter"])},
		{"and clearing it brings them back", num(got["searchCleared"]) == num(got["turnsAfter"])},
		{"the turn count is not pushed off the panel", got["countInsidePanel"] != false},
		{"the conversation does not scroll sideways", num(got["detailSideways"]) == 0},
		{"the search box fits its panel", got["searchFitsPanel"] != false},
		{"the detail panel keeps a readable width", num(got["detailPanelW"]) >= 280},
		{"the search box's left border is not clipped", got["searchLeftVisible"] != false},
		{"the detail is no wider than its panel", num(got["shellSideways"]) <= 0},
		{"the actions row does not overflow", num(got["actsSideways"]) == 0},
	}
	failed := 0
	for _, c := range checks {
		mark := "ok  "
		if !c.ok {
			mark, failed = "FAIL", failed+1
		}
		fmt.Printf("    %s  %s\n", mark, c.name)
	}
	if failed > 0 {
		fmt.Printf("    measured: %s\n", strings.TrimSpace(string(m[1])))
	}
	return failed == 0
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

	// The detail is answered from a real session, not a stub. A made-up session
	// has short ids, a short path and no actions worth laying out; the real one
	// has a uuid, a worktree path and a row of buttons, and it is the real one
	// that overflows a narrow panel. Stubbing it is how the probe reported that
	// everything fitted while it did not.
	lib := bridge.NewLibrary()
	detail, convo := realSession(ctx, lib, contents)

	data, err := json.Marshal(map[string]any{
		"stores": stores, "contents": contents, "detail": detail, "conversation": convo,
	})
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

// realSession finds a session this machine actually holds and reads it, so the
// page is laid out with the content it will really be given.
func realSession(ctx context.Context, lib *bridge.Library, contents map[string]any) (any, any) {
	for _, v := range contents {
		c, ok := v.(bridge.ContentsView)
		if !ok {
			continue
		}
		for _, f := range c.Folders {
			for _, it := range f.Items {
				id := it.CLISession
				if id == "" && it.Kind == bridge.ItemTranscript {
					id = it.Session
				}
				if id == "" {
					continue
				}
				d, err := lib.Detail(ctx, id, "")
				if err != nil || d.Error != "" || len(d.First) == 0 {
					continue
				}
				conv, _ := lib.Conversation(ctx, id)
				return d, conv
			}
		}
	}
	return nil, nil
}

const stubCall = `const Call = { async ByName(name, ...args) {
  const D = window.__data;
  if (name.endsWith('Places.Stores')) return D.stores;
  if (name.endsWith('Places.Contents')) return D.contents[args[0]] ?? {folders: []};
  if (name.endsWith('Library.List')) return {sessions: [], machine: 'preview', accounts: []};
  if (name.endsWith('Library.TakeHandOff')) return '';
  if (name.endsWith('Library.Detail')) return D.detail || {session: {id: args[0]}, prompts: 0};
  if (name.endsWith('Library.Conversation')) return D.conversation || {turns: [], total: 0};
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
    const gapBtn = document.querySelector('#pitems .gapaction .linkish');
    const turnsBefore = document.querySelectorAll('#pitems .turns .turn').length;
    if (gapBtn) { gapBtn.click(); await sleep(500); }
    // Search inside the conversation, then clear it again.
    const sbox = document.querySelector('#pitems .convosearch input');
    let searchHits = null, searchCleared = null;
    if (sbox) {
      // A word from the conversation itself, since it is a real one now and
      // whatever I invented would simply not be in it.
      const someTurn = document.querySelector('#pitems .convorow .turn');
      const word = someTurn
        ? (someTurn.textContent.split(/\s+/).find(w => w.length > 5) || 'the')
        : 'the';
      sbox.value = word;
      sbox.dispatchEvent(new Event('input', {bubbles: true}));
      await sleep(200);
      searchHits = document.querySelectorAll('#pitems .convorow:not([hidden])').length;
      sbox.value = '';
      sbox.dispatchEvent(new Event('input', {bubbles: true}));
      await sleep(200);
      searchCleared = document.querySelectorAll('#pitems .convorow:not([hidden])').length;
    }
    const said = document.querySelector('#pitems .convorow.said .turn');
    const replied = document.querySelector('#pitems .convorow.replied .turn');
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
      detailScrolls: (() => {
        const b = document.querySelector('#pitems .drawer.inpane .body');
        return b ? getComputedStyle(b).overflowY === 'auto' : null;
      })(),
      actsInsidePanel: (() => {
        const a = document.querySelector('#pitems .acts');
        return a ? Math.round(r(a).bottom) <= Math.round(r($('pitems')).bottom) + 1 : null;
      })(),
      detailOverflow: (() => {
        const d = $('pitems');
        return d.scrollHeight - d.clientHeight;
      })(),
      sessions: document.querySelectorAll('.sessionrow:not([hidden])').length,
      hadOpenOut: !!gapBtn,
      turnsBefore: turnsBefore,
      turnsAfter: document.querySelectorAll('#pitems .turns .turn').length,
      gapGone: !document.querySelector('#pitems .gapaction'),
      hasSearch: !!sbox,
      convoSearchW: sbox ? Math.round(r(sbox).width) : null,
      detailPanelW: Math.round(r($('pitems')).width),
      searchFitsPanel: sbox
        ? Math.round(r(sbox).right) <= Math.round(r($('pitems')).right) + 1 : null,
      countInsidePanel: (() => {
        const c = document.querySelector('#pitems .convocount');
        return c ? Math.round(r(c).right) <= Math.round(r($('pitems')).right) + 1 : null;
      })(),
      detailSideways: (() => {
        const b = document.querySelector('#pitems .drawer.inpane .body');
        return b ? b.scrollWidth - b.clientWidth : null;
      })(),
      shellSideways: (() => {
        const sh = document.querySelector('#pitems .drawer.inpane');
        return sh ? Math.round(r(sh).width - r($('pitems')).width) : null;
      })(),
      actsSideways: (() => {
        const a = document.querySelector('#pitems .acts');
        return a ? a.scrollWidth - a.clientWidth : null;
      })(),
      // Against the body it sits in, not the panel: the body is what clips it,
      // and a border flush with that edge is a border you cannot see.
      searchLeftVisible: (() => {
        const b = document.querySelector('#pitems .drawer.inpane .body');
        // Four pixels, not one: the focus ring is an outline, which draws
        // outside the border box, so a border that merely clears the edge
        // still has its ring cut off when the input is focused.
        return sbox && b ? Math.round(r(sbox).left) >= Math.round(r(b).left) + 4 : null;
      })(),
      searchHits: searchHits,
      searchCleared: searchCleared,
      saidOnRight: said && replied
        ? Math.round(r(said).right) >= Math.round(r(replied).right)
          && Math.round(r(said).left) > Math.round(r(replied).left)
        : null,
      rolesDiffer: said && replied
        ? getComputedStyle(said).backgroundColor !== getComputedStyle(replied).backgroundColor
        : null,
    });
  } catch (e) { document.title = 'PROBE {"error":"' + String(e).replace(/"/g, "'") + '"}'; }
})();
</script>
`
