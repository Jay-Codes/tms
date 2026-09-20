import type { MetadataRoute } from 'next';
import { BASE_PATH } from '../lib/basePath';

// Served at `${BASE_PATH}/manifest.webmanifest`. A route rather than a static
// file so start_url, scope and icon paths follow NEXT_PUBLIC_BASE_PATH.
export default function manifest(): MetadataRoute.Manifest {
  return {
    name: "TMS Landlord",
    short_name: "TMS",
    description: "Properties, renters, contracts and rent collection.",
    start_url: `${BASE_PATH}/`,
    scope: `${BASE_PATH}/`,
    display: "standalone",
    orientation: "any",
    background_color: "#fbfbf7",
    theme_color: "#2b4fd0",
    icons: [
      { src: `${BASE_PATH}/icons/icon-192.png`, sizes: "192x192", type: "image/png", purpose: "any" },
      { src: `${BASE_PATH}/icons/icon-512.png`, sizes: "512x512", type: "image/png", purpose: "any" },
      { src: `${BASE_PATH}/icons/icon-512.png`, sizes: "512x512", type: "image/png", purpose: "maskable" },
    ],
  };
}
