/** @type {import('next').NextConfig} */
const nextConfig = {
  transpilePackages: ['@tms/ui'],
  // Builds use a separate dist dir so `make build` cannot clobber the
  // running dev server's .next (see DECISIONS.md).
  distDir: process.env.NEXT_DIST_DIR || '.next',
  basePath: '/enduser',
  allowedDevOrigins: ['*.ngrok-free.app', '*.ngrok.app', '*.ngrok.dev'],
};

module.exports = nextConfig;
