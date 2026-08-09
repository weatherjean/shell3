import { AssistantTransformStream } from "../utils/stream/AssistantTransformStream.js";
import { PipeableTransformStream } from "../utils/stream/PipeableTransformStream.js";
//#region src/core/serialization/PlainText.ts
var PlainTextEncoder = class extends PipeableTransformStream {
	headers = new Headers({
		"Content-Type": "text/plain; charset=utf-8",
		"x-vercel-ai-data-stream": "v1"
	});
	constructor() {
		super((readable) => {
			const transform = new TransformStream({ transform(chunk, controller) {
				const type = chunk.type;
				switch (type) {
					case "text-delta":
						controller.enqueue(chunk.textDelta);
						break;
					case "part-start":
					case "part-finish":
					case "step-start":
					case "step-finish":
					case "message-finish":
					case "error": break;
					default: throw new Error(`unsupported chunk type: ${type}`);
				}
			} });
			return readable.pipeThrough(transform).pipeThrough(new TextEncoderStream());
		});
	}
};
var PlainTextDecoder = class extends PipeableTransformStream {
	constructor() {
		super((readable) => {
			const transform = new AssistantTransformStream({ transform(chunk, controller) {
				controller.appendText(chunk);
			} });
			return readable.pipeThrough(new TextDecoderStream()).pipeThrough(transform);
		});
	}
};
//#endregion
export { PlainTextDecoder, PlainTextEncoder };

//# sourceMappingURL=PlainText.js.map