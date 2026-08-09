import { RedisResumableStreamStore } from "./redis-impl.js";
//#region src/resumable/stores/redis.ts
const RESP_BLOB_STRING = 36;
/**
* Resumable stream store backed by [`redis`](https://www.npmjs.com/package/redis)
* v5. Expects a connected client; cluster routing relies on the shared
* `{streamId}` hash tag baked into the key scheme.
*/
function createRedisResumableStreamStore(client, options) {
	return new RedisResumableStreamStore(adapt(client), options);
}
function adapt(client) {
	return {
		async setNX(key, value, ttlSec) {
			return await client.set(key, value, {
				NX: true,
				EX: ttlSec
			}) === "OK";
		},
		async set(key, value, ttlSec) {
			await client.set(key, value, { EX: ttlSec });
		},
		async get(key) {
			return client.get(key);
		},
		async expire(key, ttlSec) {
			await client.expire(key, ttlSec);
		},
		async exists(key) {
			return await client.exists(key) > 0;
		},
		async del(keys) {
			if (keys.length === 0) return;
			await client.del(keys.length === 1 ? keys[0] : keys);
		},
		async xAdd(key, fields) {
			return client.xAdd(key, "*", toNodeFields(fields));
		},
		async xRange(key, start, end) {
			return parseXRangeReply(await client.sendCommand([
				"XRANGE",
				key,
				start,
				end
			], { typeMapping: { [RESP_BLOB_STRING]: Buffer } }));
		},
		async pipeline(commands) {
			if (commands.length === 0) return;
			let chain = client.multi();
			for (const cmd of commands) chain = applyPipelineCommand(chain, cmd);
			await chain.execAsPipeline();
		}
	};
}
function applyPipelineCommand(chain, cmd) {
	switch (cmd.type) {
		case "xAdd": return chain.xAdd(cmd.key, "*", toNodeFields(cmd.fields));
		case "expire": return chain.expire(cmd.key, cmd.ttlSec);
		case "set": return chain.set(cmd.key, cmd.value, { EX: cmd.ttlSec });
	}
}
function toNodeFields(fields) {
	const out = {};
	for (const [k, v] of Object.entries(fields)) out[k] = typeof v === "string" ? v : toBuffer(v);
	return out;
}
function toBuffer(bytes) {
	if (Buffer.isBuffer(bytes)) return bytes;
	return Buffer.from(bytes.buffer, bytes.byteOffset, bytes.byteLength);
}
function parseXRangeReply(reply) {
	if (!Array.isArray(reply)) return [];
	const out = [];
	for (const entry of reply) {
		if (!Array.isArray(entry) || entry.length < 2) continue;
		const [rawId, rawFields] = entry;
		const id = bufferOrStringToString(rawId);
		if (id === void 0 || !Array.isArray(rawFields)) continue;
		const fields = {};
		for (let i = 0; i + 1 < rawFields.length; i += 2) {
			const fieldKey = bufferOrStringToString(rawFields[i]);
			const fieldValue = rawFields[i + 1];
			if (fieldKey === void 0 || fieldValue === void 0) continue;
			fields[fieldKey] = bufferOrStringToBytes(fieldValue);
		}
		out.push({
			id,
			fields
		});
	}
	return out;
}
function bufferOrStringToString(value) {
	if (typeof value === "string") return value;
	if (Buffer.isBuffer(value)) return value.toString("utf8");
}
function bufferOrStringToBytes(value) {
	if (typeof value === "string") return value;
	if (Buffer.isBuffer(value)) return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
	return "";
}
//#endregion
export { createRedisResumableStreamStore };

//# sourceMappingURL=redis.js.map