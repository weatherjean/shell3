//#region src/utils/hooks/useManagedRef.d.ts
declare const useManagedRef: <TNode>(callback: (node: TNode) => (() => void) | undefined) => (el: TNode | null) => void;
//#endregion
export { useManagedRef };
//# sourceMappingURL=useManagedRef.d.ts.map