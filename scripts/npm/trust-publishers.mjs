#!/usr/bin/env node
// Register the release workflow as npm's trusted publisher for every wrapper
// package a release publishes.
//
//   node scripts/npm/trust-publishers.mjs --dry-run   # print what it would do
//   node scripts/npm/trust-publishers.mjs             # register
//   node scripts/npm/trust-publishers.mjs --otp 123456 # ...without being asked for it
//   node scripts/npm/trust-publishers.mjs --list      # what the registry holds
//   node scripts/npm/trust-publishers.mjs --replace   # move packages bound to another
//                                                      # workflow in this repo over to this one
//
// Trusted publishing lets the workflow mint a short-lived credential from its
// OIDC identity instead of carrying a long-lived NPM_TOKEN — the token that
// expired mid-month, left 1.19.0's npm packages unpublished, and whose failure
// then skipped five winget submissions.
//
// ONE WORKFLOW, BY DESIGN. The registry supports a single trusted-publisher
// configuration per package ("If you attempt to create a new trust relationship
// when one already exists, it will result in an error" — npm trust docs), and a
// configuration names a workflow FILE. So everything that publishes these
// packages lives in release.yml: the release itself, and the republish-npm job
// that recovers a release whose npm step alone failed. That job used to be a
// workflow of its own, which would have left it needing a stored token forever —
// folding it in is what makes NPM_TOKEN removable rather than merely reduced.
//
// RUN IT AGAIN WHENEVER A TOOL IS ADDED — a new tool means seven new packages,
// none of which can publish via OIDC until registered. It is one more
// distribution surface to forget, alongside those scripts/tooldist_test.go
// guards, so the check below refuses to run against a stale npm/dist.
//
// IT WILL ASK FOR A ONE-TIME PASSWORD. npm requires two-factor authentication
// for every trust operation, and a code opens a ~5-minute window that covers
// the calls after it — but only a code does that. Authorizing in the browser
// authorizes one request, and since each `npm trust` is its own process,
// nothing carries over and you are sent back to the browser 41 times.
//
// A code lasts 30 seconds and the window 5 minutes, while 41 packages take
// about 82 — so when the window closes mid-run the script asks for another and
// carries on from where it stopped.
//
// Requires `npm login` first: this writes to the registry as the package owner.
// A package that does not exist yet cannot be registered — publish it once with
// a token, then run this.
import { spawnSync } from 'node:child_process'
import readline from 'node:readline/promises'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = path.resolve(HERE, '..', '..')
const OUT = path.join(REPO_ROOT, 'npm', 'dist')
const REPOSITORY = 'rigsmith/rigsmith'
const WORKFLOW = 'release.yml'
const SCOPE = '@rigsmith'

// `npm trust` is newer than trusted publishing itself: publishing via OIDC
// needs 11.5.1, but the command that CONFIGURES it needs 11.15.0. Checking
// `npm whoami` alone would authenticate fine and then fail once per package.
const MIN_NPM = [11, 15, 0]

// npm's own guidance: "We recommend adding a 2-second sleep between each call
// to avoid rate limiting. With this approach, you can configure approximately
// 80 packages within the 5-minute two-factor authentication skip window."
const CALL_SPACING_MS = 2000

// A captured npm call must not wait forever. Without a usable one-time password
// npm falls back to browser authorization: it prints a URL and polls, and with
// its output captured there is no URL to see and no way to answer — so the run
// looks hung rather than failed.
const NPM_CALL_TIMEOUT_MS = 45_000

const DRY_RUN = process.argv.includes('--dry-run')
// `--otp 123456` skips the browser round trip for the first call. A code lasts
// 30 seconds and the 2FA skip window that follows lasts five minutes, so one
// code covers the whole run.
const OTP = (() => {
  const i = process.argv.indexOf('--otp')
  return i >= 0 ? process.argv[i + 1] : ''
})()
const LIST = process.argv.includes('--list')
// --replace moves a package whose single trusted-publisher configuration names
// another workflow in THIS repository (the release workflow was goreleaser.yml
// until it and release.yml became one file): revoke that one, register this.
// A configuration for any other repository is never touched.
const REPLACE = process.argv.includes('--replace')

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
    // The name decides which package on the registry gets a trusted publisher
    // pointed at this repository, so it is not taken on trust from a file in a
    // build directory. build-packages.mjs names each directory after the
    // package it holds; anything else is a stale or tampered manifest, and
    // registering it would hand publish rights over an unrelated coordinate.
    const expected = dir === 'rigsmith' ? 'rigsmith' : `${SCOPE}/${dir}`
    if (parsed.name !== expected) {
      fail(`${path.relative(REPO_ROOT, manifest)}: names "${parsed.name}" but sits in ${dir}/, ` +
        `where ${expected} belongs — refusing to register a coordinate the build did not produce`)
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

// registerArgs builds one registration command.
//
// The one-time password is NOT here. `npm trust github` parses positionals
// strictly and reads `--otp 123456` as a stray argument — "Unknown positional
// argument: 123456" — so the code never reaches npm and every package fails
// with EOTP as though none had been given. It goes through the environment
// instead, which is how npm takes any config and involves no argv parsing.
function registerArgs(name) {
  return ['trust', 'github', name,
    '--file', WORKFLOW,
    '--repo', REPOSITORY,
    '--allow-publish',
    '--yes']
}

// otpEnv passes a one-time password the way npm reads config from the
// environment: npm_config_<key>.
function otpEnv(otp) {
  return otp ? { ...process.env, npm_config_otp: otp } : process.env
}

// register makes one attempt.
//
// With a code, stderr is captured so an expired one can be told apart from a
// real failure — nothing needs to be read off the terminal, because there is no
// browser step. Without a code, everything is inherited: npm prints a URL and
// waits, and capturing that is what made every package fail with EOTP before.
function register(name, otp) {
  const args = registerArgs(name)
  if (!otp) {
    const r = spawnSync('npm', args, { stdio: 'inherit' })
    return { ok: r.status === 0, otpExpired: false, detail: '' }
  }
  const r = spawnSync('npm', args, { encoding: 'utf8', env: otpEnv(otp), timeout: NPM_CALL_TIMEOUT_MS })
  if (r.status === 0) return { ok: true, otpExpired: false, detail: '' }
  const out = `${r.stdout || ''}${r.stderr || ''}`
  const otpExpired = /EOTP|one-time password|invalid otp|otp required/i.test(out)
  // The LAST "npm error" line is "A complete log of this run can be found in
  // …", which says nothing. Take the first that carries a reason.
  const detail = out.trim().split('\n')
    .map((l) => l.replace(/^npm error\s*/, '').trim())
    .filter((l) => l && !/^A complete log|^code E|^$/.test(l))
    .find((l) => /[a-z]/i.test(l)) || ''
  return { ok: false, otpExpired, detail }
}

// promptOTP reads a code from the terminal. Refuses when there is nobody there:
// a non-interactive run should pass --otp rather than hang.
async function promptOTP(message) {
  if (!process.stdin.isTTY) return null
  const rl = readline.createInterface({ input: process.stdin, output: process.stdout })
  try {
    const answer = await rl.question(message)
    return answer.trim()
  } finally {
    rl.close()
  }
}

// failureKind decides what a failed registration means by reading the registry,
// since the command's own output goes to the terminal rather than to us:
// "ours" (already registered for this workflow — success), "other" (the single
// configuration a package may have is held by something else), or "error".
function failureKind(name, otp) {
  const held = registeredWorkflows(name, otp)
  if (held === null) return 'error'
  if (held.some((w) => w.endsWith(WORKFLOW))) return 'ours'
  if (held.length > 0) return 'other'
  return 'error'
}

// registeredWorkflows returns the workflow filenames the registry holds for a
// package, so an existing configuration is judged rather than assumed.
//
// The shape is what `npm trust list <pkg> --json` actually returns, which is a
// single object rather than a list — one configuration per package is the rule,
// so there is nothing to wrap:
//
//   { "id": "…", "type": "github", "file": "release.yml",
//     "repository": "rigsmith/rigsmith", "permissions": ["createPackage", …] }
//
// Guessing an array or a `trustedPublishers` wrapper is how a run that had
// registered all 41 reported none of them.
function registeredWorkflows(name, otp) {
  const configs = registeredConfigs(name, otp)
  return configs === null ? null : configs.map((c) => c.file)
}

// registeredConfigs is registeredWorkflows with each configuration's id and
// repository, which --replace needs to revoke the right one.
function registeredConfigs(name, otp) {
  const r = spawnSync('npm', ['trust', 'list', name, '--json'],
    { encoding: 'utf8', env: otpEnv(otp), timeout: NPM_CALL_TIMEOUT_MS })
  const body = (r.stdout || '').trim()
  // A package with no configuration prints nothing rather than an empty list,
  // and that is an answer — not a failure to read one.
  if (r.status === 0 && body === '') return []
  if (r.status !== 0 && body === '') return null
  let parsed
  try {
    parsed = JSON.parse(body)
  } catch {
    return null
  }
  const rows = Array.isArray(parsed) ? parsed : [parsed]
  const configs = rows
    .filter((row) => row && typeof row === 'object')
    .map((row) => ({
      id: row.id ?? '',
      file: row.file ?? row.workflow ?? row.workflowFilename ?? '',
      repository: row.repository ?? row.repo ?? '',
    }))
    .filter((c) => c.file)
  // Parsed, but nothing that names a workflow: better to say it could not be
  // read than to report a registered package as unregistered.
  if (configs.length === 0 && rows.some((row) => row && Object.keys(row).length > 0)) return null
  return configs
}

// replace revokes a package's configuration for another workflow in this
// repository and registers this one. Returns the workflow it replaced, or null
// when there was nothing it may replace (another repository's, or unreadable).
function replace(name, otp) {
  const configs = registeredConfigs(name, otp)
  if (configs === null) return null
  const ours = configs.filter((c) => c.repository === REPOSITORY && c.file !== WORKFLOW && c.id)
  if (ours.length === 0 || ours.length !== configs.length) return null
  for (const c of ours) {
    sleep(CALL_SPACING_MS)
    const r = spawnSync('npm', ['trust', 'revoke', name, '--id', c.id],
      { encoding: 'utf8', env: otpEnv(otp), timeout: NPM_CALL_TIMEOUT_MS })
    if (r.status !== 0) return null
  }
  sleep(CALL_SPACING_MS)
  return register(name, otp).ok ? ours.map((c) => c.file).join(', ') : null
}

const npmVersion = requireNpm()

if (LIST) {
  // A verdict per package, not raw output: the question this answers is "are all
  // of them registered for this workflow", and 41 blocks of npm output does not
  // answer it. Reading the list is itself a 2FA operation, so it wants a code
  // the same way registering does.
  if (!OTP && !process.stdin.isTTY) {
    fail('reading trust configurations needs a one-time password too — pass --otp <code>')
  }
  const listOtp = OTP || await promptOTP('npm one-time password (reading the list needs one too): ')
  const ours = []
  const other = []
  const none = []
  const unreadable = []
  const all = packageNames()
  for (const [i, name] of all.entries()) {
    process.stdout.write(`\r  reading ${i + 1}/${all.length}  ${name.padEnd(34).slice(0, 34)}`)
    const held = registeredWorkflows(name, listOtp)
    if (held === null) unreadable.push(name)
    else if (held.some((w) => w.endsWith(WORKFLOW))) ours.push(name)
    else if (held.length > 0) other.push(`${name} (${held.join(', ')})`)
    else none.push(name)
  }
  process.stdout.write('\r'.padEnd(60) + '\r')
  const total = ours.length + other.length + none.length + unreadable.length
  console.log(`${ours.length}/${total} registered for ${WORKFLOW}`)
  for (const [label, list] of [
    ['NOT registered', none],
    ['registered for a DIFFERENT workflow', other],
    ['could not be read', unreadable],
  ]) {
    if (list.length) console.log(`\n${list.length} ${label}:\n  ${list.join('\n  ')}`)
  }
  process.exit(none.length + other.length + unreadable.length > 0 ? 1 : 0)
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

// One code, then a window. npm opens a ~5-minute two-factor skip window against
// the credential that presented a one-time password — so a code has to be PASSED,
// not authorized in the browser. The browser flow authorizes one request: each
// `npm trust` is its own process, nothing carries between them, and you are sent
// back to the browser for all 41.
//
// So: ask for a code, use it, and when the window closes — a code lasts 30
// seconds, the window 5 minutes, and 41 packages take about 82 — ask again and
// carry on where it stopped.
let otp = OTP
if (!DRY_RUN && !otp && !process.stdin.isTTY) {
  fail('a one-time password is required — pass --otp <code> when there is no terminal to ask at')
}
if (!DRY_RUN && !otp) {
  otp = await promptOTP(
    'npm requires a one-time password for each trust operation, and a code opens a\n' +
    '~5-minute window that covers the rest of the run. Leave this empty to authorize\n' +
    'each package in the browser instead (41 round trips).\n\n' +
    'npm one-time password: ')
}

for (const [i, name] of names.entries()) {
  if (DRY_RUN) {
    console.log(`would: npm ${registerArgs(name).join(' ')}${otp ? '   (npm_config_otp set)' : ''}` +
      (REPLACE ? '   (revoking another workflow of this repository first, if it holds the package)' : ''))
    continue
  }
  if (i > 0) sleep(CALL_SPACING_MS)

  let result = register(name, otp)
  if (result.otpExpired) {
    // Mid-run, and expected: the window is shorter than a long run.
    const fresh = await promptOTP(`\nThe two-factor window closed at ${name}. New npm one-time password: `)
    if (fresh === null) {
      // No terminal to ask at. Stop where we are and say so — the run is
      // re-runnable, since a package already registered for this workflow
      // counts as done rather than as a conflict.
      console.error(`\nThe two-factor window closed after ${done}/${names.length} packages, ` +
        `at ${name}, and there is no terminal to ask for another code.\n` +
        `Re-run with a fresh code to carry on — what is already registered is kept:\n` +
        `  node scripts/npm/trust-publishers.mjs --otp <code>`)
      process.exit(1)
    }
    otp = fresh
    result = register(name, otp)
  }
  if (result.ok) {
    console.log(`✓ ${name}`)
    done++
    continue
  }
  const kind = failureKind(name, otp)
  if (kind === 'ours') {
    console.log(`· ${name} (already registered for ${WORKFLOW})`)
    done++
  } else if (kind === 'other' && REPLACE) {
    const was = replace(name, otp)
    if (was) {
      console.log(`↻ ${name} (was ${was})`)
      done++
    } else {
      mismatched.push(name)
    }
  } else if (kind === 'other') {
    mismatched.push(name)
  } else {
    console.error(`✗ ${name}${result.detail ? `: ${result.detail}` : ' — see npm\'s output above'}`)
    failures.push(name)
  }
}

if (!DRY_RUN) {
  console.log(`\n${done}/${names.length} registered for ${WORKFLOW}`)
  if (mismatched.length) {
    console.error(`\nAlready configured for a different publisher — the registry allows one per package,\n` +
      `so these need \`npm trust revoke --id <id> <package>\` before they can be re-registered` +
      (REPLACE ? ' (--replace moves only this repository\'s own)' : ', or --replace if the other is this repository\'s') + `:\n  ` +
      mismatched.join('\n  '))
  }
  if (failures.length) console.error(`\nfailed: ${failures.join(', ')}`)
  if (failures.length || mismatched.length) process.exit(1)
  console.log('Verify with: node scripts/npm/trust-publishers.mjs --list')
}
