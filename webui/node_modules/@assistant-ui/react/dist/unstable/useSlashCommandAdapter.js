"use client";
import { useMemo, useRef } from "@assistant-ui/tap/react-shim";
//#region src/unstable/useSlashCommandAdapter.ts
/**
* @deprecated Under active development and may change without notice.
*
* Bundles slash command definitions (with inline `execute` callbacks) into
* `{adapter, action}` that plug directly into `ComposerTriggerPopover`.
* `execute` stays in the hook closure and is never attached to the returned
* `TriggerItem`, keeping items serializable.
*
* @example
* ```tsx
* const slash = unstable_useSlashCommandAdapter({
*   commands: [
*     { id: "summarize", execute: () => runSummarize(), icon: "FileText" },
*     { id: "translate", execute: () => runTranslate(), icon: "Languages" },
*   ],
* });
*
* <ComposerTriggerPopover char="/" {...slash} />
* ```
*/
function unstable_useSlashCommandAdapter(options) {
	const { commands, removeOnExecute } = options;
	const commandsRef = useRef(commands);
	commandsRef.current = commands;
	return useMemo(() => {
		return {
			adapter: {
				categories: () => [],
				categoryItems: () => [],
				search: (query) => {
					const lower = query.toLowerCase();
					return commandsRef.current.filter((c) => matchesQuery(c, lower)).map(toItem);
				}
			},
			action: {
				onExecute: (item) => {
					commandsRef.current.find((c) => c.id === item.id)?.execute();
				},
				...removeOnExecute !== void 0 ? { removeOnExecute } : {}
			},
			...options.iconMap ? { iconMap: options.iconMap } : {},
			...options.fallbackIcon ? { fallbackIcon: options.fallbackIcon } : {}
		};
	}, [
		removeOnExecute,
		options.iconMap,
		options.fallbackIcon
	]);
}
function toItem(cmd) {
	return {
		id: cmd.id,
		type: "command",
		label: cmd.label ?? `/${cmd.id}`,
		...cmd.description !== void 0 ? { description: cmd.description } : {},
		...cmd.icon !== void 0 ? { metadata: { icon: cmd.icon } } : {}
	};
}
function matchesQuery(cmd, lower) {
	if (!lower) return true;
	if (cmd.id.toLowerCase().includes(lower)) return true;
	if (cmd.label?.toLowerCase().includes(lower)) return true;
	if (cmd.description?.toLowerCase().includes(lower)) return true;
	return false;
}
//#endregion
export { unstable_useSlashCommandAdapter };

//# sourceMappingURL=useSlashCommandAdapter.js.map