'use client';

import { Icon } from '@iconify/react';
import { ThemeSwitcher } from '@tms/ui';
import type { ReactNode } from 'react';

/* ------------------------------------------------------------------ */
/* Design-system preview, renter side (mobile).                        */
/* Every element uses @tms/ui classes and tokens only; this page is    */
/* the compliance check as well as the reference.                      */
/* ------------------------------------------------------------------ */

function Section({ title, lead, children }: { title: string; lead: string; children: ReactNode }) {
  return (
    <section style={{ paddingTop: 'var(--sp-6)' }}>
      <hr className="rule rule-strong" />
      <h2 style={{ fontSize: 'var(--text-lg)', marginTop: 'var(--sp-3)' }}>{title}</h2>
      <p style={{ color: 'var(--ink-soft)', margin: 'var(--sp-1) 0 var(--sp-4)' }}>{lead}</p>
      {children}
    </section>
  );
}

function Money({ value, currency }: { value: string; currency?: boolean }) {
  return (
    <span className="amount">
      {currency && <span className="currency">TZS</span>}
      {value}
    </span>
  );
}

function Status({ s }: { s: 'paid' | 'overdue' | { due: string } }) {
  if (s === 'paid') return <span className="stamp stamp-paid">Paid</span>;
  if (s === 'overdue') return <span className="stamp stamp-overdue">Overdue</span>;
  return <span className="pencil">due {s.due}</span>;
}

const PERIODS: Array<[string, 'paid' | 'overdue' | { due: string }, string]> = [
  ['July', 'paid', '450,000'],
  ['August', 'paid', '450,000'],
  ['September', 'overdue', '450,000'],
  ['October', { due: '5 Oct' }, '450,000'],
];

const ICONS: Array<[string, string]> = [
  ['Home', 'solar:home-2-linear'],
  ['Rent', 'solar:wallet-money-linear'],
  ['Scan', 'solar:qr-code-linear'],
  ['Agreement', 'solar:document-text-linear'],
  ['Calendar', 'solar:calendar-linear'],
  ['Messages', 'solar:chat-round-dots-linear'],
  ['Profile', 'solar:user-circle-linear'],
  ['Property', 'solar:buildings-2-linear'],
  ['Key', 'solar:key-linear'],
  ['Settings', 'solar:settings-linear'],
  ['History', 'solar:history-linear'],
  ['Receipt', 'solar:bill-check-linear'],
];

export default function DesignSystem() {
  return (
    <main style={{ maxWidth: 440, margin: '0 auto', padding: 'var(--sp-6) var(--sp-4) var(--sp-8)' }}>
      <header>
        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>TMS design system, renter app</p>
        <h1 style={{ fontSize: 'var(--text-2xl)', marginTop: 'var(--sp-2)' }}>
          A rent ledger you can read on a phone.
        </h1>
        <p style={{ marginTop: 'var(--sp-3)' }}>
          Paper, ink, ruled rows. Money sits on the right in tabular figures. When rent is paid or
          overdue, it gets stamped. Until then it is only pencilled in.
        </p>
      </header>

      <Section
        title="A renter's ledger"
        lead="The home screen. One unit, this year's periods, the next thing to do."
      >
        <div className="sheet" style={{ padding: 'var(--sp-4) var(--sp-4) 0', overflow: 'hidden' }}>
          <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>Mbezi Beach, Block A</p>
          <h3 style={{ fontSize: 'var(--text-xl)' }}>Room 4</h3>

          <table className="ledger" style={{ marginTop: 'var(--sp-4)' }}>
            <tbody>
              {PERIODS.map(([month, s, amt]) => (
                <tr key={month}>
                  <td style={{ fontWeight: 500 }}>{month}</td>
                  <td>
                    <Status s={s} />
                  </td>
                  <td className="num">{amt}</td>
                </tr>
              ))}
              <tr className="total">
                <td colSpan={2}>Owed now</td>
                <td className="num">
                  <Money value="450,000" currency />
                </td>
              </tr>
            </tbody>
          </table>

          <div style={{ display: 'grid', gap: 'var(--sp-2)', padding: 'var(--sp-4) 0' }}>
            <button className="btn btn-primary">
              <Icon icon="solar:wallet-money-linear" width={20} /> How to pay 450,000
            </button>
            <button className="btn btn-quiet">See the full agreement</button>
          </div>

          <nav className="bottom-bar" style={{ margin: '0 calc(-1 * var(--sp-4))' }} aria-label="Preview">
            {[
              ['Home', 'solar:home-2-bold', true],
              ['Rent', 'solar:wallet-money-linear', false],
              ['Agreement', 'solar:document-text-linear', false],
              ['Profile', 'solar:user-circle-linear', false],
            ].map(([label, icon, current]) => (
              <a key={label as string} className="tab" aria-current={current ? 'page' : undefined}>
                <Icon icon={icon as string} width={22} />
                {label as string}
              </a>
            ))}
          </nav>
        </div>
      </Section>

      <Section
        title="What a landlord can change"
        lead="Two things: their colour and their typeface. Everything else is the same for every landlord, so a bad colour can't break a screen."
      >
        <ThemeSwitcher />
      </Section>

      <Section
        title="Stamps"
        lead="The loudest thing in the system, so it is used for exactly one job: recording what has happened."
      >
        <table className="ledger">
          <tbody>
            <tr>
              <td>Money received and recorded</td>
              <td className="num">
                <span className="stamp stamp-paid">Paid</span>
              </td>
            </tr>
            <tr>
              <td>Due date passed, nothing recorded</td>
              <td className="num">
                <span className="stamp stamp-overdue">Overdue</span>
              </td>
            </tr>
            <tr>
              <td>Inside the payment window</td>
              <td className="num">
                <span className="pencil">due 5 Oct</span>
              </td>
            </tr>
          </tbody>
        </table>
      </Section>

      <Section title="Money" lead="Tabular figures, thousands separators, currency set small. Totals get the accountant's double rule.">
        <div style={{ fontSize: 'var(--text-display)', lineHeight: 1 }}>
          <Money value="1,350,000" currency />
        </div>
        <table className="ledger" style={{ marginTop: 'var(--sp-4)' }}>
          <tbody>
            <tr>
              <td>Expected</td>
              <td className="num">4,500,000</td>
            </tr>
            <tr>
              <td>Collected</td>
              <td className="num">3,150,000</td>
            </tr>
            <tr className="total">
              <td>Outstanding</td>
              <td className="num">1,350,000</td>
            </tr>
          </tbody>
        </table>
      </Section>

      <Section title="Paper and ink" lead="Text is ballpoint blue-black, not black. Rules are ledger blue. The brand colour appears only on the primary action and the active tab.">
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 'var(--sp-3)' }}>
          {[
            ['Paper', '--paper'],
            ['Sheet', '--sheet'],
            ['Rule', '--rule'],
            ['Ink', '--ink'],
            ['Ink, soft', '--ink-soft'],
            ['Brand', '--primary'],
            ['Paid', '--stamp-paid'],
            ['Overdue', '--stamp-overdue'],
            ['Pencil', '--pencil'],
          ].map(([name, token]) => (
            <div key={token}>
              <div
                style={{
                  height: 48,
                  borderRadius: 'var(--radius-sm)',
                  border: '1px solid var(--rule)',
                  background: `var(${token})`,
                }}
              />
              <div style={{ fontSize: 'var(--text-sm)', marginTop: 'var(--sp-1)' }}>{name}</div>
              <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>{token}</div>
            </div>
          ))}
        </div>
      </Section>

      <Section title="Type" lead="One family. Sixteen pixels is the floor so phones never zoom into a field. Headlines are set tight and balanced.">
        <div style={{ display: 'grid', gap: 'var(--sp-3)' }}>
          <div style={{ fontSize: 'var(--text-2xl)', fontWeight: 600, lineHeight: 1.15 }}>Rent for Room 4 is overdue</div>
          <div style={{ fontSize: 'var(--text-xl)', fontWeight: 600, lineHeight: 1.15 }}>September, 450,000</div>
          <div style={{ fontSize: 'var(--text-lg)', fontWeight: 600 }}>Payment history</div>
          <p>Pay at any CRDB branch or by M-Pesa to the account below, then send the reference to your landlord.</p>
          <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>Recorded by Asha, 3 Sep 2026 at 14:20</p>
        </div>
      </Section>

      <Section title="Buttons" lead="One filled button per screen. Secondary actions are outlined in ink, quiet ones are underlined text.">
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)' }}>
          <button className="btn btn-primary">
            <Icon icon="solar:qr-code-linear" width={20} /> Scan to connect
          </button>
          <button className="btn btn-secondary">View agreement</button>
          <button className="btn btn-danger">End tenancy</button>
          <button className="btn btn-quiet">Not now</button>
          <button className="btn btn-primary" disabled>
            Saving
          </button>
        </div>
      </Section>

      <Section title="Fields" lead="Labels above, hints below, errors say what to do. Never placeholder-only.">
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <div className="field">
            <label htmlFor="phone">Phone number</label>
            <input id="phone" className="input" placeholder="0712 345 678" inputMode="tel" />
            <span className="hint">We'll text you a code.</span>
          </div>
          <div className="field invalid">
            <label htmlFor="nida">NIDA number</label>
            <input id="nida" className="input" defaultValue="19900101-12345" />
            <span className="error">A NIDA number has 20 digits. Check the last group.</span>
          </div>
        </div>
      </Section>

      <Section title="Icons" lead="Solar, linear weight. Bold weight only marks the current tab.">
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 'var(--sp-4)' }}>
          {ICONS.map(([label, icon]) => (
            <div key={icon} style={{ display: 'grid', justifyItems: 'center', gap: 'var(--sp-1)' }}>
              <Icon icon={icon} width={26} />
              <span style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>{label}</span>
            </div>
          ))}
        </div>
      </Section>
    </main>
  );
}
