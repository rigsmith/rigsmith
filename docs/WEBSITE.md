# Website & Domains

The plan for the rigsmith web presence, decided 2026-06-12 before any site
code was written. This is the reference for domain roles, URL structure, and
the stack — update it when reality diverges.

## Stack

- **VitePress**, living in a `/site` directory of this monorepo, deployed to
  **Netlify** on a path filter (site deploys only when `site/**` changes).
- Pure static output. No blog, no CMS — content is markdown edited in-repo,
  with "Edit this page" links to GitHub.
- Scope: a landing page pitching the family, one docs section per tool, and
  comparison pages (vs `@changesets/cli`, vs make/just-style runners).

## Domain map

| Domain | Role |
| --- | --- |
| **rigsmith.dev** | The site. Landing page + all docs (see URL structure below). |
| **rigsmith.sh** | Canonical install domain for *all* tools, current and future (see below). |
| **rigcli.dev** | 301 → `rigsmith.dev/rig/*` (host-based rule in Netlify `_redirects`). |
| **relrig.dev** | 301 → `rigsmith.dev/shiprig/*` (back-compat redirect from the old name). |
| **relrig.sh** | Temporary back-compat alias (`curl relrig.sh \| sh` installs shiprig) until renewal, then lapses. |
| **rigcli.sh** | Redirect only; lapses at renewal. |

.sh renewals are expensive — the standing rule is **no new .sh purchases**
(there is deliberately no clauderig.sh). New tools get an install path on
rigsmith.sh for free.

## URL structure

Docs sections are keyed by **binary name**, one VitePress sidebar each:

```
rigsmith.dev/
├── /            landing: the family, the pitch
├── /rig/        the dev launcher
├── /changerig/  the changeset tool
├── /shiprig/    release orchestration
├── /clauderig/  Claude Code sync
└── /core/       the engine: plugin protocol, pathmap, release planner
```

Why command names and not concepts (`/changesets/`, `/release/`):

- URLs should match what users type — someone who just ran `changerig add`
  will guess `rigsmith.dev/changerig/add`.
- The binary name is already the key in install paths, GoReleaser artifacts,
  and (future) brew/scoop targets; doc paths reusing it keep every mapping 1:1.
- `/changesets/` would collide with `@changesets/cli` in search results;
  "changeRig" is a unique token we own completely.
- Friendly naming for newcomers is solved in nav labels
  ("shipRig — release orchestration"), not URLs.

`/core/` is the one non-command path; it matches the module name and has no
command to mirror.

## Install domain behavior (rigsmith.sh)

```sh
curl -fsSL rigsmith.sh | sh            # install the family
curl -fsSL rigsmith.sh/shiprig | sh    # install one tool (any binary name)
```

A piped script can't see the URL it came from, so per-tool paths are resolved
server-side by a single **Netlify edge function** that also sniffs the
User-Agent:

- curl/wget → returns [`scripts/install.sh`](../scripts/install.sh) with the
  tool from the path baked in as the default selection (root = all tools);
- browsers → 302 to the matching rigsmith.dev docs section.

This supersedes the two-domain scheme in [DISTRIBUTION.md](DISTRIBUTION.md)
(`rigsmith.sh` + `relrig.sh`); update that doc and `install.sh` when the edge
function lands.

## Decisions & rationale

- **One site, not per-tool micro-sites.** Three small sites fragment SEO,
  nav, search, and maintenance; the extra .dev domains earn their keep as
  redirects instead.
- **VitePress over Astro Starlight.** The core of the site is multi-tool
  reference docs, and VitePress has native per-path-prefix sidebars where
  Starlight needs a community plugin for its core nav; John is Vue-fluent and
  VitePress is Vue end-to-end (Starlight's chrome is a second component
  dialect). Starlight's edges (landing freedom, Pagefind search, zero-JS
  pages) weren't decisive at this site's size.
- **VitePress over Nuxt Content.** Nuxt's wins are blog ergonomics and Nuxt
  Studio's in-browser editing — but the blog was cut from scope, and a solo
  maintainer editing docs in the same PRs as features doesn't need a CMS.
  Not worth assembling the docs chrome by hand or carrying a full framework.
- **No blog.** Wouldn't be used; GitHub Releases covers announcements for a
  dev-tool audience. Cutting it removed the only part of VitePress that
  required hand-rolling (a `createContentLoader` blog index + RSS).
- **No Go vanity import paths.** Modules stay `github.com/rigsmith/*` —
  vanity paths bake the domain into every consumer's go.mod forever and
  require serving go-import meta tags for as long as anyone depends on them.
- **Netlify over Vercel/Cloudflare.** Host-based redirects via `_redirects`
  cover the whole domain plan in a few lines, edge functions cover the
  install domain, and the free tier has no commercial-use restriction.

## Build order

1. Scaffold VitePress under `/site` with the multi-sidebar config and landing page.
2. Port existing content: per-tool READMEs and `docs/*.md` are the seed.
3. Netlify project: attach all domains, write `_redirects`.
4. Install edge function; update `install.sh` (tool-default injection) and
   DISTRIBUTION.md, including the `rigsmith/rigsmith` repo-slug placeholders
   it already flags.
5. Comparison pages.
