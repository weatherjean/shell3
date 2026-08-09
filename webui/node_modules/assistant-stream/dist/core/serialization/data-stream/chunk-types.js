//#region src/core/serialization/data-stream/chunk-types.ts
let DataStreamStreamChunkType = /* @__PURE__ */ function(DataStreamStreamChunkType) {
	DataStreamStreamChunkType["TextDelta"] = "0";
	DataStreamStreamChunkType["Data"] = "2";
	DataStreamStreamChunkType["Error"] = "3";
	DataStreamStreamChunkType["Annotation"] = "8";
	DataStreamStreamChunkType["ToolCall"] = "9";
	DataStreamStreamChunkType["ToolCallResult"] = "a";
	DataStreamStreamChunkType["StartToolCall"] = "b";
	DataStreamStreamChunkType["ToolCallArgsTextDelta"] = "c";
	DataStreamStreamChunkType["FinishMessage"] = "d";
	DataStreamStreamChunkType["FinishStep"] = "e";
	DataStreamStreamChunkType["StartStep"] = "f";
	DataStreamStreamChunkType["ReasoningDelta"] = "g";
	DataStreamStreamChunkType["Source"] = "h";
	DataStreamStreamChunkType["RedactedReasoning"] = "i";
	DataStreamStreamChunkType["ReasoningSignature"] = "j";
	DataStreamStreamChunkType["File"] = "k";
	DataStreamStreamChunkType["AuiUpdateStateOperations"] = "aui-state";
	DataStreamStreamChunkType["AuiTextDelta"] = "aui-text-delta";
	DataStreamStreamChunkType["AuiReasoningDelta"] = "aui-reasoning-delta";
	DataStreamStreamChunkType["AuiDataPart"] = "aui-data";
	return DataStreamStreamChunkType;
}({});
//#endregion
export { DataStreamStreamChunkType };

//# sourceMappingURL=chunk-types.js.map