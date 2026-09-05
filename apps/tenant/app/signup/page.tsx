'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useState } from 'react';
import { AuthCard } from '../../components/AuthCard';
import { Field, ProblemNote } from '../../components/FormBits';
import { ApiError, authApi } from '../../lib/api';
import { useMe } from '../../lib/auth';

/** Client-side checks are UX only — the backend is authoritative. */
function localErrors(f: { org_name: string; owner_name: string; email: string; phone: string; password: string; confirm: string }) {
  const e: Record<string, string> = {};
  if (!f.org_name.trim()) e.org_name = 'Enter your business name.';
  if (!f.owner_name.trim()) e.owner_name = 'Enter your full name.';
  if (!f.email.trim()) e.email = 'Enter an email address.';
  if (!f.phone.trim()) e.phone = 'Enter a phone number, e.g. 0712 345 678.';
  if (f.password.length < 8) e.password = 'Use at least 8 characters.';
  if (f.confirm !== f.password) e.confirm = 'The two passwords do not match.';
  return e;
}

export default function SignupPage() {
  const router = useRouter();
  const { refresh } = useMe();
  const [form, setForm] = useState({
    org_name: '',
    owner_name: '',
    email: '',
    phone: '',
    password: '',
    confirm: '',
  });
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);

  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((f) => ({ ...f, [k]: e.target.value }));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const local = localErrors(form);
    setFieldErrors(local);
    if (Object.keys(local).length > 0) return;

    setBusy(true);
    setError(null);
    try {
      await authApi.signupOrg({
        org_name: form.org_name.trim(),
        owner_name: form.owner_name.trim(),
        email: form.email.trim(),
        phone: form.phone.trim(),
        password: form.password,
      });
      await refresh();
      router.replace('/setup');
    } catch (err) {
      const ae = err instanceof ApiError ? err : new ApiError(0, { detail: String(err) });
      setError(ae);
      setFieldErrors(ae.errors);
      setBusy(false);
    }
  };

  return (
    <AuthCard
      title="Register your business"
      lead="One account for the business owner. Staff are invited afterwards."
      footer={
        <>
          Already registered? <Link href="/login">Sign in</Link>.
        </>
      }
    >
      <form onSubmit={submit} style={{ display: 'grid', gap: 'var(--sp-4)' }} noValidate>
        <ProblemNote error={error} />
        <Field id="org_name" label="Business name" hint="Shown to renters, e.g. JJnE Rentals." error={fieldErrors.org_name}>
          <input id="org_name" className="input" value={form.org_name} onChange={set('org_name')} autoComplete="organization" />
        </Field>
        <Field id="owner_name" label="Your full name" error={fieldErrors.owner_name}>
          <input id="owner_name" className="input" value={form.owner_name} onChange={set('owner_name')} autoComplete="name" />
        </Field>
        <Field id="email" label="Email" hint="We send a verification link here." error={fieldErrors.email}>
          <input id="email" className="input" type="email" value={form.email} onChange={set('email')} autoComplete="email" />
        </Field>
        <Field id="phone" label="Phone" hint="07…, 2557… or +2557… all work." error={fieldErrors.phone}>
          <input id="phone" className="input" type="tel" value={form.phone} onChange={set('phone')} autoComplete="tel" />
        </Field>
        <Field id="password" label="Password" hint="At least 8 characters." error={fieldErrors.password}>
          <input id="password" className="input" type="password" value={form.password} onChange={set('password')} autoComplete="new-password" />
        </Field>
        <Field id="confirm" label="Confirm password" error={fieldErrors.confirm}>
          <input id="confirm" className="input" type="password" value={form.confirm} onChange={set('confirm')} autoComplete="new-password" />
        </Field>
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? 'Creating…' : 'Create business account'}
        </button>
      </form>
    </AuthCard>
  );
}
