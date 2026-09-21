#!/usr/bin/env node
// Register this repo's release workflows as npm trusted publishers, for every
// wrapper package a release publishes.
//
//   node scripts/npm/trust-publishers.mjs --dry-run   # print what it would do
//   node scripts/npm/trust-publishers.mjs             # register
//   node scripts/npm/trust-publishers.mjs --list      # what is registered now
//
// Trusted publishing lets a workflow mint a short-lived credential from its OIDC
// identity instead of carrying a long-lived NPM_TOKEN — the token that expired
// mid-month and left 1.19.0's npm packages unpublished, and whose failure then
// skipped five winget submissions too.
//
// Registration is per package, and this release publishes 41 of them. That is
// why this is a script: `npm trust` (npm 11.5.1+) is what makes it a loop rather
// than 41 visits to npmjs.com. RUN IT AGAIN WHENEVER A TOOL IS ADDED — a new
// tool means seven new packages, and an unregistered package cannot publish via
// OIDC at all. It is one more distribution surface to forget, alongside the ones
// scripts/tooldist_test.go guards.
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

// Both workflows publish to npm, so both need registering: goreleaser.yml is the
// release, npm-republish.yml is the recovery path for when a release publishes
// everything else and npm alone fails. A package takes up to 10 connections, so
// holding both costs nothing.
const WORKFLOWS = ['goreleaser.yml', 'npm-republish.yml']

const DRY_RUN = process.argv.includes('--dry-run')
const LIST = process.argv.includes('--list')

// The package names come from the build output rather than a list kept here:
// build-packages.mjs decides what exists (including which platforms a tool
// supports), and a second copy of that would drift the moment one changed.
function packageNames() {
  if (!fs.existsSync(OUT)) {
    fail(`no ${path.relative(REPO_ROOT, OUT)}/ — run \`node scripts/npm/build-packages.mjs\` first ` +
      `(it needs a GoReleaser dist/, e.g. from \`goreleaser release --snapshot --clean --skip=publish\`)`)
  }
  const names = fs.readdirSync(OUT)
    .map((d) => path.join(OUT, d, 'package.json'))
    .filter((p) => fs.existsSync(p))
    .map((p) => JSON.parse(fs.readFileSync(p, 'utf8')).name)
    .filter(Boolean)
    .sort()
  if (names.length === 0) fail(`${path.relative(REPO_ROOT, OUT)}/ holds no packages`)
  return names
}

function fail(msg) {
  console.error(`trust-publishers: ${msg}`)
  process.exit(1)
}

function npmLogin() {
  const r = spawnSync('npm', ['whoami'], { encoding: 'utf8' })
  if (r.status !== 0) {
    fail('not logged in to npm — run `npm login` first (this writes to the registry as the package owner)')
  }
  return r.stdout.trim()
}

if (LIST) {
  for (const name of packageNames()) {
    const r = spawnSync('npm', ['trust', 'list', name], { encoding: 'utf8' })
    const body = (r.stdout || r.stderr || '').trim().replace(/\n/g, '\n    ')
    console.log(`${name}\n    ${body || '(none)'}`)
  }
  process.exit(0)
}

const names = packageNames()
const who = DRY_RUN ? '(dry run)' : npmLogin()
console.log(`Registering ${names.length} package(s) × ${WORKFLOWS.length} workflow(s) on ${REPOSITORY} as ${who}\n`)

let done = 0
const failures = []
for (const name of names) {
  for (const workflow of WORKFLOWS) {
    const args = ['trust', 'github', name,
      '--file', workflow,
      '--repo', REPOSITORY,
      '--allow-publish',
      '--yes']
    if (DRY_RUN) {
      console.log(`would: npm ${args.join(' ')}`)
      continue
    }
    try {
      execFileSync('npm', args, { stdio: 'pipe' })
      console.log(`✓ ${name} ← ${workflow}`)
      done++
    } catch (err) {
      // Already registered is success, not failure: this script is meant to be
      // re-run whenever a tool is added, so it has to be idempotent in practice.
      const out = `${err.stdout || ''}${err.stderr || ''}`
      if (/already exists|already configured|duplicate/i.test(out)) {
        console.log(`· ${name} ← ${workflow} (already registered)`)
        done++
        continue
      }
      console.error(`✗ ${name} ← ${workflow}: ${out.trim().split('\n').slice(-2).join(' ')}`)
      failures.push(`${name} (${workflow})`)
    }
  }
}

if (!DRY_RUN) {
  console.log(`\n${done} registration(s) in place, ${failures.length} failed`)
  if (failures.length) {
    console.error(`failed: ${failures.join(', ')}`)
    process.exit(1)
  }
  console.log('Verify with: node scripts/npm/trust-publishers.mjs --list')
}
