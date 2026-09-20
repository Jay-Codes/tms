/**
 * The app's basePath, shared by next.config.js (which reads the same env var)
 * and every place that has to spell an absolute path inside the app: the
 * manifest, icons, and the service worker registration. Next's <Link> and
 * router prepend basePath themselves, so page links never use this.
 *
 * Dev default is '/tenant': the three apps share one origin behind the Go proxy.
 * Deployed on its own domain the app sets NEXT_PUBLIC_BASE_PATH= (empty) and
 * lives at the root.
 */
const raw = process.env.NEXT_PUBLIC_BASE_PATH;
export const BASE_PATH = raw === undefined ? '/tenant' : raw === '/' ? '' : raw.replace(/\/$/, '');

/** Origin of the Go API for cross-origin deploys; '' means same origin. */
export const API_ORIGIN = (process.env.NEXT_PUBLIC_API_URL ?? '').replace(/\/$/, '');
