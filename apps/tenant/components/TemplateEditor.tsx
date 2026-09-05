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
 */

import { Icon } from '@iconify/react';
import { EditorContent, useEditor, type Editor } from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import { useCallback, useEffect, useRef, useState } from 'react';
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

export const SNAPSHOT_BANNER =
  'Editing a template never changes contracts already issued — each contract keeps the terms it was signed with.';

/** Variable labels read as English so the dropdown is usable by a landlord. */
const VARIABLE_LABELS: Record<string, string> = {
  renter_name: "Renter's name",
  unit: 'Unit name',
  property: 'Property name',
  rent: 'Rent amount',
  start_date: 'Start date',
  end_date: 'End date',
  payment_period: 'Payment period',
  org_name: 'Business name',
  term_days: 'Term in days',
  due_day: 'Due day of month',
};

const EDITOR_STYLE = `
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
        padding: '0 var(--sp-2)',
        background: active ? 'var(--primary-soft)' : undefined,
      }}
    >
      <Icon icon={icon} width={18} />
    </button>
  );
}

function Toolbar({ editor }: { editor: Editor }) {
  return (
    <div
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
        label="Heading 1"
        icon="solar:text-bold-square-linear"
        active={editor.isActive('heading', { level: 1 })}
        onClick={() => editor.chain().focus().toggleHeading({ level: 1 }).run()}
      />
      <ToolButton
        label="Heading 2"
        icon="solar:text-square-linear"
        active={editor.isActive('heading', { level: 2 })}
        onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}
      />
      <ToolButton
        label="Heading 3"
        icon="solar:text-circle-linear"
        active={editor.isActive('heading', { level: 3 })}
        onClick={() => editor.chain().focus().toggleHeading({ level: 3 }).run()}
      />
      <span style={{ width: 1, height: 24, background: 'var(--rule)', margin: '0 var(--sp-2)' }} />
      <ToolButton
        label="Bold"
        icon="solar:text-bold-linear"
        active={editor.isActive('bold')}
        onClick={() => editor.chain().focus().toggleBold().run()}
      />
      <ToolButton
        label="Italic"
        icon="solar:text-italic-linear"
        active={editor.isActive('italic')}
        onClick={() => editor.chain().focus().toggleItalic().run()}
      />
      <ToolButton
        label="Underline"
        icon="solar:text-underline-linear"
        active={editor.isActive('underline')}
        onClick={() => editor.chain().focus().toggleUnderline().run()}
      />
      <span style={{ width: 1, height: 24, background: 'var(--rule)', margin: '0 var(--sp-2)' }} />
      <ToolButton
        label="Bulleted list"
        icon="solar:list-linear"
        active={editor.isActive('bulletList')}
        onClick={() => editor.chain().focus().toggleBulletList().run()}
      />
      <ToolButton
        label="Numbered list"
        icon="solar:list-check-linear"
        active={editor.isActive('orderedList')}
        onClick={() => editor.chain().focus().toggleOrderedList().run()}
      />
      <ToolButton
        label="Quote"
        icon="solar:quote-up-linear"
        active={editor.isActive('blockquote')}
        onClick={() => editor.chain().focus().toggleBlockquote().run()}
      />
      <span style={{ width: 1, height: 24, background: 'var(--rule)', margin: '0 var(--sp-2)' }} />
      <ToolButton
        label="Undo"
        icon="solar:undo-left-linear"
        onClick={() => editor.chain().focus().undo().run()}
      />
      <ToolButton
        label="Redo"
        icon="solar:undo-right-linear"
        onClick={() => editor.chain().focus().redo().run()}
      />

      <label htmlFor="tpl_var" style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
        <span style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-soft)' }}>Insert variable</span>
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
          <option value="">Choose…</option>
          {TEMPLATE_VARIABLES.map((v) => (
            <option key={v} value={v}>
              {VARIABLE_LABELS[v] ?? v} {`{{${v}}}`}
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

  const editor = useEditor({
    extensions: [StarterKit],
    content: '',
    immediatelyRender: false,
    editorProps: { attributes: { 'aria-label': 'Template body' } },
    onUpdate: () => setSaved(false),
  });

  const runPreview = useCallback(async () => {
    setPreviewing(true);
    setPreviewError(null);
    try {
      setPreview(unwrapTemplatePreview(await templatesApi.preview(templateId)));
    } catch (e) {
      setPreviewError(toApiError(e));
    } finally {
      setPreviewing(false);
    }
  }, [templateId]);

  useEffect(() => {
    const ac = new AbortController();
    templatesApi
      .get(templateId, ac.signal)
      .then((res) => {
        const t = unwrapTemplate(res);
        setTemplate(t);
        setName(t.name ?? '');
        setIsDefault(Boolean(t.is_default));
        editor?.commands.setContent(t.body_html ?? '');
        setLoadError(null);
        onSavedRef.current?.(t);
      })
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setLoadError(toApiError(e));
      });
    return () => ac.abort();
  }, [templateId, editor]);

  // First preview once the template is on screen; refreshed after every save.
  useEffect(() => {
    if (template) void runPreview();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- one preview per template load
  }, [template?.id]);

  const save = async () => {
    if (!editor) return;
    setBusy(true);
    setSaveError(null);
    setSaved(false);
    try {
      const res = await templatesApi.update(templateId, {
        name: name.trim(),
        body_html: editor.getHTML(),
        is_default: isDefault,
      });
      const t = unwrapTemplate(res);
      setTemplate(t);
      setName(t.name ?? name);
      setIsDefault(Boolean(t.is_default));
      setSaved(true);
      onSavedRef.current?.(t);
      await runPreview();
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
        <span>{SNAPSHOT_BANNER}</span>
      </p>

      <ProblemNote error={saveError} />
      {saved ? <Note>Template saved. New contracts use these terms.</Note> : null}

      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr)', gap: 'var(--sp-5)' }}>
        {/* ------------------------------ editor ------------------------------ */}
        <div style={{ display: 'grid', gap: 'var(--sp-4)', alignContent: 'start', minWidth: 0 }}>
          <Field id="tpl_name" label="Template name" error={saveError?.errors.name}>
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
            <label htmlFor="tpl_default">Default</label>
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
              Set as default — new contracts start from this template
            </label>
          </div>

          <div
            className="tpl-editor"
            style={{ border: '1px solid var(--rule)', borderRadius: 'var(--radius-sm)', background: 'var(--sheet)' }}
          >
            {editor ? <Toolbar editor={editor} /> : null}
            <EditorContent editor={editor} />
          </div>

          <p style={{ fontSize: 'var(--text-sm)', color: 'var(--ink-faint)' }}>
            Variables in double braces are replaced when a contract is written — {'{{renter_name}}'} becomes the
            renter&apos;s name on that contract.
          </p>

          <div style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
            <button type="button" className="btn btn-primary" onClick={() => void save()} disabled={busy || !editor}>
              <Icon icon="solar:diskette-linear" width={20} /> {busy ? 'Saving…' : 'Save template'}
            </button>
            <button type="button" className="btn btn-secondary" onClick={() => void runPreview()} disabled={previewing}>
              <Icon icon="solar:eye-linear" width={20} /> {previewing ? 'Loading…' : 'Preview'}
            </button>
          </div>
        </div>

        {/* ------------------------------ preview ----------------------------- */}
        <div style={{ display: 'grid', gap: 'var(--sp-3)', alignContent: 'start', minWidth: 0 }}>
          <h3 style={{ fontSize: 'var(--text-md)' }}>Preview</h3>
          <ProblemNote error={previewError} />
          {preview ? (
            <DocumentPaper
              caption="Sample values on your letterhead. The saved template is what is previewed."
              html={preview.html}
              logoUrl={preview.logo_url}
              letterheadUrl={preview.letterhead_url}
              displayName={preview.display_name}
              footerText={preview.footer_text}
            />
          ) : previewError ? null : (
            <p style={{ color: 'var(--ink-soft)' }}>Loading preview…</p>
          )}
        </div>
      </div>
    </div>
  );
}
