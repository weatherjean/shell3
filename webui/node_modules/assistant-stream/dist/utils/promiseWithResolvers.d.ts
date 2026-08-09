//#region src/utils/promiseWithResolvers.d.ts
declare const promiseWithResolvers: <T>() => {
  promise: Promise<T>;
  resolve: (value: T | PromiseLike<T>) => void;
  reject: (reason?: unknown) => void;
};
//#endregion
export { promiseWithResolvers };
//# sourceMappingURL=promiseWithResolvers.d.ts.map