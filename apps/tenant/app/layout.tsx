import { Bricolage_Grotesque, Archivo, Instrument_Sans, Hanken_Grotesk } from 'next/font/google';
import '@tms/ui/tokens.css';
import { RegisterSW } from '../components/RegisterSW';
import { AuthProvider } from '../lib/auth';

// Landlord portal is org-themed like the enduser app: same whitelisted
// fonts, org branding applied at runtime via applyOrgTheme().
const bricolage = Bricolage_Grotesque({
  subsets: ['latin'],
  axes: ['opsz', 'wdth'],
  variable: '--font-bricolage',
});
const archivo = Archivo({ subsets: ['latin'], variable: '--font-archivo', preload: false });
const instrument = Instrument_Sans({ subsets: ['latin'], variable: '--font-instrument', preload: false });
const hanken = Hanken_Grotesk({ subsets: ['latin'], variable: '--font-hanken', preload: false });

export const metadata = {
  title: 'TMS — Landlord',
  manifest: '/tenant/manifest.webmanifest',
  appleWebApp: { capable: true, title: 'TMS', statusBarStyle: 'default' as const },
  icons: {
    icon: [{ url: '/tenant/icons/icon-192.png', sizes: '192x192', type: 'image/png' }],
    apple: [{ url: '/tenant/icons/icon-192.png', sizes: '192x192', type: 'image/png' }],
  },
};

// Core palette (packages/ui tokens.css): --primary for the chrome, --paper for
// the ground the app is painted on.
export const viewport = {
  themeColor: '#2b4fd0',
  width: 'device-width',
  initialScale: 1,
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html
      lang="en"
      className={`${bricolage.variable} ${archivo.variable} ${instrument.variable} ${hanken.variable}`}
    >
      <body>
        <AuthProvider>{children}</AuthProvider>
        <RegisterSW />
      </body>
    </html>
  );
}
