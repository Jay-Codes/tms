'use client';

import { useEffect, useMemo } from 'react';
import type { PublicBranding } from '../lib/api';
import { rememberOrgLocale } from '../lib/locale';
import {
  applyTheme,
  readCachedTheme,
  toResolvedTheme,
  writeCachedTheme,
  type ResolvedTheme,
} from '../lib/theme';

/**
 * Paint the org's theme on <html>. Branding arrives with the public unit
 * payload, so a renter who scans a QR sees their landlord's paper, ink and
 * font before they have any account at all (SPEC §2.0, §2.1).
 *
 * Phase 12: the whole resolved token set is applied, not just the primary
 * colour, and the result is cached under the org slug so the signed-in
 * screens (and the next visit) open on the same paper — see OrgThemeSync.
 *
 * Until the branding lands, a cached theme is painted rather than the
 * platform default: a renter re-scanning their own landlord's QR should not
 * watch the app flash white first.
 */
export function useOrgTheme(
  branding: PublicBranding | null | undefined,
  slug?: string | null,
): void {
  const theme: ResolvedTheme | null = useMemo(
    () => (branding?.theme ? toResolvedTheme(branding.theme) : null),
    [branding?.theme],
  );

  useEffect(() => {
    if (theme) return; // the real thing is below
    const cached = readCachedTheme();
    if (cached) applyTheme(cached.theme);
  }, [theme]);

  useEffect(() => {
    if (!theme) return;
    applyTheme(theme);
    if (slug) writeCachedTheme(slug, theme);
  }, [theme, slug]);

  /* Phase 13: the same payload says which language this landlord's renters
     read by default — remembered for the pre-auth screens (lib/locale.tsx). */
  useEffect(() => {
    rememberOrgLocale(branding);
  }, [branding]);
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
