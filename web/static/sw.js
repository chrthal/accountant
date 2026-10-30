// Service worker for kmu-buchhaltung.
//
// It caches the shell — stylesheet, icons, manifest — and nothing else. HTML is
// always fetched from the network, deliberately: offline data entry is out of
// scope (ADR 0004), and serving a cached bookkeeping page would show yesterday's
// figures as if they were current. That is worse than an error message.
//
// The shell is served network-first: the cache is a fallback for when the
// network is gone, never a substitute for it. Cache-first would keep serving
// the stylesheet of the release the worker was installed with, so an
// upgrade would show old CSS against new markup until the cache name was
// bumped (ADR 0013). Bump CACHE anyway when the shell list changes.

const CACHE = 'shell-v3';

const SHELL = [
  '/static/app.css',
  '/static/app.js',
  '/static/icon-192.png',
  '/static/icon-512.png',
  '/static/manifest.webmanifest',
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE).then((cache) => cache.addAll(SHELL)).then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (event) => {
  const request = event.request;

  // Only ever serve GETs for the shell from cache. Everything else — every
  // page, every form post, every PDF — goes to the network untouched.
  if (request.method !== 'GET') return;

  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (!SHELL.includes(url.pathname)) return;

  event.respondWith(
    fetch(request)
      .then((response) => {
        if (response.ok) {
          const copy = response.clone();
          caches.open(CACHE).then((cache) => cache.put(request, copy));
        }
        return response;
      })
      .catch(() => caches.match(request))
  );
});
