"use client";
import { memoCompareNodes } from "../memoization.js";
import { createContext, memo, useContext } from "react";
import { jsx } from "react/jsx-runtime";
//#region src/overrides/PreOverride.tsx
const PreContext = createContext(null);
const useIsMarkdownCodeBlock = () => {
	return useContext(PreContext) !== null;
};
const PreOverrideImpl = ({ children, ...rest }) => {
	return /* @__PURE__ */ jsx(PreContext.Provider, {
		value: rest,
		children
	});
};
const PreOverride = memo(PreOverrideImpl, memoCompareNodes);
//#endregion
export { PreContext, PreOverride, useIsMarkdownCodeBlock };

//# sourceMappingURL=PreOverride.js.map