// Port of the assistant-ui base demo (apps/docs/components/examples/base.tsx)
// adapted for Vite: no next/image, and models come from the backend.
//
// Exports the two pieces the app shell composes: the sidebar's thread list and
// the chat surface itself.

import {
  ComposerAddAttachment,
  ComposerAttachments,
  UserMessageAttachments,
} from "@/components/assistant-ui/attachment";
import { MarkdownText } from "@/components/assistant-ui/markdown-text";
import { DotMatrix } from "@/components/assistant-ui/dot-matrix";
import { MessageTiming } from "@/components/assistant-ui/message-timing";
import { ToolCall } from "@/components/assistant-ui/tool-call";
import {
  ToolGroupContent,
  ToolGroupRoot,
  ToolGroupTrigger,
} from "@/components/assistant-ui/tool-group";
import { SlashCommands } from "@/components/shell3/slash-commands";
import { TooltipIconButton } from "@/components/assistant-ui/tooltip-icon-button";
import {
  Reasoning,
  ReasoningContent,
  ReasoningRoot,
  ReasoningText,
  ReasoningTrigger,
} from "@/components/assistant-ui/reasoning";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  ComposerQuotePreview,
  QuoteBlock,
  SelectionToolbar,
} from "@/components/assistant-ui/quote";
import {
  ActionBarMorePrimitive,
  ActionBarPrimitive,
  AuiIf,
  type AssistantState,
  ComposerPrimitive,
  ErrorPrimitive,
  groupPartByType,
  MessagePrimitive,
  ThreadPrimitive,
  useAui,
  useAuiState,
} from "@assistant-ui/react";
import {
  ArrowDownIcon,
  ArrowUpIcon,
  CheckIcon,
  CopyIcon,
  DownloadIcon,
  MicIcon,
  MoreHorizontalIcon,
  Volume2Icon,
  VolumeXIcon,
  SquareIcon,
} from "lucide-react";
import { createContext, useContext, useState, type FC, type ReactNode } from "react";
import { stopTurn } from "@/lib/api";
import { useCapabilities } from "@/lib/capabilities";
import { useVoiceStatus, VOICE_LABELS } from "@/lib/voice-status";

/**
 * What the voice pipeline is doing. Recording and playback are obvious from
 * the controls; the waits between them — uploading a clip to be transcribed,
 * waiting for speech to be generated — are not, and a control that looks idle
 * while it waits reads as broken.
 */
const VoiceStatus: FC<{ only: string[] }> = ({ only }) => {
  const status = useVoiceStatus();
  if (!only.includes(status)) return null;
  return (
    <span className="text-muted-foreground animate-pulse self-center truncate px-1 text-xs leading-none">
      {VOICE_LABELS[status]}
    </span>
  );
};

// shell3 runs exactly one agent on exactly one model, so this states which —
// it is not a choice.
const ModelLabel: FC = () => {
  const { agent } = useCapabilities();
  if (!agent.model) return null;
  return (
    <span
      className="text-ink-3 px-1 font-mono text-[9px]"
      title={`agent: ${agent.name}`}
    >
      {agent.model}
    </span>
  );
};

// Startup exposes a loading placeholder thread; treat it as a new chat so
// the composer mounts centered. Loads after startup keep the docked layout.
const isNewChatView = (s: AssistantState) =>
  s.thread.messages.length === 0 &&
  (!s.thread.isLoading || s.threads.isLoading);

export const Thread: FC = () => {
  const isEmpty = useAuiState(isNewChatView);

  return (
    <ThreadPrimitive.Root
      className="aui-root aui-thread-root bg-background @container flex h-full flex-col"
      style={{
        ["--thread-max-width" as string]: "47.5rem",
        ["--composer-bg" as string]: "var(--linen-2)",
        // A document has hairlines, not pills.
        ["--composer-radius" as string]: "3px",
        ["--composer-padding" as string]: "0px",
        // The gutter: labels and timestamps hang left of the spine, and every
        // line of the record — including the composer — starts at its edge.
        ["--gutter" as string]: "88px",
      }}
    >
      <ThreadPrimitive.Viewport
        turnAnchor="top"
        data-slot="aui_thread-viewport"
        className={cn(
          "relative flex flex-1 flex-col overflow-x-auto overflow-y-scroll scroll-smooth px-4 pt-7 md:px-7",
          isEmpty && "justify-center",
        )}
      >
        <AuiIf condition={isNewChatView}>
          <ThreadWelcome />
        </AuiIf>

        <div
          data-slot="aui_message-group"
          className="mb-14 flex flex-col gap-y-9 empty:hidden"
        >
          <ThreadPrimitive.Messages>
            {({ message }) => {
              if (message.role === "user") return <UserMessage />;
              return <AssistantMessage />;
            }}
          </ThreadPrimitive.Messages>
        </div>

        <ThreadPrimitive.ViewportFooter
          className={cn(
            "aui-thread-viewport-footer bg-background mx-auto flex w-full max-w-(--thread-max-width) flex-col gap-4 overflow-visible pb-4 md:pb-6",
            // The gutter indent aligns the composer with the message column —
            // but only once there is a gutter. The empty view centers the
            // composer under the heading, so the indent would just skew it.
            !isEmpty && "sticky bottom-0 mt-auto md:pl-[calc(var(--gutter)+1.25rem)]",
          )}
        >
          <ThreadScrollToBottom />
          <Composer />
        </ThreadPrimitive.ViewportFooter>
      </ThreadPrimitive.Viewport>

      <SelectionToolbar />
    </ThreadPrimitive.Root>
  );
};

/**
 * The gutter: who spoke, hung in the margin left of the spine.
 *
 * The spine is the hairline down the gutter's right edge, and it runs the length
 * of the transcript because every line of the record hangs off it — that is the
 * whole structure of the page.
 *
 * Below md the gutter unstacks: the label runs inline above its line and the
 * spine is dropped rather than kept as a stub, since a vertical rule only means
 * anything as a vertical.
 */
const Gutter: FC<{ who: string }> = ({ who }) => (
  // `self-stretch` because the row is baseline-aligned: without it the cell
  // shrinks to the label and the spine becomes a 14px stub.
  <div className="border-rule-2 pb-1.5 md:self-stretch md:border-r md:pr-4 md:pb-0 md:text-right">
    <span className="cap">{who}</span>
  </div>
);

const ThreadScrollToBottom: FC = () => {
  return (
    <ThreadPrimitive.ScrollToBottom asChild>
      <TooltipIconButton
        tooltip="Scroll to bottom"
        variant="outline"
        className="aui-thread-scroll-to-bottom border-rule bg-background hover:bg-lift absolute -top-12 z-10 self-center border p-3 disabled:invisible"
      >
        <ArrowDownIcon />
      </TooltipIconButton>
    </ThreadPrimitive.ScrollToBottom>
  );
};

const ThreadWelcome: FC = () => {
  return (
    <div className="aui-thread-welcome-root mx-auto mb-6 flex w-full max-w-(--thread-max-width) flex-col items-center px-4 text-center">
      <h1 className="aui-thread-welcome-message-inner fade-in slide-in-from-bottom-1 animate-in fill-mode-both font-serif text-[26px] font-medium tracking-[-.015em] duration-200">
        What should the agent do?
      </h1>
    </div>
  );
};

const Composer: FC = () => {
  return (
    <ComposerPrimitive.Root className="aui-composer-root relative flex w-full flex-col">
      {/* The trigger root has to enclose the input as well as the popover:
          the input is what notices the "/" and opens it. */}
      <ComposerPrimitive.Unstable_TriggerPopoverRoot>
        <SlashCommands />
        <ComposerPrimitive.AttachmentDropzone asChild>
          <div
            data-slot="aui_composer-shell"
            className="border-rule focus-within:border-ink-3 data-[dragging=true]:border-ring flex w-full flex-col gap-2 rounded-(--composer-radius) border bg-(--composer-bg) px-3.5 pt-3 pb-2 shadow-[var(--slip-shade)] transition-[border-color] data-[dragging=true]:border-dashed data-[dragging=true]:bg-[var(--lift)]"
          >
            <ComposerQuotePreview />
            <ComposerAttachments />
            <ComposerPrimitive.Input
              rows={1}
              autoFocus
              placeholder="Write to the agent…"
              className="aui-composer-input placeholder:text-ink-3 max-h-32 min-h-9 w-full resize-none bg-transparent text-[14px] leading-[1.55] outline-none"
            />
            <ComposerAction />
          </div>
        </ComposerPrimitive.AttachmentDropzone>
      </ComposerPrimitive.Unstable_TriggerPopoverRoot>
    </ComposerPrimitive.Root>
  );
};

const ComposerAction: FC = () => {
  return (
    <div className="aui-composer-action-wrapper relative flex items-center justify-between">
      <div className="flex min-w-0 items-center gap-1">
        <ComposerAddAttachment />
        <ModelLabel />
        <VoiceStatus only={["recording", "transcribing"]} />
      </div>
      <div className="flex items-center gap-1.5">
        <AuiIf condition={(s) => s.thread.capabilities.dictation}>
          <AuiIf condition={(s) => s.composer.dictation == null}>
            <ComposerPrimitive.Dictate asChild>
              <TooltipIconButton
                tooltip="Voice input"
                side="bottom"
                type="button"
                variant="ghost"
                size="icon"
                className="aui-composer-dictate size-7 rounded-[3px]"
                aria-label="Start voice input"
              >
                <MicIcon className="aui-composer-dictate-icon size-4" />
              </TooltipIconButton>
            </ComposerPrimitive.Dictate>
          </AuiIf>
          <AuiIf condition={(s) => s.composer.dictation != null}>
            <ComposerPrimitive.StopDictation asChild>
              <TooltipIconButton
                tooltip="Stop dictation"
                side="bottom"
                type="button"
                variant="ghost"
                size="icon"
                className="aui-composer-stop-dictation text-destructive size-7 rounded-[3px]"
                aria-label="Stop voice input"
              >
                <SquareIcon className="aui-composer-stop-dictation-icon size-3.5 animate-pulse fill-current" />
              </TooltipIconButton>
            </ComposerPrimitive.StopDictation>
          </AuiIf>
        </AuiIf>
        <AuiIf condition={(s) => !s.thread.isRunning}>
          <ComposerPrimitive.Send asChild>
            <TooltipIconButton
              tooltip="Send message"
              side="bottom"
              type="button"
              variant="default"
              size="icon"
              className="aui-composer-send size-7 rounded-[3px]"
              aria-label="Send message"
            >
              <ArrowUpIcon className="aui-composer-send-icon size-4.5" />
            </TooltipIconButton>
          </ComposerPrimitive.Send>
        </AuiIf>
        <AuiIf condition={(s) => s.thread.isRunning}>
          <StopTurnButton />
        </AuiIf>
      </div>
    </div>
  );
};

/**
 * Stop.
 *
 * Asks the SERVER to end the turn rather than aborting the request here. The
 * difference matters: aborting in the browser kills the connection, so the turn
 * ends with its last tool call never answered — which the client reads as a
 * tool waiting on the user and offers to Allow or Deny, for work already dead.
 * Stopping server-side also kills the background jobs the turn started, and
 * lets the stream close normally, so what was said stays said.
 *
 * If the server cannot be reached, fall back to the local abort: a stop button
 * that does nothing is worse than one that stops untidily.
 */
const StopTurnButton: FC = () => {
  const aui = useAui();
  const [stopping, setStopping] = useState(false);

  const stop = async () => {
    setStopping(true);
    try {
      await stopTurn();
    } catch {
      aui.thread().cancelRun();
    }
  };

  return (
    <Button
      type="button"
      variant="default"
      size="icon"
      disabled={stopping}
      onClick={() => void stop()}
      className="aui-composer-cancel size-7 rounded-[3px]"
      aria-label="Stop generating"
    >
      <SquareIcon className="aui-composer-cancel-icon size-3.5 fill-current" />
    </Button>
  );
};

/**
 * ReconnectContext carries the app shell's stream-recovery routine into the
 * error box: re-attach to the running turn (or fetch the finished reply).
 * null on the mock backend, where there is nothing to reconnect to.
 */
export const ReconnectContext = createContext<(() => void) | null>(null);

const MessageError: FC = () => {
  const reconnect = useContext(ReconnectContext);
  return (
    <MessagePrimitive.Error>
      <ErrorPrimitive.Root className="aui-message-error-root border-destructive bg-destructive/10 text-destructive dark:bg-destructive/5 mt-2 rounded-md border p-3 text-sm dark:text-red-200">
        <ErrorPrimitive.Message className="aui-message-error-message line-clamp-2" />
        {reconnect ? (
          <Button
            variant="outline"
            size="sm"
            className="mt-2"
            onClick={reconnect}
          >
            Reconnect
          </Button>
        ) : null}
      </ErrorPrimitive.Root>
    </MessagePrimitive.Error>
  );
};

const AssistantWorkingIndicator: FC = () => {
  const isEmpty = useAuiState((s) => s.message.content.length === 0);
  if (isEmpty) {
    return (
      <span
        data-slot="aui_assistant-message-indicator"
        className="text-muted-foreground inline-flex items-center gap-2 align-middle"
      >
        <DotMatrix state="connecting" aria-hidden />
        <span className="text-sm">Connecting</span>
      </span>
    );
  }
  return (
    <span
      data-slot="aui_assistant-message-indicator"
      className="animate-pulse font-sans"
      aria-label="Assistant is working"
    >
      {"●"}
    </span>
  );
};

/**
 * A run of reasoning parts, as one card. The closed card carries the opening of
 * the thought so a column of them can be skimmed — several identical
 * "Reasoning" rows say nothing about which one is worth opening.
 */
const ReasoningBlock: FC<{
  indices: readonly number[];
  running: boolean;
  children: ReactNode;
}> = ({ indices, running, children }) => {
  const preview = useAuiState((s) => {
    for (const index of indices) {
      const part = s.message.parts[index] as { text?: string } | undefined;
      const text = part?.text?.trim();
      if (text) return text.replace(/\s+/g, " ");
    }
    return "";
  });

  return (
    <ReasoningRoot variant="ghost" className="border-rule mb-4 border-l-2 pl-4">
      <ReasoningTrigger active={running} preview={preview} />
      <ReasoningContent aria-busy={running}>
        {/* No label indent: the card's own padding is the margin, and the
            thought reads better using the full width it has. */}
        <ReasoningText className="ps-0">{children}</ReasoningText>
      </ReasoningContent>
    </ReasoningRoot>
  );
};

const AssistantMessage: FC = () => {
  // The bar is always shown, so its height is simply its height. The original
  // reserved it and pulled the next message back over it with a negative
  // margin, which is what made the controls collide with the turn below.
  const ACTION_BAR_HEIGHT = "min-h-7 pt-2";

  return (
    <MessagePrimitive.Root
      data-slot="aui_assistant-message-root"
      data-role="assistant"
      className="fade-in slide-in-from-bottom-1 animate-in relative mx-auto grid w-full max-w-(--thread-max-width) grid-cols-1 items-baseline duration-150 md:grid-cols-[var(--gutter)_1fr]"
    >
      <Gutter who="Agent" />

      <div
        data-slot="aui_assistant-message-content"
        className="min-w-0 wrap-break-word md:pl-5"
      >
        <MessagePrimitive.GroupedParts
          groupBy={groupPartByType({
            reasoning: ["group-chainOfThought", "group-reasoning"],
            "tool-call": ["group-chainOfThought", "group-tool"],
            "standalone-tool-call": [],
          })}
        >
          {({ part, children }) => {
            switch (part.type) {
              case "group-chainOfThought":
                return <div data-slot="aui_chain-of-thought">{children}</div>;
              case "group-tool":
                // A group of one is not a group: "1 tool call" hides a single
                // call behind a disclosure that says nothing about it, and the
                // call already collapses itself. Only a real run gets folded.
                if (part.indices.length < 2) return children;
                return (
                  <ToolGroupRoot variant="ghost" className="mb-4">
                    <ToolGroupTrigger
                      count={part.indices.length}
                      active={part.status.type === "running"}
                    />
                    <ToolGroupContent>{children}</ToolGroupContent>
                  </ToolGroupRoot>
                );
              case "group-reasoning":
                return (
                  <ReasoningBlock
                    indices={part.indices}
                    running={part.status.type === "running"}
                  >
                    {children}
                  </ReasoningBlock>
                );
              case "text":
                return <MarkdownText />;
              case "reasoning":
                return <Reasoning {...part} />;
              case "tool-call":
                return part.toolUI ?? <ToolCall {...part} />;
              case "indicator":
                return <AssistantWorkingIndicator />;
              case "data":
                return part.dataRendererUI;
              default:
                return null;
            }
          }}
        </MessagePrimitive.GroupedParts>
        <MessageError />
      </div>

      <div
        data-slot="aui_assistant-message-footer"
        className={cn("flex items-center md:col-start-2 md:pl-4", ACTION_BAR_HEIGHT)}
      >
        <AssistantActionBar />
      </div>
    </MessagePrimitive.Root>
  );
};

const AssistantActionBar: FC = () => {
  return (
    <ActionBarPrimitive.Root
      hideWhenRunning
      className="aui-assistant-action-bar-root text-ink-3 animate-in fade-in -ml-1 flex items-center gap-1 duration-200"
    >
      <ActionBarPrimitive.Copy asChild>
        <TooltipIconButton tooltip="Copy">
          <AuiIf condition={(s) => s.message.isCopied}>
            <CheckIcon className="animate-in zoom-in-50 fade-in duration-200 ease-out" />
          </AuiIf>
          <AuiIf condition={(s) => !s.message.isCopied}>
            <CopyIcon className="animate-in zoom-in-75 fade-in duration-150" />
          </AuiIf>
        </TooltipIconButton>
      </ActionBarPrimitive.Copy>
      <AuiIf condition={(s) => s.thread.capabilities.speech}>
        <AuiIf condition={(s) => s.message.speech == null}>
          <ActionBarPrimitive.Speak asChild>
            <TooltipIconButton tooltip="Read aloud">
              <Volume2Icon />
            </TooltipIconButton>
          </ActionBarPrimitive.Speak>
        </AuiIf>
        <AuiIf condition={(s) => s.message.speech != null}>
          <ActionBarPrimitive.StopSpeaking asChild>
            <TooltipIconButton tooltip="Stop">
              <VolumeXIcon />
            </TooltipIconButton>
          </ActionBarPrimitive.StopSpeaking>
          <VoiceStatus only={["generating", "speaking"]} />
        </AuiIf>
      </AuiIf>
      <ActionBarMorePrimitive.Root>
        <ActionBarMorePrimitive.Trigger asChild>
          <TooltipIconButton
            tooltip="More"
            className="data-[state=open]:bg-accent"
          >
            <MoreHorizontalIcon />
          </TooltipIconButton>
        </ActionBarMorePrimitive.Trigger>
        <ActionBarMorePrimitive.Content
          side="bottom"
          align="start"
          sideOffset={6}
          className="aui-action-bar-more-content bg-popover/95 text-popover-foreground data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 data-[state=open]:animate-in data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 data-[state=closed]:animate-out data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 z-50 min-w-[8rem] overflow-hidden rounded-xl border p-1.5 shadow-lg backdrop-blur-sm"
        >
          <ActionBarPrimitive.ExportMarkdown asChild>
            <ActionBarMorePrimitive.Item className="aui-action-bar-more-item hover:bg-accent hover:text-accent-foreground focus:bg-accent focus:text-accent-foreground flex cursor-pointer items-center gap-2 rounded-lg px-2.5 py-1.5 text-sm outline-none select-none">
              <DownloadIcon className="size-4" />
              Export as Markdown
            </ActionBarMorePrimitive.Item>
          </ActionBarPrimitive.ExportMarkdown>
        </ActionBarMorePrimitive.Content>
      </ActionBarMorePrimitive.Root>
      <MessageTiming />
    </ActionBarPrimitive.Root>
  );
};

const UserMessage: FC = () => {
  return (
    <MessagePrimitive.Root
      data-slot="aui_user-message-root"
      data-role="user"
      className="fade-in slide-in-from-bottom-1 animate-in mx-auto grid w-full max-w-(--thread-max-width) grid-cols-1 items-baseline duration-150 md:grid-cols-[var(--gutter)_1fr]"
    >
      <Gutter who="You" />

      <div className="aui-user-message-content-wrapper relative min-w-0 md:border-l-0 md:pl-5">
        <UserMessageAttachments />
        <div className="aui-user-message-content text-ink peer text-[14px] leading-[1.55] font-medium wrap-break-word empty:hidden">
          <MessagePrimitive.Quote>
            {(quote) => <QuoteBlock {...quote} />}
          </MessagePrimitive.Quote>
          <MessagePrimitive.Parts />
        </div>
      </div>
    </MessagePrimitive.Root>
  );
};
