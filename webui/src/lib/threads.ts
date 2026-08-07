// Conversations. The server owns the list and the history; the browser keeps
// only which thread is open, so a reload — or another device — resumes exactly
// where you were.

export type Thread = {
  id: string;
  /** Empty until renamed; the preview names the thread until then. */
  title: string;
  /** The opening of the first thing asked in this thread. */
  preview: string;
  createdAt: string;
  updatedAt: string;
};

/** The AI SDK message shape a thread is rehydrated from. */
export type StoredMessage = {
  id: string;
  role: "user" | "assistant";
  parts: { type: "text"; text: string }[];
};

const json = async <T>(path: string, init?: RequestInit): Promise<T> => {
  const res = await fetch(path, {
    ...init,
    headers: { accept: "application/json", ...init?.headers },
  });
  if (!res.ok) throw new Error(`${path}: ${res.status}`);
  return (await res.json()) as T;
};

export const listThreads = async (): Promise<Thread[]> =>
  (await json<{ threads: Thread[] }>("/api/threads")).threads;

export const loadThreadMessages = async (id: string): Promise<StoredMessage[]> => {
  const { messages } = await json<{ messages: StoredMessage[] }>(
    `/api/threads/${encodeURIComponent(id)}/messages`,
  );
  return messages;
};

export const renameThread = (id: string, title: string) =>
  json<Thread>(`/api/threads/${encodeURIComponent(id)}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ title }),
  });

export const deleteThread = (id: string) =>
  json<{ deleted: boolean }>(`/api/threads/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });

/**
 * Mints an id for a conversation that has not been sent yet. The server
 * records the thread on its first message, so an abandoned draft never becomes
 * an empty entry in the list.
 */
export const newThreadId = (): string =>
  `t-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;

/**
 * What to call a thread in the list: the name you gave it, else the opening of
 * what you asked — what a conversation was about identifies it far better than
 * when it happened. A thread with neither falls back to its start time.
 */
export const threadLabel = (thread: Thread): string => {
  if (thread.title) return thread.title;
  if (thread.preview) return thread.preview;

  const started = new Date(thread.createdAt || thread.updatedAt);
  if (Number.isNaN(started.getTime())) return "New chat";

  const now = new Date();
  const sameDay =
    started.getFullYear() === now.getFullYear() &&
    started.getMonth() === now.getMonth() &&
    started.getDate() === now.getDate();

  return sameDay
    ? started.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })
    : started.toLocaleDateString(undefined, {
        month: "short",
        day: "numeric",
        hour: "numeric",
        minute: "2-digit",
      });
};
