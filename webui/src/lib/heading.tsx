import {
  createContext,
  use,
  useEffect,
  useMemo,
  useState,
  type FC,
  type ReactNode,
} from "react";

/**
 * The masthead IS the open page's heading.
 *
 * Every operational view used to print its own `<h1>` directly under a header
 * that already named it, so "Jobs" appeared twice down the left edge. Deduping
 * by hand does not survive: a detail view (one job, one run) needs the masthead
 * to say what it is showing, and only the view knows that.
 *
 * So the page publishes its heading upward and the masthead renders it. The
 * dedup is then structural — there is one place a title can appear.
 */
export type Heading = { title: string; note?: string };

type HeadingContextValue = {
  heading: Heading | null;
  publish: (heading: Heading | null) => void;
};

const HeadingContext = createContext<HeadingContextValue | null>(null);

export const HeadingProvider: FC<{ children: ReactNode }> = ({ children }) => {
  const [heading, setHeading] = useState<Heading | null>(null);
  const value = useMemo(() => ({ heading, publish: setHeading }), [heading]);
  return <HeadingContext value={value}>{children}</HeadingContext>;
};

/** What the masthead should show. Null while no view has published. */
export const useHeading = (): Heading | null => use(HeadingContext)?.heading ?? null;

/**
 * Publishes this page's heading for as long as it is mounted, clearing it on the
 * way out so a stale title never outlives its page.
 *
 * The note is a plain string on purpose. As a ReactNode it would be a fresh
 * element on nearly every render, so it could not go in the dependency list
 * without looping — and left out of the list it would freeze, which matters
 * because these views poll: Jobs would keep saying "2 running" after the count
 * changed. Anything richer than a line of text belongs in the page body.
 */
export const usePublishHeading = (title: string, note?: string) => {
  const ctx = use(HeadingContext);
  const publish = ctx?.publish;
  useEffect(() => {
    if (!publish) return;
    publish({ title, note });
    return () => publish(null);
  }, [publish, title, note]);
};
