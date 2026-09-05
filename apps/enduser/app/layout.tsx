import { Plus_Jakarta_Sans, Inter, Manrope, Figtree } from 'next/font/google';
import '@tms/ui/tokens.css';

// Whitelisted org-selectable fonts (see @tms/ui theme.ts). Only the
// active one is used; the rest load lazily without preload cost.
const jakarta = Plus_Jakarta_Sans({ subsets: ['latin'], variable: '--font-jakarta' });
const inter = Inter({ subsets: ['latin'], variable: '--font-inter', preload: false });
const manrope = Manrope({ subsets: ['latin'], variable: '--font-manrope', preload: false });
const figtree = Figtree({ subsets: ['latin'], variable: '--font-figtree', preload: false });

export const metadata = { title: 'TMS — End User' };

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html
      lang="en"
      className={`${jakarta.variable} ${inter.variable} ${manrope.variable} ${figtree.variable}`}
    >
      <body>{children}</body>
    </html>
  );
}
