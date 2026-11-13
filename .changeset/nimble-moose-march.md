---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

shiprig can read publishing credentials from Doppler. With a `release` config in the `.rig.json` `doppler` block, `doppler:NAME` works wherever shiprig takes a secret reference: a publish `auth`, a `signing.env` entry, or a release var's `secret`. A var can keep its keychain lookup beside it (`"secret": "doppler:FEEDZ_API_KEY", "os": {…}`); the keychain runs only while there is no release config. shiprig prints where each credential came from, by name only, and a missing or empty secret fails the release.