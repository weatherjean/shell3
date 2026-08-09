//#region src/CloudMessagePersistence.ts
/**
* Shared persistence logic for cloud message storage.
*
* Handles ID mapping (local → remote) and parent_id chaining for both:
* - AssistantCloudThreadHistoryAdapter (assistant-ui runtime)
* - useCloudChat (standalone AI SDK hook)
*
* The promise-based ID resolution handles concurrent appends — if message B's
* parent is message A, and A is still being created, we await A's promise
* to get its remote ID before creating B.
*/
var CloudMessagePersistence = class {
	cloud;
	idMapping = {};
	constructor(cloud) {
		this.cloud = cloud;
	}
	/**
	* Persist a message to the cloud.
	*
	* @param threadId - Remote thread ID
	* @param messageId - Local message ID (used for tracking)
	* @param parentId - Local parent message ID (or null for first message)
	* @param format - Message format (e.g., "aui/v0", "ai-sdk/v6")
	* @param content - Message content (format-specific)
	*/
	async append(threadId, messageId, parentId, format, content) {
		const resolvedParentId = parentId ? await this.idMapping[parentId] ?? parentId : null;
		const task = this.cloud.threads.messages.create(threadId, {
			parent_id: resolvedParentId,
			format,
			content
		}).then(({ message_id }) => {
			this.idMapping[messageId] = message_id;
			return message_id;
		}).catch((err) => {
			if (this.idMapping[messageId] === task) delete this.idMapping[messageId];
			throw err;
		});
		this.idMapping[messageId] = task;
		return task.then(() => {});
	}
	/**
	* Update an already-persisted message in the cloud.
	*/
	async update(threadId, messageId, _format, content) {
		const remoteId = await this.getRemoteId(messageId);
		if (!remoteId) return;
		await this.cloud.threads.messages.update(threadId, remoteId, { content });
	}
	/**
	* Check if a message has been persisted (or is currently being persisted).
	*/
	isPersisted(messageId) {
		return messageId in this.idMapping;
	}
	/**
	* Get the remote ID for a local message ID (resolved).
	* Returns undefined if not persisted.
	*/
	async getRemoteId(messageId) {
		const entry = this.idMapping[messageId];
		if (!entry) return void 0;
		return entry;
	}
	/**
	* Load messages from the cloud and populate the ID mapping.
	*
	* The ID mapping is populated so that `isPersisted()` returns true for
	* loaded messages, preventing re-persistence of already-stored messages.
	*
	* @param threadId - Remote thread ID
	* @param format - Optional format filter
	* @returns Array of cloud messages
	*/
	async load(threadId, format) {
		const { messages } = await this.cloud.threads.messages.list(threadId, format ? { format } : void 0);
		for (const m of messages) this.idMapping[m.id] = m.id;
		return messages;
	}
	/**
	* Reset the ID mapping (call when switching threads).
	*/
	reset() {
		this.idMapping = {};
	}
};
//#endregion
export { CloudMessagePersistence };

//# sourceMappingURL=CloudMessagePersistence.js.map