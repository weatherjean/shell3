import { PipeableTransformStream } from "../utils/stream/PipeableTransformStream.js";
import { AssistantMetaTransformStream } from "../utils/stream/AssistantMetaTransformStream.js";
import { ToolResponse } from "./ToolResponse.js";
import { withPromiseOrValue } from "../utils/withPromiseOrValue.js";
import { ToolCallReaderImpl } from "./ToolCallReader.js";
import sjson from "secure-json-parse";
//#region src/core/tool/ToolExecutionStream.ts
var ToolExecutionStream = class extends PipeableTransformStream {
	constructor(options) {
		const toolCallPromises = /* @__PURE__ */ new Map();
		const toolCallControllers = /* @__PURE__ */ new Map();
		super((readable) => {
			const transform = new TransformStream({
				async transform(chunk, controller) {
					if (chunk.type !== "part-finish" || chunk.meta.type !== "tool-call") controller.enqueue(chunk);
					switch (chunk.type) {
						case "part-start":
							if (chunk.part.type === "tool-call") {
								const reader = new ToolCallReaderImpl();
								toolCallControllers.set(chunk.part.toolCallId, reader);
								options.streamCall({
									reader,
									toolCallId: chunk.part.toolCallId,
									toolName: chunk.part.toolName
								});
							}
							break;
						case "text-delta":
							if (chunk.meta.type === "tool-call") {
								const toolCallId = chunk.meta.toolCallId;
								const controller = toolCallControllers.get(toolCallId);
								if (!controller) throw new Error("No controller found for tool call");
								await controller.appendArgsTextDelta(chunk.textDelta);
							}
							break;
						case "result": {
							if (chunk.meta.type !== "tool-call") break;
							const { toolCallId } = chunk.meta;
							const controller = toolCallControllers.get(toolCallId);
							if (!controller) throw new Error("No controller found for tool call");
							controller.setResponse(new ToolResponse({
								result: chunk.result,
								artifact: chunk.artifact,
								isError: chunk.isError,
								modelContent: chunk.modelContent
							}));
							break;
						}
						case "tool-call-args-text-finish": {
							if (chunk.meta.type !== "tool-call") break;
							const { toolCallId, toolName } = chunk.meta;
							const streamController = toolCallControllers.get(toolCallId);
							if (!streamController) throw new Error("No controller found for tool call");
							await streamController.finishArgsText();
							let isExecuting = false;
							const promise = withPromiseOrValue(() => {
								let args;
								try {
									args = sjson.parse(streamController.argsText);
								} catch (e) {
									throw new Error(`Function parameter parsing failed. ${JSON.stringify(e.message)}`);
								}
								const executeResult = options.execute({
									toolCallId,
									toolName,
									args
								});
								if (executeResult !== void 0) {
									isExecuting = true;
									options.onExecutionStart?.(toolCallId, toolName);
								}
								return executeResult;
							}, (c) => {
								if (isExecuting) options.onExecutionEnd?.(toolCallId, toolName);
								if (c === void 0) return;
								const result = new ToolResponse({
									artifact: c.artifact,
									result: c.result,
									isError: c.isError,
									messages: c.messages,
									modelContent: c.modelContent
								});
								streamController.setResponse(result);
								controller.enqueue({
									type: "result",
									path: chunk.path,
									...result
								});
							}, (e) => {
								if (isExecuting) options.onExecutionEnd?.(toolCallId, toolName);
								const result = new ToolResponse({
									result: String(e),
									isError: true
								});
								streamController.setResponse(result);
								controller.enqueue({
									type: "result",
									path: chunk.path,
									...result
								});
							});
							if (promise) toolCallPromises.set(toolCallId, promise);
							break;
						}
						case "part-finish": {
							if (chunk.meta.type !== "tool-call") break;
							const { toolCallId } = chunk.meta;
							const toolCallPromise = toolCallPromises.get(toolCallId);
							if (toolCallPromise) toolCallPromise.then(() => {
								toolCallPromises.delete(toolCallId);
								toolCallControllers.delete(toolCallId);
								controller.enqueue(chunk);
							});
							else controller.enqueue(chunk);
						}
					}
				},
				async flush() {
					await Promise.all(toolCallPromises.values());
				}
			});
			return readable.pipeThrough(new AssistantMetaTransformStream()).pipeThrough(transform);
		});
	}
};
//#endregion
export { ToolExecutionStream };

//# sourceMappingURL=ToolExecutionStream.js.map