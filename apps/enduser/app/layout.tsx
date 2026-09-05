import { Bricolage_Grotesque, Archivo, Instrument_Sans, Hanken_Grotesk } from 'next/font/google';
import '@tms/ui/tokens.css';
import './enduser.css';
import { AuthProvider } from '../lib/auth';
import { LocaleProvider } from '../lib/locale';
import { ServiceWorker } from '../components/ServiceWorker';

// Whitelisted org-selectable fonts (see @tms/ui theme.ts). Only the
// active one is used; the rest load lazily without preload cost.
const bricolage = Bricolage_Grotesque({
  subsets: ['latin'],
  axes: ['opsz', 'wdth'],
  variable: '--font-bricolage',
});
const archivo = Archivo({ subsets: ['latin'], variable: '--font-archivo', preload: false });
const instrument = Instrument_Sans({ subsets: ['latin'], variable: '--font-instrument', preload: false });
const hanken = Hanken_Grotesk({ subsets: ['latin'], variable: '--font-hanken', preload: false });

// PWA (SPEC §2, API.md Phase 7). Paths are written with the basePath because
// `metadata` values are emitted verbatim, and the manifest itself must be
// fetched from inside the app's own scope.
// `metadata` is a build-time constant, so it can only speak one language: the
// platform default, Kiswahili, which is also what `<html lang>` ships with.
// lib/locale.tsx re-stamps `document.title` in the renter's language on mount
// and after every client-side navigation.
export const metadata = {
  title: 'Kitabu cha kodi',
  applicationName: 'Kitabu cha kodi',
  manifest: '/enduser/manifest.webmanifest',
  icons: {
    icon: [
      { url: '/enduser/icons/icon-192.png', sizes: '192x192', type: 'image/png' },
      { url: '/enduser/icons/icon-512.png', sizes: '512x512', type: 'image/png' },
    ],
    apple: [{ url: '/enduser/icons/icon-192.png', sizes: '192x192', type: 'image/png' }],
  },
  appleWebApp: { capable: true, title: 'Kitabu cha kodi', statusBarStyle: 'default' as const },
  // `appleWebApp.capable` emits the modern `mobile-web-app-capable`; older
  // iOS still reads the vendor-prefixed name, which Next no longer writes.
  other: { 'apple-mobile-web-app-capable': 'yes' },
};

export const viewport = {
  width: 'device-width',
  initialScale: 1,
  viewportFit: 'cover' as const,
  // Core palette --paper (packages/ui/src/tokens.css); never the org's colour,
  // which is only known after branding loads.
  themeColor: '#fbfbf7',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    // `lang` is corrected to the renter's language on mount (lib/locale.tsx);
    // Kiswahili is the platform default, so it is what the shell ships with.
    <html
      lang="sw"
      className={`${bricolage.variable} ${archivo.variable} ${instrument.variable} ${hanken.variable}`}
    >
      <body>
        <AuthProvider>
          <LocaleProvider>{children}</LocaleProvider>
        </AuthProvider>
        <ServiceWorker />
      </body>
    </html>
  );
}
