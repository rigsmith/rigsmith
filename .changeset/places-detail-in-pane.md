---
type: feat
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

Show a session's full detail in the Places panel, and add a layout probe.

Clicking a session in Places now fills the third panel with what the sliding
drawer shows — the same call and the same rendering, so there is one description
of a session rather than two that drift apart. Sessions with no conversation
behind them (a Desktop record whose transcript is elsewhere, a deleted one) say
what the record holds and why there is nothing more.

`go run ./ui/frontend/layout` renders the window in headless Chrome with this
machine's own data and measures it. jsdom has no layout engine, so it will
confirm an element exists and is styled and still let a checkbox be 280 pixels
wide — which is exactly what happened. It skips cleanly where Chrome is absent.
