import {
  CheckIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlusIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react";
import { useEffect, useRef, useState, type FC } from "react";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { TooltipIconButton } from "@/components/assistant-ui/tooltip-icon-button";
import { cn } from "@/lib/utils";
import { threadLabel, type Thread } from "@/lib/threads";
import { relativeTime } from "@/lib/format";
import { Cap, Marker } from "@/components/shell3/page";

export type ThreadListProps = {
  threads: Thread[];
  activeId: string;
  collapsed?: boolean;
  onSelect: (id: string) => void;
  onNewThread: () => void;
  onRename: (id: string, title: string) => void;
  onDelete: (id: string) => void;
};

export const ThreadList: FC<ThreadListProps> = ({
  threads,
  activeId,
  collapsed,
  onSelect,
  onNewThread,
  onRename,
  onDelete,
}) => {
  const [editing, setEditing] = useState<string | null>(null);

  return (
    <div
      className={cn(
        "flex h-full flex-col overflow-hidden transition-[padding,width] duration-200",
        collapsed ? "w-12 px-2 pt-1" : "w-65",
      )}
    >
      {/* An outlined button rather than another row, so the one thing that
          starts a conversation reads as a control and not as a list item. */}
      {collapsed ? (
        <Button
          variant="outline"
          onClick={onNewThread}
          title="New page"
          // size-8 exactly fills the collapsed rail's 32px content box; a wider
          // button overflows its padding and drifts right.
          className="hover:bg-lift size-8 shrink-0 justify-center gap-0 p-0 shadow-none has-[>svg]:px-0"
        >
          <PlusIcon className="size-4 shrink-0" />
        </Button>
      ) : (
        <button
          onClick={onNewThread}
          className="border-rule-2 text-ink-2 hover:text-ink mx-5 flex shrink-0 items-center gap-2 border-y py-2 text-[12.5px] transition-colors"
        >
          <PlusIcon className="size-3.5 shrink-0" />
          <span className="whitespace-nowrap">Start a new page</span>
        </button>
      )}

      <div
        aria-hidden={collapsed}
        inert={collapsed}
        className={cn(
          "mt-1 flex min-h-0 flex-1 flex-col transition-opacity duration-150",
          collapsed && "pointer-events-none opacity-0",
        )}
      >
        <div className="px-5 pt-4 pb-1.5">
          <Cap>Conversations</Cap>
          {threads.length > 0 && (
            <span className="text-ink-3 ml-2 font-mono text-[9.5px]">{threads.length}</span>
          )}
        </div>
        <ul className="min-h-0 flex-1 overflow-y-auto">
          {threads.map((thread) => (
            <li key={thread.id}>
              {editing === thread.id ? (
                <RenameRow
                  thread={thread}
                  onCommit={(title) => {
                    onRename(thread.id, title);
                    setEditing(null);
                  }}
                  onCancel={() => setEditing(null)}
                />
              ) : (
                <ThreadRow
                  thread={thread}
                  active={thread.id === activeId}
                  onSelect={() => onSelect(thread.id)}
                  onRename={() => setEditing(thread.id)}
                  onDelete={() => onDelete(thread.id)}
                />
              )}
            </li>
          ))}

          {threads.length === 0 && (
            <li className="text-ink-3 px-5 py-2 text-[12.5px]">No conversations yet.</li>
          )}
        </ul>
      </div>
    </div>
  );
};

const ThreadRow: FC<{
  thread: Thread;
  active: boolean;
  onSelect: () => void;
  onRename: () => void;
  onDelete: () => void;
}> = ({ thread, active, onSelect, onRename, onDelete }) => (
  <div className="group/thread relative">
    {/* The button fills the row so anywhere on it selects the thread; the
        right padding keeps its text clear of the menu that sits on top. */}
    <button
      onClick={onSelect}
      onDoubleClick={onRename}
      aria-current={active ? "page" : undefined}
      title={threadLabel(thread)}
      className={cn(
        "border-rule-2 block w-full border-t py-2 pr-9 pl-5 text-left transition-colors",
        active ? "" : "hover:bg-lift",
      )}
    >
      {active ? (
        <Marker className="inline-block max-w-full truncate align-bottom text-[12.5px]">
          {threadLabel(thread)}
        </Marker>
      ) : (
        <span className="text-ink-2 block truncate text-[12.5px]">
          {threadLabel(thread)}
        </span>
      )}
      {thread.updatedAt && (
        <span className="text-ink-3 mt-1 block font-mono text-[9px]">
          {relativeTime(thread.updatedAt)}
        </span>
      )}
    </button>

    <Popover>
      <PopoverTrigger
        render={
          <TooltipIconButton
            tooltip="More"
            side="bottom"
            variant="ghost"
            className="text-ink-3 hover:text-ink absolute top-1.5 right-2 size-6 shrink-0"
          >
            <MoreHorizontalIcon className="size-3.5" />
          </TooltipIconButton>
        }
      />
      <PopoverContent align="start" sideOffset={4} className="w-44 p-1">
        <MenuItem icon={<PencilIcon className="size-4" />} onClick={onRename}>
          Rename
        </MenuItem>
        <MenuItem
          icon={<Trash2Icon className="size-4" />}
          onClick={onDelete}
          destructive
        >
          Delete
        </MenuItem>
      </PopoverContent>
    </Popover>
  </div>
);

const MenuItem: FC<{
  icon: React.ReactNode;
  destructive?: boolean;
  onClick: () => void;
  children: React.ReactNode;
}> = ({ icon, destructive, onClick, children }) => (
  <button
    onClick={onClick}
    className={cn(
      "hover:bg-accent flex w-full cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5 text-sm",
      destructive && "text-destructive",
    )}
  >
    {icon}
    {children}
  </button>
);

const RenameRow: FC<{
  thread: Thread;
  onCommit: (title: string) => void;
  onCancel: () => void;
}> = ({ thread, onCommit, onCancel }) => {
  const [value, setValue] = useState(thread.title || threadLabel(thread));
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.select();
  }, []);

  const commit = () => onCommit(value.trim());

  return (
    <div className="bg-lift border-rule-2 flex h-9 items-center gap-1 border-t pr-1 pl-5">
      <input
        ref={inputRef}
        value={value}
        autoFocus
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") commit();
          if (e.key === "Escape") onCancel();
        }}
        onBlur={commit}
        aria-label="Conversation name"
        className="min-w-0 flex-1 bg-transparent text-sm outline-none"
      />
      <TooltipIconButton
        tooltip="Save"
        side="bottom"
        variant="ghost"
        className="size-6 shrink-0"
        // onMouseDown so the click lands before the input's blur commits.
        onMouseDown={(e) => {
          e.preventDefault();
          commit();
        }}
      >
        <CheckIcon className="size-3.5" />
      </TooltipIconButton>
      <TooltipIconButton
        tooltip="Cancel"
        side="bottom"
        variant="ghost"
        className="size-6 shrink-0"
        onMouseDown={(e) => {
          e.preventDefault();
          onCancel();
        }}
      >
        <XIcon className="size-3.5" />
      </TooltipIconButton>
    </div>
  );
};
