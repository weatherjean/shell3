import {
  AssistantRuntimeProvider,
  CompositeAttachmentAdapter,
  SimpleImageAttachmentAdapter,
  SimpleTextAttachmentAdapter,
  useAuiState,
} from "@assistant-ui/react";
import { useChat } from "@ai-sdk/react";
import { useAISDKRuntime, AssistantChatTransport } from "@assistant-ui/react-ai-sdk";
import {
  ClockIcon,
  FolderTreeIcon,
  GaugeIcon,
  HistoryIcon,
  ListTodoIcon,
  LogOutIcon,
  MenuIcon,
  PanelLeftIcon,
  PencilIcon,
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FC,
  type ReactNode,
} from "react";
import { Thread } from "@/components/shell3/chat";
import { LoginScreen } from "@/components/shell3/login";
import { AskDialog } from "@/components/shell3/ask-dialog";
import { FilesView } from "@/components/shell3/files-view";
import { NotificationBell } from "@/components/shell3/notification-bell";
import { CronView } from "@/components/shell3/cron-view";
import { JobsView } from "@/components/shell3/jobs-view";
import { RunsView } from "@/components/shell3/runs-view";
import { StatusView } from "@/components/shell3/status-view";
import { ThemeToggle } from "@/components/shell3/theme-toggle";
import { ThreadList } from "@/components/shell3/thread-list";
import { TooltipIconButton } from "@/components/assistant-ui/tooltip-icon-button";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Sheet, SheetContent, SheetTrigger } from "@/components/ui/sheet";
import { checkSession, logout, setUnauthorizedHandler } from "@/lib/api";
import { CapabilitiesProvider, useCapabilities } from "@/lib/capabilities";
import { EventsProvider } from "@/lib/events";
import { HeadingProvider, useHeading } from "@/lib/heading";
import { ThemeProvider } from "@/lib/theme";
import {
  deleteThread,
  listThreads,
  loadThreadMessages,
  newThreadId,
  renameThread,
  threadLabel,
  type StoredMessage,
  type Thread as ThreadRecord,
} from "@/lib/threads";
import { cn } from "@/lib/utils";
import { buildVoiceAdapters } from "@/lib/voice";
import { mockChatFetch } from "@/mock/chat";

type View = "chat" | "jobs" | "cron" | "runs" | "status" | "files";

// Chat owns the sidebar's body; the rest is machinery you look at
// occasionally, so it sits in a strip along the bottom.
const TOOL_VIEWS: { id: View; label: string; folio: string; icon: ReactNode }[] = [
  { id: "jobs", label: "Jobs", folio: "I", icon: <ListTodoIcon className="size-4" /> },
  { id: "cron", label: "Cron", folio: "II", icon: <ClockIcon className="size-4" /> },
  { id: "runs", label: "Runs", folio: "III", icon: <HistoryIcon className="size-4" /> },
  { id: "status", label: "Status", folio: "IV", icon: <GaugeIcon className="size-4" /> },
  { id: "files", label: "Files", folio: "V", icon: <FolderTreeIcon className="size-4" /> },
];

// The shell3 mark: the snail rendered in text, as on the banner. Its face is
// pinned by `mark-face` — see index.css.
const Mark: FC<{ className?: string }> = ({ className }) => (
  <span
    className={cn("mark-face text-primary leading-none tracking-tighter", className)}
    aria-hidden
  >
    ๑ï
  </span>
);

const Logo: FC = () => (
  <div className="flex items-baseline gap-2">
    <Mark className="text-[21px]" />
    <span className="font-serif text-[21px] leading-none font-medium tracking-[-.015em]">
      shell3
    </span>
  </div>
);

// ------------------------------------------------------------------ threads

/** Owns the conversation list and which one is open. */
const useThreads = () => {
  const [threads, setThreads] = useState<ThreadRecord[]>([]);
  const [activeId, setActiveId] = useState(newThreadId);

  const refresh = useCallback(async () => {
    try {
      setThreads(await listThreads());
    } catch {
      setThreads([]); // no backend: the list is simply empty
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const select = useCallback((id: string) => setActiveId(id), []);
  const startNew = useCallback(() => setActiveId(newThreadId()), []);

  const rename = useCallback(
    async (id: string, title: string) => {
      setThreads((c) => c.map((t) => (t.id === id ? { ...t, title } : t)));
      try {
        await renameThread(id, title);
      } finally {
        void refresh();
      }
    },
    [refresh],
  );

  const remove = useCallback(
    async (id: string) => {
      setThreads((c) => c.filter((t) => t.id !== id));
      if (id === activeId) setActiveId(newThreadId());
      try {
        await deleteThread(id);
      } finally {
        void refresh();
      }
    },
    [activeId, refresh],
  );

  const active = threads.find((t) => t.id === activeId);
  return {
    threads,
    activeId,
    active,
    refresh,
    select,
    startNew,
    rename,
    remove,
  };
};

type ThreadControls = ReturnType<typeof useThreads>;

// --------------------------------------------------------------- chat surface

/**
 * One conversation. Mounted with a `key` of the thread id, so switching
 * conversations builds a fresh runtime seeded with that thread's history
 * rather than trying to mutate the current one.
 */
const ChatSurface: FC<{ threadId: string; live: boolean; onTurnEnd: () => void }> = ({
  threadId,
  live,
  onTurnEnd,
}) => {
  const { voice } = useCapabilities();
  const [initial, setInitial] = useState<StoredMessage[] | null>(null);

  useEffect(() => {
    let stale = false;
    if (!live) {
      setInitial([]);
      return;
    }
    void loadThreadMessages(threadId)
      .then((messages) => !stale && setInitial(messages))
      .catch(() => !stale && setInitial([]));
    return () => {
      stale = true;
    };
  }, [threadId, live]);

  if (initial === null) {
    return <div className="h-full" aria-busy="true" />;
  }
  return (
    <ChatRuntime
      threadId={threadId}
      live={live}
      initial={initial}
      voice={voice}
      onTurnEnd={onTurnEnd}
    />
  );
};

const ChatRuntime: FC<{
  threadId: string;
  live: boolean;
  initial: StoredMessage[];
  voice: { stt: boolean; tts: boolean };
  onTurnEnd: () => void;
}> = ({ threadId, live, initial, voice, onTurnEnd }) => {
  const adapters = useMemo(
    () => ({
      ...buildVoiceAdapters(voice),
      // Images and text files are inlined into the message; the server stores
      // them under ~/.shell3/media/ so the agent can re-read them later.
      attachments: new CompositeAttachmentAdapter([
        new SimpleImageAttachmentAdapter(),
        new SimpleTextAttachmentAdapter(),
      ]),
    }),
    [voice],
  );

  // Built once per conversation. A transport rebuilt on every render churns the
  // runtime's identity underneath itself: the history adapter is re-applied,
  // re-adds messages the repository already holds, and the whole chat unmounts
  // on a duplicate-id error.
  const transport = useMemo(
    () =>
      new AssistantChatTransport({
        api: "/api/chat",
        // Which conversation this is, sent as its own field rather than relying
        // on the request's `id` — see the note on the runtime below.
        body: { threadId },
        // Where resumeStream() reconnects: the server replays the running
        // turn from its start, or answers 204 when nothing is running.
        prepareReconnectToStreamRequest: () => ({
          api: `/api/chat/stream?thread=${encodeURIComponent(threadId)}`,
        }),
        // Without a backend the transport streams a canned reply instead.
        ...(live ? {} : { fetch: mockChatFetch }),
      }),
    [threadId, live],
  );

  // Deliberately NOT useChatRuntime. That hook wraps the chat in assistant-ui's
  // own thread list, which owns the conversation's identity: it mints a local
  // id per runtime (ignoring the one we pass, so the server saw a new thread
  // per message) and switches to a fresh, empty thread when a run is cancelled
  // — which read as Stop erasing the conversation. shell3 already has a thread
  // list, backed by the server; one chat per mounted conversation is all that
  // is wanted here.
  //
  // No auto-submit either. `sendAutomaticallyWhen` exists for tools the BROWSER
  // runs: the SDK sends the results back so the model can continue. shell3 runs
  // every tool server-side inside one turn and streams the results as part of
  // it, so the condition "assistant message complete with tool results" is true
  // at the end of any tool-using turn — and re-submitting there replays the
  // same user message forever.
  const chat = useChat({ id: threadId, messages: initial, transport });
  const runtime = useAISDKRuntime(chat, { adapters });

  // Reconnect-and-replay. A phone that locks its screen kills the SSE fetch
  // while the server keeps running the turn; resumeStream() re-attaches and
  // the server replays the turn so far. Three triggers: opening the
  // conversation (a reload mid-turn, or switching back to a busy thread —
  // 204 makes it a no-op otherwise), the tab becoming visible again, and the
  // network coming back — the latter two only when the stream actually died.
  //
  // The effect keys on the CONVERSATION, not on `chat`: useChat returns a
  // fresh object every render, and an effect keyed on it re-fires per render
  // — a resume-request storm. The ref always points at the current object.
  const chatRef = useRef(chat);
  chatRef.current = chat;
  useEffect(() => {
    if (!live) return;
    const resume = () => void chatRef.current.resumeStream().catch(() => {});
    // A healthy stream must not be interrupted: resume only from rest.
    if (chatRef.current.status === "ready" || chatRef.current.status === "error") {
      resume();
    }
    const retry = () => {
      if (document.visibilityState !== "visible") return;
      const current = chatRef.current;
      if (current.status !== "error") return;
      current.clearError();
      // The replay re-delivers the whole turn, and the SDK reuses a trailing
      // assistant message as the streaming target without clearing its parts
      // — resuming over the partial message from before the disconnect would
      // render everything before the cut twice. Drop it; the replay (or the
      // transcript, when the turn already finished) carries the whole truth.
      const msgs = current.messages;
      if (msgs.length > 0 && msgs[msgs.length - 1].role === "assistant") {
        current.setMessages(msgs.slice(0, -1));
      }
      void chatRef.current
        .resumeStream()
        .then(() => {
          // 204: the turn ended while we were away — nothing streamed back,
          // so the reply now lives only in the persisted transcript.
          if (chatRef.current.status !== "ready") return;
          return loadThreadMessages(threadId).then((stored) => {
            if (chatRef.current.status === "ready") chatRef.current.setMessages(stored);
          });
        })
        .catch(() => {});
    };
    document.addEventListener("visibilitychange", retry);
    window.addEventListener("online", retry);
    return () => {
      document.removeEventListener("visibilitychange", retry);
      window.removeEventListener("online", retry);
    };
  }, [threadId, live]);

  return (
    <AssistantRuntimeProvider runtime={runtime}>
      <TurnWatcher onTurnEnd={onTurnEnd} />
      <Thread />
    </AssistantRuntimeProvider>
  );
};

/**
 * Refreshes the conversation list when a turn finishes: the first message in a
 * thread is what creates it server-side, and every turn moves it up the list.
 */
const TurnWatcher: FC<{ onTurnEnd: () => void }> = ({ onTurnEnd }) => {
  const running = useAuiState((s) => s.thread.isRunning);
  const wasRunning = useRef(false);

  useEffect(() => {
    if (wasRunning.current && !running) onTurnEnd();
    wasRunning.current = running;
  }, [running, onTurnEnd]);

  return null;
};

// -------------------------------------------------------------------- shell

/**
 * The tool views, as a bar along the bottom of the sidebar. Chat owns the
 * sidebar's body — it is what the interface is for — so everything else is a
 * compact strip that never competes with the conversation list for room.
 */
const ToolBar: FC<{
  view: View;
  onSelect: (view: View) => void;
  collapsed?: boolean;
}> = ({ view, onSelect, collapsed }) =>
  collapsed ? (
    // Collapsed to a rail there is no room to set a folio, so it falls back to
    // the icons alone.
    <nav aria-label="Tools" className="border-rule flex shrink-0 flex-col gap-0.5 border-t p-1">
      {TOOL_VIEWS.map((item) => (
        <button
          key={item.id}
          onClick={() => onSelect(item.id)}
          aria-current={view === item.id ? "page" : undefined}
          title={item.label}
          className={cn(
            "hover:bg-lift flex flex-col items-center py-2 transition-colors",
            view === item.id ? "text-gold" : "text-ink-3",
          )}
        >
          {item.icon}
        </button>
      ))}
      <SignOutRow collapsed />
    </nav>
  ) : (
    <nav aria-label="Tools" className="border-rule shrink-0 border-t px-5 py-3.5">
      <div className="cap mb-2">Elsewhere</div>
      {TOOL_VIEWS.map((item) => {
        const active = view === item.id;
        return (
          <button
            key={item.id}
            onClick={() => onSelect(item.id)}
            aria-current={active ? "page" : undefined}
            className={cn(
              "flex w-full items-baseline gap-2.5 py-0.5 text-[12.5px] transition-colors",
              active ? "text-ink font-semibold" : "text-ink-2 hover:text-ink",
            )}
          >
            <span
              className={cn(
                "w-6 font-mono text-[9.5px] tracking-[.04em]",
                active ? "text-gold" : "text-ink-3",
              )}
            >
              {item.folio}
            </span>
            {item.label}
          </button>
        );
      })}
      <SignOutRow />
    </nav>
  );

const ThreadPanel: FC<{
  controls: ThreadControls;
  // No thread reads as active while a subpage (Jobs/Cron/Runs/Status/Files)
  // is open — the highlight belongs to Chat, not to whichever conversation
  // was last open there.
  view: View;
  collapsed?: boolean;
  onOpenChat?: () => void;
}> = ({ controls, view, collapsed, onOpenChat }) => (
  <ThreadList
    threads={controls.threads}
    activeId={view === "chat" ? controls.activeId : ""}
    collapsed={collapsed}
    onSelect={(id) => {
      controls.select(id);
      onOpenChat?.();
    }}
    onNewThread={() => {
      controls.startNew();
      onOpenChat?.();
    }}
    onRename={controls.rename}
    onDelete={controls.remove}
  />
);

const Sidebar: FC<{
  view: View;
  onSelect: (view: View) => void;
  collapsed: boolean;
  controls: ThreadControls;
}> = ({ view, onSelect, collapsed, controls }) => {
  const { agent, live } = useCapabilities();

  return (
    <aside
      className={cn(
        // Only the width animates. `transition-all` also caught the background,
        // so switching stock smeared the rail through the old ground.
        "bg-sidebar border-rule flex h-full flex-col overflow-hidden border-r transition-[width] duration-200",
        collapsed ? "w-12" : "w-65",
      )}
    >
      {/* The title plate. Set as a document's would be: the mark, the name in
          the display face, and what this thing is underneath it. */}
      {collapsed ? (
        <div className="mt-2 flex h-12 shrink-0 items-center justify-center px-2">
          <Mark className="shrink-0" />
        </div>
      ) : (
        <div className="shrink-0 px-5 pt-4 pb-3.5">
          {/* Mark and name on one line: the plate does not need to be tall to
              be a plate, and the rail's height is better spent on the index. */}
          <div className="flex items-baseline gap-2">
            <Mark className="text-[21px]" />
            <h1 className="font-serif text-[21px] leading-none font-medium tracking-[-.015em]">
              shell3
            </h1>
          </div>
          <p className="text-ink-3 mt-1.5 font-mono text-[9.5px]">{agent.model}</p>
        </div>
      )}

      {/* Conversations are always here, whichever view is showing — picking
          one takes you back to the chat. */}
      <div className="min-h-0 flex-1 overflow-hidden">
        <ThreadPanel
          controls={controls}
          view={view}
          collapsed={collapsed}
          onOpenChat={() => onSelect("chat")}
        />
      </div>

      <ToolBar view={view} onSelect={onSelect} collapsed={collapsed} />

      {!collapsed && (
        <div className="text-ink-3 border-rule truncate border-t px-5 py-3 font-mono text-[9.5px]">
          {live ? "~/.shell3" : "sample data · no backend"}
        </div>
      )}
    </aside>
  );
};

const MobileSidebar: FC<{
  view: View;
  onSelect: (view: View) => void;
  controls: ThreadControls;
}> = ({ view, onSelect, controls }) => (
  <Sheet>
    <SheetTrigger
      render={
        <Button variant="ghost" size="icon" className="size-8 shrink-0 md:hidden">
          <MenuIcon className="size-[17px]" />
          <span className="sr-only">Open menu</span>
        </Button>
      }
    />
    <SheetContent side="left" className="bg-sidebar border-rule flex w-70 flex-col p-0">
      <div className="shrink-0 px-5 pt-4 pb-3.5">
        <Logo />
      </div>
      <div className="min-h-0 flex-1 overflow-hidden">
        <ThreadPanel controls={controls} view={view} onOpenChat={() => onSelect("chat")} />
      </div>
      <ToolBar view={view} onSelect={onSelect} />
    </SheetContent>
  </Sheet>
);

const Header: FC<{
  view: View;
  onSelect: (view: View) => void;
  collapsed: boolean;
  onToggleSidebar: () => void;
  controls: ThreadControls;
}> = ({ view, onSelect, collapsed, onToggleSidebar, controls }) => (
  <header className="flex h-12 shrink-0 items-center gap-2 px-4">
    <MobileSidebar view={view} onSelect={onSelect} controls={controls} />
    <TooltipIconButton
      variant="ghost"
      size="icon"
      tooltip={collapsed ? "Show sidebar" : "Hide sidebar"}
      side="bottom"
      onClick={onToggleSidebar}
      className="hidden size-7 md:flex"
    >
      <PanelLeftIcon className="size-[17px]" />
    </TooltipIconButton>

    {view === "chat" ? <ChatTitle controls={controls} /> : <PageHeading view={view} />}

    <div className="ml-auto flex items-center gap-0.5">
      <NotificationBell />
      <ThemeToggle />
    </div>
  </header>
);

/**
 * The open page's heading, as published by the page itself. The masthead is the
 * only place a title appears — a view that printed its own would put the same
 * word twice down the left edge.
 *
 * Falls back to the view's own name for the moment before a page publishes, so
 * the masthead is never briefly blank on a switch.
 */
const PageHeading: FC<{ view: View }> = ({ view }) => {
  const heading = useHeading();
  const title = heading?.title ?? view.charAt(0).toUpperCase() + view.slice(1);
  return (
    <div className="flex min-w-0 items-baseline gap-3">
      <h1 className="min-w-0 truncate font-serif text-[22px] font-medium tracking-[-.015em]">
        {title}
      </h1>
      {heading?.note && (
        <span className="text-ink-3 hidden shrink-0 font-mono text-[9.5px] sm:inline">
          {heading.note}
        </span>
      )}
    </div>
  );
};

/**
 * The open conversation's name, renamable in place. The sidebar's menu can do
 * it too, but the title you are looking at is where you reach for it.
 */
const ChatTitle: FC<{ controls: ThreadControls }> = ({ controls }) => {
  const thread = controls.active;
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState("");

  if (!thread) {
    return <span className="text-muted-foreground text-sm font-medium">New chat</span>;
  }

  if (editing) {
    const commit = () => {
      const name = value.trim();
      if (name && name !== threadLabel(thread)) controls.rename(thread.id, name);
      setEditing(false);
    };
    return (
      <input
        value={value}
        autoFocus
        aria-label="Conversation name"
        onChange={(e) => setValue(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === "Enter") commit();
          if (e.key === "Escape") setEditing(false);
        }}
        onFocus={(e) => e.target.select()}
        className="border-input min-w-0 flex-1 rounded-md border bg-transparent px-2 py-0.5 text-sm font-medium outline-none"
      />
    );
  }

  return (
    <div className="group/title flex min-w-0 items-center gap-1">
      <span className="min-w-0 truncate text-sm font-medium">
        {threadLabel(thread)}
      </span>
      <TooltipIconButton
        variant="ghost"
        size="icon"
        tooltip="Rename"
        side="bottom"
        onClick={() => {
          setValue(thread.title || threadLabel(thread));
          setEditing(true);
        }}
        className="text-ink-3 hover:text-ink size-6 shrink-0"
      >
        <PencilIcon className="size-3.5" />
      </TooltipIconButton>
    </div>
  );
};

const Workspace: FC = () => {
  const [view, setView] = useState<View>("chat");
  const [collapsed, setCollapsed] = useState(false);
  // Set when Cron sends the user to a specific job ("see what it did").
  const [jobFocus, setJobFocus] = useState<string | null>(null);
  const { live } = useCapabilities();
  const controls = useThreads();

  return (
    <HeadingProvider>
      {/* One sheet, ruled, with the index bound down its left edge — not a
          floating panel on a tinted desk. The ruling is the stock's texture and
          belongs to the recto; the index paints its own deeper stock over it. */}
      <div className="sheet-bg flex h-dvh w-full">
        <div className="hidden md:block">
          <Sidebar
            view={view}
            onSelect={(next) => {
              if (next !== "jobs") setJobFocus(null);
              setView(next);
            }}
            collapsed={collapsed}
            controls={controls}
          />
        </div>
        <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
            <Header
              view={view}
              onSelect={(next) => {
                if (next !== "jobs") setJobFocus(null);
                setView(next);
              }}
              collapsed={collapsed}
              onToggleSidebar={() => setCollapsed(!collapsed)}
              controls={controls}
            />
            <main className="min-h-0 flex-1 overflow-hidden">
              {/* Kept mounted across view switches so a running turn is never
                  torn down by looking at Status. */}
              <div className={cn("h-full", view !== "chat" && "hidden")}>
                <ChatSurface
                  key={controls.activeId}
                  threadId={controls.activeId}
                  live={live}
                  onTurnEnd={controls.refresh}
                />
              </div>
              {view === "jobs" && <JobsView key={jobFocus ?? "jobs"} focus={jobFocus} />}
              {view === "cron" && (
                <CronView
                  onOpenJob={(id) => {
                    setJobFocus(id);
                    setView("jobs");
                  }}
                />
              )}
              {view === "runs" && <RunsView />}
              {view === "status" && <StatusView />}
              {view === "files" && <FilesView />}
            </main>
        </div>
      </div>
      <AskDialog />
    </HeadingProvider>
  );
};

/**
 * Signs out: revokes the session server-side, not just in this browser. A
 * cookie dropped locally would still be a working credential anywhere else it
 * had been copied.
 *
 * Lives at the bottom of the sidebar, last after the tool views, and always
 * behind a confirmation — signing out ends in a login screen, and on a phone
 * an accidental tap would do that mid-thought.
 */
const SignOutRow: FC<{ collapsed?: boolean }> = ({ collapsed }) => {
  const { live } = useCapabilities();
  const [open, setOpen] = useState(false);
  if (!live) return null; // nothing to sign out of on the mock backend

  const signOut = () => {
    void logout()
      .catch(() => undefined)
      .then(() => window.location.reload());
  };

  return (
    <>
      {collapsed ? (
        <button
          onClick={() => setOpen(true)}
          title="Sign out"
          className="hover:bg-lift text-ink-3 flex flex-col items-center py-2 transition-colors"
        >
          <LogOutIcon className="size-[15px]" />
        </button>
      ) : (
        <button
          onClick={() => setOpen(true)}
          className="text-ink-2 hover:text-ink mt-1 flex w-full items-baseline gap-2.5 py-0.5 text-[12.5px] transition-colors"
        >
          <span className="text-ink-3 flex w-6 items-center font-mono">
            <LogOutIcon className="size-[11px]" />
          </span>
          Sign out
        </button>
      )}

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Sign out?</DialogTitle>
            <DialogDescription>
              This revokes the session on the server. Signing back in takes the
              password — and the authenticator code, where one is set.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button onClick={signOut}>Sign out</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
};

/**
 * The gate in front of everything. Three states rather than two, because "not
 * logged in" and "no backend at all" must not be conflated: the first shows the
 * login screen, the second is the mock demo, and showing sample data to someone
 * whose session merely lapsed would be a lie about a live server.
 *
 * A session can also lapse mid-visit — expired, revoked, or the password
 * changed — so any later 401 from any request comes back here too.
 */
const AuthGate: FC<{ children: ReactNode }> = ({ children }) => {
  const [state, setState] = useState<"checking" | "login" | "in">("checking");

  const probe = useCallback(() => {
    void checkSession().then((result) => {
      // "offline" means no backend: the mock demo, which needs no login.
      setState(result === "login" ? "login" : "in");
    });
  }, []);

  useEffect(() => {
    probe();
    setUnauthorizedHandler(() => setState("login"));
    return () => setUnauthorizedHandler(() => undefined);
  }, [probe]);

  if (state === "checking") {
    // Deliberately blank: a spinner here flashes on every load, and the check is
    // one local request.
    return <div className="bg-background min-h-dvh" />;
  }
  if (state === "login") {
    return <LoginScreen onSignedIn={probe} />;
  }
  return <>{children}</>;
};

export default function App() {
  return (
    <ThemeProvider>
      <AuthGate>
        <CapabilitiesProvider>
          <EventsProvider>
            <Workspace />
          </EventsProvider>
        </CapabilitiesProvider>
      </AuthGate>
    </ThemeProvider>
  );
}
