'use client';

/**
 * One platform template — the Kiswahili and English bodies side by side, the
 * variables the kind allows as insertable chips, a live segment count, a
 * server-rendered preview, the lock, and the version history (API.md Phase 14).
 *
 * The segment count is the money line: a body is billed one credit per segment
 * against the sending org's wallet, so a careless third segment triples every
 * reminder on the platform. It is shown while typing, not after saving.
 *
 * Validation is the API's job — unknown placeholders come back as a 400 with
 * `errors.sw` / `errors.en`. The same check runs locally as a warning so an
 * admin sees the mistake before spending a round trip on it.
 */

import Link from 'next/link';
import { useParams } from 'next/navigation';
import { useCallback, useEffect, useRef, useState } from 'react';
import { Note, ProblemNote } from '../../../../components/FormBits';
import { PageHead } from '../../../../components/PageHead';
import { Sheet } from '../../../../components/Sheet';
import {
  ApiError,
  adminApi,
  toApiError,
  unwrapTemplate,
  type AdminTemplate,
  type AdminTemplatePreview,
  type AdminTemplateVersion,
} from '../../../../lib/api';
import { fmtDateTime } from '../../../../lib/format';
import { kindLabel, smsSegments, unknownVariables } from '../../../../lib/sms';

type Lang = 'sw' | 'en';
const LANGS: { id: Lang; label: string }[] = [
  { id: 'sw', label: 'Kiswahili' },
  { id: 'en', label: 'English' },
];

/** Beyond this a single notification costs more than three credits to send. */
const SEGMENT_WARN = 3;

function Counter({ body }: { body: string }) {
  const { chars, segments, unicode, remaining } = smsSegments(body);
  const loud = segments > SEGMENT_WARN;
  return (
    <span
      style={{
        fontSize: 'var(--text-xs)',
        color: loud ? 'var(--stamp-overdue)' : 'var(--ink-faint)',
        fontVariantNumeric: 'tabular-nums',
      }}
    >
      {chars} chars · {segments} segment{segments === 1 ? '' : 's'} · {remaining} left in this one
      {unicode ? ' · Unicode (70 per segment)' : ''}
      {loud ? ` · over ${SEGMENT_WARN} credits per message` : ''}
    </span>
  );
}

function Editor({
  lang,
  value,
  variables,
  disabled,
  error,
  onChange,
}: {
  lang: Lang;
  value: string;
  variables: string[];
  disabled: boolean;
  error?: string;
  onChange: (v: string) => void;
}) {
  const ref = useRef<HTMLTextAreaElement | null>(null);
  const unknown = unknownVariables(value, variables);

  /** Insert at the caret, keep the caret after what was inserted. */
  const insert = (token: string) => {
    const el = ref.current;
    if (!el) {
      onChange(value + token);
      return;
    }
    const start = el.selectionStart ?? value.length;
    const end = el.selectionEnd ?? start;
    const next = value.slice(0, start) + token + value.slice(end);
    onChange(next);
    requestAnimationFrame(() => {
      el.focus();
      const at = start + token.length;
      el.setSelectionRange(at, at);
    });
  };

  return (
    <div className={error ? 'field invalid' : 'field'} style={{ margin: 0, minWidth: 0 }}>
      <label htmlFor={`body_${lang}`}>{LANGS.find((l) => l.id === lang)?.label}</label>
      <textarea
        id={`body_${lang}`}
        ref={ref}
        className="input"
        rows={7}
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        style={{ resize: 'vertical', fontFamily: 'inherit' }}
      />
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--sp-2)', marginTop: 'var(--sp-2)' }}>
        {variables.map((v) => (
          <button
            key={v}
            type="button"
            className="btn btn-quiet"
            disabled={disabled}
            onClick={() => insert(`{{${v}}}`)}
            title={`Insert {{${v}}} at the cursor`}
            style={{ minHeight: 28, padding: '0 var(--sp-3)', fontSize: 'var(--text-xs)' }}
          >
            {`{{${v}}}`}
          </button>
        ))}
        {variables.length === 0 ? (
          <span className="pencil">this kind takes no variables</span>
        ) : null}
      </div>
      <div style={{ marginTop: 'var(--sp-2)' }}>
        <Counter body={value} />
      </div>
      {error ? <span className="error">{error}</span> : null}
      {!error && unknown.length > 0 ? (
        <span className="error">
          Not allowed for this kind: {unknown.map((v) => `{{${v}}}`).join(', ')}
        </span>
      ) : null}
    </div>
  );
}

function TemplateBody({ kind }: { kind: string }) {
  const [tpl, setTpl] = useState<AdminTemplate | null>(null);
  const [sw, setSw] = useState('');
  const [en, setEn] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);

  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<ApiError | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const [warnings, setWarnings] = useState<string[]>([]);

  const [previewLang, setPreviewLang] = useState<Lang>('sw');
  const [preview, setPreview] = useState<AdminTemplatePreview | null>(null);
  const [previewBusy, setPreviewBusy] = useState(false);
  const [previewError, setPreviewError] = useState<ApiError | null>(null);

  const [lockOpen, setLockOpen] = useState(false);
  const [lockBusy, setLockBusy] = useState(false);

  const [historyOpen, setHistoryOpen] = useState(false);
  const [versions, setVersions] = useState<AdminTemplateVersion[]>([]);
  const [versionsBusy, setVersionsBusy] = useState(false);
  const [versionsError, setVersionsError] = useState<ApiError | null>(null);
  const [revertBusy, setRevertBusy] = useState<number | null>(null);

  const apply = useCallback((t: AdminTemplate) => {
    setTpl(t);
    setSw(t.sw ?? '');
    setEn(t.en ?? '');
  }, []);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setError(null);
      try {
        // There is no single-kind GET; the list is one small document.
        const res = await adminApi.templates(signal);
        const found = (res.items ?? []).find((t) => t.kind === kind) ?? null;
        if (!found) {
          setError(new ApiError(404, { title: 'Unknown kind', detail: `No template named "${kind}".` }));
          setTpl(null);
          return;
        }
        apply(found);
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setError(toApiError(e));
        setTpl(null);
      } finally {
        setLoading(false);
      }
    },
    [kind, apply],
  );

  useEffect(() => {
    const ac = new AbortController();
    void load(ac.signal);
    return () => ac.abort();
  }, [load]);

  const variables = tpl?.variables ?? [];
  const dirty = !!tpl && (sw !== (tpl.sw ?? '') || en !== (tpl.en ?? ''));
  const localBad =
    unknownVariables(sw, variables).length > 0 || unknownVariables(en, variables).length > 0;

  const save = async () => {
    setSaving(true);
    setSaveError(null);
    setDone(null);
    setWarnings([]);
    try {
      const res = await adminApi.saveTemplate(kind, { sw, en });
      const saved = unwrapTemplate(res);
      apply(saved);
      setWarnings(res.warnings ?? []);
      setDone(`Saved as version ${saved.version}.`);
      setPreview(null);
    } catch (e) {
      setSaveError(toApiError(e));
    } finally {
      setSaving(false);
    }
  };

  const runPreview = async (lang: Lang) => {
    setPreviewLang(lang);
    setPreviewBusy(true);
    setPreviewError(null);
    try {
      setPreview(await adminApi.previewTemplate(kind, { language: lang, sample: true }));
    } catch (e) {
      setPreviewError(toApiError(e));
      setPreview(null);
    } finally {
      setPreviewBusy(false);
    }
  };

  const toggleLock = async () => {
    if (!tpl) return;
    setLockBusy(true);
    try {
      const res = await adminApi.lockTemplate(kind, !tpl.locked);
      apply(unwrapTemplate(res));
      setLockOpen(false);
      setDone(
        tpl.locked
          ? 'Unlocked. Landlords can override this kind again.'
          : 'Locked. Landlords can no longer override this kind.',
      );
    } catch (e) {
      setSaveError(toApiError(e));
      setLockOpen(false);
    } finally {
      setLockBusy(false);
    }
  };

  const openHistory = async () => {
    setHistoryOpen(true);
    setVersionsBusy(true);
    setVersionsError(null);
    try {
      const res = await adminApi.templateVersions(kind);
      setVersions(res.items ?? []);
    } catch (e) {
      setVersionsError(toApiError(e));
      setVersions([]);
    } finally {
      setVersionsBusy(false);
    }
  };

  const revert = async (version: number) => {
    setRevertBusy(version);
    setVersionsError(null);
    try {
      const res = await adminApi.revertTemplate(kind, version);
      apply(unwrapTemplate(res));
      setHistoryOpen(false);
      setPreview(null);
      setDone(`Restored version ${version} — saved as a new version.`);
      void load();
    } catch (e) {
      setVersionsError(toApiError(e));
    } finally {
      setRevertBusy(null);
    }
  };

  return (
    <>
      <p style={{ marginBottom: 'var(--sp-3)', fontSize: 'var(--text-sm)' }}>
        <Link href="/templates" style={{ color: 'var(--ink-soft)' }}>
          ← All templates
        </Link>
      </p>

      <PageHead
        title={kindLabel(kind)}
        lead={tpl ? `${kind} · version ${tpl.version}${tpl.updated_by ? ` · last edited by ${tpl.updated_by}` : ''}` : kind}
        actions={
          tpl ? (
            <>
              <button type="button" className="btn btn-quiet" onClick={() => void openHistory()}>
                History
              </button>
              <button
                type="button"
                className={tpl.locked ? 'btn btn-secondary' : 'btn btn-quiet'}
                onClick={() => setLockOpen(true)}
              >
                {tpl.locked ? 'Unlock' : 'Lock'}
              </button>
            </>
          ) : undefined
        }
      />

      <ProblemNote error={error} />
      <ProblemNote error={saveError} />
      {done ? <Note>{done}</Note> : null}
      {warnings.length > 0 ? (
        <p
          role="status"
          style={{
            color: 'var(--stamp-overdue)',
            fontSize: 'var(--text-sm)',
            border: '1px solid var(--stamp-overdue)',
            borderRadius: 'var(--radius-sm)',
            padding: 'var(--sp-2) var(--sp-3)',
            marginTop: 'var(--sp-2)',
            maxWidth: 'none',
          }}
        >
          {warnings.join(' ')}
        </p>
      ) : null}

      {loading && !tpl ? <p style={{ color: 'var(--ink-soft)' }}>Loading…</p> : null}

      {tpl ? (
        <>
          {tpl.locked ? (
            <p
              style={{
                marginTop: 'var(--sp-4)',
                border: '1px solid var(--rule)',
                borderRadius: 'var(--radius-sm)',
                padding: 'var(--sp-3)',
                color: 'var(--ink-soft)',
                maxWidth: '72ch',
              }}
            >
              <span className="stamp stamp-overdue" style={{ marginRight: 'var(--sp-3)' }}>
                Locked
              </span>
              Landlords cannot override this kind — their settings screen shows it read-only and an
              override attempt is refused.
              {kind === 'otp' ? (
                <>
                  {' '}
                  <strong style={{ color: 'var(--ink)' }}>Verification codes ship locked</strong>, because
                  the wording carries the code itself and is what a renter is told to expect; a reworded
                  code message is how a phishing message gets through. These are also exempt from credit
                  billing.
                </>
              ) : null}
            </p>
          ) : null}

          <section style={{ marginTop: 'var(--sp-5)' }}>
            <div
              style={{
                display: 'grid',
                gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))',
                gap: 'var(--sp-5)',
              }}
            >
              <Editor
                lang="sw"
                value={sw}
                variables={variables}
                disabled={saving}
                error={saveError?.errors.sw}
                onChange={setSw}
              />
              <Editor
                lang="en"
                value={en}
                variables={variables}
                disabled={saving}
                error={saveError?.errors.en}
                onChange={setEn}
              />
            </div>

            <div
              style={{
                display: 'flex',
                gap: 'var(--sp-2)',
                marginTop: 'var(--sp-4)',
                alignItems: 'center',
                flexWrap: 'wrap',
              }}
            >
              <button
                type="button"
                className="btn btn-primary"
                disabled={saving || !dirty || localBad || !sw.trim() || !en.trim()}
                onClick={() => void save()}
              >
                {saving ? 'Saving…' : 'Save'}
              </button>
              {dirty ? (
                <button
                  type="button"
                  className="btn btn-quiet"
                  disabled={saving}
                  onClick={() => {
                    setSw(tpl.sw ?? '');
                    setEn(tpl.en ?? '');
                    setSaveError(null);
                  }}
                >
                  Discard changes
                </button>
              ) : null}
              <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                {dirty ? 'Unsaved changes.' : 'Both languages are required; saving records a new version.'}
              </span>
            </div>
          </section>

          <section style={{ marginTop: 'var(--sp-6)', maxWidth: 720 }}>
            <div
              style={{
                display: 'flex',
                alignItems: 'baseline',
                justifyContent: 'space-between',
                gap: 'var(--sp-4)',
                flexWrap: 'wrap',
              }}
            >
              <h2 style={{ fontSize: 'var(--text-lg)' }}>Preview</h2>
              <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
                {LANGS.map((l) => (
                  <button
                    key={l.id}
                    type="button"
                    className={previewLang === l.id && preview ? 'btn btn-secondary' : 'btn btn-quiet'}
                    disabled={previewBusy}
                    onClick={() => void runPreview(l.id)}
                  >
                    {previewBusy && previewLang === l.id ? 'Rendering…' : l.label}
                  </button>
                ))}
              </div>
            </div>
            <p style={{ color: 'var(--ink-soft)', fontSize: 'var(--text-sm)', marginTop: 'var(--sp-2)' }}>
              Rendered by the API with sample values — it shows the <em>saved</em> template, so save
              first to preview an edit.
            </p>
            <ProblemNote error={previewError} />
            {preview ? (
              <div
                style={{
                  marginTop: 'var(--sp-3)',
                  border: '1px solid var(--rule)',
                  borderRadius: 'var(--radius-sm)',
                  padding: 'var(--sp-4)',
                  background: 'var(--surface, var(--paper))',
                }}
              >
                <p style={{ whiteSpace: 'pre-wrap' }}>{preview.body}</p>
                <p style={{ marginTop: 'var(--sp-3)', fontSize: 'var(--text-xs)', color: 'var(--ink-faint)' }}>
                  {preview.segments} segment{preview.segments === 1 ? '' : 's'}
                  {preview.encoding ? ` · ${preview.encoding.toUpperCase()}` : ''} · one credit per segment
                </p>
              </div>
            ) : null}
          </section>
        </>
      ) : null}

      {/* ------------------------------ Lock ------------------------------- */}
      <Sheet
        open={lockOpen}
        title={tpl?.locked ? `Unlock ${kindLabel(kind)}?` : `Lock ${kindLabel(kind)}?`}
        onClose={() => setLockOpen(false)}
      >
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <p style={{ color: 'var(--ink-soft)' }}>
            {tpl?.locked
              ? 'Landlords will be able to override this kind with their own wording again. Existing overrides start applying immediately.'
              : 'Landlords will no longer be able to override this kind. Their settings screen turns read-only and an override attempt is refused.'}
          </p>
          <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            <button
              type="button"
              className={tpl?.locked ? 'btn btn-primary' : 'btn btn-danger'}
              disabled={lockBusy}
              onClick={() => void toggleLock()}
            >
              {lockBusy ? 'Saving…' : tpl?.locked ? 'Unlock this kind' : 'Lock this kind'}
            </button>
            <button type="button" className="btn btn-quiet" onClick={() => setLockOpen(false)}>
              Cancel
            </button>
          </div>
        </div>
      </Sheet>

      {/* ----------------------------- History ------------------------------ */}
      <Sheet
        open={historyOpen}
        title={`${kindLabel(kind)} — version history`}
        width={720}
        onClose={() => setHistoryOpen(false)}
      >
        <div style={{ display: 'grid', gap: 'var(--sp-4)' }}>
          <ProblemNote error={versionsError} />
          {versionsBusy ? <p style={{ color: 'var(--ink-soft)' }}>Loading…</p> : null}
          {!versionsBusy && versions.length === 0 && !versionsError ? (
            <p style={{ color: 'var(--ink-soft)' }}>No earlier versions — this template is as seeded.</p>
          ) : null}
          {versions.map((v) => (
            <div
              key={v.version}
              style={{
                borderTop: '1px solid var(--rule)',
                paddingTop: 'var(--sp-3)',
                display: 'grid',
                gap: 'var(--sp-2)',
              }}
            >
              <div
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'baseline',
                  gap: 'var(--sp-3)',
                  flexWrap: 'wrap',
                }}
              >
                <strong>Version {v.version}</strong>
                <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>
                  {fmtDateTime(v.created_at)}
                  {v.admin_name ? ` · ${v.admin_name}` : ''}
                </span>
              </div>
              <div style={{ fontSize: 'var(--text-sm)' }}>
                <div style={{ color: 'var(--ink-soft)' }}>Kiswahili</div>
                <p style={{ whiteSpace: 'pre-wrap' }}>{v.sw}</p>
                <div style={{ color: 'var(--ink-soft)', marginTop: 'var(--sp-2)' }}>English</div>
                <p style={{ whiteSpace: 'pre-wrap' }}>{v.en}</p>
              </div>
              <div>
                <button
                  type="button"
                  className="btn btn-secondary"
                  disabled={revertBusy !== null || v.version === tpl?.version}
                  onClick={() => void revert(v.version)}
                  style={{ minHeight: 32 }}
                >
                  {revertBusy === v.version
                    ? 'Restoring…'
                    : v.version === tpl?.version
                      ? 'Current version'
                      : 'Restore this version'}
                </button>
              </div>
            </div>
          ))}
        </div>
      </Sheet>
    </>
  );
}

export default function TemplateEditorPage() {
  const params = useParams<{ kind: string }>();
  const kind = String(params?.kind ?? '');
  return <TemplateBody kind={kind} />;
}
