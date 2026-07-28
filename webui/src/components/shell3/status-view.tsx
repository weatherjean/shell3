import { useCallback, useState, type FC, type ReactNode } from "react";
import { fetchStatus, reloadConfig, type StatusReport } from "@/lib/api";
import { useCapabilities } from "@/lib/capabilities";
import { Button } from "@/components/ui/button";
import {
  Chip,
  Empty,
  Figure,
  Marker,
  Page,
  Pair,
  Pre,
  Ruled,
  Section,
} from "@/components/shell3/page";
import { duration, relativeTime } from "@/lib/format";
import { usePolling } from "@/lib/use-polling";
import { cn } from "@/lib/utils";

// What this agent actually is, right now: its model and tools, what the last
// turn cost, what the config warned about at load, and whether the shell is
// guarded.
//
// Set as a specification sheet — a column of names owing values, joined by
// leaders. Config warnings are marginalia rather than coloured alert boxes: on
// a printed document a tinted panel reads as pasted in from another system.

const SAMPLE: StatusReport = {
  version: "0.2.0",
  configDir: "~/.shell3",
  uptimeSeconds: 7412,
  warnings: [],
  gateArmed: false,
  params: [],
  agent: {
    name: "agent",
    model: "qwen3.7-plus",
    contextWindow: 128000,
    tools: [
      { name: "bash", description: "Run a shell command" },
      { name: "bash_bg", description: "Run a shell command in the background" },
      { name: "edit_file", description: "Edit a file in place" },
    ],
  },
  subagents: [
    { name: "explorer", description: "Reads code and reports back", model: "qwen3.7-plus" },
  ],
  projects: [],
  skills: [{ name: "scripting", description: "Write reusable wrapper scripts" }],
  cron: [],
  mcp: [],
  jobs: { running: 0, capacity: 8 },
};

/** A named thing with a description and a figure: a subagent, a skill, a job. */
const Entry: FC<{ name: ReactNode; detail?: string; trailing?: ReactNode }> = ({
  name,
  detail,
  trailing,
}) => (
  <Ruled className="grid-cols-[1fr_auto]">
    <span className="min-w-0">
      <span className="text-ink block truncate text-[12.5px] font-medium">{name}</span>
      {detail && (
        <span className="text-ink-3 mt-1 block truncate text-[11.5px]">{detail}</span>
      )}
    </span>
    {trailing ? (
      <Figure className="whitespace-nowrap">{trailing}</Figure>
    ) : (
      <span />
    )}
  </Ruled>
);

/**
 * A warning is marginalia: flagged in the margin, set in the document's own
 * voice. Never a tinted panel.
 */
const Marginal: FC<{ flag: string; bad?: boolean; children: ReactNode }> = ({
  flag,
  bad,
  children,
}) => (
  <div className="border-rule-2 grid gap-1.5 border-b px-0.5 py-2.5 min-[560px]:grid-cols-[76px_1fr] min-[560px]:gap-3.5">
    <span
      className={cn(
        "font-mono text-[9px] tracking-[.04em] uppercase min-[560px]:text-right",
        bad ? "text-fail" : "text-gold",
      )}
    >
      {flag}
    </span>
    <p className="doc text-ink-2 text-[13.5px]">{children}</p>
  </div>
);

export const StatusView: FC = () => {
  const { live } = useCapabilities();
  const [status, setStatus] = useState<StatusReport | null>(live ? null : SAMPLE);
  const [error, setError] = useState<string | null>(null);
  const [reloadResult, setReloadResult] = useState<string | null>(null);

  const refresh = useCallback(() => {
    if (!live) return;
    void fetchStatus()
      .then((report) => {
        setStatus(report);
        setError(null);
      })
      .catch((err) => setError(String(err)));
  }, [live]);

  usePolling(refresh, 5000);

  if (error && !status) {
    return (
      <Page title="Status" note="could not be read">
        <Empty>{error}</Empty>
      </Page>
    );
  }
  if (!status) {
    return (
      <Page title="Status" note="reading configuration…">
        {null}
      </Page>
    );
  }

  const { agent, usage } = status;
  const contextUsed =
    usage && agent.contextWindow ? usage.prompt / agent.contextWindow : null;

  return (
    <Page
      title="Status"
      note={[
        `shell3 ${status.version}`,
        `up ${duration(status.uptimeSeconds)}`,
        !live && "sample data",
        error && "refresh failed — last good read",
      ]
        .filter(Boolean)
        .join(" · ")}
      actions={
        live ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={() =>
              void reloadConfig()
                .then((res) => setReloadResult(res.result))
                .catch((err) => setReloadResult(String(err)))
                .finally(refresh)
            }
          >
            Reload config
          </Button>
        ) : undefined
      }
    >
      {reloadResult && (
        <p className="doc border-rule border-l-2 pl-4 text-[14px]">{reloadResult}</p>
      )}

      <Section title="The agent" aside={status.configDir}>
        <div className="flex flex-col">
          <Pair k="Name">{agent.name}</Pair>
          <Pair k="Model">{agent.status || agent.model}</Pair>
          {agent.contextWindow ? (
            <Pair k="Context window">
              {agent.contextWindow.toLocaleString()} tokens
            </Pair>
          ) : null}
          {/* The gate decides whether the shell is guarded. It is the first thing
              worth knowing and the easiest to forget, so an unarmed gate is
              marked rather than merely stated. */}
          <Pair k="Command gate">
            {status.gateArmed ? (
              "armed · hooks/tool-call.sh"
            ) : (
              <Marker>not armed</Marker>
            )}
          </Pair>
          <Pair k="Background">
            {status.jobs.running === 0
              ? `idle · up to ${status.jobs.capacity} at once`
              : `${status.jobs.running} of ${status.jobs.capacity} running`}
          </Pair>
        </div>

        <div className="mt-3 flex flex-wrap gap-1.5">
          {agent.tools.map((tool) => (
            <Chip key={tool.name} title={tool.description}>
              {tool.name}
            </Chip>
          ))}
          {agent.tools.length === 0 && <Chip>no tools</Chip>}
        </div>
      </Section>

      {usage && (
        <Section title="Context" aside="last turn">
          {contextUsed !== null && (
            <div className="mt-3">
              <div className="bg-rule-2 h-[5px] overflow-hidden rounded-[3px]">
                <div
                  className={cn(
                    "h-full rounded-[3px]",
                    contextUsed > 0.85 ? "bg-fail" : "bg-mark-fill",
                  )}
                  style={{ width: `${Math.min(100, contextUsed * 100)}%` }}
                />
              </div>
              <div className="text-ink-3 mt-2 flex justify-between font-mono text-[9.5px]">
                <span>
                  {usage.prompt.toLocaleString()} of{" "}
                  {agent.contextWindow?.toLocaleString()}
                </span>
                <span className="tabular-nums">
                  {Math.round(contextUsed * 100)}% used
                </span>
              </div>
            </div>
          )}
          <div className="mt-2 flex flex-col">
            <Pair k="Sent">
              {usage.prompt.toLocaleString()} in · {usage.completion.toLocaleString()} out
            </Pair>
            <Pair k="Total">{usage.total.toLocaleString()}</Pair>
          </div>
        </Section>
      )}

      {status.warnings.length > 0 && (
        <Section title="Noted at load" count={status.warnings.length}>
          {status.warnings.map((warning, i) => (
            <Marginal key={i} flag="warning">
              {warning}
            </Marginal>
          ))}
        </Section>
      )}

      <Section title="Subagents" count={status.subagents.length}>
        {status.subagents.length === 0 ? (
          <Empty>No subagents. Add a file to agents/ to register one.</Empty>
        ) : (
          status.subagents.map((sub) => (
            <Entry
              key={sub.name}
              name={sub.name}
              detail={sub.description}
              trailing={sub.model}
            />
          ))
        )}
      </Section>

      <Section title="Projects" count={status.projects.length}>
        {status.projects.length === 0 ? (
          <Empty>No projects. Run shell3 project new to add one.</Empty>
        ) : (
          status.projects.map((project) => (
            <Entry
              key={project.name}
              name={project.name}
              detail={project.description}
              trailing={project.workdir}
            />
          ))
        )}
      </Section>

      <Section title="Skills" count={status.skills.length}>
        {status.skills.length === 0 ? (
          <Empty>No skills. Add markdown files to skills/.</Empty>
        ) : (
          status.skills.map((skill) => (
            <Entry key={skill.name} name={skill.name} detail={skill.description} />
          ))
        )}
      </Section>

      <Section title="Tool servers" count={status.mcp.length}>
        {status.mcp.length === 0 ? (
          <Empty>No MCP servers configured.</Empty>
        ) : (
          status.mcp.map((server) =>
            server.connected ? (
              <Entry
                key={server.name}
                name={server.name}
                trailing={`${server.tools} tools`}
              />
            ) : (
              // A server that is down is a fact about the config, so it reads in
              // the same margin as a load warning.
              <Marginal key={server.name} flag="failing" bad>
                <code>{server.name}</code> is unreachable
                {server.error ? `: ${server.error}` : "."} Its tools are absent from
                this session.
              </Marginal>
            ),
          )
        )}
      </Section>

      <Section title="Scheduled" count={status.cron.length}>
        {status.cron.length === 0 ? (
          <Empty>Nothing scheduled. Add a file to cron/ to run work on a timer.</Empty>
        ) : (
          status.cron.map((job) => (
            <Entry
              key={job.name}
              name={job.name}
              detail={`${job.agent} · ${job.schedule}`}
              trailing={job.last ? `last ${relativeTime(job.last)}` : "never run"}
            />
          ))
        )}
      </Section>

      {status.params.length > 0 && (
        <Section title="Parameters" count={status.params.length}>
          <div className="flex flex-col">
            {status.params.map((param) => (
              <Pair key={param.name} k={param.name}>
                {param.value ? (
                  param.value
                ) : param.default ? (
                  <>
                    {param.default}
                    <span className="text-ink-3 ml-1.5 text-[9px] uppercase">
                      default
                    </span>
                  </>
                ) : (
                  "—"
                )}
              </Pair>
            ))}
          </div>
        </Section>
      )}

      {status.systemPrompt && (
        <Section
          title="Effective prompt"
          aside={`${status.systemPrompt.length.toLocaleString()} characters`}
        >
          <details className="group/prompt mt-3">
            <summary className="text-ink-3 hover:text-ink border-rule w-fit border-b border-dotted text-[11.5px] italic">
              <span className="group-open/prompt:hidden">Show the effective prompt</span>
              <span className="hidden group-open/prompt:inline">Hide it</span>
            </summary>
            <Pre className="mt-3 max-h-[32rem] overflow-y-auto">
              {status.systemPrompt}
            </Pre>
          </details>
        </Section>
      )}
    </Page>
  );
};
