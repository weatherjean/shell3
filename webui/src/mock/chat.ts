// In-browser mock of the shell3 chat backend.
//
// The real backend is the Go server speaking the AI SDK UI message stream
// protocol (SSE) at /api/chat. Until that exists, this fake `fetch` feeds the
// transport a canned streaming reply so the UI is fully interactive with no
// server and no API key.

const REPLY = [
  "Hey! I'm a **mocked shell3 agent** — no model behind me yet, just a fake stream so you can judge the UI.\n\n",
  "Here's what streaming markdown looks like:\n\n",
  "- tool output, `code spans`, and lists render like this\n",
  "- long replies stream word by word\n",
  "- code blocks too:\n\n",
  '```bash\nrg -n "CompletionEvent" internal/shell3 | head\n```\n\n',
  "Ask anything and I'll echo this same canned reply. ",
  "When the Go backend speaks the UI message stream protocol, this module disappears.",
];

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

const sse = (obj: Record<string, unknown>) => `data: ${JSON.stringify(obj)}\n\n`;

export const mockChatFetch: typeof fetch = async (_input, init) => {
  const encoder = new TextEncoder();
  const signal = init?.signal;

  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      const write = (obj: Record<string, unknown>) =>
        controller.enqueue(encoder.encode(sse(obj)));

      write({ type: "start", messageId: `mock-${Date.now()}` });
      write({ type: "text-start", id: "t0" });
      for (const chunk of REPLY) {
        for (const word of chunk.split(/(?<=\s)/)) {
          if (signal?.aborted) break;
          write({ type: "text-delta", id: "t0", delta: word });
          await sleep(20);
        }
      }
      write({ type: "text-end", id: "t0" });
      write({ type: "finish" });
      controller.enqueue(encoder.encode("data: [DONE]\n\n"));
      controller.close();
    },
  });

  return new Response(stream, {
    status: 200,
    headers: {
      "content-type": "text/event-stream",
      "x-vercel-ai-ui-message-stream": "v1",
    },
  });
};
