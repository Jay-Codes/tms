'use client';

export default function Home() {
  return (
    <main
      style={{
        minHeight: '100vh',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        background: '#10b981',
        color: 'white',
      }}
    >
      <h1>Hello, Tenant 🏢</h1>
      <p>TMS tenant app — dev preview</p>
    </main>
  );
}
