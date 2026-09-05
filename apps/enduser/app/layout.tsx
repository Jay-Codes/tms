import { Bricolage_Grotesque, Archivo, Instrument_Sans, Hanken_Grotesk } from 'next/font/google';
import '@tms/ui/tokens.css';

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

export const metadata = { title: 'TMS — End User' };

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html
      lang="en"
      className={`${bricolage.variable} ${archivo.variable} ${instrument.variable} ${hanken.variable}`}
    >
      <body>{children}</body>
    </html>
  );
}
