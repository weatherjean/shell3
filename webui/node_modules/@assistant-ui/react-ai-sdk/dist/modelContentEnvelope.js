//#region src/modelContentEnvelope.ts
const ENVELOPE_KEY = "__aui_modelContent";
function isModelContentEnvelope(value) {
	return value != null && typeof value === "object" && ENVELOPE_KEY in value && Array.isArray(value[ENVELOPE_KEY]);
}
function wrapModelContentEnvelope(result, modelContent) {
	return {
		[ENVELOPE_KEY]: modelContent,
		value: result
	};
}
function unwrapModelContentEnvelope(output) {
	if (isModelContentEnvelope(output)) return {
		result: output.value,
		modelContent: output[ENVELOPE_KEY]
	};
	return { result: output };
}
//#endregion
export { isModelContentEnvelope, unwrapModelContentEnvelope, wrapModelContentEnvelope };

//# sourceMappingURL=modelContentEnvelope.js.map