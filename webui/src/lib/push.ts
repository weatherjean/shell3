// Web push: notifications that arrive when the tab is closed.
//
// Push needs a secure context, so this works on localhost and over an https
// tunnel but not over plain http to another machine — the UI says so rather
// than offering a toggle that silently cannot work.

export type PushState =
  | "unsupported" // no service worker / push API, or an insecure origin
  | "unavailable" // the server has no VAPID keys
  | "denied" // the user refused permission; only they can undo it
  | "off"
  | "on";

type PushInfo = { available: boolean; publicKey?: string; subscriptions?: number };

/**
 * Bounds one step of the handshake and says which one failed.
 *
 * Every await in here can pend forever in the wild: a permission prompt the
 * browser decides not to show never settles, `serviceWorker.ready` never
 * resolves if no worker activates, and `subscribe()` waits on a push service
 * that may be unreachable. Unbounded, any of those leaves the toggle disabled
 * with nothing said — which reads as the button being broken. A named timeout
 * turns each one into something the UI can report.
 */
const step = async <T,>(label: string, work: Promise<T>, ms = 12000): Promise<T> => {
  let timer: ReturnType<typeof setTimeout>;
  const limit = new Promise<never>((_, reject) => {
    timer = setTimeout(
      () => reject(new Error(`${label} did not finish within ${Math.round(ms / 1000)}s`)),
      ms,
    );
  });
  try {
    return await Promise.race([work, limit]);
  } finally {
    clearTimeout(timer!);
  }
};

const supported = (): boolean =>
  typeof navigator !== "undefined" &&
  "serviceWorker" in navigator &&
  "PushManager" in window &&
  window.isSecureContext;

const serverInfo = async (): Promise<PushInfo> => {
  const res = await fetch("/api/push", { headers: { accept: "application/json" } });
  if (!res.ok) throw new Error(String(res.status));
  return (await res.json()) as PushInfo;
};

const registration = async (): Promise<ServiceWorkerRegistration> =>
  navigator.serviceWorker.register("/sw.js").then(() => navigator.serviceWorker.ready);

/** The current state, without prompting for anything. */
export const pushState = async (): Promise<PushState> => {
  if (!supported()) return "unsupported";
  try {
    const info = await serverInfo();
    if (!info.available) return "unavailable";
  } catch {
    return "unavailable";
  }
  if (Notification.permission === "denied") return "denied";

  try {
    const reg = await step("Starting the service worker", registration(), 6000);
    const existing = await step(
      "Checking for a subscription",
      reg.pushManager.getSubscription(),
      6000,
    );
    return existing ? "on" : "off";
  } catch {
    // The panel still has to render; "off" simply offers to turn it on.
    return "off";
  }
};

/**
 * Asks permission, subscribes, and registers the subscription with the server.
 * Returns the resulting state so the caller can report it.
 */
export const enablePush = async (): Promise<PushState> => {
  if (!supported()) return "unsupported";

  const info = await step("Reading the server's push keys", serverInfo());
  if (!info.available || !info.publicKey) return "unavailable";

  // A prompt the browser suppresses never settles, so this is bounded too.
  const permission = await step("Asking for permission", Notification.requestPermission());
  if (permission !== "granted") return permission === "denied" ? "denied" : "off";

  const reg = await step("Starting the service worker", registration());
  const existing = await step("Checking for a subscription", reg.pushManager.getSubscription());
  const subscription =
    existing ??
    (await step(
      "Subscribing with the push service",
      reg.pushManager.subscribe({
        // Every push must show a notification; silent pushes are not allowed.
        userVisibleOnly: true,
        applicationServerKey: decodeKey(info.publicKey),
      }),
      20000, // the one leg that leaves this machine
    ));

  const res = await step(
    "Registering with shell3",
    fetch("/api/push/subscribe", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(subscription.toJSON()),
    }),
  );
  if (!res.ok) throw new Error(`could not register: ${res.status}`);
  return "on";
};

/** Unsubscribes this browser and tells the server to forget it. */
export const disablePush = async (): Promise<PushState> => {
  if (!supported()) return "unsupported";

  const reg = await step("Starting the service worker", registration());
  const subscription = await step(
    "Checking for a subscription",
    reg.pushManager.getSubscription(),
  );
  if (!subscription) return "off";

  const endpoint = subscription.endpoint;
  await step("Unsubscribing", subscription.unsubscribe());
  await fetch("/api/push/subscribe", {
    method: "DELETE",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ endpoint }),
  }).catch(() => {
    /* the server prunes dead endpoints on its next send anyway */
  });
  return "off";
};

/** What a test told us: the push came back to this browser, or only left. */
export type TestResult = "delivered" | "sent";

/**
 * Sends a test notification and waits for the service worker to say it arrived.
 *
 * The POST only proves the server accepted the job — delivery is asynchronous
 * and can fail well after the response. So the worker posts back on receipt and
 * this listens for it, which is the difference between "we tried" and "it
 * works". A confirmed delivery with no visible notification points at the OS
 * rather than at shell3.
 */
export const testPush = async (): Promise<TestResult> => {
  // Listen before sending: a fast round trip must not beat the listener.
  const confirmed = awaitDelivery(8000);
  const res = await fetch("/api/push/test", { method: "POST" });
  if (!res.ok) {
    const detail = (await res.text()).trim();
    throw new Error(detail || `HTTP ${res.status}`);
  }
  return (await confirmed) ? "delivered" : "sent";
};

const awaitDelivery = (timeoutMs: number): Promise<boolean> =>
  new Promise((resolve) => {
    if (!("serviceWorker" in navigator)) return resolve(false);
    const done = (ok: boolean) => {
      clearTimeout(timer);
      navigator.serviceWorker.removeEventListener("message", onMessage);
      resolve(ok);
    };
    const onMessage = (event: MessageEvent) => {
      if ((event.data as { type?: string } | null)?.type === "push-received") done(true);
    };
    const timer = setTimeout(() => done(false), timeoutMs);
    navigator.serviceWorker.addEventListener("message", onMessage);
  });

/**
 * VAPID keys travel as base64url; PushManager wants the raw bytes in a plain
 * ArrayBuffer (a Uint8Array over a possibly-shared buffer does not satisfy it).
 */
const decodeKey = (base64url: string): ArrayBuffer => {
  const padding = "=".repeat((4 - (base64url.length % 4)) % 4);
  const base64 = (base64url + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);

  const buffer = new ArrayBuffer(raw.length);
  const bytes = new Uint8Array(buffer);
  for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
  return buffer;
};
