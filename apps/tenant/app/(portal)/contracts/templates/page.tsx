'use client';

/**
 * Contract templates (FLOWS flow 6 step 1). The list is thin on purpose — the
 * work happens in the editor, one route down.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';
import { Field, ProblemNote } from '../../../../components/FormBits';
import { PageHead } from '../../../../components/PageHead';
import { Sheet } from '../../../../components/Sheet';
import { SNAPSHOT_BANNER } from '../../../../components/TemplateEditor';
import {
  ApiError,
  templatesApi,
  toApiError,
  unwrapTemplate,
  type ContractTemplateSummary,
} from '../../../../lib/api';
import { fmtDate } from '../../../../lib/format';

/** Opening draft for a new template — the landlord edits from here. */
const STARTER_BODY =
  '<h1>Tenancy agreement</h1>' +
  '<p>This agreement is made between {{org_name}} (the landlord) and {{renter_name}} (the renter) ' +
  'for {{unit}} at {{property}}.</p>' +
  '<h2>Term</h2>' +
  '<p>The tenancy runs for {{term_days}} days, from {{start_date}} to {{end_date}}.</p>' +
  '<h2>Rent</h2>' +
  '<p>Rent is {{rent}} per {{payment_period}}, due on day {{due_day}} of each period.</p>';

function NewTemplateForm({ onCreated }: { onCreated: (id: string) => void }) {
  const [name, setName] = useState('');
  const [isDefault, setIsDefault] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const t = unwrapTemplate(
        await templatesApi.create({ name: name.trim(), body_html: STARTER_BODY, is_default: isDefault }),
      );
      onCreated(t.id);
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
      <ProblemNote error={error} />
      <p style={{ color: 'var(--ink-soft)' }}>
        A new template starts from a plain tenancy agreement. You edit the wording next.
      </p>
      <Field id="new_tpl_name" label="Template name" error={error?.errors.name}>
        <input
          id="new_tpl_name"
          className="input"
          value={name}
          maxLength={80}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Standard tenancy agreement"
        />
      </Field>
      <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', minHeight: 'var(--touch-min)' }}>
        <input
          type="checkbox"
          checked={isDefault}
          onChange={(e) => setIsDefault(e.target.checked)}
          style={{ width: 18, height: 18 }}
        />
        Make this the default template
      </label>
      <div>
        <button type="submit" className="btn btn-primary" disabled={busy || name.trim().length === 0}>
          {busy ? 'Creating…' : 'Create and edit'}
        </button>
      </div>
    </form>
  );
}

function TemplatesBody() {
  const router = useRouter();
  const [items, setItems] = useState<ContractTemplateSummary[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [open, setOpen] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const res = await templatesApi.list(signal);
      setItems(res.items ?? []);
      setError(null);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      setError(toApiError(e));
      setItems([]);
    }
  }, []);

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  return (
    <>
      <PageHead
        title="Contract templates"
        lead="The terms every new contract starts from. Variables like {{renter_name}} are filled in when a contract is written."
        actions={
          <>
            <Link href="/contracts" className="btn btn-quiet">
              Contracts
            </Link>
            <button type="button" className="btn btn-primary" onClick={() => setOpen(true)}>
              <Icon icon="solar:add-square-linear" width={20} /> New template
            </button>
          </>
        }
      />

      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', marginBottom: 'var(--sp-4)' }}>
        {SNAPSHOT_BANNER}
      </p>

      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={error} />
        <table className="ledger">
          <thead>
            <tr>
              <th>Name</th>
              <th>Default</th>
              <th>Last edited</th>
              <th>Created</th>
            </tr>
          </thead>
          <tbody>
            {items === null ? (
              <tr>
                <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                  Loading…
                </td>
              </tr>
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                  {error ? 'Nothing to show.' : 'No templates yet.'}
                </td>
              </tr>
            ) : (
              items.map((t) => (
                <tr key={t.id}>
                  <td style={{ fontWeight: 600 }}>
                    <Link href={`/contracts/templates/${t.id}`} style={{ color: 'inherit' }}>
                      {t.name}
                    </Link>
                  </td>
                  <td>{t.is_default ? <span className="stamp stamp-paid">Default</span> : null}</td>
                  <td style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{fmtDate(t.updated_at)}</td>
                  <td style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{fmtDate(t.created_at)}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <Sheet open={open} title="New template" onClose={() => setOpen(false)} width={480}>
        <NewTemplateForm onCreated={(id) => router.push(`/contracts/templates/${id}`)} />
      </Sheet>
    </>
  );
}

export default function TemplatesPage() {
  return (
    <>
      <TemplatesBody />
    </>
  );
}
