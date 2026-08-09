import { AssistantTransformStream } from "../../utils/stream/AssistantTransformStream.js";
import { PipeableTransformStream } from "../../utils/stream/PipeableTransformStream.js";
import "./chunk-types.js";
import { LineDecoderStream } from "../../utils/stream/LineDecoderStream.js";
import { DataStreamChunkDecoder, DataStreamChunkEncoder } from "./serialization.js";
import { AssistantMetaTransformStream } from "../../utils/stream/AssistantMetaTransformStream.js";
//#region src/core/serialization/data-stream/DataStream.ts
var DataStreamEncoder = class extends PipeableTransformStream {
	headers = new Headers({
		"Content-Type": "text/plain; charset=utf-8",
		"x-vercel-ai-data-stream": "v1"
	});
	constructor() {
		super((readable) => {
			const transform = new TransformStream({ transform(chunk, controller) {
				const type = chunk.type;
				switch (type) {
					case "part-start": {
						const part = chunk.part;
						if (part.type === "tool-call") {
							const { type, ...value } = part;
							controller.enqueue({
								type: "b",
								value
							});
						}
						if (part.type === "source") {
							const { type, ...value } = part;
							controller.enqueue({
								type: "h",
								value
							});
						}
						if (part.type === "data") {
							const { type, ...value } = part;
							controller.enqueue({
								type: "aui-data",
								value
							});
						}
						break;
					}
					case "text-delta": {
						const part = chunk.meta;
						switch (part.type) {
							case "text":
								if (part.parentId) controller.enqueue({
									type: "aui-text-delta",
									value: {
										textDelta: chunk.textDelta,
										parentId: part.parentId
									}
								});
								else controller.enqueue({
									type: "0",
									value: chunk.textDelta
								});
								break;
							case "reasoning":
								if (part.parentId) controller.enqueue({
									type: "aui-reasoning-delta",
									value: {
										reasoningDelta: chunk.textDelta,
										parentId: part.parentId
									}
								});
								else controller.enqueue({
									type: "g",
									value: chunk.textDelta
								});
								break;
							case "tool-call":
								controller.enqueue({
									type: "c",
									value: {
										toolCallId: part.toolCallId,
										argsTextDelta: chunk.textDelta
									}
								});
								break;
							default: throw new Error(`Unsupported part type for text-delta: ${part.type}`);
						}
						break;
					}
					case "result": {
						const part = chunk.meta;
						if (part.type !== "tool-call") throw new Error(`Result chunk on non-tool-call part not supported: ${part.type}`);
						controller.enqueue({
							type: "a",
							value: {
								toolCallId: part.toolCallId,
								result: chunk.result,
								artifact: chunk.artifact,
								...chunk.isError ? { isError: chunk.isError } : {}
							}
						});
						break;
					}
					case "step-start": {
						const { type, ...value } = chunk;
						controller.enqueue({
							type: "f",
							value
						});
						break;
					}
					case "step-finish": {
						const { type, ...value } = chunk;
						controller.enqueue({
							type: "e",
							value
						});
						break;
					}
					case "message-finish": {
						const { type, ...value } = chunk;
						controller.enqueue({
							type: "d",
							value
						});
						break;
					}
					case "error":
						controller.enqueue({
							type: "3",
							value: chunk.error
						});
						break;
					case "annotations":
						controller.enqueue({
							type: "8",
							value: chunk.annotations
						});
						break;
					case "data":
						controller.enqueue({
							type: "2",
							value: chunk.data
						});
						break;
					case "update-state":
						controller.enqueue({
							type: "aui-state",
							value: chunk.operations
						});
						break;
					case "tool-call-args-text-finish":
					case "part-finish": break;
					default: throw new Error(`Unsupported chunk type: ${type}`);
				}
			} });
			return readable.pipeThrough(new AssistantMetaTransformStream()).pipeThrough(transform).pipeThrough(new DataStreamChunkEncoder()).pipeThrough(new TextEncoderStream());
		});
	}
};
const TOOL_CALL_ARGS_CLOSING_CHUNKS = [
	"b",
	"9",
	"0",
	"g",
	"h",
	"3",
	"e",
	"d",
	"aui-text-delta",
	"aui-reasoning-delta",
	"aui-data"
];
var DataStreamDecoder = class extends PipeableTransformStream {
	constructor() {
		super((readable) => {
			const toolCallControllers = /* @__PURE__ */ new Map();
			let activeToolCallArgsText;
			const transform = new AssistantTransformStream({
				transform(chunk, controller) {
					const { type, value } = chunk;
					if (TOOL_CALL_ARGS_CLOSING_CHUNKS.includes(type)) {
						activeToolCallArgsText?.close();
						activeToolCallArgsText = void 0;
					}
					switch (type) {
						case "g":
							controller.appendReasoning(value);
							break;
						case "0":
							controller.appendText(value);
							break;
						case "aui-text-delta":
							controller.withParentId(value.parentId).appendText(value.textDelta);
							break;
						case "aui-reasoning-delta":
							controller.withParentId(value.parentId).appendReasoning(value.reasoningDelta);
							break;
						case "b": {
							const { toolCallId, toolName, parentId } = value;
							const ctrl = parentId ? controller.withParentId(parentId) : controller;
							if (toolCallControllers.has(toolCallId)) throw new Error(`Encountered duplicate tool call id: ${toolCallId}`);
							const toolCallController = ctrl.addToolCallPart({
								toolCallId,
								toolName
							});
							toolCallControllers.set(toolCallId, toolCallController);
							activeToolCallArgsText = toolCallController.argsText;
							break;
						}
						case "c": {
							const { toolCallId, argsTextDelta } = value;
							const toolCallController = toolCallControllers.get(toolCallId);
							if (!toolCallController) throw new Error(`Encountered tool call with unknown id: ${toolCallId}`);
							toolCallController.argsText.append(argsTextDelta);
							break;
						}
						case "a": {
							const { toolCallId, artifact, result, isError } = value;
							const toolCallController = toolCallControllers.get(toolCallId);
							if (!toolCallController) throw new Error(`Encountered tool call result with unknown id: ${toolCallId}`);
							toolCallController.setResponse({
								artifact,
								result,
								isError
							});
							break;
						}
						case "9": {
							const { toolCallId, toolName, args } = value;
							let toolCallController = toolCallControllers.get(toolCallId);
							if (toolCallController) toolCallController.argsText.close();
							else {
								toolCallController = controller.addToolCallPart({
									toolCallId,
									toolName,
									args
								});
								toolCallControllers.set(toolCallId, toolCallController);
							}
							break;
						}
						case "d":
							controller.enqueue({
								type: "message-finish",
								path: [],
								...value
							});
							break;
						case "f":
							controller.enqueue({
								type: "step-start",
								path: [],
								...value
							});
							break;
						case "e":
							controller.enqueue({
								type: "step-finish",
								path: [],
								...value
							});
							break;
						case "2":
							controller.enqueue({
								type: "data",
								path: [],
								data: value
							});
							break;
						case "8":
							controller.enqueue({
								type: "annotations",
								path: [],
								annotations: value
							});
							break;
						case "h": {
							const { parentId, ...sourceData } = value;
							(parentId ? controller.withParentId(parentId) : controller).appendSource({
								type: "source",
								...sourceData
							});
							break;
						}
						case "3":
							controller.enqueue({
								type: "error",
								path: [],
								error: value
							});
							break;
						case "k":
							controller.appendFile({
								type: "file",
								...value
							});
							break;
						case "aui-data":
							controller.appendData({
								type: "data",
								...value
							});
							break;
						case "aui-state":
							controller.enqueue({
								type: "update-state",
								path: [],
								operations: value
							});
							break;
						case "j":
						case "i": break;
						default: throw new Error(`unsupported chunk type: ${type}`);
					}
				},
				flush() {
					activeToolCallArgsText?.close();
					activeToolCallArgsText = void 0;
					toolCallControllers.forEach((controller) => controller.close());
					toolCallControllers.clear();
				}
			});
			return readable.pipeThrough(new TextDecoderStream()).pipeThrough(new LineDecoderStream()).pipeThrough(new DataStreamChunkDecoder()).pipeThrough(transform);
		});
	}
};
//#endregion
export { DataStreamDecoder, DataStreamEncoder };

//# sourceMappingURL=DataStream.js.map