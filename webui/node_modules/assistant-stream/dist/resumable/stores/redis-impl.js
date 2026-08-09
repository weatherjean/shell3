import { ResumableStreamError, validateStreamId } from "../errors.js";
import "../constants.js";
//#region src/resumable/stores/redis-impl.ts
const DEFAULT_POLL_INTERVAL_MS = 100;
const DEFAULT_KEY_PREFIX = "aui:resumable";
const FIELD_CHUNK = "c";
const FIELD_FIN = "fin";
const FIELD_ERROR = "error";
const FIN_DONE = "done";
const FIN_ERROR = "error";
const STREAM_START_ID = "0-0";
var RedisResumableStreamStore = class {
	client;
	keyPrefix;
	defaultTtlMs;
	pollIntervalMs;
	maxChunkBytes;
	constructor(client, options = {}) {
		this.client = client;
		this.keyPrefix = options.keyPrefix ?? DEFAULT_KEY_PREFIX;
		this.defaultTtlMs = options.defaultTtlMs ?? 864e5;
		this.pollIntervalMs = options.pollIntervalMs ?? DEFAULT_POLL_INTERVAL_MS;
		this.maxChunkBytes = options.maxChunkBytes;
	}
	async acquire(streamId, options) {
		validateStreamId(streamId);
		const ttlSec = msToSec(options?.ttlMs ?? this.defaultTtlMs);
		const meta = JSON.stringify({
			status: "streaming",
			ttlSec
		});
		if (await this.client.setNX(this.metaKey(streamId), meta, ttlSec)) {
			await this.client.del([this.dataKey(streamId)]);
			return "producer";
		}
		return "consumer";
	}
	async append(streamId, chunk) {
		validateStreamId(streamId);
		if (this.maxChunkBytes !== void 0 && chunk.byteLength > this.maxChunkBytes) throw new Error(`Chunk exceeds maxChunkBytes (${chunk.byteLength} > ${this.maxChunkBytes})`);
		const dataKey = this.dataKey(streamId);
		const metaKey = this.metaKey(streamId);
		const meta = await this.readMeta(streamId);
		if (!meta) throw new Error(`Stream not found: ${streamId}`);
		if (meta.status !== "streaming") throw new ResumableStreamError("finalized", `Stream already finalized: ${streamId}`);
		const ttlSec = meta.ttlSec ?? msToSec(this.defaultTtlMs);
		await this.client.pipeline([
			{
				type: "xAdd",
				key: dataKey,
				fields: { [FIELD_CHUNK]: chunk }
			},
			{
				type: "expire",
				key: dataKey,
				ttlSec
			},
			{
				type: "expire",
				key: metaKey,
				ttlSec
			}
		]);
	}
	async finalize(streamId, status, error) {
		validateStreamId(streamId);
		const dataKey = this.dataKey(streamId);
		const metaKey = this.metaKey(streamId);
		const existing = await this.readMeta(streamId);
		if (!existing) throw new Error(`Stream not found: ${streamId}`);
		if (existing.status !== "streaming") return;
		const ttlSec = existing.ttlSec ?? msToSec(this.defaultTtlMs);
		const meta = JSON.stringify(status === "error" ? {
			status: "error",
			error: error ?? "Stream errored",
			ttlSec
		} : {
			status: "done",
			ttlSec
		});
		const fields = { [FIELD_FIN]: status === "error" ? FIN_ERROR : FIN_DONE };
		if (status === "error") fields[FIELD_ERROR] = error ?? "Stream errored";
		await this.client.pipeline([
			{
				type: "set",
				key: metaKey,
				value: meta,
				ttlSec
			},
			{
				type: "xAdd",
				key: dataKey,
				fields
			},
			{
				type: "expire",
				key: dataKey,
				ttlSec
			}
		]);
	}
	async *read(streamId, cursor, signal) {
		validateStreamId(streamId);
		const dataKey = this.dataKey(streamId);
		const metaKey = this.metaKey(streamId);
		if (await this.client.get(metaKey) === null) throw new Error(`Stream not found: ${streamId}`);
		let lastId = cursor === "" ? STREAM_START_ID : cursor;
		while (true) {
			if (signal.aborted) return;
			const start = lastId === STREAM_START_ID ? "-" : `(${lastId}`;
			const entries = await this.client.xRange(dataKey, start, "+");
			for (const entry of entries) {
				if (signal.aborted) return;
				lastId = entry.id;
				const fin = readString(entry.fields[FIELD_FIN]);
				if (fin === FIN_DONE) return;
				if (fin === FIN_ERROR) throw new Error(readString(entry.fields[FIELD_ERROR]) ?? "Stream errored");
				const raw = entry.fields[FIELD_CHUNK];
				if (raw === void 0) continue;
				yield {
					cursor: entry.id,
					chunk: toBytes(raw)
				};
			}
			if (entries.length > 0) continue;
			if (!await this.client.exists(metaKey)) return;
			await sleep(this.pollIntervalMs, signal);
		}
	}
	async status(streamId) {
		validateStreamId(streamId);
		const meta = await this.client.get(this.metaKey(streamId));
		if (meta === null) return "missing";
		const parsed = parseMeta(meta);
		if (parsed?.status === "streaming") return "streaming";
		if (parsed?.status === "done") return "done";
		if (parsed?.status === "error") return "error";
		return "missing";
	}
	async delete(streamId) {
		validateStreamId(streamId);
		await this.client.del([this.metaKey(streamId), this.dataKey(streamId)]);
	}
	async readMeta(streamId) {
		const raw = await this.client.get(this.metaKey(streamId));
		if (raw === null) return void 0;
		return parseMeta(raw);
	}
	metaKey(streamId) {
		return `${this.keyPrefix}:{${streamId}}:meta`;
	}
	dataKey(streamId) {
		return `${this.keyPrefix}:{${streamId}}:data`;
	}
};
function parseMeta(value) {
	try {
		const parsed = JSON.parse(value);
		return parsed && typeof parsed === "object" ? parsed : void 0;
	} catch {
		return;
	}
}
function msToSec(ms) {
	return Math.max(1, Math.ceil(ms / 1e3));
}
function sleep(ms, signal) {
	return new Promise((resolve) => {
		if (signal.aborted) {
			resolve();
			return;
		}
		const timer = setTimeout(() => {
			signal.removeEventListener("abort", onAbort);
			resolve();
		}, ms);
		const onAbort = () => {
			clearTimeout(timer);
			resolve();
		};
		signal.addEventListener("abort", onAbort, { once: true });
	});
}
const SHARED_DECODER = new TextDecoder();
const SHARED_ENCODER = new TextEncoder();
function readString(value) {
	if (value === void 0) return void 0;
	if (typeof value === "string") return value;
	return SHARED_DECODER.decode(value);
}
function toBytes(value) {
	if (value instanceof Uint8Array) return value;
	return SHARED_ENCODER.encode(value);
}
//#endregion
export { RedisResumableStreamStore };

//# sourceMappingURL=redis-impl.js.map