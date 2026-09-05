import { Bricolage_Grotesque } from 'next/font/google';
import '@tms/ui/tokens.css';

// Admin app is NOT org-themed: platform default theme and font only.
const bricolage = Bricolage_Grotesque({
  subsets: ['latin'],
  axes: ['opsz', 'wdth'],
  variable: '--font-bricolage',
});

export const metadata = { title: 'TMS — Admin' };

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={bricolage.variable}>
      <body>{children}</body>
    </html>
  );
}
