/// <reference types="@assistant-ui/core/react" />
//#region src/usage.d.ts
type ThreadTokenUsage = {
  totalTokens?: number;
  inputTokens?: number;
  outputTokens?: number;
  reasoningTokens?: number;
  cachedInputTokens?: number;
};
interface TokenUsageExtractableMessage {
  role?: string;
  metadata?: unknown;
}
declare function getThreadMessageTokenUsage(message: TokenUsageExtractableMessage | undefined): ThreadTokenUsage | undefined;
declare function getLatestThreadTokenUsage(messages: readonly TokenUsageExtractableMessage[] | undefined): ThreadTokenUsage | undefined;
declare function useThreadTokenUsage(): ThreadTokenUsage | undefined;
//#endregion
export { ThreadTokenUsage, TokenUsageExtractableMessage, getLatestThreadTokenUsage, getThreadMessageTokenUsage, useThreadTokenUsage };
//# sourceMappingURL=usage.d.ts.map