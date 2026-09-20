const path = require('path');

const rawBasePath = process.env.NEXT_PUBLIC_BASE_PATH;
const basePath =
  rawBasePath === undefined ? '/tenant' : rawBasePath === '/' ? '' : rawBasePath.replace(/\/$/, '');

/** @type {import('next').NextConfig} */
const nextConfig = {
  transpilePackages: ['@tms/ui'],
  // Docker image builds a self-contained server (apps/tenant/.next/standalone).
  // outputFileTracingRoot points at the monorepo root so the workspace
  // node_modules and @tms/ui are traced into the bundle. Harmless in dev.
  output: 'standalone',
  outputFileTracingRoot: path.join(__dirname, '../..'),
  // Dev default '/tenant' (one origin, Go proxy routes by prefix). On its own
  // domain the app sets NEXT_PUBLIC_BASE_PATH= (empty) and serves from /.
  // lib/basePath.ts reads the same variable for in-app absolute paths.
  basePath: basePath,
  // `make build` sets NEXT_DIST_DIR=.next-build so a production build does not
  // clobber the running dev server's .next directory.
  distDir: process.env.NEXT_DIST_DIR || '.next',
  allowedDevOrigins: ['*.ngrok-free.app', '*.ngrok.app', '*.ngrok.dev'],
  // The worker is served from /tenant/sw.js so its default scope is already
  // /tenant/; the header is stated anyway so the scope survives the file being
  // moved or proxied, and the worker is never held in a stale HTTP cache.
  async headers() {
    return [
      {
        source: '/sw.js',
        headers: [
          { key: 'Service-Worker-Allowed', value: `${basePath}/` },
          { key: 'Cache-Control', value: 'no-cache, no-store, must-revalidate' },
          { key: 'Content-Type', value: 'application/javascript; charset=utf-8' },
        ],
      },
      {
        source: '/manifest.webmanifest',
        headers: [{ key: 'Content-Type', value: 'application/manifest+json' }],
      },
    ];
  },
};

module.exports = nextConfig;
