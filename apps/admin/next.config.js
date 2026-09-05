const path = require('path');

/** @type {import('next').NextConfig} */
const nextConfig = {
  transpilePackages: ['@tms/ui'],
  // Docker image builds a self-contained server (apps/admin/.next/standalone).
  // outputFileTracingRoot points at the monorepo root so the workspace
  // node_modules and @tms/ui are traced into the bundle. Harmless in dev.
  output: 'standalone',
  outputFileTracingRoot: path.join(__dirname, '../..'),
  basePath: '/admin',
  distDir: process.env.NEXT_DIST_DIR || '.next',
  allowedDevOrigins: ['*.ngrok-free.app', '*.ngrok.app', '*.ngrok.dev'],
};

module.exports = nextConfig;
