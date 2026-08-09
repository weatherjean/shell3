//#region src/instrumentMcpSampling.d.ts
/**
 * MCP sampling instrumentation utility.
 *
 * Wraps an MCP client's sampling handler to capture nested LLM calls
 * (sampling/createMessage requests) made during tool execution.
 * The captured data can be reported as child generation spans.
 */
type SamplingCallData = {
  model_id?: string;
  input_tokens?: number;
  output_tokens?: number;
  reasoning_tokens?: number;
  cached_input_tokens?: number;
  duration_ms?: number;
};
type McpSamplingHandler = (request: McpSamplingRequest) => Promise<McpSamplingResponse>;
type McpSamplingRequest = {
  method: "sampling/createMessage";
  params: {
    messages: unknown[];
    modelPreferences?: {
      hints?: {
        name?: string;
      }[];
    };
    maxTokens?: number;
    [key: string]: unknown;
  };
};
type McpSamplingResponse = {
  model?: string;
  content: unknown;
  usage?: {
    inputTokens?: number;
    outputTokens?: number;
    promptTokens?: number;
    completionTokens?: number;
    reasoningTokens?: number;
    cachedInputTokens?: number;
  };
  [key: string]: unknown;
};
/**
 * Wraps an MCP sampling handler to intercept and measure sampling calls.
 *
 * @param handler - The original sampling handler from the MCP client
 * @param onSamplingCall - Callback invoked with metrics for each sampling call
 * @returns A wrapped handler that transparently captures sampling metrics
 *
 * @example
 * ```ts
 * const samplingCalls: SamplingCallData[] = [];
 * const wrapped = wrapSamplingHandler(
 *   originalHandler,
 *   (data) => samplingCalls.push(data),
 * );
 * // Use `wrapped` as the MCP client's sampling handler
 * // After tool execution, `samplingCalls` contains metrics for all nested LLM calls
 * ```
 */
declare function wrapSamplingHandler(handler: McpSamplingHandler, onSamplingCall: (data: SamplingCallData) => void): McpSamplingHandler;
/**
 * Creates a collector that accumulates sampling call data during tool execution.
 * Use with `wrapSamplingHandler` to capture all sampling calls for a tool invocation.
 *
 * @example
 * ```ts
 * const collector = createSamplingCollector();
 * const wrappedHandler = wrapSamplingHandler(handler, collector.collect);
 * // ... execute MCP tool ...
 * const calls = collector.getCalls(); // SamplingCallData[]
 * ```
 */
declare function createSamplingCollector(): {
  collect: (data: SamplingCallData) => number;
  getCalls: () => SamplingCallData[];
  reset: () => void;
};
//#endregion
export { McpSamplingHandler, McpSamplingRequest, McpSamplingResponse, SamplingCallData, createSamplingCollector, wrapSamplingHandler };
//# sourceMappingURL=instrumentMcpSampling.d.ts.map