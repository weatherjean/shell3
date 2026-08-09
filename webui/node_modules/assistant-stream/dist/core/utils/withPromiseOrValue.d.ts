//#region src/core/utils/withPromiseOrValue.d.ts
declare function withPromiseOrValue<T>(callback: () => T | PromiseLike<T>, thenHandler: (value: T) => PromiseLike<void> | void, catchHandler: (error: unknown) => PromiseLike<void> | void): PromiseLike<void> | void;
//#endregion
export { withPromiseOrValue };
//# sourceMappingURL=withPromiseOrValue.d.ts.map