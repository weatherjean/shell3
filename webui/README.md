# shell3 web interface

The browser front-end for shell3, built with
[assistant-ui](https://www.assistant-ui.com) (React 19 + Vite + Tailwind).

The Go binary serves this app and every API it talks to. `make webui` (from the
repo root) builds it and stages the output into `internal/webui/dist`, which is
committed and embedded — `go install` cannot run npm, so a binary built from a
clean checkout must already carry the interface.

## The look

The interface is set as a **printed document** — shell3 already keeps every
session as JSONL, so the front-end is that audit trail rather than a dashboard
over it. Read `src/index.css` first: the stocks and the devices both live
there.

**Two stocks.** Paper on `:root`, a cyanotype on `.dark`. The dark one is not
the light one inverted — the ruling sits *lighter* than its ground on a
cyanotype and *darker* on paper, which is what each process physically does,
and the serif carries more weight on the dyed ground (`--doc-weight`). Both
rulings are matched by contrast **ratio** against their own ground (~1.06:1),
not by picking a plausible grey for each; stronger and the grid stops being
stock texture and becomes graph paper you are meant to look at.

**Four devices** carry every page, and nothing is invented per view:

| device | where it lives | rule |
|---|---|---|
| ruled section head | `<Section>` in `shell3/page.tsx` | 1px of the dim tier — 2px of near-white flattens the dyed stock |
| dotted leader | `.leader`, `<Leader>`, `<Pair>` | only where a name actually owes a figure |
| hanging figure column | `<Figure>` | mono, tabular, right-aligned, so a column lines up |
| the marker | `.swiped`, `<Marker>` | **only ever marks what is live** |

The marker is the signature, and `--mark` is deliberately the one token defined
outside both stock blocks: it is one device and must look like one thing in
both prints. An earlier pass split its geometry per stock and that cost more
than it bought — a signature that changes shape between prints is two
signatures.

**Page types.** Chat is a transcript (an 88px gutter with a hairline spine, the
reply set in the serif `.doc` column, commands quoted behind a rule); Jobs is a
ruled ledger; Cron a timetable; Runs an archive index; Status a specification
sheet whose warnings are marginalia, not tinted alert panels; Files a table of
contents.

**The masthead is the page heading.** A view publishes its title through
`lib/heading.tsx` rather than printing its own `<h1>` under a header that
already names it. The note is a plain string on purpose — as a ReactNode it
could not go in the effect's dependency list without looping, and left out it
would freeze, which matters because these views poll.

**Type is self-hosted** (`@fontsource`), because `go install` embeds this build
into a binary that has to work with no network. The mark keeps its own pinned
face (`.mark-face`, Tahoma): the snail is two borrowed glyphs whose shapes
differ enough between typefaces that inheriting the interface font redraws the
logo.

## Working on it

```bash
npm install
npm run dev        # http://localhost:5173, mocked backend
npm run build      # type-check + production build into dist/
npm run lint       # oxlint + format check
```

`npm run dev` runs against no backend at all: the capability probe fails and
the app degrades on purpose — chat streams a canned reply
(`src/mock/chat.ts`), the Status and Files views show sample data, the bell
gets sample notifications, and voice falls back to the browser's own Web
Speech APIs. That makes the whole UI explorable without an API key. Sample
data stands in **only** when there is no backend at all; against a live server
a failed request shows the error, so a broken endpoint can never masquerade as
plausible-looking data.

To develop against the real agent, run `shell3 serve` and point Vite's proxy at
it, or just `make webui && make build` and use the binary.

## Layout

```
src/App.tsx                    the shell: sidebar, header, view switching
src/components/shell3/         the six views — chat (the assistant-ui base
                               demo, ported and stripped of its leftovers),
                               jobs, cron, runs, status, files — plus the
                               notification bell, theme toggle, approval
                               dialog and the shared page frame
src/components/assistant-ui/   assistant-ui components (shadcn registry)
src/components/ui/             base UI primitives (shadcn registry)
src/lib/api.ts                 the HTTP contract, as seen from the browser
src/lib/events.tsx             /api/events: notifications + approval requests
src/lib/capabilities.tsx       what this install can do (voice, models, …)
src/lib/voice.ts               dictation + speech adapters
src/lib/theme.tsx              light/dark, system-aware and persisted
src/mock/chat.ts               the no-backend fake stream
```

`src/lib/api.ts` is the single place the server contract is written down; the
Go side of the same contract is `internal/webui/`.
