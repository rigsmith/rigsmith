---
"github.com/rigsmith/rigsmith": patch
---

clauderig: `clauderig desktop` now says how to set profiles up and when to share them. The ordered flow leads the group help, `desktop share --help` states that it must run with the windows closed and shows a worked example, `desktop add` ends by naming the next step, and `desktop list` suggests sharing only while it would change something. The design doc gains a setup walkthrough, including why the login is the one thing a new profile does not inherit: Desktop's refresh token is single-use, so two profiles holding one copy would work until the first refresh and then sign one of them out.