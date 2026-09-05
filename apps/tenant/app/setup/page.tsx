'use client';

import { Icon } from '@iconify/react';
import { useRouter } from 'next/navigation';
import { useEffect, useState } from 'react';
import { PageHead, Shell } from '../../components/Shell';
import { STEPS } from './steps';

/**
 * Guided setup wizard shell (FLOWS flow 1 step 3). Abandoning it is safe: the
 * current step is kept in localStorage so the landlord resumes where they were.
 */

const STORAGE_KEY = 'tms.tenant.setup.step';

function readStoredStep(): number {
  if (typeof window === 'undefined') return 0;
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return 0;
    const idx = STEPS.findIndex((s) => s.id === raw);
    return idx >= 0 ? idx : 0;
  } catch {
    return 0;
  }
}

function Stepper({ current, onPick }: { current: number; onPick: (i: number) => void }) {
  return (
    <ol
      style={{
        display: 'flex',
        flexWrap: 'wrap',
        gap: 'var(--sp-2)',
        listStyle: 'none',
        margin: '0 0 var(--sp-5)',
        padding: 0,
      }}
    >
      {STEPS.map((step, i) => {
        const done = i < current;
        const active = i === current;
        return (
          <li key={step.id}>
            <button
              type="button"
              onClick={() => onPick(i)}
              aria-current={active ? 'step' : undefined}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 'var(--sp-2)',
                minHeight: 'var(--touch-min)',
                padding: '0 var(--sp-3)',
                background: active ? 'var(--primary-soft)' : 'transparent',
                border: `1px solid ${active ? 'var(--primary)' : 'var(--rule)'}`,
                borderRadius: 'var(--radius-md)',
                color: active ? 'var(--ink)' : 'var(--ink-soft)',
                fontWeight: active ? 600 : 400,
                fontSize: 'var(--text-sm)',
                cursor: 'pointer',
              }}
            >
              <span className="num" style={{ fontWeight: 600 }}>
                {done ? <Icon icon="solar:check-circle-linear" width={16} /> : i + 1}
              </span>
              {step.label}
            </button>
          </li>
        );
      })}
    </ol>
  );
}

function SetupBody() {
  const router = useRouter();
  const [current, setCurrent] = useState(0);
  const [hydrated, setHydrated] = useState(false);

  useEffect(() => {
    setCurrent(readStoredStep());
    setHydrated(true);
  }, []);

  useEffect(() => {
    if (!hydrated) return;
    try {
      window.localStorage.setItem(STORAGE_KEY, STEPS[current].id);
    } catch {
      /* private mode: progress simply isn't remembered */
    }
  }, [current, hydrated]);

  const step = STEPS[current];
  const isLast = current === STEPS.length - 1;

  return (
    <>
      <PageHead
        title="Set up your business"
        lead={`Step ${current + 1} of ${STEPS.length} · ${step.label}. You can leave and come back — we remember where you stopped.`}
      />
      <Stepper current={current} onPick={setCurrent} />
      <hr className="rule rule-strong" />

      <section style={{ paddingTop: 'var(--sp-5)' }}>
        <h2 style={{ fontSize: 'var(--text-lg)' }}>{step.label}</h2>
        <div style={{ marginTop: 'var(--sp-3)' }}>
          <step.Body />
        </div>
        <p style={{ marginTop: 'var(--sp-4)', fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>
          This step is filled in during Phase {step.phase}.
        </p>
      </section>

      <div style={{ display: 'flex', gap: 'var(--sp-2)', marginTop: 'var(--sp-6)' }}>
        <button
          type="button"
          className="btn btn-secondary"
          onClick={() => setCurrent((c) => Math.max(0, c - 1))}
          disabled={current === 0}
        >
          Back
        </button>
        {isLast ? (
          <button type="button" className="btn btn-primary" onClick={() => router.push('/')}>
            Go to dashboard
          </button>
        ) : (
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => setCurrent((c) => Math.min(STEPS.length - 1, c + 1))}
          >
            Next
          </button>
        )}
        <button type="button" className="btn btn-quiet" onClick={() => router.push('/')}>
          Finish later
        </button>
      </div>
    </>
  );
}

export default function SetupPage() {
  return (
    <Shell>
      <SetupBody />
    </Shell>
  );
}
