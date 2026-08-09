//#region src/primitives/thread/topAnchor/mountTopAnchorReserve.d.ts
/**
 * Minimal slice of `ThreadViewportStore` that the top-anchor reserve needs.
 * Decoupling from the full store keeps `mountTopAnchorReserve` testable in
 * isolation and re-usable from any consumer that can adapt to this shape.
 */
type TopAnchorStore = {
  getState(): {
    turnAnchor: "top" | "bottom";
    element: {
      viewport: HTMLElement | null;
      anchor: HTMLElement | null;
      target: HTMLElement | null;
    };
    targetConfig: {
      tallerThan: number;
      visibleHeight: number;
    } | null;
  };
  subscribe(fn: () => void): () => void;
};
declare const mountTopAnchorReserve: (store: TopAnchorStore) => () => void;
//#endregion
export { TopAnchorStore, mountTopAnchorReserve };
//# sourceMappingURL=mountTopAnchorReserve.d.ts.map