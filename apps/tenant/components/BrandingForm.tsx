'use client';

/**
 * Org branding (SPEC §5.2, API.md Branding): the display name renters see, the
 * theme (a preset, or hand-set tokens, plus one whitelisted font — see
 * <ThemePanel>), the logo and letterhead images, and the footer line printed
 * under every contract.
 *
 * The theme is validated client-side with the same contrast table the backend
 * enforces, so Save is disabled rather than rejected; a server 400 still shows
 * its own `failures` in case the two ever disagree.
 *
 * Uploads follow the presign → PUT → complete dance: the backend signs a MinIO
 * URL, the browser PUTs the file straight at storage (no cookies on a signed
 * URL), then the backend is told to adopt the object.
 */

import { Icon } from '@iconify/react';
import { validateTheme } from '@tms/ui';
import { useCallback, useEffect, useRef, useState } from 'react';
import { Field, Note, ProblemNote } from './FormBits';
import {
  ApiError,
  brandingApi,
  themeFailures,
  toApiError,
  unwrapBranding,
  uploadToPresignedUrl,
  type BrandingAsset,
  type OrgBranding,
  type ThemeContrastFailure,
} from '../lib/api';
import { applyBranding, resolveTheme } from '../lib/branding';
import { LEDGER_DRAFT, ThemePanel, draftFromTheme, type ThemeDraft } from './ThemePanel';

const ACCEPT = 'image/png,image/jpeg';
const MAX_BYTES = 2 * 1024 * 1024;

function AssetField({
  asset,
  label,
  hint,
  url,
  onChanged,
}: {
  asset: BrandingAsset;
  label: string;
  hint: string;
  url: string | null;
  onChanged: (b: OrgBranding) => void;
}) {
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  const pick = async (file: File) => {
    setError(null);
    if (!['image/png', 'image/jpeg'].includes(file.type)) {
      setError(new ApiError(0, { detail: 'Choose a PNG or JPEG image.' }));
      return;
    }
    if (file.size > MAX_BYTES) {
      setError(new ApiError(0, { detail: 'That image is larger than 2 MB.' }));
      return;
    }
    setBusy(true);
    try {
      const ticket = await brandingApi.uploadTicket(asset, file.type, file.size);
      await uploadToPresignedUrl(ticket, file);
      onChanged(unwrapBranding(await brandingApi.uploadComplete(asset, ticket.object_key)));
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setBusy(false);
      if (inputRef.current) inputRef.current.value = '';
    }
  };

  const remove = async () => {
    setBusy(true);
    setError(null);
    try {
      onChanged(unwrapBranding(await brandingApi.remove(asset)));
    } catch (e) {
      setError(toApiError(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="field">
      <label htmlFor={`asset_${asset}`}>{label}</label>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-4)', flexWrap: 'wrap' }}>
        <div
          style={{
            width: asset === 'letterhead' ? 260 : 96,
            height: 96,
            border: '1px solid var(--rule)',
            borderRadius: 'var(--radius-sm)',
            background: 'var(--sheet)',
            display: 'grid',
            placeItems: 'center',
            overflow: 'hidden',
          }}
        >
          {url ? (
            /* eslint-disable-next-line @next/next/no-img-element -- presigned MinIO URL */
            <img src={url} alt={`${label} preview`} style={{ maxWidth: '100%', maxHeight: '100%' }} />
          ) : (
            <Icon icon="solar:gallery-linear" width={24} color="var(--ink-faint)" />
          )}
        </div>
        <div style={{ display: 'flex', gap: 'var(--sp-2)', flexWrap: 'wrap' }}>
          <input
            ref={inputRef}
            id={`asset_${asset}`}
            type="file"
            accept={ACCEPT}
            style={{ display: 'none' }}
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) void pick(f);
            }}
          />
          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy}
            onClick={() => inputRef.current?.click()}
          >
            <Icon icon="solar:upload-linear" width={20} /> {busy ? 'Working…' : url ? 'Replace' : 'Upload'}
          </button>
          {url ? (
            <button type="button" className="btn btn-quiet" disabled={busy} onClick={() => void remove()}>
              Remove
            </button>
          ) : null}
        </div>
      </div>
      <span className="hint">{hint}</span>
      {error ? (
        <span role="alert" className="error" style={{ color: 'var(--stamp-overdue)' }}>
          {error.detail}
        </span>
      ) : null}
    </div>
  );
}

export function BrandingForm({ onSaved }: { onSaved?: (b: OrgBranding) => void }) {
  const [branding, setBranding] = useState<OrgBranding | null>(null);
  const [displayName, setDisplayName] = useState('');
  const [theme, setTheme] = useState<ThemeDraft>(LEDGER_DRAFT);
  const [footer, setFooter] = useState('');
  const [failures, setFailures] = useState<ThemeContrastFailure[]>([]);
  const [loadError, setLoadError] = useState<ApiError | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);

  const adopt = useCallback(
    (b: OrgBranding) => {
      setBranding(b);
      setDisplayName(b.display_name ?? '');
      setTheme(draftFromTheme(resolveTheme(b), { source: b.theme?.source }));
      setFooter(b.document_footer_text ?? '');
      setFailures([]);
      applyBranding(b);
      onSaved?.(b);
    },
    [onSaved],
  );

  useEffect(() => {
    const ac = new AbortController();
    brandingApi
      .get(ac.signal)
      .then((res) => {
        adopt(unwrapBranding(res));
        setLoadError(null);
      })
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setLoadError(toApiError(e));
      });
    return () => ac.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- load once
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setSaved(false);
    setFailures([]);
    try {
      const res = await brandingApi.save({
        display_name: displayName.trim(),
        theme: {
          // The base preset always rides along; `tokens` only once a colour was
          // hand-edited, so an untouched preset stays linked to the platform's
          // copy and picks up any future correction to it.
          preset_id: theme.preset_id,
          ...(theme.customised ? { tokens: theme.tokens } : {}),
          font_id: theme.font_id,
        },
        document_footer_text: footer.trim() ? footer.trim() : null,
      });
      adopt(unwrapBranding(res));
      setSaved(true);
    } catch (err) {
      setError(toApiError(err));
      setFailures(themeFailures(err));
    } finally {
      setBusy(false);
    }
  };

  if (loadError) {
    return <ProblemNote error={loadError} />;
  }

  if (!branding) {
    return <p style={{ color: 'var(--ink-soft)' }}>Loading…</p>;
  }

  const contrastFailures = validateTheme(theme.tokens);

  return (
    <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-5)', maxWidth: 720 }} noValidate>
      <ProblemNote error={error} />
      {saved ? <Note>Branding saved. The portal is already using it.</Note> : null}

      <Field
        id="display_name"
        label="Display name"
        hint="What renters see on the scan page and on contract documents."
        error={error?.errors.display_name}
      >
        <input
          id="display_name"
          className="input"
          value={displayName}
          onChange={(e) => {
            setDisplayName(e.target.value);
            setSaved(false);
          }}
        />
      </Field>

      <ThemePanel
        draft={theme}
        onChange={(next) => {
          setTheme(next);
          setSaved(false);
          setFailures([]);
        }}
        serverFailures={failures}
      />

      <AssetField
        asset="logo"
        label="Logo"
        hint="PNG or JPEG up to 2 MB. Shown on the renter's scan page and above contracts with no letterhead."
        url={branding.logo_url}
        onChanged={adopt}
      />

      <AssetField
        asset="letterhead"
        label="Letterhead"
        hint="A wide banner printed across the top of every contract document. PNG or JPEG up to 2 MB."
        url={branding.letterhead_url}
        onChanged={adopt}
      />

      <Field
        id="footer_text"
        label="Document footer"
        hint="Address, phone or signature line printed under every contract. Up to 500 characters."
        error={error?.errors.document_footer_text}
      >
        <textarea
          id="footer_text"
          className="input"
          rows={3}
          maxLength={500}
          value={footer}
          onChange={(e) => {
            setFooter(e.target.value);
            setSaved(false);
          }}
        />
      </Field>

      <div>
        <button type="submit" className="btn btn-primary" disabled={busy || contrastFailures.length > 0}>
          {busy ? 'Saving…' : 'Save branding'}
        </button>
        {contrastFailures.length ? (
          <p className="error" style={{ marginTop: 'var(--sp-2)' }}>
            Fix the contrast on{' '}
            {contrastFailures.map((f) => `${f.pair} (${f.ratio.toFixed(2)}:1)`).join(', ')} before
            saving.
          </p>
        ) : null}
      </div>
    </form>
  );
}
