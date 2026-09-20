import type { MetadataRoute } from 'next';
import { BASE_PATH } from '../lib/basePath';

// Served at `${BASE_PATH}/manifest.webmanifest`. A route rather than a static
// file so start_url, scope and icon paths follow NEXT_PUBLIC_BASE_PATH.
export default function manifest(): MetadataRoute.Manifest {
  return {
    name: "Kitabu cha kodi",
    short_name: "Kitabu cha kodi",
    description: "Upangaji wako, malipo yako na mkataba wako \u2014 katika kitabu kimoja cha kodi.",
    id: `${BASE_PATH}/`,
    start_url: `${BASE_PATH}/`,
    scope: `${BASE_PATH}/`,
    display: "standalone",
    orientation: "portrait",
    background_color: "#fbfbf7",
    theme_color: "#fbfbf7",
    lang: "sw",
    dir: "ltr",
    icons: [
      // The static manifest said "any maskable"; Next's type takes one
      // purpose per entry, so each icon is listed twice.
      { src: `${BASE_PATH}/icons/icon-192.png`, sizes: "192x192", type: "image/png", purpose: "any" },
      { src: `${BASE_PATH}/icons/icon-192.png`, sizes: "192x192", type: "image/png", purpose: "maskable" },
      { src: `${BASE_PATH}/icons/icon-512.png`, sizes: "512x512", type: "image/png", purpose: "any" },
      { src: `${BASE_PATH}/icons/icon-512.png`, sizes: "512x512", type: "image/png", purpose: "maskable" },
    ],
  };
}
