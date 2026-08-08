// The server-push channel: `/api/events` carries everything that happens
// outside a chat turn — background job completions the notifier chose to
// surface, cron results, and command-gate approval requests.

import {
  createContext,
  use,
  useCallback,
  useEffect,
  useMemo,
  useState,
  type FC,
  type ReactNode,
} from "react";
import { checkSession, notifyUnauthorized } from "@/lib/api";

export type Notification = {
  id: string;
  /** "cron" renders with a clock, "alert" for failures, "note" otherwise. */
  kind: "note" | "cron" | "alert";
  title: string;
  body: string;
  at: string;
  read: boolean;
  /** Set when the notification came from a chat thread the user can open. */
  threadId?: string;
};

/** One write from a running background job. */
export type JobProgress = {
  jobId: string;
  kind: string;
  title?: string;
  chunk?: string;
  done: boolean;
  summary?: string;
};

/** A blocked tool call waiting on the user's Allow/Deny. */
export type PendingAsk = {
  id: string;
  /** The hook script's own wording for what it wants approved. */
  prompt: string;
  reason: string;
  at: string;
};

type EventsContextValue = {
  notifications: Notification[];
  unreadCount: number;
  asks: PendingAsk[];
  /** The newest job-progress event, for views that tail a running job. */
  jobProgress: JobProgress | null;
  connected: boolean;
  markAllRead: () => void;
  dismiss: (id: string) => void;
  clearAll: () => void;
  answerAsk: (id: string, allow: boolean) => void;
};

const EventsContext = createContext<EventsContextValue | null>(null);

const MOCK_NOTIFICATIONS: Notification[] = [
  {
    id: "n1",
    kind: "cron",
    title: "morning-brief",
    body: "3 PRs need review; inbox is clear; no alerts overnight.",
    at: new Date(Date.now() - 42 * 60_000).toISOString(),
    read: false,
  },
  {
    id: "n2",
    kind: "note",
    title: "researcher",
    body: "Finished the competitor sweep — 6 sources, notes in ~/notes/sweep.md.",
    at: new Date(Date.now() - 3 * 3600_000).toISOString(),
    read: false,
  },
  {
    id: "n3",
    kind: "alert",
    title: "backup.sh failed",
    body: "exit 2: destination volume not mounted",
    at: new Date(Date.now() - 26 * 3600_000).toISOString(),
    read: true,
  },
];

export const EventsProvider: FC<{ children: ReactNode }> = ({ children }) => {
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [asks, setAsks] = useState<PendingAsk[]>([]);
  const [jobProgress, setJobProgress] = useState<JobProgress | null>(null);
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    const source = new EventSource("/api/events");
    let opened = false;

    source.addEventListener("open", () => {
      opened = true;
      setConnected(true);
    });

    source.addEventListener("notification", (event) => {
      const incoming = JSON.parse((event as MessageEvent).data) as Notification;
      setNotifications((current) => {
        // The server replays recent notifications on connect, so a reconnect
        // must not double them up. Replayed entries carry their server-side
        // read state, which is what keeps the badge cleared across reloads;
        // live ones arrive read: false.
        if (current.some((n) => n.id === incoming.id)) return current;
        return [{ ...incoming, read: incoming.read ?? false }, ...current];
      });
    });

    source.addEventListener("ask", (event) => {
      const incoming = JSON.parse((event as MessageEvent).data) as PendingAsk;
      setAsks((current) => [...current, incoming]);
    });

    source.addEventListener("job", (event) => {
      setJobProgress(JSON.parse((event as MessageEvent).data) as JobProgress);
    });

    source.addEventListener("ask-resolved", (event) => {
      const { id } = JSON.parse((event as MessageEvent).data) as { id: string };
      setAsks((current) => current.filter((ask) => ask.id !== id));
    });

    source.addEventListener("error", () => {
      setConnected(false);
      // Never opened at all means there is no backend (`npm run dev`): seed the
      // samples once so the bell is explorable. A stream that opened and then
      // dropped is a real outage — leave the real list alone and let
      // EventSource reconnect.
      if (!opened) {
        source.close();
        // A stream that never opened is either "no backend" or "no session":
        // EventSource hides the status code, so ask. Seeding the samples on a
        // lapsed session would show sample data in place of a live server —
        // which is the one thing this UI must not do — so check before seeding.
        void checkSession().then((state) => {
          if (state === "login") {
            notifyUnauthorized();
            return;
          }
          setNotifications((current) =>
            current.length === 0 ? MOCK_NOTIFICATIONS : current,
          );
        });
      }
    });

    return () => source.close();
  }, []);

  // Read state and dismissals go to the server too: the replay on the next
  // connect is what the bell shows after a page reload, and a badge that
  // resurrects itself teaches people to ignore it. Both are fire-and-forget —
  // on the mock backend the request just 404s.
  const markAllRead = useCallback(() => {
    setNotifications((c) => c.map((n) => ({ ...n, read: true })));
    void fetch("/api/notifications/seen", { method: "POST" }).catch(() => {});
  }, []);

  const dismiss = useCallback((id: string) => {
    setNotifications((c) => c.filter((n) => n.id !== id));
    void fetch("/api/notifications/dismiss", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ id }),
    }).catch(() => {});
  }, []);

  const clearAll = useCallback(() => {
    setNotifications([]);
    void fetch("/api/notifications/dismiss", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ all: true }),
    }).catch(() => {});
  }, []);

  const answerAsk = useCallback((id: string, allow: boolean) => {
    setAsks((current) => current.filter((ask) => ask.id !== id));
    void fetch(`/api/asks/${encodeURIComponent(id)}`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ allow }),
    }).catch(() => {
      /* the turn times out on its own if the answer never lands */
    });
  }, []);

  const value = useMemo<EventsContextValue>(
    () => ({
      notifications,
      unreadCount: notifications.filter((n) => !n.read).length,
      asks,
      jobProgress,
      connected,
      markAllRead,
      dismiss,
      clearAll,
      answerAsk,
    }),
    [notifications, asks, jobProgress, connected, markAllRead, dismiss, clearAll, answerAsk],
  );

  return <EventsContext value={value}>{children}</EventsContext>;
};

export const useEvents = (): EventsContextValue => {
  const ctx = use(EventsContext);
  if (!ctx) throw new Error("useEvents must be used inside an EventsProvider");
  return ctx;
};
