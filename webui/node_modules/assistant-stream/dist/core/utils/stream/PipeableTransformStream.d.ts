//#region src/core/utils/stream/PipeableTransformStream.d.ts
declare class PipeableTransformStream<I, O> extends TransformStream<I, O> {
  constructor(transform: (readable: ReadableStream<I>) => ReadableStream<O>);
}
//#endregion
export { PipeableTransformStream };
//# sourceMappingURL=PipeableTransformStream.d.ts.map