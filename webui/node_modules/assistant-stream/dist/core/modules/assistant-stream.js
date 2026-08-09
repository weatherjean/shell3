import { AssistantStream } from "../AssistantStream.js";
import { promiseWithResolvers } from "../../utils/promiseWithResolvers.js";
import { createMergeStream } from "../utils/stream/merge.js";
import { createTextStreamController } from "./text.js";
import { createToolCallStreamController } from "./tool-call.js";
import { Counter } from "../utils/Counter.js";
import { PathAppendEncoder, PathMergeEncoder } from "../utils/stream/path-utils.js";
import { DataStreamEncoder } from "../serialization/data-stream/DataStream.js";
import { generateId } from "../utils/generateId.js";
//#region src/core/modules/assistant-stream.ts
var AssistantStreamControllerImpl = class AssistantStreamControllerImpl {
	_state;
	_parentId;
	constructor(state) {
		this._state = state || {
			merger: createMergeStream(),
			contentCounter: new Counter()
		};
	}
	get __internal_isClosed() {
		return this._state.merger.isSealed();
	}
	__internal_getReadable() {
		return this._state.merger.readable;
	}
	__internal_subscribeToClose(callback) {
		this._state.closeSubscriber = callback;
	}
	_addPart(part, stream) {
		if (this._state.append) {
			this._state.append.controller.close();
			this._state.append = void 0;
		}
		this.enqueue({
			type: "part-start",
			part,
			path: []
		});
		this._state.merger.addStream(stream.pipeThrough(new PathAppendEncoder(this._state.contentCounter.value)));
	}
	merge(stream) {
		this._state.merger.addStream(stream.pipeThrough(new PathMergeEncoder(this._state.contentCounter)));
	}
	appendText(textDelta) {
		if (this._state.append?.kind !== "text" || this._state.append.parentId !== this._parentId) this._state.append = {
			kind: "text",
			parentId: this._parentId,
			controller: this.addTextPart()
		};
		this._state.append.controller.append(textDelta);
	}
	appendReasoning(textDelta) {
		if (this._state.append?.kind !== "reasoning" || this._state.append.parentId !== this._parentId) this._state.append = {
			kind: "reasoning",
			parentId: this._parentId,
			controller: this.addReasoningPart()
		};
		this._state.append.controller.append(textDelta);
	}
	addTextPart() {
		const [stream, controller] = createTextStreamController();
		this._addPart(this._withParentIdOption({ type: "text" }), stream);
		return controller;
	}
	addReasoningPart() {
		const [stream, controller] = createTextStreamController();
		this._addPart(this._withParentIdOption({ type: "reasoning" }), stream);
		return controller;
	}
	addToolCallPart(options) {
		const opt = typeof options === "string" ? { toolName: options } : options;
		const toolName = opt.toolName;
		const toolCallId = opt.toolCallId ?? generateId();
		const [stream, controller] = createToolCallStreamController();
		this._addPart({
			type: "tool-call",
			toolName,
			toolCallId,
			...this._parentId && { parentId: this._parentId }
		}, stream);
		if (opt.argsText !== void 0) {
			controller.argsText.append(opt.argsText);
			controller.argsText.close();
		}
		if (opt.args !== void 0) {
			controller.argsText.append(JSON.stringify(opt.args));
			controller.argsText.close();
		}
		if (opt.response !== void 0) controller.setResponse(opt.response);
		return controller;
	}
	_finishedPartStream() {
		return new ReadableStream({ start(controller) {
			controller.enqueue({
				type: "part-finish",
				path: []
			});
			controller.close();
		} });
	}
	_withParentIdOption(options) {
		if (!this._parentId) return options;
		return {
			...options,
			parentId: this._parentId
		};
	}
	appendSource(options) {
		this._addPart(this._withParentIdOption(options), this._finishedPartStream());
	}
	appendFile(options) {
		this._addPart(this._withParentIdOption(options), this._finishedPartStream());
	}
	appendData(options) {
		this._addPart(this._withParentIdOption(options), this._finishedPartStream());
	}
	enqueue(chunk) {
		this._state.merger.enqueue(chunk);
		if (chunk.type === "part-start" && chunk.path.length === 0) this._state.contentCounter.up();
	}
	withParentId(parentId) {
		const controller = new AssistantStreamControllerImpl(this._state);
		controller._parentId = parentId;
		return controller;
	}
	close() {
		this._state.append?.controller?.close();
		this._state.merger.seal();
		this._state.closeSubscriber?.();
	}
};
/**
* Creates an {@link AssistantStream} and writes to it with an
* {@link AssistantStreamController}.
*
* The callback may write synchronously or asynchronously. If it throws, an
* `error` chunk is emitted before the error is rethrown; when the callback
* settles, the stream is closed automatically unless the controller was
* already closed.
*/
function createAssistantStream(callback) {
	const controller = new AssistantStreamControllerImpl();
	const runTask = async () => {
		try {
			await callback(controller);
		} catch (e) {
			if (!controller.__internal_isClosed) controller.enqueue({
				type: "error",
				path: [],
				error: String(e)
			});
			throw e;
		} finally {
			if (!controller.__internal_isClosed) controller.close();
		}
	};
	runTask();
	return controller.__internal_getReadable();
}
/**
* Creates an {@link AssistantStream} together with the controller used to
* write into it.
*
* Use this when the stream needs to be returned before all writers are known.
* Closing the returned controller finishes the paired stream.
*/
function createAssistantStreamController() {
	const { resolve, promise } = promiseWithResolvers();
	let controller;
	return [createAssistantStream((c) => {
		controller = c;
		controller.__internal_subscribeToClose(resolve);
		return promise;
	}), controller];
}
/**
* Creates a `Response` whose body is an encoded {@link AssistantStream}.
*
* This is the HTTP-route convenience form of {@link createAssistantStream}; it
* uses {@link DataStreamEncoder} so the response can be consumed by matching
* assistant-ui data stream decoders.
*/
function createAssistantStreamResponse(callback) {
	return AssistantStream.toResponse(createAssistantStream(callback), new DataStreamEncoder());
}
//#endregion
export { createAssistantStream, createAssistantStreamController, createAssistantStreamResponse };

//# sourceMappingURL=assistant-stream.js.map