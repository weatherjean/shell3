// The shell3 HTTP contract, as seen from the browser.
//
// Every endpoint here is served by the Go binary. When the app runs without a
// backend (`npm run dev` against the mock), the capability probe fails and the
// UI degrades on purpose: chat streams canned text, voice falls back to the
// browser's own speech APIs, and the views show sample data — clearly labelled.
// A backend that IS present and fails shows an error instead, never a sample.

export type Capabilities = {
  agent: { name: string; model: string };
  models: { id: string; name: string }[];
  voice: { stt: boolean; tts: boolean };
  imagegen: boolean;
  /** False when the capability probe failed — the UI is running on mocks. */
  live: boolean;
};

export const MOCK_CAPABILITIES: Capabilities = {
  agent: { name: "agent", model: "mock" },
  models: [{ id: "mock", name: "Mock agent" }],
  voice: { stt: false, tts: false },
  imagegen: false,
  live: false,
};

/**
 * Thrown when the server refuses a request for want of a session. Its own class
 * because "not logged in" is not a failure to report — it is a reason to show
 * the login screen, and the two must not be confused: see fetchCapabilities.
 */
export class Unauthorized extends Error {
  constructor() {
    super("authentication required");
    this.name = "Unauthorized";
  }
}

/** Notified whenever any request turns out to need a login. */
let onUnauthorized: () => void = () => {};

/**
 * Registers the callback that returns the app to the login screen. A session
 * can lapse mid-visit — it expires, it is revoked, the password changes — so
 * every request is a place this can be discovered, not just the first.
 */
export const setUnauthorizedHandler = (handler: () => void) => {
  onUnauthorized = handler;
};

/**
 * Reports a lost session discovered outside a json() call — notably the events
 * stream, where EventSource exposes no status code and a 401 is indistinguishable
 * from an outage until something asks.
 */
export const notifyUnauthorized = () => onUnauthorized();

const json = async <T>(path: string, init?: RequestInit): Promise<T> => {
  const res = await fetch(path, {
    ...init,
    headers: { accept: "application/json", ...init?.headers },
  });
  if (res.status === 401) {
    onUnauthorized();
    throw new Unauthorized();
  }
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return (await res.json()) as T;
};

// ---------------------------------------------------------------- auth

/** What a login attempt came back with. */
export type LoginResult =
  | { ok: true }
  /** The password was right, but this install also wants a TOTP code. */
  | { ok: false; needCode: true }
  | { ok: false; needCode: false; error: string };

/**
 * Exchanges the password — and the code, once we know one is wanted — for a
 * session cookie. The code is omitted on the first attempt because the screen
 * cannot know whether this install has a second factor: /api/capabilities is
 * behind the gate, so the server tells us in the response to this.
 */
export const login = async (password: string, code?: string): Promise<LoginResult> => {
  const res = await fetch("/api/login", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(code ? { password, code } : { password }),
  });
  if (res.ok) return { ok: true };

  const body = (await res.json().catch(() => ({}))) as { needCode?: boolean; error?: string };
  if (body.needCode) return { ok: false, needCode: true };
  return { ok: false, needCode: false, error: body.error ?? `${res.status} ${res.statusText}` };
};

/** Revokes this browser's session server-side and clears its cookie. */
export const logout = () => json<{ ok: boolean }>("/api/logout", { method: "POST" });

/**
 * Whether this browser has a session, asked of a gated endpoint that is cheap
 * and side-effect free. Distinct from fetchCapabilities: this tells apart "not
 * logged in" from "no backend", which that one deliberately collapses.
 */
export const checkSession = async (): Promise<"ok" | "login" | "offline"> => {
  try {
    const res = await fetch("/api/capabilities", { headers: { accept: "application/json" } });
    if (res.status === 401) return "login";
    return res.ok ? "ok" : "offline";
  } catch {
    return "offline";
  }
};

export const fetchCapabilities = async (): Promise<Capabilities> => {
  try {
    const caps = await json<Omit<Capabilities, "live">>("/api/capabilities");
    return { ...caps, live: true };
  } catch {
    // Mock capabilities mean "there is no backend", and that must not become
    // what a logged-out browser sees: sample data standing in for a live server
    // is the one thing this UI promises never to do. A 401 has already fired the
    // unauthorized handler inside json(), so the app is on its way to the login
    // screen and this value is never rendered.
    return MOCK_CAPABILITIES;
  }
};

// ---------------------------------------------------------------- status

export type Tool = { name: string; description?: string };

export type Param = {
  name: string;
  value?: string;
  default?: string;
  description?: string;
  enum?: string[];
};

export type StatusReport = {
  version: string;
  configDir: string;
  uptimeSeconds: number;
  warnings: string[];
  /** False when no tool-call hook governs the main agent — the shell is ungated. */
  gateArmed: boolean;
  systemPrompt?: string;
  params: Param[];
  usage?: { prompt: number; completion: number; total: number };
  agent: {
    name: string;
    model: string;
    status?: string;
    contextWindow?: number;
    tools: Tool[];
  };
  subagents: { name: string; description: string; model: string }[];
  projects: { name: string; description: string; workdir: string }[];
  skills: { name: string; description: string }[];
  cron: { name: string; schedule: string; agent: string; last: string }[];
  mcp: { name: string; connected: boolean; tools: number; error?: string }[];
  jobs: { running: number; capacity: number };
};

export const fetchStatus = () => json<StatusReport>("/api/status");

// ---------------------------------------------------------------- files

export type FileEntry = {
  name: string;
  path: string;
  dir: boolean;
  size: number;
  modified: string;
  /** True for files the server refuses to read (.env and friends). */
  redacted: boolean;
};

export const listFiles = (path: string) =>
  json<{ path: string; parent: string | null; entries: FileEntry[] }>(
    `/api/files?path=${encodeURIComponent(path)}`,
  );

export const listMedia = async (): Promise<FileEntry[]> =>
  (await json<{ entries: FileEntry[] }>("/api/media")).entries;

/** True for names the browser can show inline rather than describe. */
export const isImageName = (name: string): boolean =>
  /\.(png|jpe?g|gif|webp|svg|avif)$/i.test(name);

export const isAudioName = (name: string): boolean =>
  /\.(mp3|wav|ogg|opus|m4a|webm)$/i.test(name);

export type FileContent = {
  path: string;
  content: string;
  size: number;
  /** A credential file: never read from disk, so content is an explanation. */
  redacted: boolean;
  /** Contains NUL bytes; content is empty rather than mojibake. */
  binary: boolean;
  /** Only the first 256 KB was read. */
  truncated: boolean;
};

export const readFile = (path: string) =>
  json<FileContent>(`/api/files/content?path=${encodeURIComponent(path)}`);

// ---------------------------------------------------------------- jobs

export type Job = {
  id: string;
  kind: "bash_bg" | "subagent";
  label: string;
  status: "running" | "done" | "failed";
  startedAt: string;
  endedAt?: string;
  elapsedSeconds: number;
  exit?: number;
  summary?: string;
  error?: string;
  parent?: string;
  childOpen: boolean;
};

export const listJobs = async (): Promise<Job[]> =>
  (await json<{ jobs: Job[] }>("/api/jobs")).jobs;

export const fetchJob = (id: string) =>
  json<{ job: Job; output: string; outputKind: "transcript" | "output" }>(
    `/api/jobs/${encodeURIComponent(id)}`,
  );

export const cancelJob = (id: string) =>
  json<{ cancelled: boolean }>(`/api/jobs/${encodeURIComponent(id)}/cancel`, {
    method: "POST",
  });

/**
 * Stops the running turn from the server side, killing the background jobs it
 * started. Preferred over aborting the request in the browser: the server then
 * closes the stream properly — unfinished tool calls are reported as stopped,
 * rather than being left looking like work still waiting on the user.
 */
export const stopTurn = () => json<{ result: string }>("/api/stop", { method: "POST" });

// ---------------------------------------------------------------- cron

export type CronJob = {
  name: string;
  schedule: string;
  agent: string;
  prompt?: string;
  workdir?: string;
  /** Direct jobs skip the notifier and hand their result straight to the agent. */
  direct: boolean;
  lastRun?: string;
  lastJobId?: string;
};

export const listCron = () =>
  json<{ jobs: CronJob[]; armed: boolean }>("/api/cron");

export const runCronJob = (name: string) =>
  json<{ started: boolean }>(`/api/cron/${encodeURIComponent(name)}/run`, {
    method: "POST",
  });

// ---------------------------------------------------------------- runs

export type Run = {
  id: string;
  startedAt: string;
  endedAt?: string;
  lastAt: string;
  messages: number;
  preview: string;
  /** Set when this run is the session behind a conversation. */
  threadId?: string;
};

export type TranscriptEntry = {
  role: string;
  content?: string;
  reasoning?: string;
  toolName?: string;
  toolCalls?: { name: string; args?: string }[];
};

export const listRuns = () =>
  json<{ runs: Run[]; truncated: boolean; limit: number }>("/api/runs");

export const fetchRun = (id: string) =>
  json<{ id: string; entries: TranscriptEntry[] }>(
    `/api/runs/${encodeURIComponent(id)}`,
  );

// ---------------------------------------------------------------- controls

export const reloadConfig = () =>
  json<{ result: string }>("/api/reload", { method: "POST" });

// ---------------------------------------------------------------- voice

/** Transcribes recorded audio through the configured `media.stt` model. */
export const transcribe = async (audio: Blob): Promise<string> => {
  const body = new FormData();
  body.append("audio", audio, "recording.webm");
  const res = await fetch("/api/stt", { method: "POST", body });
  if (!res.ok) throw new Error(`/api/stt: ${res.status}`);
  const { text } = (await res.json()) as { text: string };
  return text;
};

/** Speaks text through the configured `media.tts` model. Returns audio bytes. */
export const synthesize = async (
  text: string,
  signal?: AbortSignal,
): Promise<Blob> => {
  const res = await fetch("/api/tts", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ text }),
    signal,
  });
  if (!res.ok) throw new Error(`/api/tts: ${res.status}`);
  return await res.blob();
};
