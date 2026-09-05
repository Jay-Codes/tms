'use client';

/**
 * "Add Rent Book to your home screen."
 *
 * Chrome/Edge on Android fire `beforeinstallprompt` when the app is
 * installable; we hold onto the event and offer it as a quiet ruled note on
 * the renter's home screen rather than letting the browser nag. Dismissal is
 * remembered in localStorage, so the hint is shown once per device until the
 * renter clears their data.
 *
 * Browsers without the event (Safari, Firefox) simply never see the banner —
 * there is no prompt to trigger, and a "tap Share then Add to Home Screen"
 * tutorial is not worth the room on a rent book.
 */

import { useEffect, useState } from 'react';
import { Icon } from '@iconify/react';

const DISMISSED_KEY = 'tms.enduser.install-hint-dismissed';

interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>;
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>;
}

function wasDismissed(): boolean {
  try {
    return window.localStorage.getItem(DISMISSED_KEY) === '1';
  } catch {
    // Private mode / storage blocked — show the hint rather than crash.
    return false;
  }
}

function remember(): void {
  try {
    window.localStorage.setItem(DISMISSED_KEY, '1');
  } catch {
    // Nothing to do: worst case the hint comes back next visit.
  }
}

export function InstallPrompt() {
  const [event, setEvent] = useState<BeforeInstallPromptEvent | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (wasDismissed()) return;
    // Already running as an installed app — nothing to offer.
    if (window.matchMedia?.('(display-mode: standalone)').matches) return;

    const onPrompt = (e: Event) => {
      e.preventDefault();
      setEvent(e as BeforeInstallPromptEvent);
    };
    const onInstalled = () => {
      remember();
      setEvent(null);
    };

    window.addEventListener('beforeinstallprompt', onPrompt);
    window.addEventListener('appinstalled', onInstalled);
    return () => {
      window.removeEventListener('beforeinstallprompt', onPrompt);
      window.removeEventListener('appinstalled', onInstalled);
    };
  }, []);

  if (!event) return null;

  async function install() {
    if (!event) return;
    setBusy(true);
    try {
      await event.prompt();
      await event.userChoice;
    } catch {
      // The browser refused to show it (already used, or user gesture lost).
    } finally {
      // The event is single-use either way.
      remember();
      setEvent(null);
      setBusy(false);
    }
  }

  return (
    <section
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--sp-3)',
        padding: 'var(--sp-3)',
        borderLeft: '3px solid var(--primary)',
        background: 'var(--sheet-tint)',
        borderRadius: 'var(--radius-sm)',
      }}
    >
      <Icon icon="solar:smartphone-linear" width={22} aria-hidden />
      <p style={{ margin: 0, flex: 1, fontSize: 'var(--text-sm)' }}>
        Add Rent Book to your home screen.
      </p>
      <button
        type="button"
        className="btn btn-secondary"
        disabled={busy}
        onClick={() => void install()}
        style={{ width: 'auto', height: 'var(--touch-min)', paddingInline: 'var(--sp-3)' }}
      >
        Add
      </button>
      <button
        type="button"
        className="btn btn-quiet"
        aria-label="Dismiss"
        onClick={() => {
          remember();
          setEvent(null);
        }}
        style={{ width: 'auto', height: 'var(--touch-min)', paddingInline: 'var(--sp-2)' }}
      >
        <Icon icon="solar:close-circle-linear" width={20} aria-hidden />
      </button>
    </section>
  );
}
