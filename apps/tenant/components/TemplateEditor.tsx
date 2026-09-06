'use client';

/**
 * Contract template editor (FLOWS flow 6 step 1): a rich-text body, the
 * variables that get resolved when a contract is written, and a live preview
 * on the org's letterhead.
 *
 * The snapshot rule (FLOWS 6.2) is stated on the page, not just implied: a
 * template edit never reaches a contract that already exists, because each
 * contract keeps `terms_snapshot_html` from the moment it was issued.
 *
 * Editing produces HTML the backend sanitizes on write (API.md: p, br, h1-h3,
 * lists, strong/em/u, tables, blockquote; every attribute except `class` is
 * stripped), so the toolbar deliberately offers nothing outside that set.
 *
 * Phase 13: a template carries two bodies — `body_html` (English) and
 * `body_html_sw`. Each gets its own editor behind a language tab, and the
 * preview renders whichever language is on screen. A template with only an
 * English body still works: the backend falls back to it for a Swahili
 * contract, and the editor says so rather than leaving the tab silently empty.
 */

import { Icon } from '@iconify/react';
import { EditorContent, useEditor, type Editor } from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import { useCallback, useEffect, useRef, useState } from 'react';
import { LOCALE_LABELS, useT, type Locale, type Translator } from '@tms/ui';
import { Field, Note, ProblemNote } from './FormBits';
import { DocumentPaper } from './DocumentPaper';
import {
  ApiError,
  TEMPLATE_VARIABLES,
  templatesApi,
  toApiError,
  unwrapTemplate,
  unwrapTemplatePreview,
  type ContractTemplate,
  type TemplatePreview,
} from '../lib/api';

/** The two bodies a template carries, in the order the tabs show them. */
export const TEMPLATE_LANGS: readonly Locale[] = ['en', 'sw'] as const;

/** Dictionary keys for the variable dropdown, keyed by the API's variable id. */
const VARIABLE_KEYS: Record<string, string> = {
  renter_name: 'tpl.var.renter_name',
  unit: 'tpl.var.unit',
  property: 'tpl.var.property',
  rent: 'tpl.var.rent',
  start_date: 'tpl.var.start_date',
  end_date: 'tpl.var.end_date',
  payment_period: 'tpl.var.payment_period',
  org_name: 'tpl.var.org_name',
  term_days: 'tpl.var.term_days',
  due_day: 'tpl.var.due_day',
};

const EDITOR_STYLE = `
  /* Phone: the toolbar keeps 44px targets and the variable picker takes a
     row of its own instead of being pushed off the right edge by
     \`margin-left: auto\`. */
  @media (max-width: 767px) {
    .tpl-toolbar .btn { min-height: var(--touch-min); }
    .tpl-toolbar .tpl-var { margin-left: 0; width: 100%; }
    .tpl-toolbar .tpl-var select { flex: 1; min-width: 0; width: 100% !important; min-height: var(--touch-min) !important; }
    .tpl-editor .tiptap { min-height: 240px; padding: var(--sp-3); }
  }
  .tpl-editor .tiptap {
    min-height: 340px;
    padding: var(--sp-4);
    outline: none;
    color: var(--ink);
    line-height: var(--leading-body);
  }
  .tpl-editor .tiptap:focus-visible { box-shadow: var(--focus); border-radius: var(--radius-sm); }
  .tpl-editor .tiptap h1 { font-size: var(--text-xl); margin: var(--sp-5) 0 var(--sp-3); }
  .tpl-editor .tiptap h2 { font-size: var(--text-lg); margin: var(--sp-5) 0 var(--sp-3); }
  .tpl-editor .tiptap h3 { font-size: var(--text-md); font-weight: 600; margin: var(--sp-4) 0 var(--sp-2); }
  .tpl-editor .tiptap p { margin: 0 0 var(--sp-3); }
  .tpl-editor .tiptap ul, .tpl-editor .tiptap ol { margin: 0 0 var(--sp-3); padding-left: 1.4em; }
  .tpl-editor .tiptap blockquote {
    margin: 0 0 var(--sp-3);
    padding-left: var(--sp-4);
    border-left: 2px solid var(--rule);
    color: var(--ink-soft);
  }
`;

function ToolButton({
  active,
  onClick,
  icon,
  label,
}: {
  active?: boolean;
  onClick: () => void;
  icon: string;
  label: string;
}) {
  return (
    <button
      type="button"
      className="btn btn-quiet"
      aria-pressed={active}
      aria-label={label}
      title={label}
      onMouseDown={(e) => e.preventDefault()}
      onClick={onClick}
      style={{
        minHeight: 36,
        minWidth: 'var(--touch-min)',
        padding: '0 var(--sp-2)',
        background: active ? 'var(--primary-soft)' : undefined,
      }}
    >
      <Icon icon={icon} width={18} />
    </button>
  );
}

function Toolbar({ editor }: { editor: Editor }) {
  const t = useT();
  return (
    <div
      className="tpl-toolbar"
      style={{
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'center',
        gap: 'var(--sp-1)',
        padding: 'var(--sp-2)',
        borderBottom: '1px solid var(--rule)',
        background: 'var(--sheet-tint)',
      }}
    >
      <ToolButton
        label={t('tpl.tool.h1')}
        icon="solar:text-bold-square-linear"
        active={editor.isActive('heading', { level: 1 })}
        onClick={() => editor.chain().focus().toggleHeading({ level: 1 }).run()}
      />
      <ToolButton
        label={t('tpl.tool.h2')}
        icon="solar:text-square-linear"
        active={editor.isActive('heading', { level: 2 })}
        onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}
      />
      <ToolButton
        label={t('tpl.tool.h3')}
        icon="solar:text-circle-linear"
        active={editor.isActive('heading', { level: 3 })}
        onClick={() => editor.chain().focus().toggleHeading({ level: 3 }).run()}
      />
      <span style={{ width: 1, height: 24, background: 'var(--rule)', margin: '0 var(--sp-2)' }} />
      <ToolButton
        label={t('tpl.tool.bold')}
        icon="solar:text-bold-linear"
        active={editor.isActive('bold')}
        onClick={() => editor.chain().focus().toggleBold().run()}
      />
      <ToolButton
        label={t('tpl.tool.italic')}
        icon="solar:text-italic-linear"
        active={editor.isActive('italic')}
        onClick={() => editor.chain().focus().toggleItalic().run()}
      />
      <ToolButton
        label={t('tpl.tool.underline')}
        icon="solar:text-underline-linear"
        active={editor.isActive('underline')}
        onClick={() => editor.chain().focus().toggleUnderline().run()}
      />
      <span style={{ width: 1, height: 24, background: 'var(--rule)', margin: '0 var(--sp-2)' }} />
      <ToolButton
        label={t('tpl.tool.bullets')}
        icon="solar:list-linear"
        active={editor.isActive('bulletList')}
        onClick={() => editor.chain().focus().toggleBulletList().run()}
      />
      <ToolButton
        label={t('tpl.tool.numbers')}
        icon="solar:list-check-linear"
        active={editor.isActive('orderedList')}
        onClick={() => editor.chain().focus().toggleOrderedList().run()}
      />
      <ToolButton
        label={t('tpl.tool.quote')}
        icon="solar:quote-up-linear"
        active={editor.isActive('blockquote')}
        onClick={() => editor.chain().focus().toggleBlockquote().run()}
      />
      <span style={{ width: 1, height: 24, background: 'var(--rule)', margin: '0 var(--sp-2)' }} />
      <ToolButton
        label={t('tpl.tool.undo')}
        icon="solar:undo-left-linear"
        onClick={() => editor.chain().focus().undo().run()}
      />
      <ToolButton
        label={t('tpl.tool.redo')}
        icon="solar:undo-right-linear"
        onClick={() => editor.chain().focus().redo().run()}
      />

      <label
        htmlFor="tpl_var"
        className="tpl-var"
        style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 'var(--sp-2)' }}
      >
        <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{t('tpl.insert_variable')}</span>
        <select
          id="tpl_var"
          className="input"
          value=""
          style={{ minHeight: 36, width: 'auto' }}
          onChange={(e) => {
            const v = e.target.value;
            if (!v) return;
            editor.chain().focus().insertContent(`{{${v}}}`).run();
            e.target.value = '';
          }}
        >
          <option value="">{t('tpl.choose')}</option>
          {TEMPLATE_VARIABLES.map((v) => (
            <option key={v} value={v}>
              {VARIABLE_KEYS[v] ? t(VARIABLE_KEYS[v]) : v} {`{{${v}}}`}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}

export interface TemplateEditorProps {
  templateId: string;
  /** Fires after every successful save, with the stored template. */
  onSaved?: (t: ContractTemplate) => void;
}

export function TemplateEditor({ templateId, onSaved }: TemplateEditorProps) {
  const t: Translator = useT();
  /** Which of the two bodies is being written — and previewed. */
  const [lang, setLang] = useState<Locale>('en');
  const [template, setTemplate] = useState<ContractTemplate | null>(null);
  const [name, setName] = useState('');
  const [isDefault, setIsDefault] = useState(false);
  const [loadError, setLoadError] = useState<ApiError | null>(null);
  const [saveError, setSaveError] = useState<ApiError | null>(null);
  const [previewError, setPreviewError] = useState<ApiError | null>(null);
  const [preview, setPreview] = useState<TemplatePreview | null>(null);
  const [busy, setBusy] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [saved, setSaved] = useState(false);

  // Kept in a ref so the load effect can announce the template without making
  // an inline parent callback a dependency (and re-running the load).
  const onSavedRef = useRef(onSaved);
  onSavedRef.current = onSaved;

  // One editor per language. Both are created up front (hooks cannot be
  // conditional) and only the active one is mounted into the page.
  const editorEn = useEditor({
    extensions: [StarterKit],
    content: '',
    immediatelyRender: false,
    editorProps: { attributes: { 'aria-label': 'Template body (English)' } },
    onUpdate: () => setSaved(false),
  });
  const editorSw = useEditor({
    extensions: [StarterKit],
    content: '',
    immediatelyRender: false,
    editorProps: { attributes: { 'aria-label': 'Template body (Kiswahili)' } },
    onUpdate: () => setSaved(false),
  });
  const editors: Record<Locale, Editor | null> = { en: editorEn, sw: editorSw };
  const editor = editors[lang];

  const runPreview = useCallback(
    async (previewLang: Locale) => {
      setPreviewing(true);
      setPreviewError(null);
      try {
        setPreview(unwrapTemplatePreview(await templatesApi.preview(templateId, true, previewLang)));
      } catch (e) {
        setPreviewError(toApiError(e));
      } finally {
        setPreviewing(false);
      }
    },
    [templateId],
  );

  useEffect(() => {
    const ac = new AbortController();
    templatesApi
      .get(templateId, ac.signal)
      .then((res) => {
        const tpl = unwrapTemplate(res);
        setTemplate(tpl);
        setName(tpl.name ?? '');
        setIsDefault(Boolean(tpl.is_default));
        editorEn?.commands.setContent(tpl.body_html ?? '');
        editorSw?.commands.setContent(tpl.body_html_sw ?? '');
        setLoadError(null);
        onSavedRef.current?.(tpl);
      })
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setLoadError(toApiError(e));
      });
    return () => ac.abort();
  }, [templateId, editorEn, editorSw]);

  // First preview once the template is on screen, and again whenever the
  // reader switches language or a save changes the stored body.
  useEffect(() => {
    if (template) void runPreview(lang);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- one preview per template/language
  }, [template?.id, lang]);

  /** An untouched TipTap document is `<p></p>`; that is not a body. */
  const bodyOf = (e: Editor | null): string | null => (!e || e.isEmpty ? null : e.getHTML());

  const swWritten = Boolean(editorSw && !editorSw.isEmpty);

  const save = async () => {
    if (!editorEn) return;
    setBusy(true);
    setSaveError(null);
    setSaved(false);
    try {
      const res = await templatesApi.update(templateId, {
        name: name.trim(),
        body_html: editorEn.getHTML(),
        // Clearing the Swahili tab clears the stored Swahili body, which puts
        // the template back on the English fallback.
        body_html_sw: bodyOf(editorSw),
        is_default: isDefault,
      });
      const tpl = unwrapTemplate(res);
      setTemplate(tpl);
      setName(tpl.name ?? name);
      setIsDefault(Boolean(tpl.is_default));
      setSaved(true);
      onSavedRef.current?.(tpl);
      await runPreview(lang);
    } catch (e) {
      setSaveError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  if (loadError) {
    return <ProblemNote error={loadError} />;
  }

  return (
    <div style={{ display: 'grid', gap: 'var(--sp-5)' }}>
      <style>{EDITOR_STYLE}</style>

      <p
        style={{
          display: 'flex',
          alignItems: 'flex-start',
          gap: 'var(--sp-3)',
          padding: 'var(--sp-3) var(--sp-4)',
          border: '1px solid var(--rule)',
          borderLeft: '3px solid var(--primary)',
          background: 'var(--primary-soft)',
          borderRadius: 'var(--radius-sm)',
          fontSize: 'var(--text-sm)',
        }}
      >
        <Icon icon="solar:lock-keyhole-minimalistic-linear" width={20} />
        <span>{t('tpl.snapshot_banner')}</span>
      </p>

      <ProblemNote error={saveError} />
      {saved ? <Note>{t('tpl.saved')}</Note> : null}

      <div
        // Editor and preview side by side only where both are usable: below
        // ~700px of content the pair collapses to one column, which is what a
        // phone and a portrait tablet get.
        style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(340px, 1fr))', gap: 'var(--sp-5)' }}
      >
        {/* ------------------------------ editor ------------------------------ */}
        <div style={{ display: 'grid', gap: 'var(--sp-4)', alignContent: 'start', minWidth: 0 }}>
          <Field id="tpl_name" label={t('tpl.name')} error={saveError?.errors.name}>
            <input
              id="tpl_name"
              className="input"
              value={name}
              maxLength={80}
              onChange={(e) => {
                setName(e.target.value);
                setSaved(false);
              }}
            />
          </Field>

          <div className="field">
            <label htmlFor="tpl_default">{t('tpl.default')}</label>
            <label
              htmlFor="tpl_default"
              style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', minHeight: 'var(--touch-min)', fontWeight: 400 }}
            >
              <input
                id="tpl_default"
                type="checkbox"
                checked={isDefault}
                onChange={(e) => {
                  setIsDefault(e.target.checked);
                  setSaved(false);
                }}
                style={{ width: 18, height: 18 }}
              />
              {t('tpl.default_hint')}
            </label>
          </div>

          {/* ------------------------ language tabs ------------------------ */}
          <div className="wrap-sm" role="tablist" aria-label={t('tpl.body_tablist')} style={{ display: 'flex', gap: 'var(--sp-2)' }}>
            {TEMPLATE_LANGS.map((l) => {
              const active = lang === l;
              const written = l === 'en' ? true : swWritten;
              return (
                <button
                  key={l}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  onClick={() => setLang(l)}
                  style={{
                    minHeight: 'var(--touch-min)',
                    padding: '0 var(--sp-4)',
                    border: `1px solid ${active ? 'var(--primary)' : 'var(--rule)'}`,
                    borderBottom: 'none',
                    borderRadius: 'var(--radius-sm) var(--radius-sm) 0 0',
                    background: active ? 'var(--primary-soft)' : 'transparent',
                    color: 'var(--ink)',
                    fontWeight: active ? 600 : 400,
                    fontSize: 'var(--text-sm)',
                    cursor: 'pointer',
                  }}
                >
                  {LOCALE_LABELS[l]}
                  {written ? null : (
                    <span style={{ marginLeft: 'var(--sp-2)', color: 'var(--ink-faint)', fontWeight: 400 }}>
                      · {t('tpl.body.empty')}
                    </span>
                  )}
                </button>
              );
            })}
          </div>

          <div
            className="tpl-editor"
            role="tabpanel"
            style={{
              border: '1px solid var(--rule)',
              borderRadius: '0 var(--radius-sm) var(--radius-sm) var(--radius-sm)',
              background: 'var(--sheet)',
              marginTop: 'calc(-1 * var(--sp-4))',
            }}
          >
            {editor ? <Toolbar editor={editor} /> : null}
            <EditorContent editor={editor} />
          </div>

          {lang === 'sw' && !swWritten ? (
            <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>{t('tpl.sw_fallback')}</p>
          ) : null}

          <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>
            {t('tpl.variables_note', { example: '{{renter_name}}' })}
          </p>

          <div className="wrap-sm" style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
            <button type="button" className="btn btn-primary" onClick={() => void save()} disabled={busy || !editor}>
              <Icon icon="solar:diskette-linear" width={20} /> {busy ? t('common.saving') : t('tpl.save')}
            </button>
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => void runPreview(lang)}
              disabled={previewing}
            >
              <Icon icon="solar:eye-linear" width={20} />{' '}
              {previewing ? t('common.loading') : t('tpl.preview')}
            </button>
          </div>
        </div>

        {/* ------------------------------ preview ----------------------------- */}
        <div style={{ display: 'grid', gap: 'var(--sp-3)', alignContent: 'start', minWidth: 0 }}>
          <h3 style={{ fontSize: 'var(--text-md)' }}>
            {t('tpl.preview_in', { language: LOCALE_LABELS[lang] })}
          </h3>
          <ProblemNote error={previewError} />
          {preview ? (
            <DocumentPaper
              caption={t('tpl.preview_caption')}
              html={preview.html}
              logoUrl={preview.logo_url}
              letterheadUrl={preview.letterhead_url}
              displayName={preview.display_name}
              footerText={preview.footer_text}
            />
          ) : previewError ? null : (
            <p style={{ color: 'var(--ink-soft)' }}>{t('tpl.preview_loading')}</p>
          )}
        </div>
      </div>
    </div>
  );
}
