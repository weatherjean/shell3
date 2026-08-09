import { ResumableStreamError } from "./errors.js";
//#region src/resumable/ResumableStreamContext.ts
function createResumableStreamContext(options) {
	const { store, waitUntil, onAcquire, onAppend, onFinalize, onError } = options;
	const acquireOptions = options.ttlMs !== void 0 ? { ttlMs: options.ttlMs } : void 0;
	return {
		async run(streamId, makeStream) {
			const role = await store.acquire(streamId, acquireOptions);
			onAcquire?.(streamId, role);
			if (role === "producer") startProducerTask(store, streamId, makeStream, {
				waitUntil,
				onAppend,
				onFinalize,
				onError
			});
			return readFromStore(store, streamId);
		},
		async resume(streamId) {
			if (await store.status(streamId) === "missing") return null;
			return readFromStore(store, streamId);
		},
		async requireResume(streamId) {
			if (await store.status(streamId) === "missing") throw new ResumableStreamError("missing", `resumable stream not found: ${streamId}`);
			return readFromStore(store, streamId);
		},
		async status(streamId) {
			return store.status(streamId);
		},
		async delete(streamId) {
			await store.delete(streamId);
		}
	};
}
function startProducerTask(store, streamId, makeStream, hooks) {
	const { waitUntil, onAppend, onFinalize, onError } = hooks;
	const task = (async () => {
		let reader;
		let cancelled = false;
		try {
			reader = makeStream().getReader();
			while (true) {
				const { done, value } = await reader.read();
				if (done) break;
				await store.append(streamId, value);
				onAppend?.(streamId, value.byteLength);
			}
			await store.finalize(streamId, "done");
			onFinalize?.(streamId, "done");
		} catch (err) {
			cancelled = true;
			onError?.(streamId, err);
			const message = err instanceof Error ? err.message : String(err);
			try {
				await reader?.cancel(err);
			} catch (cancelErr) {
				console.error("resumable stream reader cancel failed:", cancelErr);
			}
			try {
				await store.finalize(streamId, "error", message);
				onFinalize?.(streamId, "error", message);
			} catch (finalizeErr) {
				console.error("resumable stream finalize failed:", finalizeErr);
			}
		} finally {
			if (!cancelled) reader?.releaseLock();
		}
	})();
	if (waitUntil) waitUntil(task);
	task.catch((err) => {
		console.error("resumable producer task failed:", err);
	});
}
function readFromStore(store, streamId) {
	const ac = new AbortController();
	let iterator;
	return new ReadableStream({
		start() {
			iterator = store.read(streamId, "", ac.signal)[Symbol.asyncIterator]();
		},
		async pull(controller) {
			try {
				if (!iterator) return;
				const { done, value } = await iterator.next();
				if (done) {
					controller.close();
					return;
				}
				controller.enqueue(value.chunk);
			} catch (err) {
				ac.abort();
				try {
					await iterator?.return?.();
				} catch {}
				controller.error(err);
			}
		},
		cancel() {
			ac.abort();
			iterator?.return?.().catch(() => {});
		}
	});
}
//#endregion
export { createResumableStreamContext };

//# sourceMappingURL=ResumableStreamContext.js.map