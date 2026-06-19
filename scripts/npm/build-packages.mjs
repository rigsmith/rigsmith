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
//
// Reads dist/metadata.json + dist/artifacts.json (GoReleaser). Writes packages
// under npm/dist/. With --publish, runs `npm publish` for each (needs registry
// auth in the environment, e.g. NODE_AUTH_TOKEN from actions/setup-node).

import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'

const ROOT = process.cwd()
const DIST = path.join(ROOT, 'dist')
const OUT = path.join(ROOT, 'npm', 'dist')
const PUBLISH = process.argv.includes('--publish')

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

const version = readJson(path.join(DIST, 'metadata.json')).version
if (!version) throw new Error('no version in dist/metadata.json')

const binaries = readJson(path.join(DIST, 'artifacts.json')).filter((a) => a.type === 'Binary')

fs.rmSync(OUT, { recursive: true, force: true })
fs.mkdirSync(OUT, { recursive: true })

// 1. Per-platform packages — copy each prebuilt binary into its own package.
const platformPkgs = {} // tool -> [pkgName]
for (const b of binaries) {
  const tool = b.extra?.ID
  const npmos = OS_MAP[b.goos]
  const npmarch = ARCH_MAP[b.goarch]
  if (!TOOLS[tool] || !npmos || !npmarch) continue

  const exe = tool + (b.extra.Ext || '')
  const name = `${SCOPE}/${tool}-${npmos}-${npmarch}`
  const dir = path.join(OUT, `${tool}-${npmos}-${npmarch}`)
  fs.mkdirSync(path.join(dir, 'bin'), { recursive: true })
  fs.copyFileSync(path.join(ROOT, b.path), path.join(dir, 'bin', exe))
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
if (!PUBLISH) {
  console.log('(dry build — pass --publish to publish)')
} else {
  for (const d of order) {
    const dir = path.join(OUT, d)
    const name = readJson(path.join(dir, 'package.json')).name
    const access = name.startsWith('@') ? ['--access', 'public'] : []
    console.log(`publishing ${name}@${version}`)
    execFileSync('npm', ['publish', ...access], { cwd: dir, stdio: 'inherit' })
  }
}
