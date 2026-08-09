//#region src/primitives/thread/topAnchor/topAnchorUtils.d.ts
/**
 * Convert a supported CSS length string (`px`, `em`, `rem`) into pixels,
 * resolving font-relative units against the supplied element's computed style.
 * Unsupported or malformed values disable the tall-message clamp.
 *
 * Part of the top-anchor package's public input contract: consumers may pass
 * clamp configuration as supported CSS-length strings, and this function is the
 * single place that converts them into the pixel values the package operates on.
 */
declare const parseCssLength: (value: string, element: HTMLElement) => number;
declare const getAnchorId: (anchor: HTMLElement) => string | undefined;
declare const createReserveElement: () => HTMLDivElement;
declare const setReserveHeight: (reserve: HTMLElement, height: number) => boolean;
declare const snapScrollTop: (top: number) => number;
//#endregion
export { createReserveElement, getAnchorId, parseCssLength, setReserveHeight, snapScrollTop };
//# sourceMappingURL=topAnchorUtils.d.ts.map