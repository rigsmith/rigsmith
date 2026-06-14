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

// Tools the installer can fetch today. (changerig isn't a release artifact yet;
// requests for it fall through to the docs.)
const TOOLS = new Set(['rig', 'shiprig', 'clauderig'])

// Where a browser lands per tool.
const DOCS_PATH: Record<string, string> = {
  rig: '/rig/',
  shiprig: '/shiprig/',
  clauderig: '/clauderig/',
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

  // Anything that isn't an installable tool (or "all") → send to the docs.
  if (tool !== 'all' && !TOOLS.has(tool)) {
    return Response.redirect(DOCS_ORIGIN + (DOCS_PATH[tool] || '/'), 302)
  }

  // Browsers get the docs, not a wall of shell.
  if (wantsHtml(req)) {
    return Response.redirect(DOCS_ORIGIN + (DOCS_PATH[tool] || '/guide/installation'), 302)
  }

  // Fetch the canonical script (deployed to /install.sh by the build) and bake
  // in the tool as a positional arg the script already understands ($1).
  const scriptRes = await context.next(new Request(new URL('/install.sh', url.origin)))
  if (!scriptRes.ok) {
    return new Response('# rigsmith installer is temporarily unavailable\n', {
      status: 503,
      headers: { 'content-type': 'text/plain; charset=utf-8' },
    })
  }
  const script = await scriptRes.text()
  const prelude = tool === 'all' ? '' : `set -- ${tool}\n`

  return new Response(prelude + script, {
    status: 200,
    headers: {
      'content-type': 'text/plain; charset=utf-8',
      'cache-control': 'public, max-age=300',
    },
  })
}

export const config: Config = { path: '/*' }
