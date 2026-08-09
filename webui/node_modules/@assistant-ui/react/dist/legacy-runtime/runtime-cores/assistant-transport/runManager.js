import { useLatestRef } from "./useLatestRef.js";
import { useCallback, useRef, useState } from "@assistant-ui/tap/react-shim";
//#region src/legacy-runtime/runtime-cores/assistant-transport/runManager.ts
function useRunManager(config) {
	const [isRunning, setIsRunning] = useState(false);
	const stateRef = useRef({
		pending: false,
		abortController: null
	});
	const onRunRef = useLatestRef(config.onRun);
	const onFinishRef = useLatestRef(config.onFinish);
	const onCancelRef = useLatestRef(config.onCancel);
	const onErrorRef = useLatestRef(config.onError);
	const startRun = useCallback(() => {
		setIsRunning(true);
		stateRef.current.pending = false;
		const ac = new AbortController();
		stateRef.current.abortController = ac;
		queueMicrotask(async () => {
			try {
				await onRunRef.current(ac.signal);
			} catch (error) {
				stateRef.current.pending = false;
				if (ac.signal.aborted) onCancelRef.current?.();
				else await onErrorRef.current?.(error);
			} finally {
				onFinishRef.current?.();
				if (stateRef.current.pending) startRun();
				else {
					setIsRunning(false);
					stateRef.current.abortController = null;
				}
			}
		});
	}, [
		onRunRef,
		onFinishRef,
		onErrorRef,
		onCancelRef
	]);
	return {
		isRunning,
		schedule: useCallback(() => {
			if (stateRef.current.abortController) {
				stateRef.current.pending = true;
				return;
			}
			startRun();
		}, [startRun]),
		cancel: useCallback(() => {
			stateRef.current.pending = false;
			stateRef.current.abortController?.abort();
		}, [])
	};
}
//#endregion
export { useRunManager };

//# sourceMappingURL=runManager.js.map