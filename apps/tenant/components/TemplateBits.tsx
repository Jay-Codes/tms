'use client';

/**
 * Phase 22 — which contract template a tenancy is written on. The backend
 * resolves it (approval pick → unit → property → org default); these pieces
 * only list the org's templates and say where a resolved one came from.
 */

import { useEffect, useState } from 'react';
import type { Translator } from '@tms/ui';
import { templatesApi, type ContractTemplateSummary, type TemplateSource } from '../lib/api';

/** The org's templates for a picker; an empty list if they cannot be read. */
export function useTemplateList(): ContractTemplateSummary[] | null {
  const [items, setItems] = useState<ContractTemplateSummary[] | null>(null);
  useEffect(() => {
    const ac = new AbortController();
    templatesApi
      .list(ac.signal)
      .then((r) => setItems(r.items ?? []))
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setItems([]);
      });
    return () => ac.abort();
  }, []);
  return items;
}

/** "Set on this unit" / "From the property" / "Organisation default". */
export function templateSourceLabel(t: Translator, source: TemplateSource): string {
  return t(`tpl.source.${source}`);
}
