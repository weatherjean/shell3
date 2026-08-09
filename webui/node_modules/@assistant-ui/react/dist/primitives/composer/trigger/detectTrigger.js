//#region src/primitives/composer/trigger/detectTrigger.ts
const WHITESPACE_RE = /\s/u;
/**
* Detect a trigger character in text relative to the cursor position.
*
* @internal Exported for testing and for trigger resources.
*/
function detectTrigger(text, triggerChar, cursorPosition) {
	const textUpToCursor = text.slice(0, cursorPosition);
	for (let i = textUpToCursor.length - 1; i >= 0; i--) {
		const char = textUpToCursor[i];
		if (WHITESPACE_RE.test(char)) return null;
		if (textUpToCursor.startsWith(triggerChar, i)) {
			if (i > 0 && !WHITESPACE_RE.test(textUpToCursor[i - 1])) continue;
			return {
				query: textUpToCursor.slice(i + triggerChar.length),
				offset: i
			};
		}
	}
	return null;
}
//#endregion
export { detectTrigger };

//# sourceMappingURL=detectTrigger.js.map