"use client";
import { createReserveElement, getAnchorId, setReserveHeight, snapScrollTop } from "./topAnchorUtils.js";
import { computeTopAnchorReserve, computeTopAnchorTargetScrollTop } from "./computeTopAnchorSlack.js";
import { createReserveObservers } from "./createReserveObservers.js";
//#region src/primitives/thread/topAnchor/mountTopAnchorReserve.ts
const createFrameScheduler = (fn) => {
	let frame = null;
	return {
		schedule: () => {
			if (frame !== null) return;
			frame = requestAnimationFrame(() => {
				frame = null;
				fn();
			});
		},
		cancel: () => {
			if (frame !== null) {
				cancelAnimationFrame(frame);
				frame = null;
			}
		}
	};
};
const mountTopAnchorReserve = (store) => {
	let reserve = null;
	let lastScrolledAnchorId;
	function apply() {
		const state = store.getState();
		const { viewport, anchor, target } = state.element;
		const clamp = state.targetConfig;
		if (state.turnAnchor !== "top" || !viewport || !anchor || !target || !clamp) {
			observers.disconnect();
			if (reserve) {
				setReserveHeight(reserve, 0);
				reserve.remove();
			}
			return;
		}
		reserve ??= createReserveElement();
		if (reserve.parentElement !== target.parentElement || reserve.previousElementSibling !== target) target.after(reserve);
		observers.target(viewport, anchor, target);
		if (setReserveHeight(reserve, computeTopAnchorReserve({
			viewport,
			anchor,
			reserve,
			...clamp
		}))) {
			scheduler.schedule();
			return;
		}
		const anchorId = getAnchorId(anchor);
		if (anchorId !== void 0 && lastScrolledAnchorId === anchorId) return;
		const targetScrollTop = snapScrollTop(computeTopAnchorTargetScrollTop({
			viewport,
			anchor,
			...clamp
		}));
		if (Math.abs(viewport.scrollTop - targetScrollTop) > 1) viewport.scrollTo({
			top: targetScrollTop,
			behavior: "smooth"
		});
		if (anchorId !== void 0) lastScrolledAnchorId = anchorId;
	}
	const scheduler = createFrameScheduler(apply);
	const observers = createReserveObservers(scheduler.schedule);
	scheduler.schedule();
	const unsubscribe = store.subscribe(scheduler.schedule);
	return () => {
		scheduler.cancel();
		unsubscribe();
		observers.disconnect();
		reserve?.remove();
	};
};
//#endregion
export { mountTopAnchorReserve };

//# sourceMappingURL=mountTopAnchorReserve.js.map