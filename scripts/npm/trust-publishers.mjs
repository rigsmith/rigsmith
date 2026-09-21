#!/usr/bin/env node
// Register the release workflow as npm's trusted publisher for every wrapper
// package a release publishes.
//
//   node scripts/npm/trust-publishers.mjs --dry-run   # print what it would do
//   node scripts/npm/trust-publishers.mjs             # register
//   node scripts/npm/trust-publishers.mjs --list      # what the registry holds
//
// Trusted publishing lets the workflow mint a short-lived credential from its
// OIDC identity instead of carrying a long-lived NPM_TOKEN — the token that
// expired mid-month, left 1.19.0's npm packages unpublished, and whose failure
// then skipped five winget submissions.
//
// ONE WORKFLOW, NOT TWO. The registry supports a single trusted-publisher
// configuration per package ("If you attempt to create a new trust relationship
// when one already exists, it will result in an error" — npm trust docs), so
// this registers the release workflow and only that. The consequence is worth
// knowing: npm-republish.yml, the recovery path that published 1.19.0's npm
// packages after the release run died, cannot use OIDC and still needs
// NPM_TOKEN. Removing that secret entirely would mean giving the recovery path
// a different shape — re-running the release workflow rather than its own.
//
// RUN IT AGAIN WHENEVER A TOOL IS ADDED — a new tool means seven new packages,
// none of which can publish via OIDC until registered. It is one more
// distribution surface to forget, alongside those scripts/tooldist_test.go
// guards, so the check below refuses to run against a stale npm/dist.
//
// Requires `npm login` first: this writes to the registry as the package owner.
// A package that does not exist yet cannot be registered — publish it once with
// a token, then run this.
import { execFileSync, spawnSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = path.resolve(HERE, '..', '..')
const OUT = path.join(REPO_ROOT, 'npm', 'dist')
const REPOSITORY = 'rigsmith/rigsmith'
const WORKFLOW = 'goreleaser.yml'
const SCOPE = '@rigsmith'

// `npm trust` is newer than trusted publishing itself: publishing via OIDC
// needs 11.5.1, but the command that CONFIGURES it needs 11.15.0. Checking
// `npm whoami` alone would authenticate fine and then fail once per package.
const MIN_NPM = [11, 15, 0]

// npm's own guidance: "We recommend adding a 2-second sleep between each call
// to avoid rate limiting. With this approach, you can configure approximately
// 80 packages within the 5-minute two-factor authentication skip window."
const CALL_SPACING_MS = 2000

const DRY_RUN = process.argv.includes('--dry-run')
const LIST = process.argv.includes('--list')

function fail(msg) {
  console.error(`trust-publishers: ${msg}`)
  process.exit(1)
}

const sleep = (ms) => Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms)

// requireNpm refuses before the batch rather than once per package: an npm
// without `trust` would otherwise repeat the same failure 41 times.
function requireNpm() {
  const r = spawnSync('npm', ['--version'], { encoding: 'utf8' })
  if (r.status !== 0) fail('npm is not on PATH')
  const raw = r.stdout.trim()
  const parts = raw.split('.').map((n) => parseInt(n, 10))
  if (parts.length < 3 || parts.some(Number.isNaN)) fail(`cannot read npm version from ${JSON.stringify(raw)}`)
  for (let i = 0; i < 3; i++) {
    if (parts[i] > MIN_NPM[i]) break
    if (parts[i] < MIN_NPM[i]) {
      fail(`npm ${raw} has no usable \`npm trust\` — ${MIN_NPM.join('.')} or later is required ` +
        `(npm install -g npm@^${MIN_NPM[0]}.${MIN_NPM[1]}). Trusted PUBLISHING needs only 11.5.1; ` +
        `configuring it needs this.`)
    }
  }
  return raw
}

function npmLogin() {
  const r = spawnSync('npm', ['whoami'], { encoding: 'utf8' })
  if (r.status !== 0) {
    fail('not logged in to npm — run `npm login` first (this writes to the registry as the package owner)')
  }
  return r.stdout.trim()
}

// packageNames reads the built wrapper packages. Names come from the build
// output rather than a list kept here: build-packages.mjs decides what exists,
// including which platforms each tool supports, and a second copy of that would
// drift the moment one changed.
function packageNames() {
  if (!fs.existsSync(OUT)) {
    fail(`no ${path.relative(REPO_ROOT, OUT)}/ — run \`node scripts/npm/build-packages.mjs\` first ` +
      `(it needs a GoReleaser dist/, e.g. from \`goreleaser release --snapshot --clean --skip=publish\`)`)
  }
  const names = []
  for (const dir of fs.readdirSync(OUT)) {
    const manifest = path.join(OUT, dir, 'package.json')
    if (!fs.existsSync(manifest)) continue
    let parsed
    try {
      parsed = JSON.parse(fs.readFileSync(manifest, 'utf8'))
    } catch (err) {
      fail(`${path.relative(REPO_ROOT, manifest)}: ${err.message}`)
    }
    // A manifest is only a package if it names one. Truthiness is not enough:
    // a null root, or a name that is not a string, would be passed to npm as an
    // argument it cannot use, mid-batch.
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      fail(`${path.relative(REPO_ROOT, manifest)}: not a JSON object`)
    }
    if (typeof parsed.name !== 'string' || parsed.name.trim() === '') {
      fail(`${path.relative(REPO_ROOT, manifest)}: "name" must be a non-empty string`)
    }
    names.push(parsed.name)
  }
  if (names.length === 0) fail(`${path.relative(REPO_ROOT, OUT)}/ holds no packages`)
  return names.sort()
}

// refuseStaleBuild is the check that makes "run it again when a tool is added"
// enforceable. npm/dist is a build output that survives the tool it was built
// from: registering whatever happens to be lying there would silently leave a
// new tool's packages unregistered, and report success for the rest.
function refuseStaleBuild(names) {
  const cmdDir = path.join(REPO_ROOT, 'cmd')
  if (!fs.existsSync(cmdDir)) return // not the rigsmith tree; nothing to compare against
  const tools = fs.readdirSync(cmdDir, { withFileTypes: true })
    .filter((e) => e.isDirectory())
    .map((e) => e.name)
  const missing = tools.filter((tool) => !names.includes(`${SCOPE}/${tool}`))
  if (missing.length > 0) {
    fail(`${path.relative(REPO_ROOT, OUT)}/ has no package for ${missing.join(', ')}, ` +
      `which cmd/ says exist — the build output is stale. Rebuild it ` +
      `(goreleaser release --snapshot --clean --skip=publish && node scripts/npm/build-packages.mjs) ` +
      `and run this again, or those packages go unregistered while everything else reports success.`)
  }
}

// registeredWorkflows returns the workflow filenames the registry already holds
// for a package, so an existing configuration is judged rather than assumed.
function registeredWorkflows(name) {
  const r = spawnSync('npm', ['trust', 'list', name, '--json'], { encoding: 'utf8' })
  if (r.status !== 0) return null
  try {
    const parsed = JSON.parse(r.stdout)
    const rows = Array.isArray(parsed) ? parsed : (parsed?.trustedPublishers ?? parsed?.publishers ?? [])
    return rows.map((row) => row?.workflow ?? row?.workflowFilename ?? row?.file ?? '').filter(Boolean)
  } catch {
    return null
  }
}

const npmVersion = requireNpm()

if (LIST) {
  for (const name of packageNames()) {
    const r = spawnSync('npm', ['trust', 'list', name], { encoding: 'utf8' })
    const body = (r.stdout || r.stderr || '').trim().replace(/\n/g, '\n    ')
    console.log(`${name}\n    ${body || '(none)'}`)
  }
  process.exit(0)
}

const names = packageNames()
refuseStaleBuild(names)
const who = DRY_RUN ? '(dry run)' : npmLogin()
console.log(`npm ${npmVersion}. Registering ${names.length} package(s) for ${WORKFLOW} on ${REPOSITORY} as ${who}`)
if (!DRY_RUN) {
  console.log(`Pacing calls ${CALL_SPACING_MS}ms apart, as npm recommends; ~${Math.round(names.length * CALL_SPACING_MS / 1000)}s total.\n`)
}

let done = 0
const failures = []
const mismatched = []
for (const [i, name] of names.entries()) {
  const args = ['trust', 'github', name,
    '--file', WORKFLOW,
    '--repo', REPOSITORY,
    '--allow-publish',
    '--yes']
  if (DRY_RUN) {
    console.log(`would: npm ${args.join(' ')}`)
    continue
  }
  if (i > 0) sleep(CALL_SPACING_MS)
  try {
    execFileSync('npm', args, { stdio: 'pipe' })
    console.log(`✓ ${name}`)
    done++
  } catch (err) {
    const out = `${err.stdout || ''}${err.stderr || ''}`
    // "Already exists" is only success if what exists is what we wanted. The
    // registry allows one configuration per package, so this same message
    // appears when a DIFFERENT workflow holds the slot — counting that as
    // registered would report a package as publishable by a workflow that
    // cannot publish it.
    if (/already exists|already configured|duplicate|409/i.test(out)) {
      const held = registeredWorkflows(name)
      if (held === null) {
        mismatched.push(`${name} (already configured; could not read what holds it)`)
      } else if (held.some((w) => w.endsWith(WORKFLOW))) {
        console.log(`· ${name} (already registered for ${WORKFLOW})`)
        done++
      } else {
        mismatched.push(`${name} (held by ${held.join(', ') || 'an unreadable entry'})`)
      }
      continue
    }
    console.error(`✗ ${name}: ${out.trim().split('\n').slice(-2).join(' ')}`)
    failures.push(name)
  }
}

if (!DRY_RUN) {
  console.log(`\n${done}/${names.length} registered for ${WORKFLOW}`)
  if (mismatched.length) {
    console.error(`\nAlready configured for a different publisher — the registry allows one per package,\n` +
      `so these need \`npm trust revoke --id <id> <package>\` before they can be re-registered:\n  ` +
      mismatched.join('\n  '))
  }
  if (failures.length) console.error(`\nfailed: ${failures.join(', ')}`)
  if (failures.length || mismatched.length) process.exit(1)
  console.log('Verify with: node scripts/npm/trust-publishers.mjs --list')
}
