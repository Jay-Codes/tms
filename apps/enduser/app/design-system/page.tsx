'use client';

import { Icon } from '@iconify/react';
import { applyOrgTheme, DEFAULT_THEME, FONT_IDS, FONT_LABELS, type OrgTheme } from '@tms/ui';
import { useState, type CSSProperties, type ReactNode } from 'react';

/* ------------------------------------------------------------------ */
/* Design-system preview. Read-only reference for the tokens in        */
/* globals.css — every component here uses tokens only, no ad-hoc      */
/* values, so this page doubles as the compliance check.               */
/* ------------------------------------------------------------------ */

const styles: Record<string, CSSProperties> = {
  page: {
    maxWidth: 480,
    margin: '0 auto',
    padding: 'var(--sp-4) var(--sp-4) var(--sp-7)',
  },
  section: { marginTop: 'var(--sp-6)' },
  sectionTitle: {
    fontSize: 'var(--text-lg)',
    marginBottom: 'var(--sp-1)',
  },
  sectionNote: {
    fontSize: 'var(--text-sm)',
    color: 'var(--text-muted)',
    margin: '0 0 var(--sp-4)',
  },
  card: {
    background: 'var(--surface)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--radius-lg)',
    boxShadow: 'var(--shadow-card)',
    padding: 'var(--sp-4)',
  },
  input: {
    display: 'block',
    width: '100%',
    minHeight: 'var(--touch-min)',
    padding: '0 var(--sp-3)',
    fontSize: 'var(--text-md)',
    fontFamily: 'var(--font-sans)',
    color: 'var(--text)',
    background: 'var(--surface-sunken)',
    border: '1px solid var(--border)',
    borderRadius: 'var(--radius-sm)',
  },
  label: {
    display: 'block',
    fontSize: 'var(--text-sm)',
    fontWeight: 600,
    marginBottom: 'var(--sp-1)',
  },
};

function Section({ title, note, children }: { title: string; note: string; children: ReactNode }) {
  return (
    <section style={styles.section}>
      <h2 style={styles.sectionTitle}>{title}</h2>
      <p style={styles.sectionNote}>{note}</p>
      {children}
    </section>
  );
}

function Swatch({ name, token, dark }: { name: string; token: string; dark?: boolean }) {
  return (
    <div style={{ flex: '1 1 96px', minWidth: 96 }}>
      <div
        style={{
          height: 56,
          borderRadius: 'var(--radius-sm)',
          border: '1px solid var(--border)',
          background: `var(${token})`,
        }}
      />
      <div style={{ fontSize: 'var(--text-xs)', marginTop: 'var(--sp-1)', fontWeight: 600 }}>{name}</div>
      <div style={{ fontSize: 'var(--text-xs)', color: 'var(--text-muted)' }}>{token}</div>
    </div>
  );
}

function Row(props: { children: ReactNode }) {
  return <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-3)' }}>{props.children}</div>;
}

function Button({
  variant = 'primary',
  children,
}: {
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost';
  children: ReactNode;
}) {
  const base: CSSProperties = {
    minHeight: 'var(--touch-min)',
    padding: '0 var(--sp-4)',
    borderRadius: 'var(--radius-md)',
    fontSize: 'var(--text-md)',
    fontWeight: 700,
    fontFamily: 'var(--font-sans)',
    border: '1px solid transparent',
    display: 'inline-flex',
    alignItems: 'center',
    gap: 'var(--sp-2)',
    cursor: 'pointer',
  };
  const variants: Record<string, CSSProperties> = {
    primary: { background: 'var(--primary)', color: 'var(--on-primary)' },
    secondary: { background: 'var(--surface)', color: 'var(--primary)', borderColor: 'var(--border)' },
    danger: { background: 'var(--status-overdue)', color: 'var(--on-primary)' },
    ghost: { background: 'transparent', color: 'var(--primary)' },
  };
  return <button style={{ ...base, ...variants[variant] }}>{children}</button>;
}

function StatusChip({ status }: { status: 'paid' | 'pending' | 'overdue' }) {
  const map = {
    paid: { label: 'Paid', icon: 'solar:check-circle-bold', fg: 'var(--status-paid)', bg: 'var(--status-paid-bg)' },
    pending: { label: 'Pending', icon: 'solar:clock-circle-bold', fg: 'var(--status-pending)', bg: 'var(--status-pending-bg)' },
    overdue: { label: 'Overdue', icon: 'solar:danger-triangle-bold', fg: 'var(--status-overdue)', bg: 'var(--status-overdue-bg)' },
  } as const;
  const s = map[status];
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 'var(--sp-1)',
        padding: 'var(--sp-1) var(--sp-3)',
        borderRadius: 'var(--radius-full)',
        fontSize: 'var(--text-xs)',
        fontWeight: 700,
        color: s.fg,
        background: s.bg,
      }}
    >
      <Icon icon={s.icon} width={16} />
      {s.label}
    </span>
  );
}

const PRESET_COLORS: Array<[string, string]> = [
  ['Teal (default)', '#0f766e'],
  ['Indigo', '#4338ca'],
  ['Maroon', '#9f1239'],
  ['Forest', '#166534'],
  ['Amber', '#b45309'],
];

function ThemeSwitcher() {
  const [theme, setTheme] = useState<OrgTheme>(DEFAULT_THEME);

  const update = (next: OrgTheme) => {
    setTheme(next);
    applyOrgTheme(next);
  };

  return (
    <div style={{ ...styles.card, display: 'grid', gap: 'var(--sp-4)' }}>
      <div>
        <div style={styles.label}>Primary color</div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)' }}>
          {PRESET_COLORS.map(([label, hex]) => (
            <button
              key={hex}
              onClick={() => update({ ...theme, primaryColor: hex })}
              title={label}
              aria-label={label}
              style={{
                width: 'var(--touch-min)',
                height: 'var(--touch-min)',
                borderRadius: 'var(--radius-full)',
                background: hex,
                cursor: 'pointer',
                border:
                  theme.primaryColor === hex ? '3px solid var(--text)' : '1px solid var(--border)',
              }}
            />
          ))}
        </div>
      </div>
      <div>
        <div style={styles.label}>Font</div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)' }}>
          {FONT_IDS.map((id) => (
            <button
              key={id}
              onClick={() => update({ ...theme, font: id })}
              style={{
                minHeight: 'var(--touch-min)',
                padding: '0 var(--sp-3)',
                borderRadius: 'var(--radius-md)',
                fontSize: 'var(--text-sm)',
                fontWeight: 600,
                fontFamily: `var(--font-${id}), system-ui, sans-serif`,
                cursor: 'pointer',
                background: theme.font === id ? 'var(--primary-soft)' : 'var(--surface)',
                color: theme.font === id ? 'var(--primary-strong)' : 'var(--text)',
                border: theme.font === id ? '2px solid var(--primary)' : '1px solid var(--border)',
              }}
            >
              {FONT_LABELS[id]}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}

const ICONS: Array<[string, string]> = [
  ['Home', 'solar:home-2-linear'],
  ['Payments', 'solar:wallet-money-linear'],
  ['Scan QR', 'solar:qr-code-linear'],
  ['Contract', 'solar:document-text-linear'],
  ['Calendar', 'solar:calendar-linear'],
  ['Notifications', 'solar:bell-linear'],
  ['Profile', 'solar:user-circle-linear'],
  ['Property', 'solar:buildings-2-linear'],
  ['Unit', 'solar:key-linear'],
  ['Settings', 'solar:settings-linear'],
  ['History', 'solar:history-linear'],
  ['Support', 'solar:chat-round-dots-linear'],
];

export default function DesignSystem() {
  return (
    <main style={styles.page}>
      <header style={{ paddingTop: 'var(--sp-5)' }}>
        <p style={{ margin: 0, fontSize: 'var(--text-sm)', color: 'var(--text-muted)', fontWeight: 600 }}>
          TMS · Design System v1
        </p>
        <h1 style={{ fontSize: 'var(--text-xl)', marginTop: 'var(--sp-1)' }}>
          Legible. Simple. Mobile-first.
        </h1>
        <p style={{ color: 'var(--text-muted)', margin: 'var(--sp-2) 0 0' }}>
          Plus Jakarta Sans · Solar icons · 4px grid · 44px touch targets. Org theming overrides the
          primary color at runtime; everything else stays fixed.
        </p>
      </header>

      <Section
        title="Org theme"
        note="What a landlord can configure: one primary color + one whitelisted font. Strong/soft/on-primary variants derive automatically. Everything else is fixed core. Try it — the whole page updates."
      >
        <ThemeSwitcher />
      </Section>

      <Section title="Color" note="Warm neutrals, one brand color, three semantic statuses. Status colors are never used decoratively.">
        <Row>
          <Swatch name="Primary" token="--primary" />
          <Swatch name="Primary strong" token="--primary-strong" />
          <Swatch name="Primary soft" token="--primary-soft" />
        </Row>
        <div style={{ height: 'var(--sp-3)' }} />
        <Row>
          <Swatch name="Background" token="--bg" />
          <Swatch name="Surface" token="--surface" />
          <Swatch name="Sunken" token="--surface-sunken" />
          <Swatch name="Border" token="--border" />
        </Row>
        <div style={{ height: 'var(--sp-3)' }} />
        <Row>
          <Swatch name="Paid" token="--status-paid" />
          <Swatch name="Pending" token="--status-pending" />
          <Swatch name="Overdue" token="--status-overdue" />
        </Row>
      </Section>

      <Section title="Typography" note="Plus Jakarta Sans, 1.25 scale, 16px body minimum (prevents iOS input zoom). Amounts use tabular figures.">
        <div style={styles.card}>
          <div style={{ fontSize: 'var(--text-2xl)', fontWeight: 700 }} className="amount">TZS 450,000</div>
          <div style={{ fontSize: 'var(--text-xl)', fontWeight: 700, marginTop: 'var(--sp-3)' }}>Page title · 25</div>
          <div style={{ fontSize: 'var(--text-lg)', fontWeight: 700, marginTop: 'var(--sp-2)' }}>Section title · 20</div>
          <div style={{ fontSize: 'var(--text-md)', marginTop: 'var(--sp-2)' }}>Body text · 16 — Rent for Mbezi Beach Block A, Room 4 is due on 5 October.</div>
          <div style={{ fontSize: 'var(--text-sm)', color: 'var(--text-muted)', marginTop: 'var(--sp-2)' }}>Secondary · 14 — Recorded by Asha (manager)</div>
          <div style={{ fontSize: 'var(--text-xs)', color: 'var(--text-muted)', marginTop: 'var(--sp-2)' }}>CAPTION · 13 — LAST UPDATED TODAY 09:00</div>
        </div>
      </Section>

      <Section title="Icons — Solar" note="Iconify solar set. Linear weight by default; bold weight only for active/selected states.">
        <div
          style={{
            ...styles.card,
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fill, minmax(96px, 1fr))',
            gap: 'var(--sp-4)',
          }}
        >
          {ICONS.map(([label, icon]) => (
            <div key={icon} style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 'var(--sp-1)' }}>
              <Icon icon={icon} width={28} color="var(--text)" />
              <span style={{ fontSize: 'var(--text-xs)', color: 'var(--text-muted)' }}>{label}</span>
            </div>
          ))}
        </div>
      </Section>

      <Section title="Buttons" note="One primary action per screen. All targets ≥ 44px tall.">
        <Row>
          <Button variant="primary">
            <Icon icon="solar:qr-code-linear" width={20} /> Scan to connect
          </Button>
          <Button variant="secondary">View contract</Button>
          <Button variant="danger">Terminate</Button>
          <Button variant="ghost">Cancel</Button>
        </Row>
      </Section>

      <Section title="Status chips" note="The three payment states from the spec. Icon + word, never color alone.">
        <Row>
          <StatusChip status="paid" />
          <StatusChip status="pending" />
          <StatusChip status="overdue" />
        </Row>
      </Section>

      <Section title="Inputs" note="Sunken fill, 16px text, labels always visible — no placeholder-only fields.">
        <div style={{ ...styles.card, display: 'grid', gap: 'var(--sp-4)' }}>
          <div>
            <label style={styles.label}>Phone number</label>
            <input style={styles.input} placeholder="0712 345 678" inputMode="tel" />
          </div>
          <div>
            <label style={styles.label}>NIDA number</label>
            <input style={styles.input} placeholder="XXXXXXXX-XXXXX-XXXXX-XX" />
          </div>
        </div>
      </Section>

      <Section title="Cards" note="The core pattern: next-payment card as a renter sees it on the dashboard.">
        <div style={styles.card}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
            <div>
              <div style={{ fontSize: 'var(--text-sm)', color: 'var(--text-muted)', fontWeight: 600 }}>Next payment due</div>
              <div className="amount" style={{ fontSize: 'var(--text-2xl)', marginTop: 'var(--sp-1)' }}>TZS 450,000</div>
              <div style={{ fontSize: 'var(--text-sm)', color: 'var(--text-muted)', marginTop: 'var(--sp-1)' }}>
                Due 5 Oct 2026 · Room 4, Block A
              </div>
            </div>
            <StatusChip status="pending" />
          </div>
          <div style={{ marginTop: 'var(--sp-4)' }}>
            <Button variant="primary">
              <Icon icon="solar:wallet-money-linear" width={20} /> How to pay
            </Button>
          </div>
        </div>
      </Section>

      <Section title="List rows" note="Payment history rows: 44px minimum, amount right-aligned with tabular figures.">
        <div style={{ ...styles.card, padding: 0 }}>
          {[
            ['Jul 2026', 'paid', '450,000'],
            ['Aug 2026', 'paid', '450,000'],
            ['Sep 2026', 'overdue', '450,000'],
          ].map(([month, status, amt], i) => (
            <div
              key={month}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 'var(--sp-3)',
                minHeight: 'var(--touch-min)',
                padding: 'var(--sp-3) var(--sp-4)',
                borderTop: i ? '1px solid var(--border)' : 'none',
              }}
            >
              <Icon icon="solar:history-linear" width={22} color="var(--text-muted)" />
              <div style={{ flex: 1, fontWeight: 600 }}>{month}</div>
              <StatusChip status={status as 'paid' | 'overdue'} />
              <div className="amount" style={{ minWidth: 90, textAlign: 'right' }}>{amt}</div>
            </div>
          ))}
        </div>
      </Section>

      <Section title="Bottom navigation" note="Renter app: four destinations max. Bold icon weight marks the active tab.">
        <div
          style={{
            ...styles.card,
            display: 'flex',
            justifyContent: 'space-around',
            padding: 'var(--sp-2) 0',
          }}
        >
          {[
            ['Home', 'solar:home-2-bold', true],
            ['Payments', 'solar:wallet-money-linear', false],
            ['Contract', 'solar:document-text-linear', false],
            ['Profile', 'solar:user-circle-linear', false],
          ].map(([label, icon, active]) => (
            <div
              key={label as string}
              style={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'center',
                gap: 2,
                minWidth: 'var(--touch-min)',
                minHeight: 'var(--touch-min)',
                justifyContent: 'center',
                color: active ? 'var(--primary)' : 'var(--text-muted)',
              }}
            >
              <Icon icon={icon as string} width={24} />
              <span style={{ fontSize: 'var(--text-xs)', fontWeight: active ? 700 : 500 }}>{label as string}</span>
            </div>
          ))}
        </div>
      </Section>
    </main>
  );
}
