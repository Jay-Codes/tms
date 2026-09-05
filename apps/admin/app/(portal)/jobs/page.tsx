'use client';

/**
 * Background jobs — `GET /admin/jobs` for what ran when, plus the three manual
 * triggers the API exposes to a platform admin:
 *
 *   POST /admin/jobs/contract-lifecycle → {expiring, ended, units_freed}
 *   POST /admin/jobs/overdue            → {flipped}
 *   POST /admin/jobs/notifications      → {queued:{kind:n}}   (body {force_hour?})
 *
 * All three are idempotent sweeps the API also runs on its own ticker; the JSON
 * each one answers is shown verbatim, because that result is the record of what
 * the run did. `force_hour` ignores each org's send hour, so it is off unless
 * an admin deliberately turns it on — it can put SMS out ahead of schedule.
 */

import { useCallback, useEffect, useState } from 'react';
import { ProblemNote } from '../../components/FormBits';
import { PageHead, Shell } from '../../components/Shell';
import { ApiError, adminApi, jobKey, toApiError, type AdminJob } from '../../lib/api';
import { fmtDateTime, fmtJson } from '../../lib/format';

type RunKey = 'contract-lifecycle' | 'overdue' | 'notifications';

const RUNNERS: { key: RunKey; label: string; lead: string }[] = [
  {
    key: 'contract-lifecycle',
    label: 'Contract lifecycle',
    lead: 'Flags contracts within 30 days of their end date as expiring, ends the ones past it, and frees their units.',
  },
  {
    key: 'overdue',
    label: 'Overdue sweep',
    lead: 'Moves pending and partial schedules past their due date (plus the org grace days) to overdue.',
  },
  {
    key: 'notifications',
    label: 'Notification scheduler',
    lead: 'Derives today’s due sends per org and queues them. Respects each org’s send hour unless forced.',
  },
];

function JobsBody() {
  const [jobs, setJobs] = useState<AdminJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);

  const [running, setRunning] = useState<RunKey | null>(null);
  const [forceHour, setForceHour] = useState(false);
  const [results, setResults] = useState<Partial<Record<RunKey, unknown>>>({});
  const [runErrors, setRunErrors] = useState<Partial<Record<RunKey, ApiError>>>({});

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setError(null);
    try {
      const res = await adminApi.jobs(signal);
      setJobs(res.items ?? []);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setError(toApiError(e));
      setJobs([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const run = async (key: RunKey) => {
    setRunning(key);
    setRunErrors((e) => ({ ...e, [key]: undefined }));
    try {
      const result =
        key === 'contract-lifecycle'
          ? await adminApi.runContractLifecycle()
          : key === 'overdue'
            ? await adminApi.runOverdue()
            : await adminApi.runNotifications(forceHour ? { force_hour: true } : {});
      setResults((r) => ({ ...r, [key]: result ?? {} }));
      void load();
    } catch (e) {
      setRunErrors((errs) => ({ ...errs, [key]: toApiError(e) }));
    } finally {
      setRunning(null);
    }
  };

  return (
    <>
      <PageHead
        title="Jobs"
        lead="The sweeps the API runs on its own schedule, and a button for each when a support case needs one now."
        actions={
          <button type="button" className="btn btn-secondary" disabled={loading} onClick={() => void load()}>
            {loading ? 'Refreshing…' : 'Refresh'}
          </button>
        }
      />

      <ProblemNote error={error} />

      <table className="ledger" style={{ marginTop: 'var(--sp-4)' }}>
        <thead>
          <tr>
            <th>Job</th>
            <th>Audit action</th>
            <th>Last run</th>
            <th>Result</th>
          </tr>
        </thead>
        <tbody>
          {jobs.length === 0 && !loading ? (
            <tr>
              <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                No job history reported.
              </td>
            </tr>
          ) : (
            jobs.map((job, i) => (
              <tr key={jobKey(job) || i}>
                <td style={{ fontWeight: 500 }}>
                  {jobKey(job)}
                  {job.description ? (
                    <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)', fontWeight: 400 }}>
                      {job.description}
                    </div>
                  ) : null}
                </td>
                <td style={{ color: 'var(--ink-soft)' }}>{job.action ?? '—'}</td>
                <td style={{ whiteSpace: 'nowrap' }}>
                  {fmtDateTime(job.last_run_at)}
                  {job.last_status ? (
                    <div style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{job.last_status}</div>
                  ) : null}
                </td>
                <td style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                  {job.last_result === undefined ? '—' : fmtJson(job.last_result)}
                </td>
              </tr>
            ))
          )}
          {loading ? (
            <tr>
              <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                Loading…
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>

      <section style={{ marginTop: 'var(--sp-6)' }}>
        <h2 style={{ fontSize: 'var(--text-lg)', marginBottom: 'var(--sp-3)' }}>Run now</h2>
        <div style={{ display: 'grid', gap: 'var(--sp-4)', maxWidth: 760 }}>
          {RUNNERS.map((r) => (
            <div
              key={r.key}
              style={{
                border: '1px solid var(--rule)',
                borderRadius: 'var(--radius-sm)',
                padding: 'var(--sp-4)',
              }}
            >
              <div style={{ display: 'flex', gap: 'var(--sp-4)', alignItems: 'flex-start', justifyContent: 'space-between' }}>
                <div>
                  <div style={{ fontWeight: 600 }}>{r.label}</div>
                  <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', marginTop: 'var(--sp-1)' }}>
                    {r.lead}
                  </p>
                </div>
                <button
                  type="button"
                  className="btn btn-secondary"
                  disabled={running !== null}
                  onClick={() => void run(r.key)}
                  style={{ flexShrink: 0 }}
                >
                  {running === r.key ? 'Running…' : 'Run'}
                </button>
              </div>

              {r.key === 'notifications' ? (
                <label
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 'var(--sp-2)',
                    marginTop: 'var(--sp-3)',
                    fontSize: 'var(--text-sm)',
                  }}
                >
                  <input
                    type="checkbox"
                    checked={forceHour}
                    onChange={(e) => setForceHour(e.target.checked)}
                  />
                  Force hour — queue sends even before each org’s send hour
                </label>
              ) : null}

              {runErrors[r.key] ? (
                <div style={{ marginTop: 'var(--sp-3)' }}>
                  <ProblemNote error={runErrors[r.key] ?? null} />
                </div>
              ) : null}

              {results[r.key] !== undefined ? (
                <pre
                  style={{
                    marginTop: 'var(--sp-3)',
                    marginBottom: 0,
                    padding: 'var(--sp-3)',
                    background: 'var(--sheet-tint)',
                    borderRadius: 'var(--radius-sm)',
                    fontSize: 'var(--text-sm)',
                    whiteSpace: 'pre-wrap',
                    overflow: 'auto',
                    maxHeight: 260,
                  }}
                >
                  {fmtJson(results[r.key])}
                </pre>
              ) : null}
            </div>
          ))}
        </div>
      </section>
    </>
  );
}

export default function JobsPage() {
  return (
    <Shell>
      <JobsBody />
    </Shell>
  );
}
