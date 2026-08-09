"use client";
import { useAui } from "@assistant-ui/store";
import { unstable_defaultDirectiveFormatter } from "@assistant-ui/core";
import { useMemo } from "@assistant-ui/tap/react-shim";
//#region src/unstable/useMentionAdapter.ts
/**
* @deprecated Under active development and might change without notice.
*
* Creates a spreadable `{ adapter, directive }` bundle for `@` mentions.
* Supports tools registered in model context, explicit items, or both —
* flat or categorized.
*
* @example
* ```tsx
* const mention = unstable_useMentionAdapter();
* <ComposerTriggerPopover char="@" {...mention} />
* ```
*/
function unstable_useMentionAdapter(options) {
	const aui = useAui();
	const items = options?.items;
	const categories = options?.categories;
	const includeTools = options?.includeModelContextTools ?? (!items && !categories);
	const toolsConfig = typeof includeTools === "object" ? includeTools : void 0;
	const wantsTools = includeTools !== false;
	const formatter = options?.formatter;
	const onInserted = options?.onInserted;
	return {
		adapter: useMemo(() => {
			const getModelContextTools = () => {
				if (!wantsTools) return [];
				const tools = aui.thread().getModelContext().tools;
				if (!tools) return [];
				const formatLabel = toolsConfig?.formatLabel;
				const defaultIcon = toolsConfig?.icon;
				return Object.entries(tools).map(([name, tool]) => toTriggerItem({
					id: name,
					type: "tool",
					label: formatLabel ? formatLabel(name) : name,
					description: tool.description ?? void 0,
					icon: defaultIcon
				}));
			};
			if (categories && categories.length > 0) {
				const groups = categories.map((cat) => ({
					id: cat.id,
					label: cat.label,
					items: cat.items.map(toTriggerItem)
				}));
				let toolCategory = null;
				if (wantsTools) {
					const toolItems = getModelContextTools();
					if (toolItems.length > 0) toolCategory = {
						id: toolsConfig?.category?.id ?? "tools",
						label: toolsConfig?.category?.label ?? "Tools",
						items: toolItems
					};
				}
				const allGroups = toolCategory ? [...groups, toolCategory] : groups;
				return {
					categories: () => allGroups.map(({ id, label }) => ({
						id,
						label
					})),
					categoryItems: (id) => allGroups.find((g) => g.id === id)?.items ?? [],
					search: (query) => {
						const lower = query.toLowerCase();
						return allGroups.flatMap((g) => g.items).filter((item) => matchesQuery(item, lower));
					}
				};
			}
			const flatItems = (items ?? []).map(toTriggerItem);
			const getFlatPool = () => {
				if (!wantsTools) return flatItems;
				const toolItems = getModelContextTools();
				const seen = new Set(flatItems.map((i) => i.id));
				return [...flatItems, ...toolItems.filter((t) => !seen.has(t.id))];
			};
			return {
				categories: () => [],
				categoryItems: () => [],
				search: (query) => {
					const lower = query.toLowerCase();
					return getFlatPool().filter((item) => matchesQuery(item, lower));
				}
			};
		}, [
			aui,
			items,
			categories,
			wantsTools,
			toolsConfig
		]),
		directive: useMemo(() => ({
			formatter: formatter ?? unstable_defaultDirectiveFormatter,
			...onInserted ? { onInserted } : {}
		}), [formatter, onInserted]),
		...options?.iconMap ? { iconMap: options.iconMap } : {},
		...options?.fallbackIcon ? { fallbackIcon: options.fallbackIcon } : {}
	};
}
function toTriggerItem(m) {
	const metadata = m.icon !== void 0 ? {
		...m.metadata ?? {},
		icon: m.icon
	} : m.metadata;
	return {
		id: m.id,
		type: m.type,
		label: m.label,
		...m.description !== void 0 ? { description: m.description } : {},
		...metadata !== void 0 ? { metadata } : {}
	};
}
function matchesQuery(item, lower) {
	if (!lower) return true;
	if (item.id.toLowerCase().includes(lower)) return true;
	if (item.label.toLowerCase().includes(lower)) return true;
	if (item.description?.toLowerCase().includes(lower)) return true;
	return false;
}
//#endregion
export { unstable_useMentionAdapter };

//# sourceMappingURL=useMentionAdapter.js.map