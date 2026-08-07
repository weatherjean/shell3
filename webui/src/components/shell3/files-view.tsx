import { ArrowLeftIcon, ChevronRightIcon, LockIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useCallback, useEffect, useMemo, useRef, useState, type FC } from "react";
import {
  isAudioName,
  isImageName,
  listFiles,
  listMedia,
  readFile,
  type FileContent,
  type FileEntry,
} from "@/lib/api";
import { useCapabilities } from "@/lib/capabilities";
import { highlight, languageFor } from "@/lib/highlight";
import { humanSize, relativeTime } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Cap, Marker } from "@/components/shell3/page";

const SAMPLE_ENTRIES: FileEntry[] = [
  { name: "agent.md", path: "agent.md", dir: false, size: 2841, modified: "", redacted: false },
  { name: "shell3.yaml", path: "shell3.yaml", dir: false, size: 3120, modified: "", redacted: false },
  { name: ".env", path: ".env", dir: false, size: 412, modified: "", redacted: true },
  { name: "agents", path: "agents", dir: true, size: 0, modified: "", redacted: false },
  { name: "skills", path: "skills", dir: true, size: 0, modified: "", redacted: false },
  { name: "cron", path: "cron", dir: true, size: 0, modified: "", redacted: false },
  { name: "projects", path: "projects", dir: true, size: 0, modified: "", redacted: false },
  { name: "hooks", path: "hooks", dir: true, size: 0, modified: "", redacted: false },
];

const Breadcrumbs: FC<{ path: string; onNavigate: (path: string) => void }> = ({
  path,
  onNavigate,
}) => {
  const parts = path ? path.split("/") : [];
  return (
    <nav
      aria-label="Path"
      className="flex flex-wrap items-center gap-1 font-mono text-[10.5px]"
    >
      <button onClick={() => onNavigate("")} className="text-ink-3 hover:text-ink">
        ~/.shell3
      </button>
      {parts.map((part, index) => (
        <span key={index} className="flex items-center gap-1">
          <ChevronRightIcon className="text-ink-3 size-3" />
          <button
            onClick={() => onNavigate(parts.slice(0, index + 1).join("/"))}
            className={cn(
              "hover:text-ink",
              index === parts.length - 1 ? "text-ink" : "text-ink-3",
            )}
          >
            {part}
          </button>
        </span>
      ))}
    </nav>
  );
};

type Root = "config" | "media";

export const FilesView: FC = () => {
  const { live } = useCapabilities();
  // The media dir sits outside the config root, so it is a second root rather
  // than a folder — uploads and generated images live there permanently.
  const [root, setRoot] = useState<Root>("config");
  const [path, setPath] = useState("");
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [file, setFile] = useState<FileContent | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let stale = false;
    setError(null);
    if (!live) {
      setEntries(root === "config" && path === "" ? SAMPLE_ENTRIES : []);
      setError("No backend connected — showing a sample listing.");
      return;
    }
    const load =
      root === "media" ? listMedia() : listFiles(path).then((res) => res.entries);
    void load
      .then((next) => !stale && setEntries(next))
      .catch((err) => {
        if (stale) return;
        setEntries([]);
        setError(`Could not read this folder: ${err}`);
      });
    return () => {
      stale = true;
    };
  }, [path, root, live]);

  const rootRef = useRef(root);
  rootRef.current = root;

  const open = useCallback((entry: FileEntry) => {
    if (entry.dir) {
      setPath(entry.path);
      setSelected(null);
      setFile(null);
      return;
    }
    setSelected(entry.path);
    setFile(null);
    if (rootRef.current === "media") return;
    void readFile(entry.path)
      .then(setFile)
      .catch((err) =>
        setFile({
          path: entry.path,
          content: `Could not read this file: ${err}`,
          size: entry.size,
          redacted: false,
          binary: false,
          truncated: false,
        }),
      );
  }, []);

  return (
    <div className="flex h-full min-h-0">
      <div
        className={cn(
          "border-rule flex min-h-0 flex-col border-r md:w-72 md:shrink-0",
          // One pane at a time on a phone: the listing, or the file.
          selected ? "hidden md:flex" : "w-full",
        )}
      >
        {/* Two roots, named as the sections of a contents page rather than as
            tabs. The open one is marked, not filled. */}
        <div className="border-rule flex items-baseline gap-4 border-b px-3 py-2.5">
          {(["config", "media"] as const).map((option) => (
            <button
              key={option}
              type="button"
              onClick={() => {
                setRoot(option);
                setPath("");
                setSelected(null);
                setFile(null);
              }}
            >
              {root === option ? (
                <Marker className="cap !text-swipe">{option}</Marker>
              ) : (
                <Cap className="hover:text-ink">{option}</Cap>
              )}
            </button>
          ))}
        </div>
        {root === "config" && (
          <div className="border-rule-2 border-b px-3 py-2">
            <Breadcrumbs path={path} onNavigate={setPath} />
          </div>
        )}
        {/* A table of contents: the name owes a figure, and the leader carries
            the eye across. A directory is marked by its trailing slash and full
            ink rather than by an icon. */}
        <ul className="min-h-0 flex-1 overflow-y-auto">
          {entries.map((entry) => (
            <li key={entry.path}>
              <button
                onClick={() => open(entry)}
                className={cn(
                  "border-rule-2 hover:bg-lift flex w-full items-baseline gap-1 border-b px-3 py-2 text-left transition-colors",
                  selected === entry.path && "bg-lift",
                )}
              >
                <span
                  className={cn(
                    "min-w-0 truncate font-mono text-[11px]",
                    entry.dir ? "text-ink" : "text-ink-2",
                  )}
                >
                  {entry.name}
                  {entry.dir && "/"}
                </span>
                <span className="leader" aria-hidden />
                {entry.redacted ? (
                  <Marker className="cap !text-swipe shrink-0">redacted</Marker>
                ) : (
                  <span
                    className="text-ink-3 shrink-0 font-mono text-[9.5px] tabular-nums"
                    title={
                      entry.modified ? `modified ${relativeTime(entry.modified)}` : undefined
                    }
                  >
                    {entry.dir ? "" : humanSize(entry.size)}
                  </span>
                )}
              </button>
            </li>
          ))}
          {entries.length === 0 && (
            <li className="text-ink-3 px-3 py-3 text-[12.5px]">
              {root === "media"
                ? "Nothing uploaded or generated yet."
                : "This folder is empty."}
            </li>
          )}
        </ul>
        {error && (
          <p className="text-ink-3 border-rule border-t px-3 py-2 text-[11px]">{error}</p>
        )}
      </div>

      <div
        className={cn(
          "min-w-0 flex-1 overflow-auto",
          selected ? "flex flex-col" : "hidden md:block",
        )}
      >
        {selected === null ? (
          <p className="text-ink-3 p-7 text-[12.5px]">
            Pick a file to read it. Everything here is read-only.
          </p>
        ) : (
          <div className="flex min-h-full flex-col">
            <div className="bg-background border-rule sticky top-0 z-10 flex items-center gap-2 border-b px-3 py-2.5 md:px-4">
              <Button
                variant="ghost"
                size="icon"
                aria-label="Back to the file list"
                onClick={() => {
                  setSelected(null);
                  setFile(null);
                }}
                className="size-7 shrink-0 md:hidden"
              >
                <ArrowLeftIcon className="size-4" />
              </Button>
              <span className="text-ink min-w-0 flex-1 truncate font-mono text-[12px]">
                {selected}
              </span>
              {file && (
                <span className="text-ink-3 shrink-0 font-mono text-[9.5px]">
                  {[
                    humanSize(file.size),
                    file.redacted && "redacted",
                    file.binary && "binary",
                    file.truncated && "first 256 KB",
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
              )}
            </div>
            {root === "media" ? (
              <MediaBody name={selected} />
            ) : (
              <FileBody path={selected} file={file} />
            )}
          </div>
        )}
      </div>
    </div>
  );
};

/**
 * Renders a file's text, syntax-highlighted when the language is known.
 * Credential and binary files carry no content to show — they get an
 * explanation rather than an empty pane.
 */
const FileBody: FC<{ path: string; file: FileContent | null }> = ({ path, file }) => {
  const html = useMemo(
    () =>
      !file || file.redacted || file.binary
        ? null
        : highlight(file.content, languageFor(path)),
    [file, path],
  );

  if (!file) {
    return <p className="text-ink-3 p-4 text-[12.5px]">Loading…</p>;
  }
  if (file.redacted) {
    return (
      <div className="flex items-start gap-2.5 p-4">
        <LockIcon className="text-gold mt-0.5 size-4 shrink-0" />
        <p className="doc text-ink-2 text-[14px]">{file.content}</p>
      </div>
    );
  }
  if (file.binary) {
    return (
      <p className="text-ink-3 p-4 text-[12.5px]">
        This is a binary file ({humanSize(file.size)}) — nothing useful to show.
      </p>
    );
  }
  return (
    <pre className="text-ink-2 flex-1 overflow-x-auto p-4 font-mono text-[11.5px] leading-[1.75]">
      {html === null ? (
        <code className="whitespace-pre-wrap">{file.content}</code>
      ) : (
        <code dangerouslySetInnerHTML={{ __html: html }} />
      )}
    </pre>
  );
};

/** A media file: images and audio play here, anything else offers a link. */
const MediaBody: FC<{ name: string }> = ({ name }) => {
  const url = `/api/media/${encodeURIComponent(name)}`;

  if (isImageName(name)) {
    return (
      <div className="p-4">
        <img
          src={url}
          alt={name}
          className="border-rule max-h-[70vh] max-w-full rounded-[3px] border object-contain"
        />
      </div>
    );
  }
  if (isAudioName(name)) {
    return (
      <div className="p-4">
        {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
        <audio src={url} controls className="w-full" />
      </div>
    );
  }
  return (
    <p className="text-ink-2 p-4 text-[12.5px]">
      <a href={url} className="underline" target="_blank" rel="noreferrer">
        Open {name}
      </a>
    </p>
  );
};
