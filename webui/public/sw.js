// shell3's service worker. Its only job is push: the browser wakes it when a
// notification arrives, even with every tab closed.
//
// It deliberately does not cache anything. The app is served by the local
// binary and updated by restarting it; a caching worker would serve a stale
// interface after an upgrade and be confusing to clear.

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));

self.addEventListener("push", (event) => {
  let payload = {};
  try {
    payload = event.data ? event.data.json() : {};
  } catch {
    payload = { title: "shell3", body: event.data ? event.data.text() : "" };
  }

  const title = payload.title || "shell3";
  const options = {
    body: payload.body || "",
    // Raster, not the SVG favicon: Chrome silently ignores SVG for notification
    // icons, so an SVG here means a notification with no mark at all. The badge
    // is drawn as a monochrome mask, hence white on transparent.
    icon: "/notification-icon.png",
    badge: "/notification-badge.png",
    // Same tag replaces rather than stacks, so a retried delivery — or a
    // repeatedly pressed Test — shows once instead of piling up.
    tag: payload.id || payload.kind || "shell3",
    data: { threadId: payload.threadId || null },
    // Failures are worth interrupting for; routine completions are not.
    requireInteraction: payload.kind === "alert",
  };

  // Showing the notification is the point; telling open pages about it is what
  // makes the Test button a test rather than a hope. A page that is not open
  // simply has nobody to tell.
  event.waitUntil(
    Promise.all([
      self.registration.showNotification(title, options),
      self.clients
        .matchAll({ type: "window", includeUncontrolled: true })
        .then((clients) => {
          for (const client of clients) {
            client.postMessage({ type: "push-received", kind: payload.kind, id: payload.id });
          }
        }),
    ]),
  );
});

// Clicking a notification focuses an open tab rather than opening a second one.
self.addEventListener("notificationclick", (event) => {
  event.notification.close();

  event.waitUntil(
    self.clients
      .matchAll({ type: "window", includeUncontrolled: true })
      .then((clients) => {
        for (const client of clients) {
          if ("focus" in client) return client.focus();
        }
        return self.clients.openWindow("/");
      }),
  );
});
