'use client';

/**
 * One property: its details and the units ledger underneath (SPEC §5.3).
 * Units are added one at a time or in bulk; the QR sheet prints from here.
 */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useParams, useRouter } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';
import { ProblemNote } from '../../../../components/FormBits';
import { PropertyExpenses } from '../../../../components/PropertyExpenses';
import { PropertyForm } from '../../../../components/PropertyForm';
import { PageHead } from '../../../../components/PageHead';
import { Sheet } from '../../../../components/Sheet';
import { StatusMark } from '../../../../components/UnitStatus';
import { AddManyUnitsForm, AddUnitForm } from '../../../../components/UnitForms';
import {
  ApiError,
  periodsApi,
  propertiesApi,
  toApiError,
  unwrapProperty,
  type PaymentPeriod,
  type Property,
  type Unit,
} from '../../../../lib/api';
import { Amount } from '../../../../lib/format';
import { TableScroll, useT } from '@tms/ui';

function PropertyBody({ id }: { id: string }) {
  const t = useT();
  const router = useRouter();
  const [property, setProperty] = useState<Property | null>(null);
  const [units, setUnits] = useState<Unit[] | null>(null);
  const [periods, setPeriods] = useState<PaymentPeriod[]>([]);
  const [error, setError] = useState<ApiError | null>(null);
  const [actionError, setActionError] = useState<ApiError | null>(null);
  const [sheet, setSheet] = useState<'none' | 'edit' | 'unit' | 'many'>('none');

  const loadUnits = useCallback(
    async (signal?: AbortSignal) => {
      const res = await propertiesApi.units(id, signal);
      setUnits(res.items ?? []);
    },
    [id],
  );

  useEffect(() => {
    const ac = new AbortController();
    (async () => {
      try {
        const [p] = await Promise.all([propertiesApi.get(id, ac.signal), loadUnits(ac.signal)]);
        setProperty(unwrapProperty(p));
        setError(null);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
      }
      try {
        const per = await periodsApi.list(false, ac.signal);
        setPeriods((per.items ?? []).filter((x) => x.active));
      } catch {
        /* the picker simply shows nothing; the unit still saves */
      }
    })();
    return () => ac.abort();
  }, [id, loadUnits]);

  const refresh = useCallback(async () => {
    try {
      const [p] = await Promise.all([propertiesApi.get(id), loadUnits()]);
      setProperty(unwrapProperty(p));
    } catch (e) {
      setActionError(toApiError(e));
    }
  }, [id, loadUnits]);

  const remove = async () => {
    if (!property) return;
    if (
      !window.confirm(t('properties.delete_confirm', { name: property.name }))
    )
      return;
    setActionError(null);
    try {
      await propertiesApi.remove(property.id);
      router.push('/properties');
    } catch (e) {
      setActionError(toApiError(e));
    }
  };

  if (error) {
    return (
      <>
        <PageHead title={t('common.property')} />
        <hr className="rule rule-strong" />
        <div style={{ paddingTop: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-4)', maxWidth: 640 }}>
          <ProblemNote error={error} />
          <div>
            <Link href="/properties" className="btn btn-secondary">
              {t('properties.back')}
            </Link>
          </div>
        </div>
      </>
    );
  }

  return (
    <>
      <PageHead
        title={property?.name ?? t('common.loading')}
        lead={property?.location_text || undefined}
        actions={
          <>
            <Link href={`/properties/${id}/qr`} className="btn btn-secondary">
              <Icon icon="solar:printer-linear" width={20} /> {t('qr.print_sheet')}
            </Link>
            <button type="button" className="btn btn-quiet" onClick={() => setSheet('edit')} disabled={!property}>
              <Icon icon="solar:pen-linear" width={18} /> {t('common.edit')}
            </button>
            <button type="button" className="btn btn-danger" onClick={() => void remove()} disabled={!property}>
              {t('common.delete')}
            </button>
          </>
        }
      />
      <hr className="rule rule-strong" />

      <div style={{ paddingTop: 'var(--sp-5)', display: 'grid', gap: 'var(--sp-4)' }}>
        <ProblemNote error={actionError} />

        {property?.notes ? (
          <p style={{ color: 'var(--ink-soft)', maxWidth: 'var(--measure)' }}>{property.notes}</p>
        ) : null}

        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--sp-4)' }}>
          <h2 style={{ fontSize: 'var(--text-lg)' }}>
            {t('nav.units')} {units ? <span className="num">({units.length})</span> : null}
          </h2>
          <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button type="button" className="btn btn-secondary" onClick={() => setSheet('many')}>
              {t('properties.add_many')}
            </button>
            <button type="button" className="btn btn-primary" onClick={() => setSheet('unit')}>
              <Icon icon="solar:add-circle-linear" width={20} /> {t('units.add')}
            </button>
          </div>
        </div>

        <TableScroll label={t('nav.units')}>
        <table className="ledger">
          <thead>
            <tr>
              <th>{t('common.unit')}</th>
              <th>{t('common.status')}</th>
              <th className="num">{t('units.th.current_price')}</th>
              <th>{t('units.th.scan_code')}</th>
            </tr>
          </thead>
          <tbody>
            {units === null ? (
              <tr>
                <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                  {t('common.loading')}
                </td>
              </tr>
            ) : units.length === 0 ? (
              <tr>
                <td colSpan={4} style={{ color: 'var(--ink-soft)' }}>
                  {t('properties.units_empty')}
                </td>
              </tr>
            ) : (
              units.map((u) => (
                <tr key={u.id}>
                  <td style={{ fontWeight: 600 }}>
                    <Link href={`/units/${u.id}`} style={{ color: 'inherit' }}>
                      {u.name}
                    </Link>
                  </td>
                  <td>
                    <StatusMark status={u.status} override={u.status_override} />
                  </td>
                  <td className="num">
                    <Amount value={u.current_price?.amount ?? null} per={u.current_price?.period_days ?? null} />
                  </td>
                  <td className="num" style={{ textAlign: 'left', color: 'var(--ink-soft)' }}>
                    {u.unit_code}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
        </TableScroll>
      </div>

      <section style={{ paddingTop: 'var(--sp-7)' }}>
        <hr className="rule rule-strong" />
        <div style={{ paddingTop: 'var(--sp-5)' }}>
          <PropertyExpenses propertyId={id} propertyName={property?.name} />
        </div>
      </section>

      <Sheet open={sheet === 'edit'} title={t('properties.edit_title')} onClose={() => setSheet('none')}>
        {property ? (
          <PropertyForm
            initial={property}
            submitLabelKey="properties.form.submit_save"
            onSaved={(p) => {
              setProperty(p);
              setSheet('none');
            }}
            onCancel={() => setSheet('none')}
          />
        ) : null}
      </Sheet>

      <Sheet open={sheet === 'unit'} title={t('units.add')} onClose={() => setSheet('none')}>
        <AddUnitForm
          propertyId={id}
          periods={periods}
          onAdded={() => {
            setSheet('none');
            void refresh();
          }}
          onCancel={() => setSheet('none')}
        />
      </Sheet>

      <Sheet open={sheet === 'many'} title={t('properties.sheet.add_many')} onClose={() => setSheet('none')}>
        <AddManyUnitsForm
          propertyId={id}
          onAdded={() => {
            setSheet('none');
            void refresh();
          }}
          onCancel={() => setSheet('none')}
        />
      </Sheet>
    </>
  );
}

export default function PropertyPage() {
  const params = useParams<{ id: string }>();
  const id = typeof params?.id === 'string' ? params.id : '';
  return (
    <>
      <PropertyBody id={id} />
    </>
  );
}
