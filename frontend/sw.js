// POS Simulator Service Worker v3
const CACHE_NAME = "pos-v3";

// Install - no pre-cache
self.addEventListener("install", (e) => {
  self.skipWaiting();
});

// Activate - clear old caches
self.addEventListener("activate", (e) => {
  e.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k)))
    )
  );
  self.clients.claim();
});

// Fetch - NETWORK FIRST (always get fresh content)
self.addEventListener("fetch", (e) => {
  const url = new URL(e.request.url);

  // API/WS requests - network only
  if (url.pathname.startsWith("/api/") || url.pathname === "/ws" || url.pathname === "/health") {
    return;
  }

  // Everything else - network first, fallback to cache
  e.respondWith(
    fetch(e.request)
      .then((response) => {
        const clone = response.clone();
        caches.open(CACHE_NAME).then((cache) => cache.put(e.request, clone));
        return response;
      })
      .catch(() => caches.match(e.request))
  );
});
