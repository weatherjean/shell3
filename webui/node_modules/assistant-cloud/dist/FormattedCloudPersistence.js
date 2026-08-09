//#region src/FormattedCloudPersistence.ts
/**
* Wraps a CloudMessagePersistence instance with format-aware encode/decode.
*
* This centralizes the pattern used by both:
* - useCloudChat (standalone AI SDK hook)
* - AssistantCloudThreadHistoryAdapter.withFormat() (assistant-ui runtime)
*
* The persistence parameter is typed structurally (not by class) so callers
* don't need to import CloudMessagePersistence directly.
*/
const createFormattedPersistence = (persistence, adapter) => ({
	append: async (threadId, item) => {
		const messageId = adapter.getId(item.message);
		const encoded = adapter.encode(item);
		return persistence.append(threadId, messageId, item.parentId, adapter.format, encoded);
	},
	update: persistence.update ? async (threadId, item, messageId) => {
		const encoded = adapter.encode(item);
		return persistence.update(threadId, messageId, adapter.format, encoded);
	} : void 0,
	load: async (threadId) => {
		return { messages: (await persistence.load(threadId, adapter.format)).filter((m) => m.format === adapter.format).map((m) => adapter.decode({
			id: m.id,
			parent_id: m.parent_id,
			format: m.format,
			content: m.content
		})).reverse() };
	},
	isPersisted: (messageId) => persistence.isPersisted(messageId)
});
//#endregion
export { createFormattedPersistence };

//# sourceMappingURL=FormattedCloudPersistence.js.map