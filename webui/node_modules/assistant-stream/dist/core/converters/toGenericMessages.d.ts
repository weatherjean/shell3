//#region src/core/converters/toGenericMessages.d.ts
/**
 * Generic message types for framework-agnostic LLM message interchange.
 * These types represent a common format that can be converted to/from
 * various LLM provider formats (AI SDK, LangChain, etc.).
 */
type GenericTextPart = {
  type: "text";
  text: string;
};
type GenericFilePart = {
  type: "file";
  data: string | URL;
  mediaType: string;
};
type GenericToolCallPart = {
  type: "tool-call";
  toolCallId: string;
  toolName: string;
  args: Record<string, unknown>;
};
type GenericToolResultPart = {
  type: "tool-result";
  toolCallId: string;
  toolName: string;
  result: unknown;
  isError?: boolean;
};
type GenericSystemMessage = {
  role: "system";
  content: string;
};
type GenericUserMessage = {
  role: "user";
  content: (GenericTextPart | GenericFilePart)[];
};
type GenericAssistantMessage = {
  role: "assistant";
  content: (GenericTextPart | GenericToolCallPart)[];
};
type GenericToolMessage = {
  role: "tool";
  content: GenericToolResultPart[];
};
type GenericMessage = GenericSystemMessage | GenericUserMessage | GenericAssistantMessage | GenericToolMessage;
type MessagePartLike = {
  type: string;
  text?: string;
  image?: string;
  data?: string;
  mimeType?: string;
  toolCallId?: string;
  toolName?: string;
  args?: Record<string, unknown>;
  result?: unknown;
  isError?: boolean;
};
type AttachmentLike = {
  content: readonly MessagePartLike[];
};
type ThreadMessageLike = {
  role: "system" | "user" | "assistant";
  content: readonly MessagePartLike[];
  attachments?: readonly AttachmentLike[];
};
/**
 * Converts thread messages to generic LLM messages.
 * This format can then be easily converted to provider-specific formats.
 */
declare function toGenericMessages(messages: readonly ThreadMessageLike[]): GenericMessage[];
//#endregion
export { GenericAssistantMessage, GenericFilePart, GenericMessage, GenericSystemMessage, GenericTextPart, GenericToolCallPart, GenericToolMessage, GenericToolResultPart, GenericUserMessage, toGenericMessages };
//# sourceMappingURL=toGenericMessages.d.ts.map