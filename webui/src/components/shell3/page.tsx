import type { FC, ReactNode } from "react";
import { cn } from "@/lib/utils";
import { usePublishHeading } from "@/lib/heading";

/**
 * The furniture every page in the document shares, so Jobs, Cron, Runs, Status
 * and Files read as one bound thing rather than five screens.
 *
 * Four devices carry all of it:
 *
 *   Section  the ruled section head — a tracked-caps label over a hairline
 *   Leader   the dotted leader, wherever a name on the left owes a figure right
 *   Figure   the hanging figure column: mono, tabular, right-aligned
 *   Marker   the highlighter, which only ever marks what is live
 *
 * Nothing here draws a card. A document has hairlines and indentation; boxing
 * every group is what made the old views read as a dashboard.
 */

/** One page of the document. Its title goes to the masthead, not into the body. */
export const Page: FC<{
  title: string;
  /** One line of plain text for the masthead. Richer detail belongs in `children`. */
  note?: string;
  actions?: ReactNode;
  children: ReactNode;
}> = ({ title, note, actions, children }) => {
  usePublishHeading(title, note);
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-[800px] flex-col gap-7 px-7 pt-7 pb-11">
        {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
        {children}
      </div>
    </div>
  );
};

/** A tracked-caps label. The document's only kind of small heading. */
export const Cap: FC<{ children: ReactNode; className?: string }> = ({
  children,
  className,
}) => <span className={cn("cap", className)}>{children}</span>;

/**
 * A ruled section head. The rule under it is one pixel of the dim tier: on the
 * dyed stock a 2px near-white bar becomes the loudest thing on the page and
 * flattens everything set below it.
 */
export const Section: FC<{
  title: string;
  count?: number;
  /** Set right, in the head's own voice — a source, a scope, a timezone. */
  aside?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}> = ({ title, count, aside, actions, children }) => (
  <section className="min-w-0">
    <div className="border-ink-3 flex items-baseline gap-2 border-b pb-1.5">
      <Cap>{title}</Cap>
      {count !== undefined && (
        <span className="text-ink-3 font-mono text-[9.5px] tabular-nums">{count}</span>
      )}
      {(aside || actions) && (
        <div className="text-ink-3 ml-auto flex items-center gap-3 font-mono text-[9.5px]">
          {aside}
          {actions}
        </div>
      )}
    </div>
    {children}
  </section>
);

/**
 * A ruled row. The hairline belongs to the row, not to a box around the group,
 * so a list can end without a border closing it off.
 */
export const Ruled: FC<{
  children: ReactNode;
  className?: string;
  onClick?: () => void;
  title?: string;
}> = ({ children, className, onClick, title }) => {
  const shared = cn(
    "border-rule-2 hover:bg-lift grid w-full items-baseline gap-3.5 border-b px-0.5 py-2.5 text-left transition-colors",
    className,
  );
  return onClick ? (
    <button type="button" onClick={onClick} title={title} className={shared}>
      {children}
    </button>
  ) : (
    <div className={shared}>{children}</div>
  );
};

/** The dotted leader, carrying the eye from a name to the figure it owes. */
export const Leader: FC = () => <span className="leader" aria-hidden />;

/**
 * One line of a specification: a name on the left owing a value on the right,
 * joined by a leader. The spec sheet, the cron entry and the file listing are
 * all made of these.
 *
 * On a narrow phone the leader has no room left to lead, so the pair stacks and
 * drops it rather than shrinking to a stub.
 */
export const Pair: FC<{
  k: ReactNode;
  children: ReactNode;
  className?: string;
}> = ({ k, children, className }) => (
  <div
    className={cn(
      "border-rule-2 hover:bg-lift flex flex-col items-baseline gap-0.5 border-b px-0.5 py-2 min-[460px]:flex-row min-[460px]:gap-0",
      className,
    )}
  >
    <span className="text-ink-2 text-[12.5px]">{k}</span>
    <span className="leader hidden min-[460px]:block" aria-hidden />
    <span className="text-ink font-mono text-[11px] min-[460px]:text-right">
      {children}
    </span>
  </div>
);

/** A hanging figure: mono, tabular, right-aligned, so a column of them lines up. */
export const Figure: FC<{ children: ReactNode; className?: string }> = ({
  children,
  className,
}) => (
  <span className={cn("text-ink-2 font-mono text-[11px] tabular-nums", className)}>
    {children}
  </span>
);

/**
 * The marker. The signature of the whole interface, and it only ever marks what
 * is live — a running job, the next cron to fire, the redacted `.env`.
 */
export const Marker: FC<{ children: ReactNode; className?: string }> = ({
  children,
  className,
}) => <span className={cn("swiped", className)}>{children}</span>;

export const Empty: FC<{ children: ReactNode }> = ({ children }) => (
  <p className="text-ink-3 px-0.5 py-3 text-[12.5px]">{children}</p>
);

/** A mono tag: hairline, not a filled pill. For tool names and kinds. */
export const Chip: FC<{ children: ReactNode; title?: string }> = ({
  children,
  title,
}) => (
  <span
    title={title}
    className="border-rule-2 text-ink-3 rounded-[3px] border px-1.5 py-px font-mono text-[9px] tracking-[.04em] whitespace-nowrap uppercase"
  >
    {children}
  </span>
);

/**
 * Quoted machine output: indented behind its own rule, the way a block quote is
 * set. No filled background — on a document, a grey box reads as pasted in.
 */
export const Pre: FC<{ children: ReactNode; className?: string }> = ({
  children,
  className,
}) => (
  <pre
    className={cn(
      "border-rule text-ink-2 overflow-x-auto border-l-2 py-0.5 pl-4 font-mono text-[11px] leading-[1.7]",
      "break-words whitespace-pre-wrap",
      className,
    )}
  >
    {children}
  </pre>
);
