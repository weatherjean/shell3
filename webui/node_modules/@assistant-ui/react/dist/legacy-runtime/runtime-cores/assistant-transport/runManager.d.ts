//#region src/legacy-runtime/runtime-cores/assistant-transport/runManager.d.ts
type RunManager = Readonly<{
  isRunning: boolean;
  schedule: () => void;
  cancel: () => void;
}>;
declare function useRunManager(config: {
  onRun: (signal: AbortSignal) => Promise<void>;
  onFinish?: (() => void) | undefined;
  onCancel?: (() => void) | undefined;
  onError?: ((error: Error) => void | Promise<void>) | undefined;
}): RunManager;
//#endregion
export { RunManager, useRunManager };
//# sourceMappingURL=runManager.d.ts.map