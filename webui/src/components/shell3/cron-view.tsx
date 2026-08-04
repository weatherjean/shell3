import { ArrowLeftIcon, PlayIcon } from "lucide-react";
import { useCallback, useState, type FC } from "react";
import { Button } from "@/components/ui/button";
import {
  Empty,
  Figure,
  Marker,
  Page,
  Pair,
  Ruled,
  Section,
} from "@/components/shell3/page";
import { relativeTime } from "@/lib/format";
import { usePolling } from "@/lib/use-polling";
import { listCron, runCronJob, type CronJob } from "@/lib/api";
import { useCapabilities } from "@/lib/capabilities";

// Scheduled work: what runs on a timer, what it asks for, when it last ran, and
// a way to fire one now instead of waiting for its schedule.
//
// Set as a timetable. The schedule comes first, in the figure column, because
// that is what you scan a timetable by — and each job's prompt is its brief,
// set in the serif italic, since the prompt is what the job actually IS.

const SLOT = "grid-cols-[112px_1fr_auto]";

export const CronView: FC<{ onOpenJob: (jobId: string) => void }> = ({ onOpenJob }) => {
  const { live } = useCapabilities();
  const [jobs, setJobs] = useState<CronJob[]>([]);
  const [armed, setArmed] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [ran, setRan] = useState<string | null>(null);

  const refresh = useCallback(() => {
    if (!live) return;
    void listCron()
      .then((res) => {
        setJobs(res.jobs);
        setArmed(res.armed);
        setError(null);
      })
      .catch((err) => setError(String(err)));
  }, [live]);

  usePolling(refresh, 10000);

  const fire = (name: string) => {
    setRan(null);
    void runCronJob(name)
      .then(() => setRan(`Started ${name}. Its result arrives in notifications.`))
      .catch((err) => setRan(`Could not start ${name}: ${err}`))
      .finally(refresh);
  };

  if (!live) {
    return (
      <Page title="Cron" note="no backend connected">
        <Empty>Scheduled jobs appear here once the agent is running.</Empty>
      </Page>
    );
  }

  const job = jobs.find((j) => j.name === selected);
  if (job) {
    return (
      <CronDetail
        job={job}
        onBack={() => setSelected(null)}
        onRun={() => fire(job.name)}
        onOpenJob={onOpenJob}
      />
    );
  }

  return (
    <Page
      title="Cron"
      note={
        error
          ? `could not read the schedule: ${error}`
          : armed
            ? `${jobs.length} scheduled`
            : "no scheduler armed — nothing fires on its own"
      }
    >
      {ran && <p className="doc border-rule border-l-2 pl-4 text-[14px]">{ran}</p>}

      <Section title="Scheduled" count={jobs.length}>
        {jobs.length === 0 ? (
          <Empty>
            Nothing scheduled. Add a file to cron/ — its frontmatter names the
            schedule and which agent runs it.
          </Empty>
        ) : (
          jobs.map((entry) => (
            <Ruled key={entry.name} className={`group/cron items-start ${SLOT}`}>
              <span className="text-ink font-mono text-[11px]">{entry.schedule}</span>

              <button
                type="button"
                onClick={() => setSelected(entry.name)}
                className="min-w-0 text-left"
              >
                <span className="text-ink block truncate text-[13.5px] font-medium">
                  {entry.name}
                </span>
                <span className="text-ink-3 mt-1 block truncate font-mono text-[9.5px]">
                  {entry.agent}
                  {" · "}
                  {entry.direct ? "direct delivery" : "notifier triage"}
                  {entry.workdir ? ` · ${entry.workdir}` : ""}
                </span>
                {entry.prompt && (
                  <span className="doc border-rule text-ink-2 mt-2.5 block border-l-2 pl-3 text-[13px] italic">
                    {entry.prompt.length > 180
                      ? `${entry.prompt.slice(0, 180).trimEnd()}…`
                      : entry.prompt}
                  </span>
                )}
              </button>

              <span className="flex flex-col items-end gap-2">
                <Figure className="text-[9.5px] whitespace-nowrap">
                  {entry.lastRun ? `ran ${relativeTime(entry.lastRun)}` : "never run"}
                </Figure>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => fire(entry.name)}
                  disabled={!armed}
                  className="border-rule hover:bg-mark-fill hover:text-swipe hover:border-mark-fill h-auto gap-1.5 px-2.5 py-1 text-[11px] font-normal transition"
                >
                  <PlayIcon className="size-3" />
                  Run now
                </Button>
              </span>
            </Ruled>
          ))
        )}
      </Section>
    </Page>
  );
};

const CronDetail: FC<{
  job: CronJob;
  onBack: () => void;
  onRun: () => void;
  onOpenJob: (jobId: string) => void;
}> = ({ job, onBack, onRun, onOpenJob }) => (
  <Page
    title={job.name}
    note={`${job.schedule} · runs ${job.agent}`}
    actions={
      <>
        <Button variant="ghost" size="sm" onClick={onRun} className="gap-1.5">
          <PlayIcon className="size-4" />
          Run now
        </Button>
        <Button variant="ghost" size="sm" onClick={onBack} className="gap-1.5">
          <ArrowLeftIcon className="size-4" />
          All jobs
        </Button>
      </>
    }
  >
    <Section title="Entry">
      <div className="flex flex-col">
        <Pair k="Schedule">
          <Marker>{job.schedule}</Marker>
        </Pair>
        <Pair k="Agent">{job.agent}</Pair>
        {job.workdir && <Pair k="Working directory">{job.workdir}</Pair>}
        <Pair k="Delivery">
          {job.direct ? "direct — wakes the agent" : "triaged by the notifier"}
        </Pair>
        <Pair k="Last run">
          {job.lastRun ? relativeTime(job.lastRun) : "never since the server started"}
        </Pair>
      </div>
      {job.lastJobId && (
        <button
          type="button"
          onClick={() => onOpenJob(job.lastJobId!)}
          className="text-ink-2 hover:text-gold border-rule hover:border-gold mt-3 border-b border-dotted text-[12px]"
        >
          See what it did
        </button>
      )}
    </Section>

    {/* The prompt is the brief: what this job is for, in its own words. */}
    <Section title="Brief">
      {job.prompt ? (
        <p className="doc text-ink-2 border-rule mt-3 border-l-2 pl-4 text-[14px] italic">
          {job.prompt}
        </p>
      ) : (
        <Empty>This job carries no prompt.</Empty>
      )}
    </Section>
  </Page>
);
