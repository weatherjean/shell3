# Third-party attributions

## opencode — edit tool replacer cascade

`internal/edittool` is a Go port of the str-replace edit algorithm from
[opencode](https://github.com/sst/opencode), specifically:

- `packages/opencode/src/tool/edit.ts`

That file (and therefore this port) cites:

- [cline diff-apply](https://github.com/cline/cline) — line-trimmed and block-anchor replacer ideas
- [gemini-cli editCorrector](https://github.com/google-gemini/gemini-cli) — fuzzy match strategies

The 9 replacers (`SimpleReplacer`, `LineTrimmedReplacer`, `BlockAnchorReplacer`,
`WhitespaceNormalizedReplacer`, `IndentationFlexibleReplacer`,
`EscapeNormalizedReplacer`, `TrimmedBoundaryReplacer`, `ContextAwareReplacer`,
`MultiOccurrenceReplacer`) and the levenshtein helper are direct ports;
similarity thresholds and ordering match opencode.
