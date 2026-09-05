import { Plus_Jakarta_Sans, Inter, Manrope, Figtree } from 'next/font/google';
import '@tms/ui/tokens.css';

// Landlord portal is org-themed like the enduser app: same whitelisted
// fonts, org branding applied at runtime via applyOrgTheme().
const jakarta = Plus_Jakarta_Sans({ subsets: ['latin'], variable: '--font-jakarta' });
const inter = Inter({ subsets: ['latin'], variable: '--font-inter', preload: false });
const manrope = Manrope({ subsets: ['latin'], variable: '--font-manrope', preload: false });
const figtree = Figtree({ subsets: ['latin'], variable: '--font-figtree', preload: false });

export const metadata = { title: 'TMS — Landlord' };

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
