import { ShieldAlertIcon } from "lucide-react";
import type { FC } from "react";
import { Button } from "@/components/ui/button";
import { useEvents } from "@/lib/events";

/**
 * The command gate. A hook script asked for a human before this tool runs, and
 * the agent's turn is parked until the answer lands — so this takes over the
 * screen rather than sitting in a corner.
 */
export const AskDialog: FC = () => {
  const { asks, answerAsk } = useEvents();
  const ask = asks[0];
  if (!ask) return null;

  return (
    <div
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="ask-title"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm"
    >
      <div className="bg-background fade-in zoom-in-95 animate-in w-full max-w-lg rounded-xl border p-5 shadow-lg duration-150">
        <div className="flex items-start gap-3">
          <ShieldAlertIcon className="text-destructive mt-0.5 size-5 shrink-0" />
          <div className="min-w-0 flex-1">
            <h2 id="ask-title" className="text-base font-semibold">
              {ask.prompt || "Approve this command?"}
            </h2>
            <p className="text-muted-foreground mt-1 text-sm">
              {ask.reason || "A tool-call hook is holding this until you decide."}
            </p>
          </div>
        </div>

        <p className="text-muted-foreground mt-4 text-sm">
          The agent is paused until you answer. Denying tells it to check with
          you before trying again.
        </p>

        <div className="mt-5 flex items-center justify-end gap-2">
          <Button variant="ghost" onClick={() => answerAsk(ask.id, false)}>
            Deny
          </Button>
          <Button onClick={() => answerAsk(ask.id, true)}>Allow</Button>
        </div>

        {asks.length > 1 && (
          <p className="text-muted-foreground mt-3 text-center text-xs">
            {asks.length - 1} more waiting
          </p>
        )}
      </div>
    </div>
  );
};
