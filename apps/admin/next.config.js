/** @type {import('next').NextConfig} */
const nextConfig = {
  transpilePackages: ['@tms/ui'],
  basePath: '/admin',
  allowedDevOrigins: ['*.ngrok-free.app', '*.ngrok.app', '*.ngrok.dev'],
};

module.exports = nextConfig;
