"use client";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getExternalStoreMessages } from "@assistant-ui/core";
import { MessageRepository } from "@assistant-ui/core/internal";
import { useAui } from "@assistant-ui/store";
//#region src/ui/use-chat/useExternalHistory.ts
const toExportedMessageRepository = (toThreadMessages, messages) => {
	const survivingIds = /* @__PURE__ */ new Set();
	const survivors = messages.messages.flatMap((m) => {
		const message = toThreadMessages([m.message])[0];
		if (!message) {
			console.warn("Skipping a stored message that could not be loaded.");
			return [];
		}
		if (m.parentId && !survivingIds.has(m.parentId)) return [];
		survivingIds.add(message.id);
		return [{
			...m,
			message
		}];
	});
	return {
		headId: messages.headId && survivingIds.has(messages.headId) ? messages.headId : null,
		messages: survivors
	};
};
const useExternalHistory = (runtimeRef, historyAdapter, toThreadMessages, storageFormatAdapter, onSetMessages) => {
	const loadedRef = useRef(false);
	const aui = useAui();
	const optionalThreadListItem = useCallback(() => aui.threadListItem.source ? aui.threadListItem() : null, [aui]);
	const [isLoading, setIsLoading] = useState(false);
	const historyIds = useRef(/* @__PURE__ */ new Set());
	const onSetMessagesRef = useRef(onSetMessages);
	useEffect(() => {
		onSetMessagesRef.current = onSetMessages;
	});
	const formatAdapter = useMemo(() => {
		if (!historyAdapter) return void 0;
		if (!historyAdapter.withFormat) throw new Error("useAISDKRuntime: ThreadHistoryAdapter is missing the required `withFormat` method.");
		return historyAdapter.withFormat(storageFormatAdapter);
	}, [historyAdapter, storageFormatAdapter]);
	useEffect(() => {
		if (!formatAdapter || loadedRef.current) return;
		const loadHistory = async () => {
			setIsLoading(true);
			try {
				const repo = await formatAdapter.load();
				if (repo && repo.messages.length > 0) {
					const converted = toExportedMessageRepository(toThreadMessages, repo);
					runtimeRef.current.thread.import(converted);
					const tempRepo = new MessageRepository();
					tempRepo.import(converted);
					const messages = tempRepo.getMessages();
					onSetMessagesRef.current(messages.flatMap(getExternalStoreMessages));
					historyIds.current = new Set(converted.messages.map((m) => m.message.id));
				}
			} catch (error) {
				console.error("Failed to load message history:", error);
			} finally {
				setIsLoading(false);
			}
		};
		loadedRef.current = true;
		if (!optionalThreadListItem()?.getState().remoteId) {
			setIsLoading(false);
			return;
		}
		loadHistory();
	}, [
		formatAdapter,
		toThreadMessages,
		runtimeRef,
		optionalThreadListItem
	]);
	const runStartRef = useRef(null);
	const persistTimerRef = useRef(null);
	const stepBoundariesRef = useRef([]);
	const wasRunningRef = useRef(false);
	const toolCallCountRef = useRef(0);
	useEffect(() => {
		if (!formatAdapter) return;
		const unsubscribe = runtimeRef.current.thread.subscribe(() => {
			const { isRunning } = runtimeRef.current.thread.getState();
			const wasRunning = wasRunningRef.current;
			wasRunningRef.current = isRunning;
			if (runStartRef.current != null) {
				const lastMsg = runtimeRef.current.thread.getState().messages.at(-1);
				if (lastMsg?.role === "assistant") {
					const currentToolCallCount = lastMsg.content.filter((p) => p.type === "tool-call").length;
					while (toolCallCountRef.current < currentToolCallCount) {
						stepBoundariesRef.current.push(Date.now() - runStartRef.current);
						toolCallCountRef.current++;
					}
				}
			}
			if (isRunning) {
				if (runStartRef.current == null) {
					runStartRef.current = Date.now();
					stepBoundariesRef.current = [];
					toolCallCountRef.current = 0;
				}
				if (persistTimerRef.current) {
					clearTimeout(persistTimerRef.current);
					persistTimerRef.current = null;
				}
				return;
			}
			if (!wasRunning) return;
			if (runStartRef.current != null) stepBoundariesRef.current.push(Date.now() - runStartRef.current);
			if (persistTimerRef.current) clearTimeout(persistTimerRef.current);
			persistTimerRef.current = setTimeout(async () => {
				persistTimerRef.current = null;
				const latest = runtimeRef.current.thread.getState();
				if (latest.isRunning) return;
				const boundaries = stepBoundariesRef.current;
				const durationMs = boundaries.length > 0 ? boundaries.at(-1) : void 0;
				if (boundaries.length === 1 && durationMs != null) {
					const lastAssistant = latest.messages.findLast((m) => m.role === "assistant");
					if (lastAssistant) {
						const tcCount = lastAssistant.content.filter((p) => p.type === "tool-call").length;
						if (tcCount > 0) {
							const totalSteps = tcCount + 1;
							const stepDur = durationMs / totalSteps;
							boundaries.length = 0;
							for (let i = 0; i < totalSteps; i++) boundaries.push(Math.round((i + 1) * stepDur));
						}
					}
				}
				const stepTimestamps = boundaries.length > 1 ? boundaries.map((endMs, i) => ({
					start_ms: i === 0 ? 0 : boundaries[i - 1],
					end_ms: endMs
				})) : void 0;
				runStartRef.current = null;
				stepBoundariesRef.current = [];
				const telemetryOptions = {
					...durationMs != null ? { durationMs } : void 0,
					...stepTimestamps != null ? { stepTimestamps } : void 0
				};
				const { messages } = latest;
				let lastInnerMessageId = null;
				const getLastInnerId = (msgs) => msgs.length > 0 ? storageFormatAdapter.getId(msgs.at(-1)) : null;
				const toBatchItems = (msgs) => msgs.map((msg, idx) => ({
					parentId: idx === 0 ? lastInnerMessageId : storageFormatAdapter.getId(msgs[idx - 1]),
					message: msg
				}));
				for (const message of messages) {
					const innerMessages = getExternalStoreMessages(message);
					if (!(message.status === void 0 || message.status.type === "complete" || message.status.type === "incomplete")) {
						lastInnerMessageId = getLastInnerId(innerMessages) ?? lastInnerMessageId;
						continue;
					}
					if (historyIds.current.has(message.id)) {
						if (durationMs !== void 0) {
							let parentId = lastInnerMessageId;
							for (const innerMessage of innerMessages) {
								try {
									await formatAdapter.update?.({
										parentId,
										message: innerMessage
									}, storageFormatAdapter.getId(innerMessage));
								} catch {}
								parentId = storageFormatAdapter.getId(innerMessage);
							}
						}
						lastInnerMessageId = getLastInnerId(innerMessages) ?? lastInnerMessageId;
						continue;
					}
					historyIds.current.add(message.id);
					const batchItems = toBatchItems(innerMessages);
					for (const item of batchItems) await formatAdapter.append(item);
					lastInnerMessageId = getLastInnerId(innerMessages) ?? lastInnerMessageId;
					formatAdapter.reportTelemetry?.(batchItems, telemetryOptions);
				}
			}, 0);
		});
		return () => {
			unsubscribe();
			if (persistTimerRef.current) {
				clearTimeout(persistTimerRef.current);
				persistTimerRef.current = null;
			}
		};
	}, [
		formatAdapter,
		storageFormatAdapter,
		runtimeRef
	]);
	return {
		isLoading,
		deleteMessage: useCallback(async (messageId) => {
			if (!formatAdapter?.delete) return;
			const messages = runtimeRef.current.thread.getState().messages;
			const messageIndex = messages.findIndex((m) => m.id === messageId);
			if (messageIndex === -1) return;
			const previousInnerMessages = messages.slice(0, messageIndex).flatMap(getExternalStoreMessages);
			let parentId = previousInnerMessages.at(-1) ? storageFormatAdapter.getId(previousInnerMessages.at(-1)) : null;
			const itemsToDelete = getExternalStoreMessages(messages[messageIndex]).map((message) => {
				const item = {
					parentId,
					message
				};
				parentId = storageFormatAdapter.getId(message);
				return item;
			});
			await formatAdapter.delete(itemsToDelete);
			historyIds.current.delete(messageId);
		}, [
			formatAdapter,
			runtimeRef,
			storageFormatAdapter
		])
	};
};
//#endregion
export { toExportedMessageRepository, useExternalHistory };

//# sourceMappingURL=useExternalHistory.js.map