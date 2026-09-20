import type { Metadata, Viewport } from 'next';
import { Bricolage_Grotesque } from 'next/font/google';
import '@tms/ui/tokens.css';
import { ServiceWorker } from '../components/ServiceWorker';
import { AuthProvider } from '../lib/auth';
import { BASE_PATH } from '../lib/basePath';

// Admin app is NOT org-themed (SPEC §2.0): platform default palette and the
// default font only. applyOrgTheme() is never called here.
const bricolage = Bricolage_Grotesque({
  subsets: ['latin'],
  axes: ['opsz', 'wdth'],
  variable: '--font-bricolage',
});

export const metadata: Metadata = {
  title: 'TMS — Admin',
  description: 'Platform administration for the TMS tenancy management system.',
  manifest: `${BASE_PATH}/manifest.webmanifest`,
  applicationName: 'TMS Admin',
  appleWebApp: { capable: true, title: 'TMS Admin', statusBarStyle: 'default' },
  icons: {
    icon: [
      { url: `${BASE_PATH}/icon-192.png`, sizes: '192x192', type: 'image/png' },
      { url: `${BASE_PATH}/icon-512.png`, sizes: '512x512', type: 'image/png' },
    ],
    apple: [{ url: `${BASE_PATH}/icon-192.png`, sizes: '192x192', type: 'image/png' }],
  },
};

export const viewport: Viewport = {
  themeColor: '#1c2b5a',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={bricolage.variable}>
      <body>
        <AuthProvider>{children}</AuthProvider>
        <ServiceWorker />
      </body>
    </html>
  );
}
