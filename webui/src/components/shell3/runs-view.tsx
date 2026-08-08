import { ArrowLeftIcon } from "lucide-react";
import { useCallback, useEffect, useState, type FC } from "react";
import { Button } from "@/components/ui/button";
import { Chip, Empty, Figure, Page, Ruled, Section } from "@/components/shell3/page";
import { relativeTime } from "@/lib/format";
import { usePolling } from "@/lib/use-polling";
import { fetchRun, listRuns, type Run, type TranscriptEntry } from "@/lib/api";
import { Transcript } from "@/components/shell3/transcript";
import { useCapabilities } from "@/lib/capabilities";

// Every session on disk — conversations, subagent children, cron runs, and
// terminal sessions from `shell3 ask`. The chat view replays only what was
// said; this replays how it was done.
//
// Set as an archive index: the run id is the shelf mark, so it leads, and the
// preview is the subject line beside it.

const ARCH = "grid-cols-[136px_1fr_auto]";

export const RunsView: FC<{ focus?: string | null }> = ({ focus }) => {
  const { live } = useCapabilities();
  const [runs, setRuns] = useState<Run[]>([]);
  const [truncated, setTruncated] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // A notification's "See the run" arrives with the run already chosen.
  const [selected, setSelected] = useState<string | null>(focus ?? null);

  const refresh = useCallback(() => {
    if (!live) return;
    void listRuns()
      .then((res) => {
        setRuns(res.runs);
        setTruncated(res.truncated);
        setError(null);
      })
      .catch((err) => setError(String(err)));
  }, [live]);

  usePolling(refresh, 10000);

  if (!live) {
    return (
      <Page title="Runs" note="no backend connected">
        <Empty>Past sessions appear here once the agent has run.</Empty>
      </Page>
    );
  }
  if (selected) {
    return <RunDetail id={selected} onBack={() => setSelected(null)} />;
  }

  return (
    <Page
      title="Runs"
      note={
        error
          ? `could not read the run store: ${error}`
          : `${runs.length} kept, newest first${truncated ? " · older ones on disk" : ""}`
      }
    >
      <Section
        title="Sessions"
        count={runs.length}
        aside={truncated ? "listing truncated" : undefined}
      >
        {runs.length === 0 ? (
          <Empty>Nothing has run yet.</Empty>
        ) : (
          runs.map((run) => (
            <Ruled
              key={run.id}
              className={ARCH}
              onClick={() => setSelected(run.id)}
              title={run.id}
            >
              <span className="text-ink truncate font-mono text-[10.5px]">{run.id}</span>
              <span className="min-w-0">
                <span className="text-ink-2 block truncate text-[12.5px]">
                  {run.preview || <span className="text-ink-3">(no preview)</span>}
                </span>
                <span className="mt-1 flex items-center gap-2">
                  <Chip>{run.threadId ? "chat" : "session"}</Chip>
                </span>
              </span>
              <Figure className="text-[10px] whitespace-nowrap">
                {run.messages} · {relativeTime(run.lastAt)}
              </Figure>
            </Ruled>
          ))
        )}
      </Section>
    </Page>
  );
};

const RunDetail: FC<{ id: string; onBack: () => void }> = ({ id, onBack }) => {
  const [entries, setEntries] = useState<TranscriptEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let stale = false;
    void fetchRun(id)
      .then((res) => !stale && setEntries(res.entries))
      .catch((err) => !stale && setError(String(err)));
    return () => {
      stale = true;
    };
  }, [id]);

  const back = (
    <Button variant="ghost" size="sm" onClick={onBack} className="gap-1.5">
      <ArrowLeftIcon className="size-4" />
      All runs
    </Button>
  );

  if (error) {
    return (
      <Page title={id} note="could not be read" actions={back}>
        <Empty>Could not read this run: {error}</Empty>
      </Page>
    );
  }

  return (
    <Page title={id} note="replayed at full fidelity" actions={back}>
      {entries === null ? (
        <Empty>Loading…</Empty>
      ) : entries.length === 0 ? (
        <Empty>This run has no stored messages.</Empty>
      ) : (
        <Transcript entries={entries} />
      )}
    </Page>
  );
};
