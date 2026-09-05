'use client';

import { Icon } from '@iconify/react';
import { ThemeSwitcher } from '@tms/ui';
import type { ReactNode } from 'react';

/* ------------------------------------------------------------------ */
/* Design-system preview, landlord side (desktop).                     */
/* The dashboard mock at the top is the reference for how the pieces   */
/* fit; the sections below are the pieces on their own.                */
/* ------------------------------------------------------------------ */

type Status = 'paid' | 'overdue' | { due: string };

const ROWS: Array<[string, string, string, Status, string]> = [
  ['Asha Juma', 'Room 4, Block A', '1 Sep to 30 Sep', 'paid', '450,000'],
  ['Baraka Mwinyi', 'Room 2, Block A', '1 Sep to 30 Sep', 'paid', '450,000'],
  ['Neema Kessy', 'House B', '1 Sep to 30 Nov', 'paid', '1,350,000'],
  ['Joseph Mrema', 'Room 1, Block A', '1 Sep to 30 Sep', 'overdue', '450,000'],
  ['Zawadi Omari', 'Room 3, Block A', '1 Sep to 30 Sep', 'overdue', '450,000'],
  ['Hamisi Said', 'Room 6, Block A', '1 Sep to 30 Sep', 'paid', '450,000'],
  ['Grace Lyimo', 'Room 5, Block A', '5 Sep to 4 Oct', { due: '5 Sep' }, '450,000'],
  ['Idris Kombo', 'Shop 1', '1 Sep to 30 Sep', 'overdue', '450,000'],
];

function StatusCell({ s }: { s: Status }) {
  if (s === 'paid') return <span className="stamp stamp-paid">Paid</span>;
  if (s === 'overdue') return <span className="stamp stamp-overdue">Overdue</span>;
  return <span className="pencil">due {s.due}</span>;
}

function Dashboard() {
  const tabs: Array<[string, string, boolean]> = [
    ['Overview', 'solar:home-2-linear', true],
    ['Renters', 'solar:users-group-rounded-linear', false],
    ['Units', 'solar:buildings-2-linear', false],
    ['Payments', 'solar:wallet-money-linear', false],
    ['Messages', 'solar:chat-round-dots-linear', false],
    ['Settings', 'solar:settings-linear', false],
  ];

  return (
    <div
      className="sheet"
      style={{
        display: 'grid',
        gridTemplateColumns: '220px 1fr',
        minHeight: 640,
        overflow: 'hidden',
      }}
    >
      {/* Tab rail */}
      <aside style={{ background: 'var(--paper)', borderRight: '1px solid var(--rule)', padding: 'var(--sp-5) 0' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)', padding: '0 var(--sp-4)', marginBottom: 'var(--sp-6)' }}>
          <span
            aria-hidden
            style={{ width: 28, height: 28, borderRadius: 'var(--radius-sm)', background: 'var(--primary)' }}
          />
          <div style={{ lineHeight: 1.15 }}>
            <div style={{ fontWeight: 600 }}>JJnE Rentals</div>
            <div style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>Mbezi Beach</div>
          </div>
        </div>
        <nav className="tabs" aria-label="Preview">
          {tabs.map(([label, icon, current]) => (
            <a key={label} className="tab" aria-current={current ? 'page' : undefined}>
              <Icon icon={icon} width={20} />
              {label}
            </a>
          ))}
        </nav>
      </aside>

      {/* Ledger */}
      <div style={{ padding: 'var(--sp-5) var(--sp-6) var(--sp-6)' }}>
        <div style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', gap: 'var(--sp-4)' }}>
          <div>
            <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>Saturday 5 September 2026</p>
            <h1 style={{ fontSize: 'var(--text-2xl)', marginTop: 'var(--sp-1)' }}>September rent</h1>
            <p style={{ marginTop: 'var(--sp-2)' }}>
              Seven of ten renters have paid. Three are overdue; reminders went out this morning at 09:00.
            </p>
          </div>
          <div style={{ display: 'flex', gap: 'var(--sp-2)', flexShrink: 0 }}>
            <button className="btn btn-secondary">
              <Icon icon="solar:chat-round-dots-linear" width={20} /> Send reminders
            </button>
            <button className="btn btn-primary">
              <Icon icon="solar:bill-check-linear" width={20} /> Record payment
            </button>
          </div>
        </div>

        <table className="ledger" style={{ marginTop: 'var(--sp-5)' }}>
          <thead>
            <tr>
              <th>Renter</th>
              <th>Unit</th>
              <th>Period</th>
              <th style={{ textAlign: 'right' }}>Amount</th>
              <th style={{ textAlign: 'right' }}>Status</th>
            </tr>
          </thead>
          <tbody>
            {ROWS.map(([name, unit, period, s, amt]) => (
              <tr key={name}>
                <td style={{ fontWeight: 500 }}>{name}</td>
                <td>{unit}</td>
                <td style={{ color: 'var(--ink-soft)' }}>{period}</td>
                <td className="num">{amt}</td>
                <td className="num">
                  <StatusCell s={s} />
                </td>
              </tr>
            ))}
            <tr>
              <td colSpan={3}>Collected</td>
              <td className="num">3,150,000</td>
              <td />
            </tr>
            <tr className="total">
              <td colSpan={3}>Outstanding</td>
              <td className="num">1,350,000</td>
              <td />
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Section({ title, lead, children }: { title: string; lead: string; children: ReactNode }) {
  return (
    <section style={{ display: 'grid', gridTemplateColumns: '280px 1fr', gap: 'var(--sp-6)', paddingTop: 'var(--sp-6)' }}>
      <div>
        <hr className="rule rule-strong" />
        <h2 style={{ fontSize: 'var(--text-lg)', marginTop: 'var(--sp-3)' }}>{title}</h2>
        <p style={{ color: 'var(--ink-soft)', marginTop: 'var(--sp-1)' }}>{lead}</p>
      </div>
      <div style={{ paddingTop: 'var(--sp-3)' }}>{children}</div>
    </section>
  );
}

export default function DesignSystem() {
  return (
    <main style={{ maxWidth: 1200, margin: '0 auto', padding: 'var(--sp-7) var(--sp-6) var(--sp-8)' }}>
      <header style={{ maxWidth: 720, marginBottom: 'var(--sp-6)' }}>
        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>TMS design system, landlord portal</p>
        <h1 style={{ fontSize: 'var(--text-display)', marginTop: 'var(--sp-2)' }}>The stamped ledger.</h1>
        <p style={{ marginTop: 'var(--sp-4)', fontSize: 'var(--text-lg)' }}>
          A landlord already keeps a rent book. This is that book: ruled rows, money on the right,
          and a stamp for what has happened. The screen should feel like the desk, not like a dashboard.
        </p>
      </header>

      <Dashboard />

      <Section
        title="The landlord's two knobs"
        lead="Brand colour and typeface. Both are applied at runtime from the org's settings; nothing else is exposed."
      >
        <ThemeSwitcher />
      </Section>

      <Section
        title="Stamps"
        lead="Only for what has happened. Pending is pencilled, because nothing has been recorded yet."
      >
        <div style={{ display: 'flex', gap: 'var(--sp-6)', alignItems: 'center', flexWrap: 'wrap' }}>
          <span className="stamp stamp-paid" style={{ fontSize: 'var(--text-lg)' }}>Paid</span>
          <span className="stamp stamp-overdue" style={{ fontSize: 'var(--text-lg)' }}>Overdue</span>
          <span className="pencil" style={{ fontSize: 'var(--text-lg)' }}>due 5 Oct</span>
        </div>
      </Section>

      <Section title="Money" lead="Tabular lining figures, right-aligned, currency set small and lifted. Totals: single rule above, double rule below.">
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-6)', alignItems: 'start' }}>
          <div style={{ fontSize: 'var(--text-display)', lineHeight: 1 }}>
            <span className="amount">
              <span className="currency">TZS</span>3,150,000
            </span>
          </div>
          <table className="ledger">
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
        </div>
      </Section>

      <Section title="Paper and ink" lead="Text is blue-black ballpoint, rules are ledger blue, paper is barely warm. The brand colour touches the primary button and the current tab only.">
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(5, 1fr)', gap: 'var(--sp-3)' }}>
          {[
            ['Paper', '--paper'],
            ['Sheet', '--sheet'],
            ['Rule', '--rule'],
            ['Ink', '--ink'],
            ['Ink, soft', '--ink-soft'],
            ['Ink, faint', '--ink-faint'],
            ['Brand', '--primary'],
            ['Brand, tint', '--primary-soft'],
            ['Paid', '--stamp-paid'],
            ['Overdue', '--stamp-overdue'],
          ].map(([name, token]) => (
            <div key={token}>
              <div
                style={{
                  height: 56,
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

      <Section title="Type" lead="One family, four sizes in daily use, one display size for totals. Weight 600 for headings, 500 for names, 400 for everything else.">
        <div style={{ display: 'grid', gap: 'var(--sp-3)', maxWidth: 'var(--measure)' }}>
          <div style={{ fontSize: 'var(--text-display)', fontWeight: 600, lineHeight: 1.05 }}>Three renters are overdue</div>
          <div style={{ fontSize: 'var(--text-2xl)', fontWeight: 600, lineHeight: 1.15 }}>September rent</div>
          <div style={{ fontSize: 'var(--text-lg)', fontWeight: 600 }}>Block A, Mbezi Beach</div>
          <p>
            Asha paid 450,000 by M-Pesa on 3 September, reference QX7M2P. Her next payment of 450,000 is
            due on 5 October, and she will get a text a week before.
          </p>
          <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>Recorded by Neema, 3 Sep 2026 at 14:20</p>
        </div>
      </Section>

      <Section title="Buttons" lead="One filled button per screen. Outlined in ink for the alternative, underlined for the quiet way out.">
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)' }}>
          <button className="btn btn-primary">
            <Icon icon="solar:bill-check-linear" width={20} /> Record payment
          </button>
          <button className="btn btn-secondary">Send reminders</button>
          <button className="btn btn-danger">End tenancy</button>
          <button className="btn btn-quiet">Cancel</button>
          <button className="btn btn-primary" disabled>
            Saving
          </button>
        </div>
      </Section>

      <Section title="Fields" lead="Label above, hint below. An error says what is wrong and what to do about it.">
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)', maxWidth: 640 }}>
          <div className="field">
            <label htmlFor="amt">Amount received</label>
            <input id="amt" className="input num" defaultValue="450,000" inputMode="numeric" />
            <span className="hint">In shillings.</span>
          </div>
          <div className="field">
            <label htmlFor="ref">Reference</label>
            <input id="ref" className="input" placeholder="M-Pesa code or bank slip number" />
          </div>
          <div className="field invalid" style={{ gridColumn: '1 / -1' }}>
            <label htmlFor="date">Date received</label>
            <input id="date" className="input" defaultValue="31/09/2026" />
            <span className="error">September has 30 days. Enter a date between 1 and 30.</span>
          </div>
        </div>
      </Section>

      <Section title="Icons" lead="Solar, linear weight at 20px in rows and buttons. Bold weight marks the current tab only.">
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(6, 1fr)', gap: 'var(--sp-4)', maxWidth: 640 }}>
          {[
            ['Overview', 'solar:home-2-linear'],
            ['Renters', 'solar:users-group-rounded-linear'],
            ['Units', 'solar:buildings-2-linear'],
            ['Payments', 'solar:wallet-money-linear'],
            ['Receipt', 'solar:bill-check-linear'],
            ['Messages', 'solar:chat-round-dots-linear'],
            ['QR codes', 'solar:qr-code-linear'],
            ['Agreements', 'solar:document-text-linear'],
            ['Calendar', 'solar:calendar-linear'],
            ['History', 'solar:history-linear'],
            ['Settings', 'solar:settings-linear'],
            ['Export', 'solar:download-linear'],
          ].map(([label, icon]) => (
            <div key={icon} style={{ display: 'grid', justifyItems: 'center', gap: 'var(--sp-1)' }}>
              <Icon icon={icon} width={24} />
              <span style={{ fontSize: 'var(--text-xs)', color: 'var(--ink-soft)' }}>{label}</span>
            </div>
          ))}
        </div>
      </Section>
    </main>
  );
}
