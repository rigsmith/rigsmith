// The curl|sh install domains (rigsmith.sh, rigcli.sh).
//
// Same family resolved one way: the path picks the tool, the User-Agent picks
// the response. A shell (curl/wget) gets the install script with the tool baked
// in as $1; a browser gets redirected to that tool's docs on rigsmith.dev.
//
// On the docs host (rigsmith.dev) and anything we don't recognize, this passes
// through untouched. See docs/WEBSITE.md.

import type { Config, Context } from 'https://edge.netlify.com'

const DOCS_ORIGIN = 'https://rigsmith.dev'

// Hosts that should serve the installer, and the tool each defaults to at "/".
const INSTALL_HOSTS: Record<string, string> = {
  'rigsmith.sh': 'all',
  'www.rigsmith.sh': 'all',
  'rigcli.sh': 'rig',
  'www.rigcli.sh': 'rig',
}

// Tools the installer can fetch. All four ship a release archive for every
// platform, and scripts/install.{sh,ps1} accept all four by name.
const TOOLS = new Set(['rig', 'changerig', 'shiprig', 'clauderig', 'codexrig'])

// Packages installable with Homebrew. A superset of TOOLS: `rigsmith` is the
// bundle cask, and `clauderig-ui` is the window, which the direct installer does
// not build — it fetches release archives, and the window ships as a signed .app
// inside a cask instead.
const BREW = new Set([...TOOLS, 'rigsmith', 'clauderig-ui'])

// Where a browser lands per tool.
const DOCS_PATH: Record<string, string> = {
  rig: '/rig/',
  changerig: '/changerig/',
  shiprig: '/shiprig/',
  clauderig: '/clauderig/',
  codexrig: '/codexrig/',
  all: '/guide/installation',
}

function wantsHtml(req: Request): boolean {
  // Browsers send Accept: text/html...; curl/wget send */* or omit it.
  const accept = req.headers.get('accept') || ''
  if (accept.includes('text/html')) return true
  const ua = (req.headers.get('user-agent') || '').toLowerCase()
  return ua.includes('mozilla')
}

export default async function handler(req: Request, context: Context) {
  const url = new URL(req.url)
  const host = url.hostname.toLowerCase()

  const hostDefault = INSTALL_HOSTS[host]
  if (!hostDefault) return // docs host / unknown — let Netlify serve normally.

  // The first path segment selects the tool; empty path uses the host default.
  const seg = url.pathname.replace(/^\/+|\/+$/g, '').split('/')[0]
  const tool = seg === '' ? hostDefault : seg

  // /brew installs with Homebrew rather than fetching an archive. The second
  // segment picks the package the way the first picks the tool elsewhere:
  // /brew, /brew/clauderig, /brew/clauderig-ui.
  //
  // Its own branch, before the tool check below, because the packages are not
  // the same set: the bundle and the window exist as casks and nowhere else.
  if (tool === 'brew') {
    const pkg = url.pathname.replace(/^\/+|\/+$/g, '').split('/')[1] || 'rigsmith'
    if (!BREW.has(pkg)) {
      return Response.redirect(DOCS_ORIGIN + '/guide/installation', 302)
    }
    // Browsers get the docs, as everywhere else here. PowerShell is not a case
    // worth handling: these are macOS casks.
    if (wantsHtml(req)) {
      return Response.redirect(DOCS_ORIGIN + (DOCS_PATH[pkg] || '/guide/installation'), 302)
    }
    const brewRes = await context.next(new Request(new URL('/brew.sh', url.origin)))
    if (!brewRes.ok) {
      return new Response('# rigsmith installer is temporarily unavailable\n', {
        status: 503,
        headers: { 'content-type': 'text/plain; charset=utf-8' },
      })
    }
    return new Response(`set -- ${pkg}\n` + (await brewRes.text()), {
      status: 200,
      headers: {
        'content-type': 'text/plain; charset=utf-8',
        'cache-control': 'public, max-age=300',
      },
    })
  }

  // Anything that isn't an installable tool (or "all") → send to the docs.
  if (tool !== 'all' && !TOOLS.has(tool)) {
    return Response.redirect(DOCS_ORIGIN + (DOCS_PATH[tool] || '/'), 302)
  }

  // PowerShell (`irm … | iex`) sends a UA containing "PowerShell" — and also
  // "Mozilla", so detect it before the browser check and serve the .ps1 installer.
  const ua = (req.headers.get('user-agent') || '').toLowerCase()
  const isPowerShell = ua.includes('powershell')

  // Browsers get the docs, not a wall of shell (PowerShell excepted — it wants it).
  if (!isPowerShell && wantsHtml(req)) {
    return Response.redirect(DOCS_ORIGIN + (DOCS_PATH[tool] || '/guide/installation'), 302)
  }

  // Fetch the canonical script for the client (both deployed by the build) and
  // bake in the tool: sh reads $1 (`set --`), PowerShell reads $RigsmithTool.
  const scriptPath = isPowerShell ? '/install.ps1' : '/install.sh'
  const scriptRes = await context.next(new Request(new URL(scriptPath, url.origin)))
  if (!scriptRes.ok) {
    return new Response('# rigsmith installer is temporarily unavailable\n', {
      status: 503,
      headers: { 'content-type': 'text/plain; charset=utf-8' },
    })
  }
  const script = await scriptRes.text()
  const prelude =
    tool === 'all' ? '' : isPowerShell ? `$RigsmithTool = '${tool}'\n` : `set -- ${tool}\n`

  return new Response(prelude + script, {
    status: 200,
    headers: {
      'content-type': 'text/plain; charset=utf-8',
      'cache-control': 'public, max-age=300',
    },
  })
}

export const config: Config = { path: '/*' }
