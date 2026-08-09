import { vi } from "vitest";
//#region src/tests/remote-thread-list-test-helpers.ts
function makeAdapter(overrides = {}) {
	return {
		list: vi.fn(async () => ({ threads: [] })),
		initialize: vi.fn(async (threadId) => ({
			remoteId: threadId,
			externalId: threadId
		})),
		rename: vi.fn(async () => {}),
		archive: vi.fn(async () => {}),
		unarchive: vi.fn(async () => {}),
		delete: vi.fn(async () => {}),
		generateTitle: vi.fn(async () => new ReadableStream({ start(c) {
			c.close();
		} })),
		fetch: vi.fn(async (id) => ({
			status: "regular",
			remoteId: id,
			externalId: id,
			title: "Test"
		})),
		...overrides
	};
}
//#endregion
export { makeAdapter };

//# sourceMappingURL=remote-thread-list-test-helpers.js.map