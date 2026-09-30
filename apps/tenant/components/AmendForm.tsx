'use client';

/**
 * Phase 22.4 / 31 — change or renew a running contract. The result is a
 * draft only the org can see (Phase 31, maker-checker): a manager submits it
 * for an owner's approval; an owner may approve their own draft at once. Once
 * approved the renter gets it with the note in their own language and signs
 * or declines it. The running contract keeps governing until the effective
 * date; on activation the backend moves money already paid for later periods
 * onto the new one. Nothing here computes any of that — the form only offers
 * the dates the backend will accept and sends what changed.
 */

import { EditorContent, useEditor } from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import { useEffect, useState } from 'react';
import { useT } from '@tms/ui';
import { Field, ProblemNote } from './FormBits';
import { useTemplateList } from './TemplateBits';
import { EDITOR_STYLE, Toolbar } from './TemplateEditor';
import {
  ApiError,
  allowedEffectiveDates,
  contractsApi,
  periodsApi,
  templatesApi,
  toApiError,
  unwrapContract,
  unwrapTemplate,
  type AmendInput,
  type Contract,
  type PaymentPeriod,
  type ScheduleRow,
} from '../lib/api';
import { useMe } from '../lib/auth';
import { fmtDate, fmtTZS, todayISO } from '../lib/format';

export type AmendMode = 'amend' | 'renew';

/** What the drafter does after saving. */
type Then = 'save' | 'submit' | 'approve';

const NOTE_MAX = 200;

/**
 * The contract's own wording, in template form (`{{variables}}` kept so a
 * rent change still reaches the text). Starts from the draft's wording, else
 * the template's body in the document's language.
 */
function WordingEditor({
  initial,
  templateId,
  language,
  onChange,
}: {
  initial: string | null;
  templateId: string | null;
  language: string;
  onChange: (html: string) => void;
}) {
  const t = useT();
  const [loaded, setLoaded] = useState(false);
  const editor = useEditor({
    extensions: [StarterKit],
    content: '',
    immediatelyRender: false,
    editorProps: { attributes: { 'aria-label': t('contracts.amend.wording') } },
    onUpdate: ({ editor: e }) => onChange(e.getHTML()),
  });

  useEffect(() => {
    if (!editor || loaded) return;
    if (initial) {
      editor.commands.setContent(initial);
      onChange(initial);
      setLoaded(true);
      return;
    }
    if (!templateId) {
      setLoaded(true);
      return;
    }
    const ac = new AbortController();
    templatesApi
      .get(templateId, ac.signal)
      .then((r) => {
        const tpl = unwrapTemplate(r);
        const body = language === 'sw' && tpl.body_html_sw ? tpl.body_html_sw : tpl.body_html;
        editor.commands.setContent(body);
        onChange(body);
      })
      .catch(() => undefined)
      .finally(() => setLoaded(true));
    return () => ac.abort();
  }, [editor, initial, templateId, language, loaded, onChange]);

  return (
    <div className="tpl-editor" style={{ border: '1px solid var(--rule)', borderRadius: 'var(--radius-sm)', background: 'var(--sheet)' }}>
      <style>{EDITOR_STYLE}</style>
      {editor ? <Toolbar editor={editor} /> : null}
      <EditorContent editor={editor} />
    </div>
  );
}

export function AmendForm({
  contract,
  schedules,
  mode,
  draft,
  onDone,
  onCancel,
}: {
  /** The running contract being changed. */
  contract: Contract;
  /** This contract's own periods; the future period starts are the choices. */
  schedules: ScheduleRow[];
  mode: AmendMode;
  /** Phase 31 — an existing draft to edit instead of writing a new one. */
  draft?: Contract | null;
  onDone: (saved: Contract) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const { org: me } = useMe();
  // Phase 31: an owner approves (their own draft too); a manager submits.
  const isOwner = me?.role === 'org_owner';
  const { user } = useMe();
  // An owner may approve a new draft or their own; someone else's draft goes
  // through submit (the backend answers 409 otherwise).
  const canApprove = isOwner && (!draft || draft.amendment?.stage === 'submitted' || draft.amendment?.drafted_by === user?.id);
  const templates = useTemplateList();
  const [periods, setPeriods] = useState<PaymentPeriod[]>([]);
  const today = todayISO();
  const starts = Array.from(
    new Set(schedules.map((s) => s.period_start).filter((d) => d >= today && d !== contract.end_date)),
  ).sort();
  const a = draft?.amendment ?? null;

  const [effective, setEffective] = useState(
    draft?.amendment_effective_date ?? (mode === 'renew' ? contract.end_date : (starts[0] ?? contract.end_date)),
  );
  const [noteSw, setNoteSw] = useState(a?.note_sw ?? '');
  const [noteEn, setNoteEn] = useState(a?.note_en ?? '');
  const [rent, setRent] = useState(draft ? String(draft.rent_amount) : '');
  const [periodId, setPeriodId] = useState(draft?.payment_period?.id ?? '');
  const [dueDay, setDueDay] = useState(draft?.due_day ? String(draft.due_day) : '');
  const [termDays, setTermDays] = useState(
    draft ? String(draft.term_days) : mode === 'renew' ? String(contract.term_days) : '',
  );
  const [templateId, setTemplateId] = useState(draft?.template_id ?? '');
  const [ownWording, setOwnWording] = useState(Boolean(a?.body_html));
  const [wording, setWording] = useState(a?.body_html ?? '');
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState<Then | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    periodsApi
      .list(false, ac.signal)
      .then((r) => setPeriods((r.items ?? []).filter((p) => p.active)))
      .catch(() => setPeriods([]));
    return () => ac.abort();
  }, []);

  const allowed = allowedEffectiveDates(error);
  const renewal = effective === contract.end_date;
  const notesReady = noteSw.trim().length > 0 && noteEn.trim().length > 0;
  // An owner edits a submitted draft; anything else of theirs they may approve.
  const submitted = a?.stage === 'submitted';

  const save = async (then: Then) => {
    setBusy(then);
    setError(null);
    // Blank means "carry it from the running contract" — never sent.
    const body: AmendInput = { effective_date: effective, note_sw: noteSw.trim(), note_en: noteEn.trim() };
    if (rent) body.rent_amount = Math.round(Number(rent));
    if (periodId) body.payment_period_id = periodId;
    if (dueDay) body.due_day = Math.round(Number(dueDay));
    if (termDays) body.term_days = Math.round(Number(termDays));
    if (templateId) body.template_id = templateId;
    if (ownWording && wording.trim()) body.body_html = wording;
    else if (draft && a?.body_html) body.body_html = '';
    try {
      let saved = draft
        ? unwrapContract(await contractsApi.updateAmendment(draft.id, body))
        : unwrapContract(await contractsApi.amend(contract.id, body));
      if (then === 'submit') saved = unwrapContract(await contractsApi.submitAmendment(saved.id));
      if (then === 'approve') saved = unwrapContract(await contractsApi.approveAmendment(saved.id));
      onDone(saved);
    } catch (err) {
      setError(toApiError(err));
    } finally {
      setBusy(null);
    }
  };

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void save('save');
      }}
      style={{ display: 'grid', gap: 'var(--sp-4)' }}
      noValidate
    >
      <ProblemNote error={error} />
      {allowed.length ? (
        <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
          {t('contracts.amend.allowed', { dates: allowed.map((d) => fmtDate(d)).join(', ') })}
        </p>
      ) : null}
      <p style={{ color: 'var(--ink-soft)' }}>{t('contracts.amend.lead_review')}</p>

      <Field id="am_effective" label={t('contracts.amend.effective')} error={error?.errors.effective_date}>
        <select id="am_effective" className="input" value={effective} onChange={(e) => setEffective(e.target.value)}>
          {starts.map((d) => (
            <option key={d} value={d}>
              {fmtDate(d)}
            </option>
          ))}
          <option value={contract.end_date}>
            {t('contracts.amend.at_end', { date: fmtDate(contract.end_date) })}
          </option>
        </select>
      </Field>

      <Field
        id="am_note_sw"
        label={t('contracts.amend.note_sw')}
        hint={t('contracts.amend.note_count', { n: noteSw.length, max: NOTE_MAX })}
        error={error?.errors.note_sw}
      >
        <textarea
          id="am_note_sw"
          className="input"
          rows={2}
          maxLength={NOTE_MAX}
          lang="sw"
          value={noteSw}
          onChange={(e) => setNoteSw(e.target.value)}
          placeholder={t('contracts.amend.note_sw_ph')}
        />
      </Field>
      <Field
        id="am_note_en"
        label={t('contracts.amend.note_en')}
        hint={t('contracts.amend.note_count', { n: noteEn.length, max: NOTE_MAX })}
        error={error?.errors.note_en}
      >
        <textarea
          id="am_note_en"
          className="input"
          rows={2}
          maxLength={NOTE_MAX}
          lang="en"
          value={noteEn}
          onChange={(e) => setNoteEn(e.target.value)}
          placeholder={renewal ? t('contracts.amend.reason_ph_renew') : t('contracts.amend.note_en_ph')}
        />
      </Field>
      <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>{t('contracts.amend.note_hint')}</p>

      <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>{t('contracts.amend.optional_note')}</p>

      <div className="stack-sm" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--sp-4)' }}>
        <Field
          id="am_rent"
          label={t('contracts.amend.rent', { days: contract.rent_period_days })}
          hint={rent ? fmtTZS(Number(rent)) : t('contracts.amend.now', { value: fmtTZS(contract.rent_amount) })}
          error={error?.errors.rent_amount}
        >
          <input
            id="am_rent"
            className="input num"
            type="number"
            min={1}
            step={1}
            inputMode="numeric"
            value={rent}
            onChange={(e) => setRent(e.target.value)}
          />
        </Field>
        <Field id="am_period" label={t('contracts.new.period')} error={error?.errors.payment_period_id}>
          <select id="am_period" className="input" value={periodId} onChange={(e) => setPeriodId(e.target.value)}>
            <option value="">
              {t('contracts.amend.keep', { value: contract.payment_period?.label ?? '—' })}
            </option>
            {periods.map((p) => (
              <option key={p.id} value={p.id}>
                {p.label}
              </option>
            ))}
          </select>
        </Field>
        <Field
          id="am_due_day"
          label={t('contracts.new.due_day')}
          hint={contract.due_day ? t('contracts.amend.now', { value: String(contract.due_day) }) : undefined}
          error={error?.errors.due_day}
        >
          <input
            id="am_due_day"
            className="input num"
            type="number"
            min={1}
            max={28}
            value={dueDay}
            onChange={(e) => setDueDay(e.target.value)}
          />
        </Field>
        <Field
          id="am_term"
          label={t('contracts.new.term')}
          hint={renewal ? t('contracts.amend.term_hint_renew') : t('contracts.amend.term_hint')}
          error={error?.errors.term_days}
        >
          <input
            id="am_term"
            className="input num"
            type="number"
            min={1}
            value={termDays}
            onChange={(e) => setTermDays(e.target.value)}
          />
        </Field>
      </div>

      <Field id="am_template" label={t('tpl.one')} error={error?.errors.template_id}>
        <select
          id="am_template"
          className="input"
          value={templateId}
          disabled={templates === null}
          onChange={(e) => setTemplateId(e.target.value)}
        >
          <option value="">{t('contracts.amend.template_same')}</option>
          {(templates ?? []).map((x) => (
            <option key={x.id} value={x.id}>
              {x.name}
            </option>
          ))}
        </select>
      </Field>

      <label style={{ display: 'flex', gap: 'var(--sp-2)', alignItems: 'center' }}>
        <input type="checkbox" checked={ownWording} onChange={(e) => setOwnWording(e.target.checked)} />
        <span>{t('contracts.amend.own_wording')}</span>
      </label>
      {ownWording ? (
        <div style={{ display: 'grid', gap: 'var(--sp-2)' }}>
          <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{t('contracts.amend.own_wording_hint')}</p>
          <WordingEditor
            initial={a?.body_html ?? null}
            templateId={templateId || contract.template_id || null}
            language={draft?.language ?? contract.language ?? 'en'}
            onChange={setWording}
          />
          {error?.errors.body_html ? (
            <p style={{ color: 'var(--stamp-overdue)', fontSize: 'var(--text-sm)' }}>{error.errors.body_html}</p>
          ) : null}
        </div>
      ) : null}

      <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
        {canApprove ? (
          <button
            type="button"
            className="btn btn-primary"
            disabled={busy !== null || !notesReady}
            onClick={() => void save('approve')}
          >
            {busy === 'approve' ? t('common.saving') : t('contracts.amend.approve_send')}
          </button>
        ) : (
          <button
            type="button"
            className="btn btn-primary"
            disabled={busy !== null || !notesReady || submitted}
            onClick={() => void save('submit')}
          >
            {busy === 'submit' ? t('common.saving') : t('contracts.amend.submit_approval')}
          </button>
        )}
        <button type="submit" className="btn btn-secondary" disabled={busy !== null || !notesReady}>
          {busy === 'save' ? t('common.saving') : t('contracts.amend.save_draft')}
        </button>
        <button type="button" className="btn btn-quiet" onClick={onCancel} disabled={busy !== null}>
          {t('common.cancel')}
        </button>
      </div>
    </form>
  );
}

/**
 * Phase 31 — edit an existing draft: loads the contract it amends and that
 * contract's periods (the effective-date choices), then the same form.
 */
export function AmendDraftForm({
  draft,
  onDone,
  onCancel,
}: {
  draft: Contract;
  onDone: (saved: Contract) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const [running, setRunning] = useState<Contract | null>(null);
  const [schedules, setSchedules] = useState<ScheduleRow[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const oldId = draft.supersedes_contract_id;
  useEffect(() => {
    if (!oldId) return;
    const ac = new AbortController();
    Promise.all([contractsApi.get(oldId, ac.signal), contractsApi.schedules(oldId, ac.signal)])
      .then(([c, s]) => {
        setRunning(unwrapContract(c));
        setSchedules(s.items ?? []);
      })
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
      });
    return () => ac.abort();
  }, [oldId]);
  if (error) return <ProblemNote error={error} />;
  if (!running || !schedules) return <p style={{ color: 'var(--ink-soft)' }}>{t('common.loading')}</p>;
  const mode: AmendMode = draft.amendment_effective_date === running.end_date ? 'renew' : 'amend';
  return <AmendForm contract={running} schedules={schedules} mode={mode} draft={draft} onDone={onDone} onCancel={onCancel} />;
}
