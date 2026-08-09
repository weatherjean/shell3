import { memoCompareNodes } from "../memoization.js";
import { PreContext, useIsMarkdownCodeBlock } from "./PreOverride.js";
import { DefaultCodeBlockContent } from "./defaultComponents.js";
import { DefaultCodeBlock } from "./CodeBlock.js";
import { withDefaultProps } from "./withDefaults.js";
import { memo, useContext } from "react";
import { jsx } from "react/jsx-runtime";
import { useCallbackRef } from "@radix-ui/react-use-callback-ref";
//#region src/overrides/CodeOverride.tsx
const CodeBlockOverride = ({ node, components: { Pre, Code, SyntaxHighlighter: FallbackSyntaxHighlighter, CodeHeader: FallbackCodeHeader }, componentsByLanguage = {}, children, ...codeProps }) => {
	const getPreProps = withDefaultProps(useContext(PreContext));
	const WrappedPre = useCallbackRef((props) => /* @__PURE__ */ jsx(Pre, { ...getPreProps(props) }));
	const getCodeProps = withDefaultProps(codeProps);
	const WrappedCode = useCallbackRef((props) => /* @__PURE__ */ jsx(Code, { ...getCodeProps(props) }));
	const language = /language-(\w+)/.exec(codeProps.className || "")?.[1] ?? "";
	if (typeof children !== "string") return /* @__PURE__ */ jsx(DefaultCodeBlockContent, {
		node,
		components: {
			Pre: WrappedPre,
			Code: WrappedCode
		},
		code: children
	});
	return /* @__PURE__ */ jsx(DefaultCodeBlock, {
		node,
		components: {
			Pre: WrappedPre,
			Code: WrappedCode,
			SyntaxHighlighter: componentsByLanguage[language]?.SyntaxHighlighter ?? FallbackSyntaxHighlighter,
			CodeHeader: componentsByLanguage[language]?.CodeHeader ?? FallbackCodeHeader
		},
		language: language || "unknown",
		code: children
	});
};
const CodeOverrideImpl = ({ node, components, componentsByLanguage, ...props }) => {
	if (!useIsMarkdownCodeBlock()) return /* @__PURE__ */ jsx(components.Code, { ...props });
	return /* @__PURE__ */ jsx(CodeBlockOverride, {
		node,
		components,
		componentsByLanguage,
		...props
	});
};
const CodeOverride = memo(CodeOverrideImpl, (prev, next) => {
	return prev.components === next.components && prev.componentsByLanguage === next.componentsByLanguage && memoCompareNodes(prev, next);
});
//#endregion
export { CodeOverride };

//# sourceMappingURL=CodeOverride.js.map