import { WrenchIcon } from "lucide-react";
import type { FC } from "react";
import { Chip, Pre } from "@/components/shell3/page";
import type { TranscriptEntry } from "@/lib/api";
import { cn } from "@/lib/utils";

// A session replayed at full fidelity. Unlike the chat view this keeps the
// machinery — tool calls, their arguments and results, and the model's
// reasoning — because seeing how the work was done is the whole point of
// looking at a run or a subagent after the fact.

export const Transcript: FC<{ entries: TranscriptEntry[] }> = ({ entries }) => (
  <div className="flex flex-col gap-3">
    {entries.map((entry, i) => (
      <TranscriptRow key={i} entry={entry} />
    ))}
  </div>
);

const TranscriptRow: FC<{ entry: TranscriptEntry }> = ({ entry }) => {
  if (entry.role === "tool") {
    return (
      <details className="rounded-lg border">
        <summary className="hover:bg-muted/40 flex cursor-pointer items-center gap-2 px-3 py-2 text-sm">
          <WrenchIcon className="text-muted-foreground size-3.5" />
          <span className="font-mono text-xs">{entry.toolName || "tool"}</span>
          <span className="text-muted-foreground ml-auto text-xs">result</span>
        </summary>
        <Pre className="rounded-none border-t bg-transparent">
          {entry.content || "(no output)"}
        </Pre>
      </details>
    );
  }

  const isUser = entry.role === "user";
  return (
    <div className={cn("flex flex-col gap-2", isUser && "items-end")}>
      {entry.reasoning && (
        <details className="w-full rounded-lg border border-dashed">
          <summary className="text-muted-foreground hover:bg-muted/40 cursor-pointer px-3 py-2 text-xs">
            Thinking
          </summary>
          <Pre className="rounded-none bg-transparent">{entry.reasoning}</Pre>
        </details>
      )}

      {entry.content && (
        <div
          className={cn(
            "max-w-full rounded-xl px-3.5 py-2 text-sm wrap-break-word whitespace-pre-wrap",
            isUser ? "bg-muted" : "bg-transparent",
          )}
        >
          {entry.content}
        </div>
      )}

      {entry.toolCalls?.map((call, i) => (
        <details key={i} className="w-full rounded-lg border">
          <summary className="hover:bg-muted/40 flex cursor-pointer items-center gap-2 px-3 py-2 text-sm">
            <WrenchIcon className="text-muted-foreground size-3.5" />
            <span className="font-mono text-xs">{call.name}</span>
            <span className="text-muted-foreground ml-auto text-xs">call</span>
          </summary>
          <Pre className="rounded-none border-t bg-transparent">
            {prettyArgs(call.args)}
          </Pre>
        </details>
      ))}

      {!entry.content && !entry.reasoning && !entry.toolCalls?.length && (
        <Chip>{entry.role}</Chip>
      )}
    </div>
  );
};

/** Re-indents JSON tool arguments; leaves anything else as it came. */
const prettyArgs = (args: string | undefined): string => {
  if (!args) return "(no arguments)";
  try {
    return JSON.stringify(JSON.parse(args), null, 2);
  } catch {
    return args;
  }
};

/**
 * Strips the `[tool_call_id=N]\n` prefix the turn loop prepends to each stored
 * tool result — an internal addressing handle, not part of the output.
 */
const stripToolIdPrefix = (content: string): string => {
  if (!content.startsWith("[tool_call_id=")) return content;
  const newline = content.indexOf("\n");
  return newline >= 0 ? content.slice(newline + 1) : content;
};

/**
 * Parses a subagent's stored transcript. It is the child session's raw
 * messages.jsonl — one `llm.Message` JSON record per line, whose keys are the
 * Go store's (`reasoning_content`, `name`, and untagged `tool_calls` fields
 * `ID`/`Name`/`RawArgs`). Returns null when the text is not JSONL, so callers
 * can fall back to showing it verbatim.
 */
export const parseTranscript = (raw: string): TranscriptEntry[] | null => {
  const lines = raw.split("\n").filter((line) => line.trim() !== "");
  if (lines.length === 0) return null;

  const entries: TranscriptEntry[] = [];
  for (const line of lines) {
    try {
      const row = JSON.parse(line) as {
        role?: string;
        content?: string;
        reasoning_content?: string;
        name?: string;
        tool_calls?: { Name?: string; RawArgs?: string }[];
      };
      if (!row.role) return null;
      entries.push({
        role: row.role,
        content:
          row.role === "tool" && row.content
            ? stripToolIdPrefix(row.content)
            : row.content,
        reasoning: row.reasoning_content,
        toolName: row.name,
        toolCalls: row.tool_calls?.map((call) => ({
          name: call.Name ?? "tool",
          args: call.RawArgs,
        })),
      });
    } catch {
      return null;
    }
  }
  return entries;
};
