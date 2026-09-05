'use client';

/**
 * Org theming for the landlord portal.
 *
 * `GET /org/branding` carries the one colour and one font a landlord may set
 * (SPEC §2.0); `applyOrgTheme` from `@tms/ui` derives the pressed/tint/on-primary
 * variants so contrast can never break. Called once when the portal chrome
 * mounts and again whenever the branding form saves, so the change is visible
 * without a reload.
 */

import { applyOrgTheme, DEFAULT_THEME, FONT_IDS, type FontId } from '@tms/ui';
import { brandingApi, unwrapBranding, type OrgBranding } from './api';

export function fontId(value: string | null | undefined): FontId {
  return (FONT_IDS as readonly string[]).includes(String(value))
    ? (value as FontId)
    : DEFAULT_THEME.font;
}

export function applyBranding(branding: OrgBranding | null | undefined): void {
  if (typeof document === 'undefined') return;
  applyOrgTheme({
    primaryColor: branding?.theme?.primary_color || DEFAULT_THEME.primaryColor,
    font: fontId(branding?.theme?.font_id),
  });
}

/**
 * Fetch and apply. Failures are deliberately silent: an unthemed portal is a
 * working portal, and the branding endpoint is not live before Phase 4.
 */
export async function loadAndApplyOrgTheme(signal?: AbortSignal): Promise<void> {
  try {
    applyBranding(unwrapBranding(await brandingApi.get(signal)));
  } catch {
    /* keep the platform default */
  }
}
