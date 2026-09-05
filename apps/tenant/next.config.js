/** @type {import('next').NextConfig} */
const nextConfig = {
  transpilePackages: ['@tms/ui'],
  basePath: '/tenant',
  // `make build` sets NEXT_DIST_DIR=.next-build so a production build does not
  // clobber the running dev server's .next directory.
  distDir: process.env.NEXT_DIST_DIR || '.next',
  allowedDevOrigins: ['*.ngrok-free.app', '*.ngrok.app', '*.ngrok.dev'],
};

module.exports = nextConfig;
