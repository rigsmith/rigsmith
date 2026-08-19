---
"github.com/rigsmith/rigsmith": minor
---

clauderig: `account switch` now moves Claude Desktop too, without a re-login. Desktop keeps tokens for the active account only, so switching used to mean signing in again; clauderig now snapshots the outgoing session and restores the incoming one. Both of Desktop's auth surfaces move together — the OAuth token cache in `config.json` and the claude.ai session cookies — copied as ciphertext and never decrypted, so no plaintext credential is written to disk. Snapshots are sealed with the machine's own key and are refused on any other machine, which keeps this strictly local: accounts are never synced. Because the CLI and Desktop are independent logins that can be signed in as different accounts, a session is only ever filed under the account that actually owns it, matched on account uuid.
