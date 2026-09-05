/**
 * Theme v2 — the contrast guard, the derivation map and the v1 fallback.
 *
 * The validator table here is the client half of a rule the backend enforces
 * too; if the two ever drift, a landlord gets a Save button that lies. Run with
 * `npm test -w packages/ui`.
 */

import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  CONTRAST_PAIRS,
  LEDGER_THEME,
  PRESETS,
  PRESET_IDS,
  applyOrgTheme,
  contrastRatio,
  deriveTokens,
  onPrimary,
  presetById,
  themeContrastReport,
  toResolvedTheme,
  validateTheme,
  type ThemeTokens,
} from './theme.ts';

const tokens = (over: Partial<ThemeTokens> = {}): ThemeTokens => ({
  ...LEDGER_THEME.tokens,
  ...over,
});

/* ------------------------------- contrast ------------------------------- */

test('contrastRatio matches the WCAG reference points', () => {
  assert.equal(Math.round(contrastRatio('#000000', '#ffffff')), 21);
  assert.equal(Math.round(contrastRatio('#ffffff', '#ffffff')), 1);
  // Order does not matter.
  assert.equal(contrastRatio('#777777', '#888888'), contrastRatio('#888888', '#777777'));
});

test('onPrimary follows the luminance > 0.4 rule', () => {
  assert.equal(onPrimary('#2b4fd0'), '#ffffff'); // dark brand → white label
  assert.equal(onPrimary('#eda100'), '#1c1917'); // bright brand → ink label
  assert.equal(onPrimary('#ffffff'), '#1c1917');
  assert.equal(onPrimary('#000000'), '#ffffff');
});

/* ------------------------------- validator ------------------------------ */

test('the Ledger default passes every pair', () => {
  assert.deepEqual(validateTheme(LEDGER_THEME.tokens), []);
});

test('all eight shipped presets pass AA', () => {
  assert.deepEqual(
    PRESETS.map((p) => p.id),
    [...PRESET_IDS],
  );
  for (const preset of PRESETS) {
    assert.deepEqual(validateTheme(preset.tokens), [], `${preset.id} must pass`);
  }
});

test('#777 ink on #888 paper fails ink/paper under 4.5', () => {
  const failures = validateTheme(tokens({ ink: '#777777', paper: '#888888' }));
  const inkPaper = failures.find((f) => f.pair === 'ink/paper');
  assert.ok(inkPaper, 'ink/paper must be reported');
  assert.equal(inkPaper.minimum, 4.5);
  assert.ok(inkPaper.ratio < 4.5, `ratio ${inkPaper.ratio} should be under 4.5`);
});

test('a mid-grey ink fails only the pairs it should', () => {
  const failures = validateTheme(tokens({ ink: '#888888' }));
  const pairs = failures.map((f) => f.pair).sort();
  assert.deepEqual(pairs, ['ink/paper', 'ink/surface']);
});

test('on_primary/primary fails for a mid-tone brand colour', () => {
  // Luminance sits just under 0.4 so the rule picks white, which then fails.
  const failures = validateTheme(tokens({ primary: '#8d9cc4' }));
  assert.ok(failures.some((f) => f.pair === 'on_primary/primary'));
});

test('primary/paper uses a 3.0 minimum, rule/paper 1.2', () => {
  const mins = Object.fromEntries(CONTRAST_PAIRS.map((p) => [p.pair, p.minimum]));
  assert.equal(mins['primary/paper'], 3);
  assert.equal(mins['rule/paper'], 1.2);
  assert.equal(mins['ink/paper'], 4.5);
  assert.equal(mins['ink_muted/surface'], 4.5);

  // A near-invisible rule is caught.
  assert.ok(validateTheme(tokens({ rule: '#fbfbf6' })).some((f) => f.pair === 'rule/paper'));
});

test('the report lists every pair, passing or not', () => {
  const report = themeContrastReport(LEDGER_THEME.tokens);
  assert.equal(report.length, CONTRAST_PAIRS.length);
  assert.ok(report.every((r) => r.ok));
  assert.ok(report.every((r) => typeof r.label === 'string' && r.label.length > 0));
});

/* ------------------------------ derivation ------------------------------ */

test('deriveTokens emits the whole variable map and nothing else', () => {
  const vars = deriveTokens(LEDGER_THEME.tokens, 'bricolage');
  assert.deepEqual(Object.keys(vars).sort(), [
    '--accent',
    '--accent-soft',
    '--font-sans',
    '--ink',
    '--ink-faint',
    '--ink-soft',
    '--on-primary',
    '--paper',
    '--primary',
    '--primary-soft',
    '--primary-strong',
    '--rule',
    '--rule-strong',
    '--sheet',
    '--sheet-tint',
  ]);
  assert.equal(vars['--paper'], '#fbfbf7');
  assert.equal(vars['--sheet'], '#ffffff');
  assert.equal(vars['--rule-strong'], LEDGER_THEME.tokens.ink);
  assert.equal(vars['--ink-soft'], LEDGER_THEME.tokens.ink_muted);
  assert.equal(vars['--on-primary'], '#ffffff');
  assert.equal(vars['--font-sans'], 'var(--font-bricolage), system-ui, -apple-system, sans-serif');
  // Stamp inks, focus ring, spacing, radii and the type scale are never themed.
  assert.ok(!Object.keys(vars).some((k) => /stamp|focus|--sp-|radius|--text-/.test(k)));
});

test('primary-soft mixes toward paper so dark presets stay dark', () => {
  const night = presetById('night_ledger');
  assert.ok(night);
  const vars = deriveTokens(night.tokens, night.font_id);
  // A tint toward white would land near #fff; toward paper it stays dark.
  assert.ok(
    contrastRatio(vars['--primary-soft'], night.tokens.paper) < 2,
    `soft ${vars['--primary-soft']} should sit close to paper`,
  );
  assert.equal(vars['--on-primary'], '#1c1917');
});

test('deriveTokens defaults to bricolage and every font id round-trips', () => {
  assert.match(deriveTokens(LEDGER_THEME.tokens)['--font-sans'], /--font-bricolage/);
  assert.match(deriveTokens(LEDGER_THEME.tokens, 'hanken')['--font-sans'], /--font-hanken/);
});

/* --------------------------- legacy + coercion -------------------------- */

test('a v1 theme maps onto the Ledger tokens', () => {
  const resolved = toResolvedTheme({ primaryColor: '#C2262E', font: 'archivo' });
  assert.equal(resolved.preset_id, null);
  assert.equal(resolved.font_id, 'archivo');
  assert.equal(resolved.dark, false);
  assert.equal(resolved.tokens.primary, '#c2262e'); // normalised to lower case
  assert.equal(resolved.tokens.accent, '#c2262e');
  assert.equal(resolved.tokens.paper, LEDGER_THEME.tokens.paper);
  assert.equal(resolved.tokens.ink, LEDGER_THEME.tokens.ink);
});

test('missing, junk and partial themes fall back to Ledger', () => {
  assert.deepEqual(toResolvedTheme(null).tokens, LEDGER_THEME.tokens);
  assert.deepEqual(toResolvedTheme(undefined), LEDGER_THEME);

  const partial = toResolvedTheme({
    preset_id: 'forest',
    tokens: { primary: 'not-a-colour', accent: '#2C6F52' } as never,
    font_id: 'comic' as never,
    dark: false,
  });
  assert.equal(partial.tokens.primary, LEDGER_THEME.tokens.primary);
  assert.equal(partial.tokens.accent, '#2c6f52');
  assert.equal(partial.font_id, 'bricolage');
});

test('applyOrgTheme is a no-op without a document', () => {
  assert.equal(typeof document, 'undefined');
  applyOrgTheme(LEDGER_THEME); // must not throw in Node
});
