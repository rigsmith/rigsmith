#!/usr/bin/env node
// Build (and optionally publish) the npm binary-wrapper packages from the
// GoReleaser dist/ output — the esbuild model:
//
//   @rigsmith/<tool>-<os>-<arch>   one per platform, carrying the prebuilt binary
//                                  (os/cpu fields, so npm installs only the match)
//   @rigsmith/<tool>               the package you install; selects the right
//                                  platform package via optionalDependencies and
//                                  execs it through a tiny launcher shim
//   rigsmith                       meta package depending on all four tools
//
// Why npm at all: an npm-installed binary runs via the shim from node_modules, so
// it never carries Windows' Mark-of-the-Web — `npm i -g @rigsmith/rig` sidesteps
// the SmartScreen prompt that a browser-downloaded .exe triggers.
//
// Usage:
//   node scripts/npm/build-packages.mjs [--publish]
//   node scripts/npm/build-packages.mjs --from-release v1.5.0 [--publish]
//
// Reads dist/metadata.json + dist/artifacts.json (GoReleaser). Writes packages
// under npm/dist/. With --publish, runs `npm publish` for each (needs registry
// auth in the environment, e.g. NODE_AUTH_TOKEN from actions/setup-node).
//
// --from-release <tag> takes the binaries from a *published* GitHub release
// instead of a local dist/. It is the recovery path for a release whose npm step
// never ran: dist/ holds the signed binaries and exists only on the runner that
// built them, so rebuilding locally would push unsigned Windows binaries to npm,
// and cutting a fresh version to fix one channel drags every other channel along
// with it (five more winget PRs, for one). Downloading the published archives
// republishes the exact bytes users already have everywhere else.
//
// Every archive is checksum-verified against the release's own checksums.txt
// before it is unpacked — these binaries are about to go out under our name.

import { execFileSync } from 'node:child_process'
import crypto from 'node:crypto'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const ROOT = process.cwd()
const DIST = path.join(ROOT, 'dist')
const OUT = path.join(ROOT, 'npm', 'dist')
const PUBLISH = process.argv.includes('--publish')
const DRY_PUBLISH = process.argv.includes('--dry-publish') // npm publish --dry-run: pack + validate, no upload
const FROM_RELEASE = argValue('--from-release') // e.g. v1.5.0
const REPO = process.env.RIGSMITH_REPO || 'rigsmith/rigsmith'

function argValue(flag) {
  const i = process.argv.indexOf(flag)
  if (i === -1) return ''
  const v = process.argv[i + 1]
  if (!v || v.startsWith('--')) throw new Error(`${flag} needs a value, e.g. ${flag} v1.5.0`)
  return v
}

const SCOPE = '@rigsmith'
const HOMEPAGE = 'https://rigsmith.dev'
const LICENSE = 'MIT'
const OS_MAP = { darwin: 'darwin', linux: 'linux', windows: 'win32' }
const ARCH_MAP = { amd64: 'x64', arm64: 'arm64' }

// The tools to wrap, with the npm `description` for each main package.
const TOOLS = {
  rig: 'Convention-first dev launcher across .NET, Node, Go, and Rust',
  shiprig: 'Uniform changeset -> version -> publish, across every ecosystem',
  changerig: 'Changesets: capture intent, then version across every ecosystem',
  clauderig: 'Sync your Claude Code setup across machines, path-correct on restore',
}

const readJson = (p) => JSON.parse(fs.readFileSync(p, 'utf8'))
const writeJson = (dir, obj) => {
  fs.mkdirSync(dir, { recursive: true })
  fs.writeFileSync(path.join(dir, 'package.json'), JSON.stringify(obj, null, 2) + '\n')
}

// Both sources yield the same shape: { version, binaries: [{ tool, goos, goarch, ext, file }] }.
const { version, binaries } = FROM_RELEASE ? fromRelease(FROM_RELEASE) : fromDist()
if (!version) throw new Error('no version resolved')
if (binaries.length === 0) throw new Error('no binaries found — nothing to package')

// fromDist reads what GoReleaser just built in this working tree.
function fromDist() {
  const v = readJson(path.join(DIST, 'metadata.json')).version
  if (!v) throw new Error('no version in dist/metadata.json')
  const bins = readJson(path.join(DIST, 'artifacts.json'))
    .filter((a) => a.type === 'Binary')
    .map((a) => ({
      tool: a.extra?.ID,
      goos: a.goos,
      goarch: a.goarch,
      ext: a.extra?.Ext || '',
      file: path.join(ROOT, a.path),
    }))
  return { version: v, binaries: bins }
}

// fromRelease downloads a published release's per-tool archives, verifies each
// against checksums.txt, and unpacks the binary out of each one.
function fromRelease(tag) {
  const v = tag.replace(/^v/, '')
  const work = fs.mkdtempSync(path.join(os.tmpdir(), 'rigsmith-npm-'))
  console.log(`Fetching ${REPO}@${tag} release assets into ${work}`)

  // Per-tool archives only. The `rigsmith_*` bundle carries all four binaries and
  // would double every platform package.
  const patterns = Object.keys(TOOLS).flatMap((t) => ['--pattern', `${t}_${v}_*`])
  gh(['release', 'download', tag, '--repo', REPO, '--dir', work, '--clobber', ...patterns, '--pattern', 'checksums.txt'])

  const sums = parseChecksums(fs.readFileSync(path.join(work, 'checksums.txt'), 'utf8'))
  const bins = []
  for (const archive of fs.readdirSync(work).sort()) {
    // <tool>_<version>_<goos>_<goarch>.tar.gz | .zip
    const m = /^([a-z]+)_.+_(darwin|linux|windows)_(amd64|arm64)\.(tar\.gz|zip)$/.exec(archive)
    if (!m) continue
    const [, tool, goos, goarch, kind] = m
    if (!TOOLS[tool]) continue

    verifyChecksum(path.join(work, archive), sums[archive], archive)

    const into = path.join(work, `x-${tool}-${goos}-${goarch}`)
    fs.mkdirSync(into, { recursive: true })
    unpack(path.join(work, archive), into, kind)

    const ext = goos === 'windows' ? '.exe' : ''
    const file = path.join(into, tool + ext)
    if (!fs.existsSync(file)) throw new Error(`${archive} contains no ${tool}${ext}`)
    bins.push({ tool, goos, goarch, ext, file })
  }
  console.log(`Verified and unpacked ${bins.length} binaries from ${tag}`)
  return { version: v, binaries: bins }
}

function gh(args) {
  try {
    execFileSync('gh', args, { stdio: ['ignore', 'inherit', 'inherit'] })
  } catch (e) {
    throw new Error(`gh ${args.slice(0, 3).join(' ')} failed — is the GitHub CLI installed and authenticated? (${e.message})`)
  }
}

// checksums.txt is `<sha256>  <filename>` per line, as GoReleaser writes it.
function parseChecksums(text) {
  const out = {}
  for (const line of text.split('\n')) {
    const m = /^([0-9a-f]{64})\s+\*?(.+)$/.exec(line.trim())
    if (m) out[m[2]] = m[1]
  }
  return out
}

function verifyChecksum(file, want, name) {
  if (!want) throw new Error(`${name} is not listed in checksums.txt — refusing to publish an unverified binary`)
  const got = crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')
  if (got !== want) throw new Error(`${name} checksum mismatch: release says ${want}, downloaded file is ${got}`)
}

function unpack(archive, into, kind) {
  if (kind === 'zip') {
    // GNU tar can't read zips; unzip is present on every CI runner and macOS.
    execFileSync('unzip', ['-o', '-q', archive, '-d', into], { stdio: ['ignore', 'inherit', 'inherit'] })
    return
  }
  execFileSync('tar', ['-xzf', archive, '-C', into], { stdio: ['ignore', 'inherit', 'inherit'] })
}

fs.rmSync(OUT, { recursive: true, force: true })
fs.mkdirSync(OUT, { recursive: true })

// 1. Per-platform packages — copy each prebuilt binary into its own package.
const platformPkgs = {} // tool -> [pkgName]
for (const b of binaries) {
  const { tool } = b
  const npmos = OS_MAP[b.goos]
  const npmarch = ARCH_MAP[b.goarch]
  if (!TOOLS[tool] || !npmos || !npmarch) continue

  const exe = tool + b.ext
  const name = `${SCOPE}/${tool}-${npmos}-${npmarch}`
  const dir = path.join(OUT, `${tool}-${npmos}-${npmarch}`)
  fs.mkdirSync(path.join(dir, 'bin'), { recursive: true })
  fs.copyFileSync(b.file, path.join(dir, 'bin', exe))
  fs.chmodSync(path.join(dir, 'bin', exe), 0o755)
  writeJson(dir, {
    name,
    version,
    description: `${tool} binary for ${npmos}-${npmarch}`,
    license: LICENSE,
    homepage: HOMEPAGE,
    os: [npmos],
    cpu: [npmarch],
    files: ['bin'],
  })
  ;(platformPkgs[tool] ||= []).push(name)
}

// 2. Main package per tool — selects the platform package + execs its binary.
const launcher = (tool) => `#!/usr/bin/env node
'use strict'
const { spawnSync } = require('node:child_process')
const path = require('node:path')

function binaryPath() {
  const os = process.platform // darwin | linux | win32
  const arch = process.arch // x64 | arm64
  const exe = '${tool}' + (os === 'win32' ? '.exe' : '')
  let pkgJson
  try {
    pkgJson = require.resolve('${SCOPE}/${tool}-' + os + '-' + arch + '/package.json')
  } catch {
    throw new Error(
      '${tool}: no prebuilt binary for ' + os + '-' + arch + '. ' +
        "Install from ${HOMEPAGE} or 'go install github.com/rigsmith/rigsmith/cmd/${tool}@latest'."
    )
  }
  return path.join(path.dirname(pkgJson), 'bin', exe)
}

const res = spawnSync(binaryPath(), process.argv.slice(2), { stdio: 'inherit' })
if (res.error) {
  console.error(String(res.error.message || res.error))
  process.exit(1)
}
process.exit(res.status === null ? 1 : res.status)
`

for (const [tool, description] of Object.entries(TOOLS)) {
  const dir = path.join(OUT, tool)
  fs.mkdirSync(path.join(dir, 'bin'), { recursive: true })
  fs.writeFileSync(path.join(dir, 'bin', `${tool}.js`), launcher(tool))
  fs.chmodSync(path.join(dir, 'bin', `${tool}.js`), 0o755)
  const optionalDependencies = Object.fromEntries((platformPkgs[tool] || []).sort().map((n) => [n, version]))
  writeJson(dir, {
    name: `${SCOPE}/${tool}`,
    version,
    description,
    license: LICENSE,
    homepage: HOMEPAGE,
    bin: { [tool]: `bin/${tool}.js` },
    files: ['bin'],
    optionalDependencies,
  })
}

// 3. Meta package — installs all four; `rigsmith <tool>` dispatches to each shim.
const metaLauncher = `#!/usr/bin/env node
'use strict'
const { spawnSync } = require('node:child_process')
const TOOLS = ${JSON.stringify(Object.keys(TOOLS))}
const [tool, ...rest] = process.argv.slice(2)
if (!tool || !TOOLS.includes(tool)) {
  console.log('rigsmith — the CLI family: ' + TOOLS.join(', '))
  console.log('Usage: rigsmith <tool> [args]   (each tool is also installed on its own, e.g. \\\`rig\\\`)')
  process.exit(tool ? 1 : 0)
}
const shim = require.resolve('${SCOPE}/' + tool + '/bin/' + tool + '.js')
const res = spawnSync(process.execPath, [shim, ...rest], { stdio: 'inherit' })
process.exit(res.status === null ? 1 : res.status)
`
{
  const dir = path.join(OUT, 'rigsmith')
  fs.mkdirSync(path.join(dir, 'bin'), { recursive: true })
  fs.writeFileSync(path.join(dir, 'bin', 'rigsmith.js'), metaLauncher)
  fs.chmodSync(path.join(dir, 'bin', 'rigsmith.js'), 0o755)
  writeJson(dir, {
    name: 'rigsmith',
    version,
    description: 'The rigsmith CLI family: rig, shiprig, changerig, clauderig',
    license: LICENSE,
    homepage: HOMEPAGE,
    bin: { rigsmith: 'bin/rigsmith.js' },
    files: ['bin'],
    dependencies: Object.fromEntries(Object.keys(TOOLS).map((t) => [`${SCOPE}/${t}`, version])),
  })
}

// Publish order: platform packages first (they're the optionalDependencies),
// then the main packages, then the meta.
const platformDirs = Object.values(platformPkgs).flat().map((n) => n.slice(SCOPE.length + 1))
const order = [...platformDirs, ...Object.keys(TOOLS), 'rigsmith']

console.log(`Built ${order.length} package(s) at v${version} under npm/dist/`)
if (!PUBLISH && !DRY_PUBLISH) {
  console.log('(dry build — pass --publish to publish, or --dry-publish to validate packaging)')
} else {
  const dryRun = DRY_PUBLISH ? ['--dry-run'] : []
  const verb = DRY_PUBLISH ? 'validating' : 'publishing'
  // A prerelease version (anything with a `-`, e.g. a snapshot or `1.0.0-rc.1`)
  // must be published under a dist-tag so it doesn't move `latest`; a stable
  // version publishes as `latest` (npm's default).
  const tag = version.includes('-') ? ['--tag', 'next'] : []
  for (const d of order) {
    const dir = path.join(OUT, d)
    const name = readJson(path.join(dir, 'package.json')).name
    const access = name.startsWith('@') ? ['--access', 'public'] : []
    console.log(`${verb} ${name}@${version}`)
    execFileSync('npm', ['publish', ...dryRun, ...tag, ...access], { cwd: dir, stdio: 'inherit' })
  }
}
