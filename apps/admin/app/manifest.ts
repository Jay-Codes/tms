import type { MetadataRoute } from 'next';
import { BASE_PATH } from '../lib/basePath';

// Served at `${BASE_PATH}/manifest.webmanifest`. A route rather than a static
// file so start_url, scope and icon paths follow NEXT_PUBLIC_BASE_PATH.
export default function manifest(): MetadataRoute.Manifest {
  return {
    name: "TMS Admin",
    short_name: "TMS Admin",
    description: "Platform administration for the TMS tenancy management system.",
    start_url: `${BASE_PATH}/`,
    scope: `${BASE_PATH}/`,
    display: "standalone",
    orientation: "any",
    background_color: "#fbfbf7",
    theme_color: "#1c2b5a",
    icons: [
      { src: `${BASE_PATH}/icon-192.png`, sizes: "192x192", type: "image/png", purpose: "any" },
      { src: `${BASE_PATH}/icon-512.png`, sizes: "512x512", type: "image/png", purpose: "any" },
      { src: `${BASE_PATH}/icon-maskable-512.png`, sizes: "512x512", type: "image/png", purpose: "maskable" },
    ],
  };
}
