//#region src/utils/AsyncIterableStream.d.ts
type AsyncIterableStream<T> = AsyncIterable<T> & ReadableStream<T>;
declare function asAsyncIterableStream<T>(source: ReadableStream<T>): AsyncIterableStream<T>;
//#endregion
export { AsyncIterableStream, asAsyncIterableStream };
//# sourceMappingURL=AsyncIterableStream.d.ts.map