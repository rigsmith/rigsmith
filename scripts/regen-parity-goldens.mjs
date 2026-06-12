#!/usr/bin/env node
// Regenerates (or verifies) the parity-corpus goldens from the Node @changesets
// oracle. The goldens are the oracle — run with --write only after an
// intentional, upstream-validated change, or to capture a NEW scenario.
//
//   node scripts/regen-parity-goldens.mjs [--write] [scenario-id ...]
//
// Without --write it materializes each scenario, runs `changeset version`, and
// diffs the produced CHANGELOGs against golden/ (exit 1 on drift). It always
// prints the resulting versions and in-repo dependency ranges so a new
// scenario's expectedVersions/expectedRanges can be filled from observation.
//
// The oracle binary is resolved from $CHANGESETS_BIN, defaulting to the
// net-changesets demo install (v3.0.0-next.5):
//   ~/Git/net-changesets/demo/node-sample/node_modules/@changesets/cli/bin.js

import { execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, cpSync, existsSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const corpusDir = join(repoRoot, 'core', 'testdata', 'parity');
const bin = process.env.CHANGESETS_BIN
  ?? join(process.env.HOME, 'Git/net-changesets/demo/node-sample/node_modules/@changesets/cli/bin.js');

const args = process.argv.slice(2);
const write = args.includes('--write');
const only = args.filter(a => a !== '--write');

const { scenarios } = JSON.parse(readFileSync(join(corpusDir, 'scenarios.json'), 'utf8'));
let drift = 0;

for (const sc of scenarios) {
  if (only.length && !only.includes(sc.id)) continue;

  const ws = mkdtempSync(join(tmpdir(), `parity-${sc.id}-`));
  try {
    materialize(ws, sc);
    execFileSync('node', [bin, 'version'], { cwd: ws, stdio: 'pipe' });

    const versions = {}, ranges = {};
    for (const p of sc.packages) {
      const pj = JSON.parse(readFileSync(join(ws, 'packages', p.name, 'package.json'), 'utf8'));
      versions[p.name] = pj.version;
      if (pj.dependencies) ranges[p.name] = pj.dependencies;
    }
    console.log(`=== ${sc.id}`);
    console.log(`  versions: ${JSON.stringify(versions)}`);
    if (Object.keys(ranges).length) console.log(`  ranges:   ${JSON.stringify(ranges)}`);

    for (const p of sc.packages) {
      const actual = join(ws, 'packages', p.name, 'CHANGELOG.md');
      const golden = join(corpusDir, 'golden', sc.id, p.name, 'CHANGELOG.md');
      const hasActual = existsSync(actual);
      if (write) {
        if (hasActual) {
          mkdirSync(dirname(golden), { recursive: true });
          cpSync(actual, golden);
          console.log(`  wrote golden/${sc.id}/${p.name}/CHANGELOG.md`);
        }
        continue;
      }
      const hasGolden = existsSync(golden);
      if (hasActual !== hasGolden) {
        console.log(`  DRIFT(${p.name}): golden ${hasGolden ? 'exists' : 'absent'}, node output ${hasActual ? 'exists' : 'absent'}`);
        drift++;
      } else if (hasActual && normalize(readFileSync(actual, 'utf8')) !== normalize(readFileSync(golden, 'utf8'))) {
        console.log(`  DRIFT(${p.name}): CHANGELOG differs from golden`);
        drift++;
      }
    }
  } finally {
    rmSync(ws, { recursive: true, force: true });
  }
}

if (drift) {
  console.error(`\n${drift} golden(s) drifted from the Node oracle`);
  process.exit(1);
}

// Mirrors changerig/parity harness writeNodeRepo, plus the Node-only changelog
// config the goldens were frozen with (default generator, format off).
function materialize(root, sc) {
  writeFileSync(join(root, 'package.json'), '{ "name": "root", "private": true, "workspaces": ["packages/*"] }');
  writeFileSync(join(root, 'package-lock.json'), '{}');
  mkdirSync(join(root, '.changeset'), { recursive: true });

  const versionOf = Object.fromEntries(sc.packages.map(p => [p.name, p.version]));
  for (const p of sc.packages) {
    const dir = join(root, 'packages', p.name);
    mkdirSync(dir, { recursive: true });
    const pj = { name: p.name, version: p.version };
    if (p.dependencies?.length) {
      pj.dependencies = Object.fromEntries(p.dependencies.map(d =>
        typeof d === 'string' ? [d, versionOf[d]] : [d.name, d.range || versionOf[d.name]]));
    }
    writeFileSync(join(dir, 'package.json'), JSON.stringify(pj, null, 2));
  }

  const cfg = {
    changelog: '@changesets/cli/changelog',
    format: false,
    updateInternalDependencies: sc.updateInternalDependencies,
  };
  if (sc.fixed) cfg.fixed = sc.fixed;
  if (sc.linked) cfg.linked = sc.linked;
  if (sc.ignore) cfg.ignore = sc.ignore;
  writeFileSync(join(root, '.changeset', 'config.json'), JSON.stringify(cfg, null, 2));

  for (const cs of sc.changesets) {
    const fm = cs.releases.map(r => `"${r.package}": ${r.bump}`).join('\n');
    writeFileSync(join(root, '.changeset', cs.file), `---\n${fm}\n---\n\n${cs.summary}`);
  }
}

function normalize(s) {
  return s.replace(/\r\n/g, '\n').split('\n').map(l => l.replace(/[ \t]+$/, '')).join('\n').replace(/\n+$/, '');
}
