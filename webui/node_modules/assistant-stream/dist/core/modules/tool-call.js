import { createTextStream } from "./text.js";
//#region src/core/modules/tool-call.ts
var ToolCallStreamControllerImpl = class {
	_controller;
	_isClosed = false;
	_mergeTask;
	constructor(_controller) {
		this._controller = _controller;
		const stream = createTextStream({ start: (c) => {
			this._argsTextController = c;
		} });
		let hasArgsText = false;
		this._mergeTask = stream.pipeTo(new WritableStream({ write: (chunk) => {
			switch (chunk.type) {
				case "text-delta":
					hasArgsText = true;
					this._controller.enqueue(chunk);
					break;
				case "part-finish":
					if (!hasArgsText) this._controller.enqueue({
						type: "text-delta",
						textDelta: "{}",
						path: []
					});
					this._controller.enqueue({
						type: "tool-call-args-text-finish",
						path: []
					});
					break;
				default: throw new Error(`Unexpected chunk type: ${chunk.type}`);
			}
		} }));
	}
	get argsText() {
		return this._argsTextController;
	}
	_argsTextController;
	async setResponse(response) {
		this._argsTextController.close();
		await Promise.resolve();
		this._controller.enqueue({
			type: "result",
			path: [],
			...response.artifact !== void 0 ? { artifact: response.artifact } : {},
			result: response.result,
			isError: response.isError ?? false,
			...response.modelContent !== void 0 ? { modelContent: response.modelContent } : {},
			...response.messages !== void 0 ? { messages: response.messages } : {}
		});
	}
	async close() {
		if (this._isClosed) return;
		this._isClosed = true;
		this._argsTextController.close();
		await this._mergeTask;
		this._controller.enqueue({
			type: "part-finish",
			path: []
		});
		this._controller.close();
	}
};
const createToolCallStream = (readable) => {
	return new ReadableStream({
		start(c) {
			return readable.start?.(new ToolCallStreamControllerImpl(c));
		},
		pull(c) {
			return readable.pull?.(new ToolCallStreamControllerImpl(c));
		},
		cancel(c) {
			return readable.cancel?.(c);
		}
	});
};
const createToolCallStreamController = () => {
	let controller;
	return [createToolCallStream({ start(c) {
		controller = c;
	} }), controller];
};
//#endregion
export { createToolCallStream, createToolCallStreamController };

//# sourceMappingURL=tool-call.js.map