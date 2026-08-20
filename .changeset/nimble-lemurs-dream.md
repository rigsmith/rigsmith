---
"github.com/rigsmith/rigsmith": patch
---

clauderig: `clauderig desktop` now says how to set profiles up. The ordered flow leads the group help, `desktop add` ends by naming the next step, and `open`/`quit` ask which profile instead of guessing when given no name. The design doc gains a setup walkthrough, including why the login is the one thing a new profile does not inherit: Desktop's refresh token is single-use, so two profiles holding one copy would work until the first refresh and then sign one of them out.
