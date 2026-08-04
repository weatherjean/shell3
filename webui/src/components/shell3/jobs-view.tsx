import { ArrowLeftIcon, XIcon } from "lucide-react";
import { useCallback, useEffect, useMemo, useState, type FC } from "react";
import { Button } from "@/components/ui/button";
import {
  Chip,
  Empty,
  Figure,
  Marker,
  Page,
  Pre,
  Ruled,
  Section,
} from "@/components/shell3/page";
import { duration, relativeTime } from "@/lib/format";
import { usePolling } from "@/lib/use-polling";
import { cancelJob, fetchJob, listJobs, type Job } from "@/lib/api";
import { Transcript, parseTranscript } from "@/components/shell3/transcript";
import { useCapabilities } from "@/lib/capabilities";
import { useEvents } from "@/lib/events";
import { cn } from "@/lib/utils";

// What the agent is doing in the background: bash_bg commands and subagents,
// running and finished, with the output each produced.
//
// Set as a ruled ledger — the job id is the entry, elapsed time and exit code
// hang in figure columns so they line up down the page the way a process
// listing does, and the marker picks out what is still running.

/** The ledger's columns, shared by every row so the figures align. */
const LEDGER = "grid-cols-[1fr_74px_52px_22px]";
const ENTRY = "grid grid-cols-[56px_1fr] items-baseline gap-3.5 text-left";

export const JobsView: FC<{ focus?: string | null }> = ({ focus }) => {
  const { live } = useCapabilities();
  const [jobs, setJobs] = useState<Job[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(focus ?? null);

  const refresh = useCallback(() => {
    if (!live) return;
    void listJobs()
      .then((next) => {
        setJobs(next);
        setError(null);
      })
      .catch((err) => setError(String(err)));
  }, [live]);

  // Jobs change without the user doing anything, so this view refreshes itself.
  usePolling(refresh, 3000);

  if (!live) {
    return (
      <Page title="Jobs" note="no backend connected">
        <Empty>Background work appears here once the agent is running.</Empty>
      </Page>
    );
  }
  if (selected) {
    return <JobDetail id={selected} onBack={() => setSelected(null)} onChanged={refresh} />;
  }

  const running = jobs.filter((job) => job.status === "running");
  const finished = jobs.filter((job) => job.status !== "running");

  return (
    <Page
      title="Jobs"
      note={
        error
          ? `could not read jobs: ${error}`
          : running.length > 0
            ? `${running.length} running · ${finished.length} finished`
            : "nothing running"
      }
    >
      <Section title="Running" count={running.length}>
        {running.length === 0 ? (
          <Empty>
            Ask the agent to do something in the background and it shows up here.
          </Empty>
        ) : (
          running.map((job) => (
            <JobRow
              key={job.id}
              job={job}
              onOpen={() => setSelected(job.id)}
              onCancel={() => void cancelJob(job.id).finally(refresh)}
            />
          ))
        )}
      </Section>

      <Section title="Finished" count={finished.length}>
        {finished.length === 0 ? (
          <Empty>Nothing has finished yet.</Empty>
        ) : (
          finished.map((job) => (
            <JobRow key={job.id} job={job} onOpen={() => setSelected(job.id)} />
          ))
        )}
      </Section>
    </Page>
  );
};

const JobRow: FC<{ job: Job; onOpen: () => void; onCancel?: () => void }> = ({
  job,
  onOpen,
  onCancel,
}) => {
  const live = job.status === "running";
  const id = <span className="text-ink font-mono text-[11px]">{job.id}</span>;

  return (
    <Ruled className={cn("group/job", LEDGER)}>
      {/* The id and the label are one target: clicking anywhere in the entry
          opens the job. Cancel stays outside it — nested buttons are invalid,
          and an X inside the open-target would fire both. */}
      <button type="button" onClick={onOpen} className={cn(ENTRY, "min-w-0")}>
        {live ? <Marker className="justify-self-start">{job.id}</Marker> : id}
        <span className="min-w-0">
          <span className="text-ink-2 block truncate font-mono text-[11px]">
            {job.label || job.kind}
          </span>
          {(job.summary || job.error) && (
            <span
              className={cn(
                "mt-1 block truncate text-[11px]",
                job.error ? "text-fail" : "text-ink-3",
              )}
            >
              {job.error || job.summary}
            </span>
          )}
        </span>
      </button>

      <Figure className={live ? "text-ink" : undefined}>
        {job.status === "running"
          ? duration(job.elapsedSeconds)
          : relativeTime(job.endedAt ?? job.startedAt)}
      </Figure>

      <Figure
        className={cn(
          "text-right",
          job.exit === 0 && "text-ok",
          job.exit !== undefined && job.exit !== 0 && "text-fail",
        )}
      >
        {live ? "—" : (job.exit ?? "·")}
      </Figure>

      {onCancel ? (
        <button
          type="button"
          aria-label={`Cancel ${job.id}`}
          onClick={onCancel}
          className="text-ink-3 hover:text-fail justify-self-end p-0.5"
        >
          <XIcon className="size-3" />
        </button>
      ) : (
        <span />
      )}
    </Ruled>
  );
};

const JobDetail: FC<{ id: string; onBack: () => void; onChanged: () => void }> = ({
  id,
  onBack,
  onChanged,
}) => {
  const [job, setJob] = useState<Job | null>(null);
  const [output, setOutput] = useState("");
  const [kind, setKind] = useState<"transcript" | "output">("output");
  const [error, setError] = useState<string | null>(null);
  const { jobProgress } = useEvents();

  const refresh = useCallback(() => {
    void fetchJob(id)
      .then((res) => {
        setJob(res.job);
        setOutput(res.output);
        setKind(res.outputKind);
        setError(null);
      })
      .catch((err) => setError(String(err)));
  }, [id]);

  useEffect(refresh, [refresh]);

  // A running job pushes its output as it goes; re-read on each chunk so the
  // pane tails rather than sitting still.
  useEffect(() => {
    if (jobProgress?.jobId === id) refresh();
  }, [jobProgress, id, refresh]);

  const back = (
    <Button variant="ghost" size="sm" onClick={onBack} className="gap-1.5">
      <ArrowLeftIcon className="size-4" />
      All jobs
    </Button>
  );

  if (error) {
    return (
      <Page title={id} note={error} actions={back}>
        <Empty>This job is no longer in the runtime&rsquo;s memory.</Empty>
      </Page>
    );
  }
  if (!job) {
    return (
      <Page title={id} note="loading…" actions={back}>
        {null}
      </Page>
    );
  }

  return (
    <Page
      title={job.label || job.id}
      note={`${job.kind} · ${job.status} · ${duration(job.elapsedSeconds)}`}
      actions={
        <>
          {job.status === "running" && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => void cancelJob(job.id).then(onChanged).then(refresh)}
            >
              Cancel
            </Button>
          )}
          {back}
        </>
      }
    >
      {/* The chips carry what does not fit in one line of masthead note. */}
      <div className="text-ink-3 -mt-3 flex flex-wrap items-center gap-2 font-mono text-[9.5px]">
        <Chip>{job.id}</Chip>
        <Chip>{job.kind}</Chip>
        <span>started {relativeTime(job.startedAt)}</span>
        {job.exit !== undefined && (
          <span className={job.exit === 0 ? "text-ok" : "text-fail"}>
            exit {job.exit}
          </span>
        )}
        {job.childOpen && <span>still open</span>}
      </div>

      {job.error && (
        <Section title="Error">
          <Pre className="text-fail mt-3">{job.error}</Pre>
        </Section>
      )}
      {job.summary && (
        <Section title="Summary">
          <p className="doc mt-3">{job.summary}</p>
        </Section>
      )}
      <Section title={kind === "transcript" ? "Transcript" : "Output"}>
        <JobOutput output={output} kind={kind} running={job.status === "running"} />
      </Section>
    </Page>
  );
};

/**
 * A command's captured stdout, or a subagent's transcript. The transcript is
 * the child session's raw messages.jsonl — readable by a machine, not a
 * person — so it is parsed into the same view a run gets, falling back to the
 * raw text if it turns out not to be JSONL.
 */
const JobOutput: FC<{ output: string; kind: string; running: boolean }> = ({
  output,
  kind,
  running,
}) => {
  const entries = useMemo(
    () => (kind === "transcript" ? parseTranscript(output) : null),
    [kind, output],
  );

  if (!output) {
    return (
      <Empty>{running ? "No output yet." : "Nothing was captured for this job."}</Empty>
    );
  }
  if (entries) {
    return (
      <div className="max-h-[32rem] overflow-y-auto pt-3">
        <Transcript entries={entries} />
      </div>
    );
  }
  return <Pre className="mt-3 max-h-[32rem] overflow-y-auto">{output}</Pre>;
};
