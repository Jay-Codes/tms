#!/usr/bin/env node
// Fails when the two dictionaries of an app do not share the same key set,
// or when a value is empty. Usage: node i18n-check.mjs <dir with en.ts sw.ts>
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const dir = process.argv[2];
if (!dir) { console.error('usage: i18n-check.mjs <dir>'); process.exit(2); }

function keysOf(file) {
  const src = readFileSync(join(dir, file), 'utf8');
  const keys = new Map();
  const re = /^\s*['"]([^'"]+)['"]\s*:\s*(['"`])((?:\\.|(?!\2)[\s\S])*)\2\s*,?\s*$/gm;
  let m;
  while ((m = re.exec(src))) keys.set(m[1], m[3]);
  return keys;
}

const en = keysOf('en.ts');
const sw = keysOf('sw.ts');
let bad = 0;
for (const k of en.keys()) if (!sw.has(k)) { console.error(`sw.ts missing: ${k}`); bad++; }
for (const k of sw.keys()) if (!en.has(k)) { console.error(`en.ts missing: ${k}`); bad++; }
for (const [k, v] of en) if (!v.trim()) { console.error(`en.ts empty: ${k}`); bad++; }
for (const [k, v] of sw) if (!v.trim()) { console.error(`sw.ts empty: ${k}`); bad++; }
const placeholders = (s) => (s.match(/\{\w+\}/g) ?? []).sort().join(',');
for (const k of en.keys()) if (sw.has(k) && placeholders(en.get(k)) !== placeholders(sw.get(k))) { console.error(`placeholder mismatch: ${k}`); bad++; }
if (bad) { console.error(`${bad} i18n problem(s) in ${dir}`); process.exit(1); }
console.log(`i18n ok: ${en.size} keys in ${dir}`);
