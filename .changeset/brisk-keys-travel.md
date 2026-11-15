---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

shiprig can sign and notarize a velopack macOS app from secrets alone, with nothing in the login keychain. Put the Developer ID `.p12` (base64) and its password in the velopack signing env as `MACOS_SIGN_P12` and `MACOS_SIGN_PASSWORD`, and optionally an App Store Connect API key as `MACOS_NOTARY_KEY` (the `.p8` as PEM text or base64), `MACOS_NOTARY_KEY_ID` and `MACOS_NOTARY_ISSUER_ID`. Each can be a `doppler:` reference. A signed macOS build then creates a throwaway keychain, imports the cert, stores the notary key under `macos.notaryProfile`, points `vpk pack --keychain` and the DMG's `codesign`/`notarytool` at it, and deletes it when the build ends, whether or not the build succeeded. A cert that doesn't hold `macos.signIdentity`, a notary key without a cert, `notaryProfile` or key id, or a cert with `notaryProfile` but no notary key, fails before anything is built. Errors from these steps never show the passwords. Builds without these variables behave as before.
