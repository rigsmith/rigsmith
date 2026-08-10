---
type: fix
"github.com/rigsmith/rigsmith"
---

`clauderig account`'s profile-block write is now atomic, and `doctor --fix` refuses an ambiguous repair. Both are follow-ups to the identity-desync tooling.

Writing the `oauthAccount` block used `os.WriteFile`, which truncates the destination before writing. `~/.claude.json` holds far more than the identity block — project state, history, per-org caches, around 75 KB in practice — so a failure partway through would have left it truncated, and would also have made `switch`'s "credential rolled back, nothing changed" message untrue, since the credential would be restored while the profile stayed corrupt. The write now goes to a sibling temp file that is flushed and renamed over the destination, so the file is either wholly the old contents or wholly the new one, never a fragment. The destination's permissions are carried across rather than inherited from the temp file.

`doctor --fix` located the account to repair from by organization and took the first match. Two logins can legitimately belong to the same organization, and a credential names only the organization — no email, no account uuid — so there was nothing to tell them apart, and the repair could have stamped one identity's profile block over another's. That is precisely the mislabeling the command exists to prevent, so it now refuses when more than one stored account claims the credential's organization, names the candidates, and points at `switch` or a fresh `add` to resolve it deliberately.
