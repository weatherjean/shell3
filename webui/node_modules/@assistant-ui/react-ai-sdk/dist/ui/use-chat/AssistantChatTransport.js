import { RESUMABLE_STREAM_ID_HEADER } from "../resumable.js";
import { DefaultChatTransport } from "ai";
import { toToolsJSONSchema } from "assistant-stream";
//#region src/ui/use-chat/AssistantChatTransport.ts
const FINISH_MARKER = "\"type\":\"finish\"";
const FINISH_BUFFER_LIMIT = 4096;
var AssistantChatTransport = class extends DefaultChatTransport {
	runtime;
	getThreadListItem;
	resumable;
	constructor(initOptions) {
		const { resumable, ...rest } = initOptions ?? {};
		const userFetch = rest.fetch;
		const userPrepareReconnect = rest.prepareReconnectToStreamRequest;
		super({
			...rest,
			...resumable && {
				fetch: wrapFetchWithResumable(resumable, userFetch),
				prepareReconnectToStreamRequest: wrapPrepareReconnect(resumable, userPrepareReconnect)
			},
			prepareSendMessagesRequest: async (options) => {
				const context = this.runtime?.thread.getModelContext();
				const id = (await (this.getThreadListItem?.() ?? this.runtime?.threads.mainItem)?.initialize())?.remoteId ?? options.id;
				const optionsEx = {
					...options,
					body: {
						callSettings: context?.callSettings,
						system: context?.system,
						config: context?.config,
						tools: toToolsJSONSchema(context?.tools ?? {}),
						...options?.body
					}
				};
				const preparedRequest = await rest.prepareSendMessagesRequest?.(optionsEx);
				return {
					...preparedRequest,
					body: preparedRequest?.body ?? {
						...optionsEx.body,
						id,
						messages: options.messages,
						trigger: options.trigger,
						messageId: options.messageId,
						metadata: options.requestMetadata
					}
				};
			}
		});
		this.resumable = resumable;
	}
	setRuntime(runtime) {
		this.runtime = runtime;
	}
	getResumableAdapter() {
		return this.resumable;
	}
	__internal_setGetThreadListItem(getter) {
		this.getThreadListItem = getter;
	}
};
function wrapFetchWithResumable(resumable, userFetch) {
	const baseFetch = userFetch ? (input, init) => userFetch(input, init) : globalThis.fetch.bind(globalThis);
	return async (input, init) => {
		const res = await baseFetch(input, init);
		const id = res.headers.get(RESUMABLE_STREAM_ID_HEADER);
		if (id) resumable.storage.setStreamId(id);
		if (!res.body) return res;
		const detectFinish = resumable.isFinishEvent ?? defaultIsFinishEvent;
		const decoder = new TextDecoder();
		let accumulator = "";
		const tap = new TransformStream({ transform(chunk, controller) {
			controller.enqueue(chunk);
			accumulator += decoder.decode(chunk, { stream: true });
			if (detectFinish(chunk, accumulator)) {
				resumable.storage.clear();
				accumulator = "";
			} else if (accumulator.length > FINISH_BUFFER_LIMIT) accumulator = accumulator.slice(-1024);
		} });
		return new Response(res.body.pipeThrough(tap), {
			status: res.status,
			statusText: res.statusText,
			headers: res.headers
		});
	};
}
function defaultIsFinishEvent(_chunk, accumulator) {
	return accumulator.includes(FINISH_MARKER);
}
function wrapPrepareReconnect(resumable, userPrepareReconnect) {
	return async (options) => {
		const streamId = resumable.storage.getStreamId();
		if (!streamId) throw new Error("AssistantChatTransport: no resumable stream id available; nothing to resume");
		const api = typeof resumable.resumeApi === "function" ? resumable.resumeApi(streamId) : resumable.resumeApi;
		const userPrepared = await userPrepareReconnect?.({
			...options,
			api
		});
		return {
			...userPrepared,
			api: userPrepared?.api ?? api
		};
	};
}
//#endregion
export { AssistantChatTransport };

//# sourceMappingURL=AssistantChatTransport.js.map