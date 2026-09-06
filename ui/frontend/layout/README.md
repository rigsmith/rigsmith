# Layout probe

jsdom has no layout engine. It will tell you an element exists, is not hidden
and carries the styles you meant to give it — and then let a checkbox be 280
pixels wide, a filter collapse to 18, and a panel scroll sideways clipping every
row in it. All three shipped past a passing suite.

This renders the real page in a real engine with real data and reports geometry.

    go run ./ui/frontend/layout          # writes a preview and measures it

It needs Google Chrome installed and skips cleanly when it is not there, so it
stays a local check rather than a CI dependency.
