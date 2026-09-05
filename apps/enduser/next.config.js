const path = require('path');

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
  basePath: '/enduser',
  allowedDevOrigins: ['*.ngrok-free.app', '*.ngrok.app', '*.ngrok.dev'],
};

module.exports = nextConfig;
