//#region src/primitives/thread/topAnchor/createReserveObservers.d.ts
declare const createReserveObservers: (onChange: () => void) => {
  target: (viewport: HTMLElement, anchor: HTMLElement, target: HTMLElement) => void;
  disconnect: () => void;
};
//#endregion
export { createReserveObservers };
//# sourceMappingURL=createReserveObservers.d.ts.map