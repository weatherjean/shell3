"use client";

/**
 * The `/` menu in the composer.
 *
 * A slash command acts on the conversation rather than continuing it — the
 * server answers it without the model ever seeing it. Typing `/` opens this
 * list; picking one sends it. Commands are declared once here and matched
 * server-side in `internal/webui/command.go`; keep the two in step.
 */

import { useMemo, type FC } from "react";
import { ComposerPrimitive, useAui } from "@assistant-ui/react";
import { unstable_useSlashCommandAdapter } from "@assistant-ui/react";
import { ArchiveIcon } from "lucide-react";
import { cn } from "@/lib/utils";

type CommandSpec = {
  id: string;
  description: string;
  icon: FC<{ className?: string }>;
};

const COMMANDS: CommandSpec[] = [
  {
    id: "compact",
    description: "Summarise the conversation so far and free the context it was using",
    icon: ArchiveIcon,
  },
];

const ICONS = Object.fromEntries(COMMANDS.map((c) => [c.id, c.icon]));

/**
 * Mount inside `ComposerPrimitive.Root`. Renders nothing until `/` is typed.
 */
export const SlashCommands: FC = () => {
  const aui = useAui();

  const commands = useMemo(
    () =>
      COMMANDS.map((command) => ({
        id: command.id,
        label: `/${command.id}`,
        description: command.description,
        icon: command.id,
        execute: () => {
          // Sent as an ordinary message: the server recognises it, so the
          // command lands in the conversation where it happened.
          aui.thread().append(`/${command.id}`);
        },
      })),
    [aui],
  );

  const slash = unstable_useSlashCommandAdapter({ commands, removeOnExecute: true });

  return (
    <ComposerPrimitive.Unstable_TriggerPopover
      char="/"
      adapter={slash.adapter}
      className={cn(
        "aui-slash-commands bg-popover text-popover-foreground absolute inset-x-0 bottom-full z-50 mb-2",
        "overflow-hidden rounded-lg border p-1 shadow-md",
        "animate-in fade-in-0 slide-in-from-bottom-1 duration-150",
      )}
    >
      <ComposerPrimitive.Unstable_TriggerPopover.Action {...slash.action} />
      <ComposerPrimitive.Unstable_TriggerPopoverItems>
        {(items) =>
          items.length === 0 ? (
            <p className="text-muted-foreground px-2 py-1.5 text-sm">No matching command</p>
          ) : (
            items.map((item, index) => {
              const Icon = ICONS[item.id];
              return (
                <ComposerPrimitive.Unstable_TriggerPopoverItem
                  key={item.id}
                  item={item}
                  index={index}
                  className={cn(
                    "flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left text-sm",
                    "data-highlighted:bg-accent data-highlighted:text-accent-foreground",
                    "hover:bg-accent hover:text-accent-foreground",
                  )}
                >
                  {Icon && <Icon className="text-muted-foreground size-4 shrink-0" />}
                  <span className="shrink-0 font-medium">{item.label}</span>
                  {item.description && (
                    <span className="text-muted-foreground min-w-0 flex-1 truncate text-xs">
                      {item.description}
                    </span>
                  )}
                </ComposerPrimitive.Unstable_TriggerPopoverItem>
              );
            })
          )
        }
      </ComposerPrimitive.Unstable_TriggerPopoverItems>
    </ComposerPrimitive.Unstable_TriggerPopover>
  );
};
