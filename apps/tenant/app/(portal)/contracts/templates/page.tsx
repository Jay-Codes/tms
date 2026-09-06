'use client';

/**
 * Contract templates (FLOWS flow 6 step 1). The list is thin on purpose — the
 * work happens in the editor, one route down.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';
import { TableScroll, useT } from '@tms/ui';
import { Field, ProblemNote } from '../../../../components/FormBits';
import { PageHead } from '../../../../components/PageHead';
import { Sheet } from '../../../../components/Sheet';
import {
  ApiError,
  templatesApi,
  toApiError,
  unwrapTemplate,
  type ContractTemplateSummary,
} from '../../../../lib/api';
import { fmtDate } from '../../../../lib/format';

/**
 * Opening draft for a new template — the landlord edits from here.
 *
 * Both bodies are seeded (Phase 13): the platform default is Kiswahili, so a
 * template that only had an English body would silently issue every Kiswahili
 * contract in English. These are deliberately not dictionary strings — they are
 * contract wording that ends up stored on the org, not portal chrome.
 */
const STARTER_BODY =
  '<h1>Tenancy agreement</h1>' +
  '<p>This agreement is made between {{org_name}} (the landlord) and {{renter_name}} (the renter) ' +
  'for {{unit}} at {{property}}.</p>' +
  '<h2>Term</h2>' +
  '<p>The tenancy runs for {{term_days}} days, from {{start_date}} to {{end_date}}.</p>' +
  '<h2>Rent</h2>' +
  '<p>Rent is {{rent}} per {{payment_period}}, due on day {{due_day}} of each period.</p>';

const STARTER_BODY_SW =
  '<h1>Mkataba wa upangaji</h1>' +
  '<p>Mkataba huu unafanywa kati ya {{org_name}} (mwenye nyumba) na {{renter_name}} (mpangaji) ' +
  'kwa {{unit}} katika {{property}}.</p>' +
  '<h2>Muda</h2>' +
  '<p>Upangaji unadumu kwa siku {{term_days}}, kuanzia {{start_date}} hadi {{end_date}}.</p>' +
  '<h2>Kodi</h2>' +
  '<p>Kodi ni {{rent}} kwa {{payment_period}}, inayolipwa siku ya {{due_day}} ya kila kipindi.</p>';

function NewTemplateForm({ onCreated }: { onCreated: (id: string) => void }) {
  const t = useT();
  const [name, setName] = useState('');
  const [isDefault, setIsDefault] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const created = unwrapTemplate(
        await templatesApi.create({
          name: name.trim(),
          body_html: STARTER_BODY,
          body_html_sw: STARTER_BODY_SW,
          is_default: isDefault,
        }),
      );
      onCreated(created.id);
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
      <ProblemNote error={error} />
      <p style={{ color: 'var(--ink-soft)' }}>{t('tpl.new.lead')}</p>
      <Field id="new_tpl_name" label={t('tpl.name')} error={error?.errors.name}>
        <input
          id="new_tpl_name"
          className="input"
          value={name}
          maxLength={80}
          onChange={(e) => setName(e.target.value)}
          placeholder={t('tpl.new.placeholder')}
        />
      </Field>
      <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', minHeight: 'var(--touch-min)' }}>
        <input
          type="checkbox"
          checked={isDefault}
          onChange={(e) => setIsDefault(e.target.checked)}
          style={{ width: 18, height: 18 }}
        />
        {t('tpl.new.make_default')}
      </label>
      <div>
        <button type="submit" className="btn btn-primary" disabled={busy || name.trim().length === 0}>
          {busy ? t('common.creating') : t('tpl.new.submit')}
        </button>
      </div>
    </form>
  );
}

function TemplatesBody() {
  const t = useT();
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
        title={t('tpl.list.title')}
        lead={t('tpl.list.lead', { example: '{{renter_name}}' })}
        actions={
          <>
            <Link href="/contracts" className="btn btn-quiet">
              {t('nav.contracts')}
            </Link>
            <button type="button" className="btn btn-primary" onClick={() => setOpen(true)}>
              <Icon icon="solar:add-square-linear" width={20} /> {t('tpl.list.new')}
            </button>
          </>
        }
      />

      <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', marginBottom: 'var(--sp-4)' }}>
        {t('tpl.snapshot_banner')}
      </p>

      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-4)', display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={error} />
        <TableScroll label={t('tpl.list.table_label')}>
<table className="ledger">
          <thead>
            <tr>
              <th>{t('common.name')}</th>
              <th>{t('tpl.default')}</th>
              <th>{t('tpl.list.last_edited')}</th>
              <th>{t('tpl.list.created')}</th>
            </tr>
          </thead>
          <tbody>
            {items === null ? (
              <tr>
                <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                  {t('common.loading')}
                </td>
              </tr>
            ) : items.length === 0 ? (
              <tr>
                <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                  {error ? t('common.no_results') : t('tpl.list.empty')}
                </td>
              </tr>
            ) : (
              items.map((row) => (
                <tr key={row.id}>
                  <td style={{ fontWeight: 600 }}>
                    <Link href={`/contracts/templates/${row.id}`} style={{ color: 'inherit' }}>
                      {row.name}
                    </Link>
                  </td>
                  <td>{row.is_default ? <span className="stamp stamp-paid">{t('tpl.default')}</span> : null}</td>
                  <td style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{fmtDate(row.updated_at)}</td>
                  <td style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)' }}>{fmtDate(row.created_at)}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
</TableScroll>
      </div>

      <Sheet open={open} title={t('tpl.list.new')} onClose={() => setOpen(false)} width={480}>
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
