'use client';

import { useEffect } from 'react';
import { applyOrgTheme, DEFAULT_THEME, FONT_IDS, type FontId } from '@tms/ui';
import type { PublicBranding } from '../lib/api';

/** Narrow the branding endpoint's free-form `font_id` to the whitelist. */
function toFontId(raw: string | undefined): FontId {
  return (FONT_IDS as readonly string[]).includes(raw ?? '')
    ? (raw as FontId)
    : DEFAULT_THEME.font;
}

/**
 * Paint the org's theme on <html>. Branding arrives with the public unit
 * payload, so a renter who scans a QR sees their landlord's colour and font
 * before they have any account at all (SPEC §2.0, §2.1).
 */
export function useOrgTheme(branding: PublicBranding | null | undefined): void {
  const color = branding?.theme?.primary_color;
  const font = branding?.theme?.font_id;
  useEffect(() => {
    if (!color) return;
    applyOrgTheme({ primaryColor: color, font: toFontId(font) });
  }, [color, font]);
}

/** Landlord's mark at the top of the scan screens: logo (if any) + name. */
export function OrgHeader({ branding }: { branding: PublicBranding }) {
  return (
    <header
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--sp-3)',
        paddingBottom: 'var(--sp-4)',
        borderBottom: '1px solid var(--rule)',
      }}
    >
      {branding.logo_url && (
        // Landlord-uploaded logo served from MinIO; next/image would need the
        // host allow-listed at build time, and this app is CSR-only.
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={branding.logo_url}
          alt=""
          width={40}
          height={40}
          style={{ width: 40, height: 40, objectFit: 'contain', borderRadius: 'var(--radius-sm)' }}
        />
      )}
      <p style={{ margin: 0, fontWeight: 600, color: 'var(--primary)' }}>
        {branding.display_name}
      </p>
    </header>
  );
}
