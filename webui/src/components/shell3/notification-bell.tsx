import {
  AlarmClockIcon,
  BellIcon,
  BellOffIcon,
  BellRingIcon,
  TriangleAlertIcon,
  XIcon,
} from "lucide-react";
import { useEffect, useState, type FC } from "react";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { TooltipIconButton } from "@/components/assistant-ui/tooltip-icon-button";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { relativeTime } from "@/lib/format";
import { useEvents, type Notification } from "@/lib/events";
import { disablePush, enablePush, pushState, testPush, type PushState } from "@/lib/push";

const KindIcon: FC<{ kind: Notification["kind"] }> = ({ kind }) => {
  if (kind === "cron") return <AlarmClockIcon className="size-4 shrink-0" />;
  if (kind === "alert")
    return <TriangleAlertIcon className="text-destructive size-4 shrink-0" />;
  return <BellIcon className="size-4 shrink-0" />;
};

/**
 * Where a notification can take you: its stored run first (durable, full
 * transcript), the live job second (bash_bg has no run), the chat thread last.
 */
export type NotificationTarget =
  | { kind: "run"; id: string }
  | { kind: "job"; id: string }
  | { kind: "thread"; id: string };

const targetOf = (n: Notification): NotificationTarget | null => {
  if (n.runId) return { kind: "run", id: n.runId };
  if (n.jobId) return { kind: "job", id: n.jobId };
  if (n.threadId) return { kind: "thread", id: n.threadId };
  return null;
};

const TARGET_LABEL: Record<NotificationTarget["kind"], string> = {
  run: "See the run",
  job: "See the job",
  thread: "Open the conversation",
};

export const NotificationBell: FC<{
  onOpen?: (target: NotificationTarget) => void;
}> = ({ onOpen }) => {
  const { notifications, unreadCount, markAllRead, dismiss, clearAll } = useEvents();
  // Controlled so following a link can close the sheet: the view it opened
  // is the point, and a popover left over it forces a second gesture.
  const [open, setOpen] = useState(false);

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) markAllRead();
      }}
    >
      <PopoverTrigger
        render={
          <TooltipIconButton
            variant="ghost"
            size="icon"
            tooltip="Notifications"
            side="bottom"
            className="relative size-7"
            aria-label={
              unreadCount > 0
                ? `Notifications, ${unreadCount} unread`
                : "Notifications"
            }
          >
            <BellIcon className="size-4" />
            {unreadCount > 0 && (
              <span
                className="bg-mark-fill text-swipe absolute -top-1 -right-1 flex h-[13px] min-w-[13px] items-center justify-center rounded-[2px] px-[3px] font-mono text-[9px] leading-none font-semibold tabular-nums"
                aria-hidden
              >
                {unreadCount > 9 ? "9+" : unreadCount}
              </span>
            )}
          </TooltipIconButton>
        }
      />
      <PopoverContent align="end" sideOffset={8} className="w-88 p-0">
        <div className="flex items-center justify-between border-b px-3 py-2">
          <span className="text-sm font-medium">Notifications</span>
          {notifications.length > 0 && (
            <Button
              variant="ghost"
              size="sm"
              onClick={clearAll}
              className="text-muted-foreground hover:text-foreground h-6 px-2 text-xs"
            >
              Clear all
            </Button>
          )}
        </div>

        <PushToggle />

        {notifications.length === 0 ? (
          <p className="text-muted-foreground px-3 py-8 text-center text-sm">
            Background jobs and cron results land here.
          </p>
        ) : (
          <ul className="max-h-96 overflow-y-auto py-1">
            {notifications.map((item) => (
              <li
                key={item.id}
                className={cn(
                  "group/notification hover:bg-muted/60 flex gap-2.5 px-3 py-2.5 transition-colors",
                  !item.read && "bg-muted/30",
                )}
              >
                <span className="text-muted-foreground mt-0.5">
                  <KindIcon kind={item.kind} />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="flex items-baseline gap-2">
                    <span className="truncate text-sm font-medium">{item.title}</span>
                    <span className="text-muted-foreground ml-auto shrink-0 text-xs">
                      {relativeTime(item.at)}
                    </span>
                  </div>
                  <p className="text-muted-foreground mt-0.5 text-sm wrap-break-word">
                    {item.body}
                  </p>
                  {(() => {
                    const target = onOpen ? targetOf(item) : null;
                    if (!target) return null;
                    return (
                      <button
                        type="button"
                        onClick={() => {
                          setOpen(false);
                          onOpen?.(target);
                        }}
                        className="text-ink-2 hover:text-gold border-rule hover:border-gold mt-1.5 border-b border-dotted text-[12px]"
                      >
                        {TARGET_LABEL[target.kind]}
                      </button>
                    );
                  })()}
                </div>
                {/* Always visible — a hover-only control does not exist on a
                    phone, and this list is read on phones. */}
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`Dismiss ${item.title}`}
                  onClick={() => dismiss(item.id)}
                  className="size-6 shrink-0 opacity-50 transition-opacity hover:opacity-100 group-hover/notification:opacity-100"
                >
                  <XIcon className="size-3.5" />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </PopoverContent>
    </Popover>
  );
};

/**
 * Push delivery, for when the tab is closed. Push needs a secure context, so
 * over plain http to another machine the row explains why rather than offering
 * a switch that cannot work.
 */
const PushToggle: FC = () => {
  const [state, setState] = useState<PushState | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const [testing, setTesting] = useState(false);

  useEffect(() => {
    void pushState().then(setState);
  }, []);

  if (state === null || state === "unavailable") return null;

  const label: Record<PushState, string> = {
    unsupported: "This browser cannot receive push over an insecure connection.",
    unavailable: "",
    denied: "Notifications are blocked in your browser settings.",
    off: "Get notified when this tab is closed.",
    on: "Notifications reach you with the tab closed.",
  };

  const toggle = async () => {
    setBusy(true);
    setNote(null);
    try {
      setState(state === "on" ? await disablePush() : await enablePush());
    } catch (err) {
      setNote(String(err));
    } finally {
      setBusy(false);
    }
  };

  const actionable = state === "on" || state === "off";

  return (
    <div className="flex items-start gap-2.5 border-b px-3 py-2.5">
      {state === "on" ? (
        <BellRingIcon className="text-primary mt-0.5 size-4 shrink-0" />
      ) : (
        <BellOffIcon className="text-muted-foreground mt-0.5 size-4 shrink-0" />
      )}
      <div className="min-w-0 flex-1">
        <p className="text-muted-foreground text-xs">{note ?? label[state]}</p>
      </div>
      {actionable && (
        <div className="flex shrink-0 gap-1">
          {state === "on" && (
            <Button
              variant="ghost"
              size="sm"
              disabled={testing}
              className="h-6 px-2 text-xs"
              onClick={() => {
                setTesting(true);
                setNote(null);
                void testPush()
                  .then((result) =>
                    setNote(
                      result === "delivered"
                        ? "Delivered: the browser received it and asked to show it. " +
                          "If nothing appeared on screen, the block is macOS — " +
                          "System Settings › Notifications › Google Chrome › Allow."
                        : "Sent, but this browser never received it. The push " +
                          "service could not reach it; turning push off and on " +
                          "again mints a fresh subscription.",
                    ),
                  )
                  .catch((err) => setNote(`Could not send: ${err}`))
                  .finally(() => setTesting(false));
              }}
            >
              {testing ? "Testing…" : "Test"}
            </Button>
          )}
          <Button
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={() => void toggle()}
            className="h-6 px-2 text-xs"
          >
            {state === "on" ? "Turn off" : "Turn on"}
          </Button>
        </div>
      )}
    </div>
  );
};
