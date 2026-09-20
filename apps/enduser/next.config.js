const path = require('path');

const rawBasePath = process.env.NEXT_PUBLIC_BASE_PATH;
const basePath =
  rawBasePath === undefined ? '/enduser' : rawBasePath === '/' ? '' : rawBasePath.replace(/\/$/, '');

/** @type {import('next').NextConfig} */
const nextConfig = {
  transpilePackages: ['@tms/ui'],
  // Docker image builds a self-contained server (apps/enduser/.next/standalone).
  // outputFileTracingRoot points at the monorepo root so the workspace
  // node_modules and @tms/ui are traced into the bundle. Harmless in dev.
  output: 'standalone',
  outputFileTracingRoot: path.join(__dirname, '../..'),
  // Builds use a separate dist dir so `make build` cannot clobber the
  // running dev server's .next (see DECISIONS.md).
  distDir: process.env.NEXT_DIST_DIR || '.next',
  // Dev default '/enduser' (one origin, Go proxy routes by prefix). On its own
  // domain the app sets NEXT_PUBLIC_BASE_PATH= (empty) and serves from /.
  // lib/basePath.ts reads the same variable for in-app absolute paths.
  basePath: basePath,
  allowedDevOrigins: ['*.ngrok-free.app', '*.ngrok.app', '*.ngrok.dev'],
};

module.exports = nextConfig;
