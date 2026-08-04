"use client";

/**
 * How a tool call reads in the chat.
 *
 * The generic fallback says "Used tool: bash" and hides everything else one
 * click away — which, for an agent whose whole vocabulary is bash, tells you
 * nothing. A shell3 tool call is a verb and an object, so the collapsed line
 * shows both: the tool, and the command (or path, or subagent) it was given.
 * You only open one when the result matters.
 *
 * Set as quoted material: indented behind its own rule, the way a block quote
 * is, with the shell's own prompt kept. A command the agent ran is not a card
 * in a dashboard — it is something the transcript is quoting.
 */

import { memo, useState } from "react";
import { ChevronDownIcon } from "lucide-react";
import { useToolCallElapsed, type ToolCallMessagePartComponent } from "@assistant-ui/react";
import {
  ToolFallbackApproval,
  ToolFallbackArgs,
  ToolFallbackContent,
  ToolFallbackError,
  ToolFallbackResult,
  ToolFallbackRoot,
} from "@/components/assistant-ui/tool-fallback";
import { CollapsibleTrigger } from "@/components/ui/collapsible";
import { cn } from "@/lib/utils";

/** The argument that says what a call actually did, per tool. */
const HEADLINE_ARG: Record<string, string> = {
  bash: "command",
  bash_bg: "command",
  edit_file: "file_path",
  read: "path",
  read_media: "path",
  list_files: "path",
  task: "description",
  task_status: "id",
  task_cancel: "id",
  image_generate: "prompt",
};

/** The two tools whose headline argument is literally shell input. */
const isCommand = (toolName: string) => toolName === "bash" || toolName === "bash_bg";

/**
 * The one-line "what did it do" for a call. The tool's headline argument when
 * it has one, else the first argument that is worth reading — anything beats
 * showing the name twice.
 */
const headline = (toolName: string, args: unknown): string => {
  if (args == null || typeof args !== "object") return "";
  const record = args as Record<string, unknown>;

  const named = HEADLINE_ARG[toolName];
  if (named && typeof record[named] === "string") return record[named].trim();
  // task's description is optional; the subagent it went to is the next best
  // thing to name, and more useful than its (very long) prompt.
  if (toolName === "task" && typeof record["subagent_type"] === "string") {
    return record["subagent_type"];
  }
  const first = Object.values(record).find(
    (value) => typeof value === "string" && value.trim() !== "",
  );
  return typeof first === "string" ? first.trim() : "";
};

/** Collapses a headline to one line: multi-line commands read as `first …`. */
const oneLine = (text: string): string => {
  const [first = "", ...rest] = text.split("\n");
  return rest.some((line) => line.trim() !== "") ? `${first} …` : first;
};

/**
 * The arguments, expanded. The headline argument is the thing you came to
 * read, so it gets shown as itself — a command as a command, a path as a path
 * — and only what is left over falls back to JSON. Rendering
 * `{"command":"echo one"}` under a header that already says `echo one` wastes
 * the one place there was room to be useful.
 */
function ToolCallArgs({ toolName, args, argsText }: {
  toolName: string;
  args: unknown;
  argsText?: string;
}) {
  const named = HEADLINE_ARG[toolName];
  const record =
    args != null && typeof args === "object" ? (args as Record<string, unknown>) : undefined;
  const headlineValue = named && record && typeof record[named] === "string" ? record[named] : "";

  if (!headlineValue.trim()) {
    return <ToolFallbackArgs argsText={argsText} />;
  }

  const rest = Object.fromEntries(
    Object.entries(record ?? {}).filter(([key, value]) => key !== named && value !== undefined),
  );
  return (
    <div className="aui-tool-call-args flex flex-col gap-2">
      <pre
        className={cn(
          "text-ink overflow-x-auto font-mono text-[12px] leading-[1.6]",
          "whitespace-pre-wrap",
        )}
      >
        {isCommand(toolName) ? (
          <>
            <span className="text-ink-3">$ </span>
            {headlineValue}
          </>
        ) : (
          headlineValue
        )}
      </pre>
      {Object.keys(rest).length > 0 && <ToolFallbackArgs argsText={JSON.stringify(rest, null, 2)} />}
    </div>
  );
}

const formatDuration = (ms: number) => {
  if (ms < 1000) return "<1s";
  const seconds = ms / 1000;
  if (seconds < 10) return `${(Math.floor(seconds * 10) / 10).toFixed(1)}s`;
  if (seconds < 60) return `${Math.floor(seconds)}s`;
  return `${Math.floor(seconds / 60)}m ${Math.floor(seconds % 60)}s`;
};

const Duration = () => {
  const elapsedMs = useToolCallElapsed();
  if (elapsedMs === undefined) return null;
  return (
    <span className="text-ink-3 shrink-0 font-mono text-[9px] tabular-nums">
      {formatDuration(elapsedMs)}
    </span>
  );
};

const ToolCallImpl: ToolCallMessagePartComponent = ({
  toolName,
  args,
  argsText,
  result,
  status,
  addResult,
  resume,
  interrupt,
  approval,
  respondToApproval,
}) => {
  const statusType = status?.type ?? "complete";
  const isRunning = statusType === "running";
  const isCancelled = status?.type === "incomplete" && status.reason === "cancelled";
  const needsApproval = statusType === "requires-action";

  // An approval request is the one case the user must see without clicking.
  const [open, setOpen] = useState(needsApproval);
  const [wasAsking, setWasAsking] = useState(needsApproval);
  if (needsApproval !== wasAsking) {
    setWasAsking(needsApproval);
    if (needsApproval) setOpen(true);
  }

  const summary = oneLine(headline(toolName, args));

  return (
    <ToolFallbackRoot
      open={open}
      onOpenChange={setOpen}
      className="aui-shell3-tool border-rule mb-4 border-l-2 pl-4"
      data-tool={toolName}
    >
      <CollapsibleTrigger
        className={cn(
          "group/trigger text-ink-3 hover:text-ink-2",
          "flex w-full origin-left items-baseline gap-2 py-0.5",
          "transition-colors",
        )}
      >
        <span
          className={cn(
            "shrink-0 font-mono text-[9px] tracking-[.04em] uppercase",
            isRunning ? "text-gold" : "text-ink-3",
            isCancelled && "line-through",
          )}
        >
          {toolName}
        </span>
        {summary && (
          <span
            className={cn(
              "aui-shell3-tool-summary text-ink min-w-0 flex-1 truncate",
              "text-start font-mono text-[12px]",
            )}
            title={summary}
          >
            {isCommand(toolName) && <span className="text-ink-3">$ </span>}
            {summary}
          </span>
        )}
        <span className={cn("ms-auto flex shrink-0 items-center gap-2", summary && "ms-0")}>
          {isRunning ? (
            <span className="shimmer font-mono text-[9px] uppercase motion-reduce:animate-none">
              running
            </span>
          ) : (
            <Duration />
          )}
          <ChevronDownIcon
            className={cn(
              "size-2.5 shrink-0 transition-transform",
              "duration-(--animation-duration) ease-[cubic-bezier(0.32,0.72,0,1)]",
              "motion-reduce:transition-none",
              "-rotate-90 group-data-open/trigger:rotate-0 group-data-panel-open/trigger:rotate-0",
            )}
          />
        </span>
      </CollapsibleTrigger>

      {/* The body uses the card's full width — the fallback's label indent
          would leave args and output floating in the middle of it. */}
      <ToolFallbackContent className="[&>div]:ps-0">
        <ToolFallbackError status={status} />
        <div className={cn(isCancelled && "opacity-60")}>
          <ToolCallArgs toolName={toolName} args={args} argsText={argsText} />
        </div>
        {needsApproval && (
          <ToolFallbackApproval
            addResult={addResult}
            resume={resume}
            interrupt={interrupt}
            approval={approval}
            respondToApproval={respondToApproval}
          />
        )}
        {!isCancelled && <ToolFallbackResult result={result} />}
      </ToolFallbackContent>
    </ToolFallbackRoot>
  );
};

export const ToolCall = memo(ToolCallImpl) as unknown as ToolCallMessagePartComponent;
ToolCall.displayName = "ToolCall";

export { headline, oneLine };
