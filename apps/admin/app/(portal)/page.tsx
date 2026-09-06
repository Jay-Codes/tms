'use client';

/**
 * Platform dashboard — everything on this screen is `GET /admin/metrics`
 * (API.md Phase 7). Nothing is derived here beyond the occupancy percentage,
 * which is a presentation of two numbers the backend already sent.
 */

import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import { StatTile } from '@tms/ui';
import { ProblemNote } from '../../components/FormBits';
import { PageHead } from '../../components/PageHead';
import { ApiError, adminApi, toApiError, type AdminMetrics } from '../../lib/api';
import { fmtNum, fmtTZS } from '../../lib/format';

function Stat({
  label,
  value,
  sub,
  href,
}: {
  label: string;
  value: string;
  sub?: string;
  href?: string;
}) {
  const body = (
    <>
      <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{label}</div>
      <div className="num" style={{ fontSize: 'var(--text-2xl)', textAlign: 'left', marginTop: 'var(--sp-1)' }}>
        {value}
      </div>
      {sub ? (
        <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', marginTop: 'var(--sp-1)' }}>
          {sub}
        </div>
      ) : null}
    </>
  );
  const style: React.CSSProperties = {
    border: '1px solid var(--rule)',
    borderRadius: 'var(--radius-sm)',
    padding: 'var(--sp-4)',
    background: 'var(--paper)',
    display: 'block',
    color: 'inherit',
    textDecoration: 'none',
  };
  return href ? (
    <Link href={href} style={style}>
      {body}
    </Link>
  ) : (
    <div style={style}>{body}</div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section style={{ marginTop: 'var(--sp-6)' }}>
      <h2 style={{ fontSize: 'var(--text-lg)', marginBottom: 'var(--sp-3)' }}>{title}</h2>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(190px, 1fr))',
          gap: 'var(--sp-4)',
        }}
      >
        {children}
      </div>
    </section>
  );
}

/** An infra check reads as a stamp: it either answered or it did not. */
function InfraStamp({ name, ok }: { name: string; ok: boolean | undefined }) {
  return (
    <div
      style={{
        border: '1px solid var(--rule)',
        borderRadius: 'var(--radius-sm)',
        padding: 'var(--sp-4)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 'var(--sp-3)',
      }}
    >
      <span style={{ fontWeight: 500 }}>{name}</span>
      {ok === undefined ? (
        <span className="pencil">unknown</span>
      ) : (
        <span className={ok ? 'stamp stamp-paid' : 'stamp stamp-overdue'}>{ok ? 'OK' : 'Down'}</span>
      )}
    </div>
  );
}

function DashboardBody() {
  const [metrics, setMetrics] = useState<AdminMetrics | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setError(null);
    try {
      setMetrics(await adminApi.metrics(signal));
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setError(toApiError(e));
      setMetrics(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const occupancy =
    metrics && metrics.units?.total
      ? `${Math.round((metrics.units.occupied / metrics.units.total) * 100)}% occupied`
      : undefined;

  return (
    <>
      <PageHead
        title="Platform"
        lead="Every organization on this deployment, at a glance."
        actions={
          <button type="button" className="btn btn-secondary" disabled={loading} onClick={() => void load()}>
            {loading ? 'Refreshing…' : 'Refresh'}
          </button>
        }
      />

      <ProblemNote error={error} />

      {loading && !metrics ? <p style={{ color: 'var(--ink-soft)' }}>Loading…</p> : null}

      {metrics ? (
        <>
          <Section title="Organizations">
            <Stat label="Total" value={fmtNum(metrics.orgs?.total)} href="/orgs" />
            <Stat label="Active" value={fmtNum(metrics.orgs?.active)} href="/orgs?status=active" />
            <Stat
              label="Suspended"
              value={fmtNum(metrics.orgs?.suspended)}
              href="/orgs?status=suspended"
            />
          </Section>

          <Section title="Tenancy">
            <Stat label="Renters" value={fmtNum(metrics.renters?.total)} />
            <Stat
              label="Units"
              value={fmtNum(metrics.units?.total)}
              sub={occupancy ?? undefined}
            />
            <Stat label="Units occupied" value={fmtNum(metrics.units?.occupied)} />
            <Stat label="Active contracts" value={fmtNum(metrics.contracts?.active)} />
          </Section>

          <Section title="SMS — last 24 hours">
            <Stat label="Sent" value={fmtNum(metrics.sms?.sent_24h)} />
            <Stat label="Failed" value={fmtNum(metrics.sms?.failed_24h)} />
            <Stat label="Queued" value={fmtNum(metrics.sms?.queued)} sub="waiting on the worker" />
          </Section>

          {/*
            Credits (Phase 14). Tiles rather than the Stat card above because
            these three carry a judgement: an org under its watermark and a
            held message are both "someone must act", where a 24-hour send
            count is only a number. `metrics.sms` gains them when the credits
            backend lands; until then they read as em dashes.
          */}
          <section style={{ marginTop: 'var(--sp-6)' }}>
            <h2 style={{ fontSize: 'var(--text-lg)', marginBottom: 'var(--sp-3)' }}>SMS credits</h2>
            <div
              style={{
                display: 'grid',
                gridTemplateColumns: 'repeat(auto-fit, minmax(210px, 1fr))',
                gap: 'var(--sp-5)',
              }}
            >
              <StatTile
                label="Credits used today"
                value={fmtNum(metrics.sms?.credits_used_today)}
                change={null}
                goodDirection="none"
                sub="one credit per message segment"
              />
              <StatTile
                label="Orgs under watermark"
                value={fmtNum(metrics.sms?.orgs_under_watermark)}
                tone={metrics.sms?.orgs_under_watermark ? 'overdue' : undefined}
                sub={
                  metrics.sms?.orgs_under_watermark ? (
                    <Link href="/orgs">Review organizations →</Link>
                  ) : (
                    'every organization is above its low watermark'
                  )
                }
              />
              <StatTile
                label="Messages held"
                value={fmtNum(metrics.sms?.held_total)}
                tone={metrics.sms?.held_total ? 'overdue' : undefined}
                sub="held until credits are added — not failed"
              />
            </div>
          </section>

          <Section title="Payments — last 30 days">
            <Stat label="Recorded" value={fmtNum(metrics.payments?.recorded_30d)} />
            <Stat label="Amount" value={fmtTZS(metrics.payments?.amount_30d)} />
          </Section>

          <Section title="Infrastructure">
            <InfraStamp name="Postgres" ok={metrics.db?.ok} />
            <InfraStamp name="Redis" ok={metrics.redis?.ok} />
            <InfraStamp name="MinIO" ok={metrics.minio?.ok} />
          </Section>
        </>
      ) : null}
    </>
  );
}

export default function DashboardPage() {
  return (
    <>
      <DashboardBody />
    </>
  );
}
