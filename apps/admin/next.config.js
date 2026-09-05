/** @type {import('next').NextConfig} */
const nextConfig = {
  transpilePackages: ['@tms/ui'],
  basePath: '/admin',
  distDir: process.env.NEXT_DIST_DIR || '.next',
  allowedDevOrigins: ['*.ngrok-free.app', '*.ngrok.app', '*.ngrok.dev'],
};

module.exports = nextConfig;
