//#region src/core/utils/stream/UnderlyingReadable.d.ts
type UnderlyingReadable<TController> = {
  start?: (controller: TController) => void;
  pull?: (controller: TController) => void | PromiseLike<void>;
  cancel?: UnderlyingSourceCancelCallback;
};
//#endregion
export { UnderlyingReadable };
//# sourceMappingURL=UnderlyingReadable.d.ts.map