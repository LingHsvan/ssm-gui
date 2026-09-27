#!/usr/bin/env node
// Generates ../src/hanzi-fold.js — the compact glyph-fold table used by the
// song search (see the generated file header for sources and purpose).
//
// Usage:
//   node scripts/gen-hanzi-fold.mjs                # download from OpenCC
//   node scripts/gen-hanzi-fold.mjs st.txt jp.txt  # use local copies
//
// Each dictionary row `key<TAB>v1 v2 ...` declares key and all values to be
// one equivalence class whose *representative* is chosen once (first row that
// creates the class wins). At the end every class member maps to that single
// representative, so the runtime fold stays one flat map lookup — no chains,
// no cycles (measurement showed a naive per-key union produces 56 two-cycles
// like 内<->內 / 没<->沒 / 猫<->貓, which would defeat the folding).

import { readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const ST_URL = 'https://raw.githubusercontent.com/BYVoid/OpenCC/master/data/dictionary/STCharacters.txt';
const JP_URL = 'https://raw.githubusercontent.com/BYVoid/OpenCC/master/data/dictionary/JPShinjitaiCharacters.txt';

// Keep in sync with songmatch.FoldConfusables (songmatch/songmatch.go,
// var confusableFold) — katakana glyphs OCR often misreads as kanji.
const CONFUSABLE = [
  ['ハ', '八'], ['ニ', '二'], ['ー', '一'], ['ロ', '口'], ['カ', '力'], ['タ', '夕'],
  ['ト', '卜'], ['エ', '工'], ['オ', '才'], ['ミ', '三'], ['チ', '千'], ['ク', '夕'],
];

async function loadSource(url, localPath, fallbackPath) {
  if (localPath) {
    console.log(`  using local file ${localPath}`);
    return readFileSync(localPath, 'utf8');
  }
  try {
    const res = await fetch(url, { signal: AbortSignal.timeout(30000) });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    console.log(`  downloaded ${url}`);
    return await res.text();
  } catch (e) {
    console.warn(`  download failed (${e.message}); trying local sample ${fallbackPath}`);
    return readFileSync(fallbackPath, 'utf8');
  }
}

// parseRows returns [key, values] pairs for every non-comment dictionary line.
function parseRows(text) {
  const rows = [];
  for (const line of text.split(/\r?\n/)) {
    if (!line || line.startsWith('#')) continue;
    const tab = line.indexOf('\t');
    if (tab < 0) continue;
    const key = line.slice(0, tab);
    const values = line.slice(tab + 1).split(' ').filter(Boolean);
    if (key && values.length > 0) rows.push([key, values]);
  }
  return rows;
}

// ── equivalence classes ────────────────────────────────────────────────
const parent = new Map();   // node -> parent (path-compressed)
const classRep = new Map(); // root -> canonical glyph
const classSeq = new Map(); // root -> creation order (first-wins on merge)
let seq = 0;

function find(x) {
  while (parent.get(x) !== x) {
    parent.set(x, parent.get(parent.get(x)));
    x = parent.get(x);
  }
  return x;
}

// addClass declares `members` (key + all values) as one class, preferring v1
// as the representative when the class is new.
function addClass(members, v1) {
  for (const m of members) if (!parent.has(m)) parent.set(m, m);
  const roots = [...new Set(members.map(find))];
  let keep = null;
  for (const r of roots) {
    if (classRep.has(r) && (keep === null || classSeq.get(r) < classSeq.get(keep))) keep = r;
  }
  if (keep === null) {
    keep = find(v1);
    classRep.set(keep, v1);
    classSeq.set(keep, seq++);
  }
  for (const r of roots) if (r !== keep) parent.set(r, keep);
}

async function main() {
  const [stArg, jpArg] = process.argv.slice(2);
  console.log('loading dictionaries:');
  const stText = await loadSource(ST_URL, stArg, join(tmpdir(), 'st_chars.txt'));
  const jpText = await loadSource(JP_URL, jpArg, join(tmpdir(), 'jp_chars.txt'));
  const stRows = parseRows(stText);
  const jpRows = parseRows(jpText);
  console.log(`  ST rows: ${stRows.length}, JP rows: ${jpRows.length}`);

  // Diagnostics only: a naive first-wins per-key union (target = v1) to show
  // what the class merge below resolves away.
  const naive = new Map();
  const conflicts = [];
  const naiveAdd = (k, v) => {
    if (!k || !v || k === v) return;
    if (!naive.has(k)) naive.set(k, v);
    else if (naive.get(k) !== v) conflicts.push(`${k}: ${naive.get(k)} vs ${v}`);
  };
  for (const [k, vals] of stRows) { naiveAdd(k, vals[0]); for (const v of vals) naiveAdd(v, vals[0]); }
  for (const [k, vals] of jpRows) { naiveAdd(k, vals[0]); for (const v of vals) naiveAdd(v, vals[0]); }
  let naiveCycles = 0;
  for (const k of naive.keys()) {
    const seen = new Set();
    let cur = k;
    while (naive.has(cur) && naive.get(cur) !== cur) {
      if (seen.has(cur)) { naiveCycles++; break; }
      seen.add(cur);
      cur = naive.get(cur);
    }
  }
  console.log(`  per-key conflicts (first-wins list): ${conflicts.length}`);
  for (const c of conflicts.slice(0, 20)) console.log(`    ${c}`);
  console.log(`  two-cycles a naive per-key map would leave: ${naiveCycles}`);

  // Build the canonical classes: ST first, then JP, then the confusable pairs.
  for (const [k, vals] of stRows) addClass([k, ...vals], vals[0]);
  for (const [k, vals] of jpRows) addClass([k, ...vals], vals[0]);
  for (const [k, v] of CONFUSABLE) addClass([k, v], v);

  // Sanity: the confusable targets must not be folded onto something else.
  for (const [, v] of CONFUSABLE) {
    const rep = classRep.get(find(v));
    if (rep !== undefined && rep !== v) console.warn(`  WARNING: confusable target ${v} folds to ${rep}`);
  }

  const fold = new Map();
  for (const node of parent.keys()) {
    const rep = classRep.get(find(node));
    if (node !== rep) fold.set(node, rep);
  }
  console.log(`fold entries: ${fold.size} (classes: ${seq}, identity removed: ${parent.size - fold.size})`);

  const pairs = [...fold.entries()]
    .sort((a, b) => a[0].codePointAt(0) - b[0].codePointAt(0))
    .map(([k, v]) => k + v)
    .join('');

  const header = [
    '// Generated by scripts/gen-hanzi-fold.mjs — DO NOT EDIT MANUALLY.',
    '// Regenerate: node scripts/gen-hanzi-fold.mjs [st.txt jp.txt]',
    '//',
    '// Hanzi glyph folding for song search: folds simplified Chinese and',
    '// Japanese shinjitai onto one canonical glyph per character class, and',
    '// folds OCR-confusable katakana onto their kanji lookalikes, so that',
    '// e.g. 「极乐」 finds 「極楽」 and 「口三才」 finds 「ロミオ」.',
    '//',
    '// Sources (Apache License 2.0, https://github.com/BYVoid/OpenCC):',
    '//   - STCharacters.txt          (simplified -> traditional)',
    '//   - JPShinjitaiCharacters.txt (shinjitai -> traditional)',
    '//   - Katakana lookalike pairs, kept in sync with songmatch.FoldConfusables',
    '//     (see songmatch/songmatch.go).',
    '//',
    '// FOLD_PAIRS: every two adjacent code points are one mapping key -> value.',
    '',
    `const FOLD_PAIRS = ${JSON.stringify(pairs)};`,
    '',
    'const FOLD_MAP = (function () {',
    '  const map = new Map();',
    '  const chars = Array.from(FOLD_PAIRS);',
    '  for (let i = 0; i + 1 < chars.length; i += 2) map.set(chars[i], chars[i + 1]);',
    '  return map;',
    '})();',
    '',
    '// foldHanzi maps each code point of `s` through the table; characters not',
    '// in the table are kept as-is.',
    'export function foldHanzi(s) {',
    "  let out = '';",
    "  for (const ch of String(s || '')) {",
    '    out += FOLD_MAP.get(ch) || ch;',
    '  }',
    '  return out;',
    '}',
    '',
  ].join('\n');

  const outPath = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'hanzi-fold.js');
  writeFileSync(outPath, header, 'utf8');
  console.log(`wrote ${outPath}`);

  // Self-checks against the freshly written module.
  const { foldHanzi } = await import(pathToFileURL(outPath).href);
  const checks = [
    ['极乐', '極楽'],
    ['口三才', 'ロミオ'],
    ['没', '沒'],   // class-merge regression guard (naive union cycles here)
    ['猫', '貓'],
    ['楽', '樂'],
  ];
  let failed = 0;
  for (const [a, b] of checks) {
    const fa = foldHanzi(a), fb = foldHanzi(b);
    console.log(`  check fold(${a})=${fa} vs fold(${b})=${fb} -> ${fa === fb ? 'OK' : 'FAIL'}`);
    if (fa !== fb) failed++;
  }
  if (failed > 0) {
    console.error(`self-check FAILED (${failed})`);
    process.exitCode = 1;
  }
}

await main();
