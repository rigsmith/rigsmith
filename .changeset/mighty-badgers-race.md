---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

The generated npm packages declare the repository that builds them, which publishing through OIDC requires: npm attaches a provenance attestation automatically when a public repository publishes that way, and provenance needs the manifest to name its repository.