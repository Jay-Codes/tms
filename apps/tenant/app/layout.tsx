import { Bricolage_Grotesque, Archivo, Instrument_Sans, Hanken_Grotesk } from 'next/font/google';
import '@tms/ui/tokens.css';
import { RegisterSW } from '../components/RegisterSW';
import { AuthProvider } from '../lib/auth';
import { LocaleProvider } from '../lib/locale';

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
      // The real language is set on the client by <LocaleProvider> as soon as
      // the stored/account locale resolves; Swahili is the platform default.
      lang="sw"
      className={`${bricolage.variable} ${archivo.variable} ${instrument.variable} ${hanken.variable}`}
    >
      <body>
        {/*
          No flash of platform blue: the last resolved org theme is cached by
          lib/branding.ts under `tms.tenant.theme` with its derived variable map
          already expanded, and painted here before React hydrates. The branding
          fetch in <Shell> then confirms or replaces it.
        */}
        <script
          dangerouslySetInnerHTML={{
            __html: `(function(){try{var e=JSON.parse(localStorage.getItem('tms.tenant.theme')||'null');if(!e||!e.vars)return;var r=document.documentElement;for(var k in e.vars)r.style.setProperty(k,e.vars[k]);if(e.theme&&e.theme.dark)r.setAttribute('data-theme','dark');}catch(_){}})();`,
          }}
        />
        <AuthProvider>
          <LocaleProvider>{children}</LocaleProvider>
        </AuthProvider>
        <RegisterSW />
      </body>
    </html>
  );
}
