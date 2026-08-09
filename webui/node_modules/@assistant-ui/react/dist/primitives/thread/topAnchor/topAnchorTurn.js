"use client";
//#region src/primitives/thread/topAnchor/topAnchorTurn.ts
const getActiveTopAnchorTurn = ({ isRunning, messages }) => {
	if (!isRunning) return null;
	const target = messages.at(-1);
	const anchor = messages.at(-2);
	if (anchor?.role !== "user" || target?.role !== "assistant") return null;
	return {
		anchorId: anchor.id,
		targetId: target.id
	};
};
const getActiveTopAnchorAnchorId = (options) => getActiveTopAnchorTurn(options)?.anchorId;
const getActiveTopAnchorTargetId = (options) => getActiveTopAnchorTurn(options)?.targetId;
//#endregion
export { getActiveTopAnchorAnchorId, getActiveTopAnchorTargetId, getActiveTopAnchorTurn };

//# sourceMappingURL=topAnchorTurn.js.map