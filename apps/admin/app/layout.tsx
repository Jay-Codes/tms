import { Plus_Jakarta_Sans } from 'next/font/google';
import '@tms/ui/tokens.css';

// Admin app is NOT org-themed: platform default theme and font only.
const jakarta = Plus_Jakarta_Sans({ subsets: ['latin'], variable: '--font-jakarta' });

export const metadata = { title: 'TMS — Admin' };

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={jakarta.variable}>
      <body>{children}</body>
    </html>
  );
}
