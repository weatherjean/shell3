import { ToolResponse } from "./ToolResponse.js";
import { ToolExecutionStream } from "./ToolExecutionStream.js";
//#region src/core/tool/toolResultStream.ts
const isStandardSchemaV1 = (schema) => {
	return typeof schema === "object" && schema !== null && "~standard" in schema && schema["~standard"].version === 1;
};
function getToolResponse(tools, abortSignal, toolCall, human) {
	const tool = tools?.[toolCall.toolName];
	if (!tool?.execute) return void 0;
	const getResult = async (toolExecute) => {
		if (abortSignal.aborted) return new ToolResponse({
			result: "Tool execution was cancelled.",
			isError: true
		});
		let executeFn = toolExecute;
		if (isStandardSchemaV1(tool.parameters)) {
			let result = tool.parameters["~standard"].validate(toolCall.args);
			if (result instanceof Promise) result = await result;
			if (result.issues) executeFn = tool.experimental_onSchemaValidationError ?? (() => {
				throw new Error(`Function parameter validation failed. ${JSON.stringify(result.issues)}`);
			});
		}
		const abortPromise = new Promise((resolve) => {
			const onAbort = () => {
				queueMicrotask(() => {
					queueMicrotask(() => {
						resolve(new ToolResponse({
							result: "Tool execution was cancelled.",
							isError: true
						}));
					});
				});
			};
			if (abortSignal.aborted) onAbort();
			else abortSignal.addEventListener("abort", onAbort, { once: true });
		});
		const executePromise = (async () => {
			const result = await executeFn(toolCall.args, {
				toolCallId: toolCall.toolCallId,
				abortSignal,
				human: (payload) => human(toolCall.toolCallId, payload)
			});
			const response = ToolResponse.toResponse(result);
			if (tool.toModelOutput && !response.isError && response.modelContent === void 0) try {
				const modelContent = await tool.toModelOutput({
					toolCallId: toolCall.toolCallId,
					input: toolCall.args,
					output: response.result
				});
				return new ToolResponse({
					result: response.result,
					artifact: response.artifact,
					isError: response.isError,
					messages: response.messages,
					modelContent
				});
			} catch (e) {
				console.warn(`[assistant-stream] tool "${toolCall.toolName}" toModelOutput threw; falling back to default projection.`, e);
			}
			return response;
		})();
		return Promise.race([executePromise, abortPromise]);
	};
	return getResult(tool.execute);
}
function getToolStreamResponse(tools, abortSignal, reader, context, human) {
	tools?.[context.toolName]?.streamCall?.(reader, {
		toolCallId: context.toolCallId,
		abortSignal,
		human: (payload) => human(context.toolCallId, payload)
	});
}
async function unstable_runPendingTools(message, tools, abortSignal, human) {
	const toolCallPromises = message.parts.filter((part) => part.type === "tool-call").map(async (part) => {
		const promiseOrUndefined = getToolResponse(tools, abortSignal, part, human ?? (async () => {
			throw new Error("Tool human input is not supported in this context");
		}));
		if (promiseOrUndefined) {
			const result = await promiseOrUndefined;
			return {
				toolCallId: part.toolCallId,
				result
			};
		}
		return null;
	});
	const toolCallResults = (await Promise.all(toolCallPromises)).filter((result) => result !== null);
	if (toolCallResults.length === 0) return message;
	const toolCallResultsById = toolCallResults.reduce((acc, { toolCallId, result }) => {
		acc[toolCallId] = result;
		return acc;
	}, {});
	const updatedParts = message.parts.map((p) => {
		if (p.type === "tool-call") {
			const toolResponse = toolCallResultsById[p.toolCallId];
			if (toolResponse) return {
				...p,
				state: "result",
				...toolResponse.artifact !== void 0 ? { artifact: toolResponse.artifact } : {},
				...toolResponse.modelContent !== void 0 ? { modelContent: toolResponse.modelContent } : {},
				result: toolResponse.result,
				isError: toolResponse.isError
			};
		}
		return p;
	});
	return {
		...message,
		parts: updatedParts,
		content: updatedParts
	};
}
/**
* Transform stream that executes frontend tools and appends tool results.
*
* The transform watches streamed tool-call arguments, runs the matching
* frontend tool once its arguments are complete, and emits a result chunk for
* the tool call. Backend and human tools pass through according to their tool
* definition.
*
* @param tools Tool registry or function returning the current registry.
* @param abortSignal Signal, or signal getter, used for the current run.
* @param human Callback used to resolve human-tool requests from UI input.
* @param options Optional execution lifecycle callbacks.
*/
function toolResultStream(tools, abortSignal, human, options) {
	const toolsFn = typeof tools === "function" ? tools : () => tools;
	const abortSignalFn = typeof abortSignal === "function" ? abortSignal : () => abortSignal;
	return new ToolExecutionStream({
		execute: (toolCall) => getToolResponse(toolsFn(), abortSignalFn(), toolCall, human),
		streamCall: ({ reader, ...context }) => getToolStreamResponse(toolsFn(), abortSignalFn(), reader, context, human),
		onExecutionStart: options?.onExecutionStart,
		onExecutionEnd: options?.onExecutionEnd
	});
}
//#endregion
export { toolResultStream, unstable_runPendingTools };

//# sourceMappingURL=toolResultStream.js.map