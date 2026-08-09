"use client";
import { useTriggerBehaviorRegistration } from "./TriggerPopover.js";
import { unstable_defaultDirectiveFormatter } from "@assistant-ui/core";
import { useEffect, useRef } from "@assistant-ui/tap/react-shim";
//#region src/primitives/composer/trigger/TriggerPopoverDirective.tsx
/**
* Configures a `<TriggerPopover>` to insert a directive chip when an item is
* selected. Render exactly one behavior sub-primitive per `<TriggerPopover>`.
*
* Exposed as `ComposerPrimitive.Unstable_TriggerPopover.Directive`.
*
* @example
* ```tsx
* <ComposerPrimitive.Unstable_TriggerPopover char="@" adapter={mentionAdapter}>
*   <ComposerPrimitive.Unstable_TriggerPopover.Directive
*     formatter={unstable_defaultDirectiveFormatter}
*     onInserted={(item) => track("mention", item.id)}
*   />
* </ComposerPrimitive.Unstable_TriggerPopover>
* ```
*/
const ComposerPrimitiveTriggerPopoverDirective = ({ formatter, onInserted }) => {
	const { register } = useTriggerBehaviorRegistration();
	const onInsertedRef = useRef(onInserted);
	onInsertedRef.current = onInserted;
	useEffect(() => {
		return register({
			kind: "directive",
			formatter: formatter ?? unstable_defaultDirectiveFormatter,
			onInserted: (item) => onInsertedRef.current?.(item)
		});
	}, [register, formatter]);
	return null;
};
ComposerPrimitiveTriggerPopoverDirective.displayName = "ComposerPrimitive.TriggerPopoverDirective";
//#endregion
export { ComposerPrimitiveTriggerPopoverDirective };

//# sourceMappingURL=TriggerPopoverDirective.js.map