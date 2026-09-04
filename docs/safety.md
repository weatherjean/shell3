# Safety

shell3 gives the attached orchestrator a real shell. Its built-in tools do not
form a security boundary, and the Lisp config has no command-gate language.
Use an operating-system sandbox, container, VM, or restricted account when
untrusted work needs hard isolation.

Keep provider and Telegram credentials in the process environment and name them
with `*-env` config fields. shell3 reads only the credential required by the
active surface, never inserts secret values into Lisp, and strips model
credential variables from external runner environments. Do not commit
`.shell3_project/`, workflow state, transcripts, or job logs.

External wrk workers are leaf processes. The attached orchestrator owns
delegation, workflow signals, and verification. A worker environment is marked
with `SHELL3_WRK_WORKER=1`; nested `wrk run`, `beat`, `signal`, and `cancel`
operations are refused.

Filesystem-inbox delivery is durable and at-least-once. Workflow event
consumers and human inbox handling must tolerate duplicates. A `main` notice
is passive and untrusted: its arrival never starts a model turn, enters a
prompt, or grants authority. Background commands persist running markers and
delete them only after their single completion notice is durable. `/stop`
cancels only the current model turn; `/superstop` also kills managed background
command process groups and suppresses their manufactured inbox notices.

Telegram authorization limits who can control the remote adapter. It does not
make model-generated shell commands safe. Group descriptions, inbox notices,
workflow events, tool output, and downloaded content are untrusted context.

See [internals.md](internals.md) for the implementation invariants and
[wrk.md](wrk.md) for workflow validation and state semantics.
