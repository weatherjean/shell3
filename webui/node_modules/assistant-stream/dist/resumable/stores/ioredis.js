import { RedisResumableStreamStore } from "./redis-impl.js";
//#region src/resumable/stores/ioredis.ts
/**
* Resumable stream store backed by [`ioredis`](https://www.npmjs.com/package/ioredis)
* v5. Accepts a `Redis` or `Cluster` instance.
*/
function createIoredisResumableStreamStore(client, options) {
	return new RedisResumableStreamStore(adapt(client), options);
}
function adapt(client) {
	return {
		async setNX(key, value, ttlSec) {
			return await client.set(key, value, "EX", ttlSec, "NX") === "OK";
		},
		async set(key, value, ttlSec) {
			await client.set(key, value, "EX", ttlSec);
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
			await client.del(...keys);
		},
		async xAdd(key, fields) {
			return await client.xadd(key, "*", ...toFieldArgs(fields)) ?? "";
		},
		async xRange(key, start, end) {
			return (await client.xrangeBuffer(key, start, end)).map(([idBuf, fieldArray]) => ({
				id: idBuf.toString("utf8"),
				fields: bufferFieldsToRecord(fieldArray)
			}));
		},
		async pipeline(commands) {
			if (commands.length === 0) return;
			const pipe = client.pipeline();
			for (const cmd of commands) applyPipelineCommand(pipe, cmd);
			const results = await pipe.exec() ?? [];
			for (const [err] of results) if (err) throw err;
		}
	};
}
function applyPipelineCommand(pipe, cmd) {
	switch (cmd.type) {
		case "xAdd":
			pipe.xadd(cmd.key, "*", ...toFieldArgs(cmd.fields));
			return;
		case "expire":
			pipe.expire(cmd.key, cmd.ttlSec);
			return;
		case "set":
			pipe.set(cmd.key, cmd.value, "EX", cmd.ttlSec);
			return;
	}
}
function toFieldArgs(fields) {
	const args = [];
	for (const [k, v] of Object.entries(fields)) args.push(k, typeof v === "string" ? v : toBuffer(v));
	return args;
}
function toBuffer(bytes) {
	if (Buffer.isBuffer(bytes)) return bytes;
	return Buffer.from(bytes.buffer, bytes.byteOffset, bytes.byteLength);
}
function bufferFieldsToRecord(fields) {
	const out = {};
	for (let i = 0; i + 1 < fields.length; i += 2) {
		const key = fields[i]?.toString("utf8");
		const value = fields[i + 1];
		if (key !== void 0 && value !== void 0) out[key] = new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
	}
	return out;
}
//#endregion
export { createIoredisResumableStreamStore };

//# sourceMappingURL=ioredis.js.map