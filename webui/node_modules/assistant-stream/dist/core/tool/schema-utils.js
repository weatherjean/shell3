//#region src/core/tool/schema-utils.ts
function isStandardSchema(schema) {
	return typeof schema === "object" && schema !== null && "~standard" in schema && typeof schema["~standard"] === "object";
}
function hasToJSONSchemaMethod(schema) {
	return typeof schema === "object" && schema !== null && "toJSONSchema" in schema && typeof schema.toJSONSchema === "function";
}
function hasToJSONMethod(schema) {
	return typeof schema === "object" && schema !== null && "toJSON" in schema && typeof schema.toJSON === "function";
}
/**
* Converts a schema to JSONSchema7.
* Supports:
* - StandardSchemaV1 with ~standard.toJSONSchema (e.g., Zod v4)
* - StandardSchemaV1 with ~standard.jsonSchema.input() (e.g., Zod v4)
* - Objects with toJSONSchema() method (e.g., Zod v4)
* - Objects with toJSON() method
* - Plain JSONSchema7 objects (must have a "type" property)
*/
function toJSONSchema(schema) {
	if (isStandardSchema(schema)) {
		const toJSONSchemaMethod = schema["~standard"].toJSONSchema;
		if (typeof toJSONSchemaMethod === "function") return toJSONSchemaMethod();
		const jsonSchema = schema["~standard"].jsonSchema;
		if (typeof jsonSchema === "object" && jsonSchema !== null && typeof jsonSchema.input === "function") return jsonSchema.input();
	}
	if (hasToJSONSchemaMethod(schema)) return schema.toJSONSchema();
	if (hasToJSONMethod(schema)) return schema.toJSON();
	if (isStandardSchema(schema)) throw new Error("Could not convert schema to JSON Schema. The schema implements Standard Schema but does not support JSON Schema conversion. If you are using Zod, please upgrade to Zod v4 (npm install zod@latest). Alternatively, pass a plain JSON Schema object instead.");
	return schema;
}
/**
* Returns a copy of the JSON Schema with `required` removed recursively,
* making every property optional. Array item schemas are left unchanged.
*/
function toPartialJSONSchema(schema) {
	const { required: _, ...result } = schema;
	if (result.properties) result.properties = Object.fromEntries(Object.entries(result.properties).map(([key, prop]) => {
		if (typeof prop === "object" && prop !== null && !Array.isArray(prop)) {
			const p = prop;
			return [key, p.properties != null ? toPartialJSONSchema(p) : prop];
		}
		return [key, prop];
	}));
	return result;
}
function defaultToolFilter(_name, tool) {
	return !tool.disabled && tool.type !== "backend" && (tool.type !== "frontend" || tool.execute !== void 0);
}
function toolHasUploadableParameters(tool) {
	return tool.parameters !== void 0 && !tool.unstable_backendDefault?.parameters;
}
/**
* Converts a record of tools to a record of tool definitions with JSON Schema parameters.
* By default, filters out disabled tools and backend tools.
*
* Entries are emitted in alphabetical order so the resulting request body is
* byte-identical regardless of the order in which tools were registered. This
* keeps provider prompt caches stable across renders that mount tools in
* different orders.
*/
function toToolsJSONSchema(tools, options = {}) {
	if (!tools) return {};
	const filter = options.filter ?? defaultToolFilter;
	return Object.fromEntries(Object.entries(tools).filter(([name, tool]) => filter(name, tool)).filter((entry) => toolHasUploadableParameters(entry[1])).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0).map(([name, tool]) => [name, {
		...tool.description && { description: tool.description },
		parameters: toJSONSchema(tool.parameters),
		...tool.providerOptions && { providerOptions: tool.providerOptions }
	}]));
}
//#endregion
export { toJSONSchema, toPartialJSONSchema, toToolsJSONSchema };

//# sourceMappingURL=schema-utils.js.map