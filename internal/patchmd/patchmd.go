// Package mdrender is a small ANSI markdown renderer optimised for streaming
// LLM output. It supports the markdown subset that LLMs actually produce:
// headers (# ## ###), **bold**, `inline code`, [links](url), `- *` lists,
// `> blockquotes`, and ``` code fences. Italics, tables, and HTML are
// passed through unchanged.
//
// Render is pure: pass it the full accumulated markdown text and it returns
// rendered lines. There is no streaming state, so callers can re-render
// after each chunk without inconsistency.
package patchmd

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiItalic = "\033[3m"
	ansiDim    = "\033[2m"
)

func fgRGB(r, g, b int) string { return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b) }

// Theme colors. Tweak here to restyle the renderer.
var (
	colYellow = fgRGB(234, 179, 8)
	colCyan   = fgRGB(6, 182, 212)
	colGreen  = fgRGB(34, 197, 94)
	colMag    = fgRGB(217, 70, 239)
	colDim    = fgRGB(156, 163, 175)
	codeFg    = fgRGB(229, 231, 235)
)

var (
	boldItalicRe = regexp.MustCompile(`\*\*\*([^*\n]+?)\*\*\*`)
	boldRe       = regexp.MustCompile(`\*\*([^*\n]+?)\*\*`)
	italicRe     = regexp.MustCompile(`\*([^*\n]+?)\*`)
	strikeRe     = regexp.MustCompile(`~~([^~\n]+?)~~`)
	codeRe       = regexp.MustCompile("`([^`\n]+?)`")
	linkRe       = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\n]+)\)`)
	listRe       = regexp.MustCompile(`^(\s*)([-*]|\d+\.)\s+(.*)$`)
	codeTokenRe  = regexp.MustCompile("\\d+") //nolint:staticcheck
)

// Render converts markdown text to a slice of ANSI-styled lines.
// width is used only to enforce a minimum sanity bound; line wrapping
// is the caller's responsibility (e.g. via the TUI's wrapToWidth).
func Render(text string, width int) []string {
	if width < 10 {
		width = 80
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))

	inCode := false
	codeLang := ""
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")

		// Fenced code block toggle.
		if strings.HasPrefix(trimmed, "```") {
			if !inCode {
				codeLang = strings.TrimSpace(trimmed[3:])
				inCode = true
			} else {
				inCode = false
				codeLang = ""
			}
			out = append(out, colDim+strings.Repeat("─", min(40, width-2))+ansiReset)
			continue
		}

		if inCode {
			out = append(out, "  "+highlightCode(strings.ReplaceAll(line, "\t", "    "), codeLang))
			continue
		}

		// Headers (# ## ###).
		if h := headerLevel(trimmed); h > 0 {
			content := trimmed[h+1:] // skip the # chars and trailing space
			style := ansiBold + colYellow
			if h >= 3 {
				style = ansiBold + colDim
			}
			out = append(out, style+strings.Repeat("#", h)+" "+applyInline(content)+ansiReset)
			continue
		}

		// Blockquote.
		if strings.HasPrefix(trimmed, "> ") {
			indent := line[:len(line)-len(trimmed)]
			content := trimmed[2:]
			out = append(out, indent+colDim+"│ "+ansiReset+applyInline(content))
			continue
		}

		// List item (- *  or  1.).
		if m := listRe.FindStringSubmatch(line); m != nil {
			indent := m[1]
			marker := m[2]
			content := m[3]
			bullet := "•"
			if marker != "-" && marker != "*" {
				// Numbered: keep the original marker.
				bullet = marker
			}
			out = append(out, indent+colYellow+bullet+ansiReset+" "+applyInline(content))
			continue
		}

		// Plain paragraph (or empty line).
		out = append(out, applyInline(line))
	}

	return out
}

// codeToken is a placeholder character (U+E000, BMP private use area) used
// to stash inline-code spans during inline rendering. Without this, the
// ANSI escape that styles a code span (e.g. "\033[38;2;...m") contains a
// literal '[' that linkRe treats as the start of "[label](url)", which
// causes it to swallow the escape, the code span, and any link that
// follows. The byte is unlikely to appear in user input; if it does, it
// is treated as ordinary text and escapes the inline renderer unchanged.
const codeToken = ''

// applyInline applies inline formatting to a single line.
//
// Code spans are extracted to placeholder tokens FIRST, then inline
// formatters run over the placeholder-only text, then code spans are
// re-inserted with their styling. This isolation prevents the '['/'\033['
// collision between regex-based link parsing and ANSI-escaped code spans.
func applyInline(s string) string {
	// Stash code-span contents as placeholders so subsequent regexes
	// don't see escape sequences containing '['. We keep order via a
	// counter; the placeholder is "<token><index><token>".
	var stash []string
	s = codeRe.ReplaceAllStringFunc(s, func(m string) string {
		idx := len(stash)
		stash = append(stash, m[1:len(m)-1]) // inner, no backticks
		return fmt.Sprintf("%c%d%c", codeToken, idx, codeToken)
	})

	// Bold italic ***text***.
	s = boldItalicRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[3 : len(m)-3]
		return ansiBold + ansiItalic + inner + ansiReset
	})
	// Bold **text**.
	s = boldRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-2]
		return ansiBold + inner + ansiReset
	})
	// Italic *text*.
	s = italicRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[1 : len(m)-1]
		return ansiItalic + inner + ansiReset
	})
	// Strikethrough ~~text~~.
	s = strikeRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-2]
		return "\033[9m" + inner + ansiReset
	})
	// Links — render as cyan underlined text, drop URL.
	s = linkRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := linkRe.FindStringSubmatch(m)
		return "\033[4m" + colCyan + sub[1] + ansiReset
	})

	// Expand code-span placeholders.
	if len(stash) > 0 {
		s = codeTokenRe.ReplaceAllStringFunc(s, func(m string) string {
			// Strip the surrounding tokens to get the index digits.
			idx := 0
			for _, r := range m {
				if r == codeToken {
					continue
				}
				idx = idx*10 + int(r-'0')
			}
			if idx < 0 || idx >= len(stash) {
				return m
			}
			return colYellow + "`" + stash[idx] + "`" + ansiReset
		})
	}
	return s
}

// headerLevel returns the header level (1-6) if line starts with "# " etc,
// or 0 if it isn't a header.
func headerLevel(line string) int {
	n := 0
	for n < 6 && n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n >= len(line) || line[n] != ' ' {
		return 0
	}
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── code highlighting ────────────────────────────────────────────────────────

// langSpec describes a language for the simple highlighter:
// keyword set, line-comment marker, and acceptable string delimiters.
type langSpec struct {
	keywords    map[string]bool
	lineComment string // e.g. "//", "#", "--"
	stringChars string // chars that delimit strings (any in this set)
}

// langs is keyed by lower-cased fence language tag.
var langs = map[string]langSpec{
	"go": {
		keywords:    setOf("break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select", "struct", "switch", "type", "var", "true", "false", "nil"),
		lineComment: "//",
		stringChars: "\"`",
	},
	"py": {
		keywords:    setOf("def", "class", "if", "elif", "else", "for", "while", "return", "import", "from", "as", "try", "except", "finally", "raise", "with", "lambda", "yield", "pass", "break", "continue", "global", "nonlocal", "in", "is", "not", "and", "or", "True", "False", "None", "self"),
		lineComment: "#",
		stringChars: "\"'",
	},
	"python": {
		keywords:    setOf("def", "class", "if", "elif", "else", "for", "while", "return", "import", "from", "as", "try", "except", "finally", "raise", "with", "lambda", "yield", "pass", "break", "continue", "global", "nonlocal", "in", "is", "not", "and", "or", "True", "False", "None", "self"),
		lineComment: "#",
		stringChars: "\"'",
	},
	"js": jsLang(),
	"ts": jsLang(),
	"javascript": jsLang(),
	"typescript": jsLang(),
	"rust": {
		keywords:    setOf("fn", "let", "mut", "const", "if", "else", "for", "while", "loop", "match", "return", "use", "mod", "pub", "struct", "enum", "trait", "impl", "self", "Self", "type", "where", "async", "await", "move", "true", "false", "Some", "None", "Ok", "Err", "as", "in", "ref"),
		lineComment: "//",
		stringChars: "\"",
	},
	"sh":   shLang(),
	"bash": shLang(),
	"zsh":  shLang(),
	"json": {
		keywords:    setOf("true", "false", "null"),
		lineComment: "",
		stringChars: "\"",
	},
	"java": {
		keywords:    setOf("abstract", "assert", "boolean", "break", "byte", "case", "catch", "char", "class", "const", "continue", "default", "do", "double", "else", "enum", "extends", "final", "finally", "float", "for", "goto", "if", "implements", "import", "instanceof", "int", "interface", "long", "native", "new", "null", "package", "private", "protected", "public", "return", "short", "static", "strictfp", "super", "switch", "synchronized", "this", "throw", "throws", "transient", "try", "void", "volatile", "while", "true", "false", "var", "record", "sealed", "yield"),
		lineComment: "//",
		stringChars: "\"",
	},
	"c": cLang(),
	"cpp": cLang(),
	"c++": cLang(),
	"h": cLang(),
	"hpp": cLang(),
	"kt": kotlinLang(),
	"kotlin": kotlinLang(),
	"swift": {
		keywords:    setOf("class", "struct", "enum", "protocol", "extension", "func", "var", "let", "if", "else", "for", "while", "return", "import", "guard", "switch", "case", "default", "break", "continue", "throw", "try", "catch", "do", "as", "is", "in", "where", "self", "Self", "true", "false", "nil", "private", "public", "internal", "fileprivate", "open", "static", "final", "override", "init", "deinit"),
		lineComment: "//",
		stringChars: "\"",
	},
	"rb": rubyLang(),
	"ruby": rubyLang(),
	"php": {
		keywords:    setOf("abstract", "and", "array", "as", "break", "case", "catch", "class", "clone", "const", "continue", "declare", "default", "do", "echo", "else", "elseif", "extends", "final", "finally", "for", "foreach", "function", "global", "if", "implements", "instanceof", "interface", "namespace", "new", "or", "private", "protected", "public", "return", "static", "switch", "throw", "trait", "try", "use", "var", "while", "xor", "yield", "true", "false", "null"),
		lineComment: "//",
		stringChars: "\"'",
	},
	"sql": {
		keywords:    setOf("SELECT", "FROM", "WHERE", "INSERT", "INTO", "VALUES", "UPDATE", "SET", "DELETE", "CREATE", "TABLE", "INDEX", "VIEW", "DROP", "ALTER", "ADD", "JOIN", "LEFT", "RIGHT", "INNER", "OUTER", "ON", "GROUP", "BY", "ORDER", "HAVING", "LIMIT", "OFFSET", "AS", "AND", "OR", "NOT", "IN", "BETWEEN", "LIKE", "IS", "NULL", "PRIMARY", "KEY", "FOREIGN", "REFERENCES", "UNIQUE", "DEFAULT", "WITH", "UNION", "ALL", "DISTINCT", "CASE", "WHEN", "THEN", "ELSE", "END", "select", "from", "where", "insert", "into", "values", "update", "set", "delete", "create", "table", "index", "view", "drop", "alter", "add", "join", "left", "right", "inner", "outer", "on", "group", "by", "order", "having", "limit", "offset", "as", "and", "or", "not", "in", "between", "like", "is", "null", "primary", "key", "foreign", "references", "unique", "default", "with", "union", "all", "distinct", "case", "when", "then", "else", "end"),
		lineComment: "--",
		stringChars: "'\"",
	},
	"yaml": {
		keywords:    setOf("true", "false", "null", "yes", "no", "on", "off"),
		lineComment: "#",
		stringChars: "\"'",
	},
	"yml": {
		keywords:    setOf("true", "false", "null", "yes", "no", "on", "off"),
		lineComment: "#",
		stringChars: "\"'",
	},
	"toml": {
		keywords:    setOf("true", "false"),
		lineComment: "#",
		stringChars: "\"'",
	},
	"css": {
		keywords:    setOf("important", "auto", "none", "inherit", "initial", "unset"),
		lineComment: "",
		stringChars: "\"'",
	},
	"html": {
		keywords:    setOf(),
		lineComment: "",
		stringChars: "\"'",
	},
	"xml": {
		keywords:    setOf(),
		lineComment: "",
		stringChars: "\"'",
	},
	"lua": {
		keywords:    setOf("and", "break", "do", "else", "elseif", "end", "false", "for", "function", "goto", "if", "in", "local", "nil", "not", "or", "repeat", "return", "then", "true", "until", "while"),
		lineComment: "--",
		stringChars: "\"'",
	},
}

func jsLang() langSpec {
	return langSpec{
		keywords:    setOf("var", "let", "const", "function", "return", "if", "else", "for", "while", "do", "switch", "case", "break", "continue", "class", "extends", "new", "this", "super", "import", "export", "default", "from", "as", "async", "await", "try", "catch", "finally", "throw", "typeof", "instanceof", "in", "of", "true", "false", "null", "undefined"),
		lineComment: "//",
		stringChars: "\"'`",
	}
}

func shLang() langSpec {
	return langSpec{
		keywords:    setOf("if", "then", "else", "elif", "fi", "for", "while", "do", "done", "case", "esac", "in", "function", "return", "break", "continue", "exit", "local", "export", "echo", "read", "set", "unset", "true", "false"),
		lineComment: "#",
		stringChars: "\"'",
	}
}

func cLang() langSpec {
	return langSpec{
		keywords:    setOf("auto", "break", "case", "char", "const", "continue", "default", "do", "double", "else", "enum", "extern", "float", "for", "goto", "if", "inline", "int", "long", "register", "return", "short", "signed", "sizeof", "static", "struct", "switch", "typedef", "union", "unsigned", "void", "volatile", "while", "class", "namespace", "template", "typename", "public", "private", "protected", "virtual", "override", "new", "delete", "this", "nullptr", "true", "false", "bool", "auto"),
		lineComment: "//",
		stringChars: "\"'",
	}
}

func kotlinLang() langSpec {
	return langSpec{
		keywords:    setOf("abstract", "as", "break", "by", "catch", "class", "companion", "const", "constructor", "continue", "crossinline", "data", "do", "dynamic", "else", "enum", "external", "false", "final", "finally", "for", "fun", "if", "import", "in", "infix", "init", "inline", "inner", "interface", "internal", "is", "it", "lateinit", "noinline", "null", "object", "open", "operator", "out", "override", "package", "private", "protected", "public", "reified", "return", "sealed", "suspend", "tailrec", "this", "throw", "true", "try", "typealias", "val", "var", "vararg", "when", "where", "while"),
		lineComment: "//",
		stringChars: "\"",
	}
}

func rubyLang() langSpec {
	return langSpec{
		keywords:    setOf("alias", "and", "begin", "break", "case", "class", "def", "defined?", "do", "else", "elsif", "end", "ensure", "false", "for", "if", "in", "module", "next", "nil", "not", "or", "redo", "rescue", "retry", "return", "self", "super", "then", "true", "undef", "unless", "until", "when", "while", "yield", "require", "require_relative", "include", "extend", "attr_reader", "attr_writer", "attr_accessor", "puts", "print"),
		lineComment: "#",
		stringChars: "\"'",
	}
}

func setOf(kws ...string) map[string]bool {
	m := make(map[string]bool, len(kws))
	for _, k := range kws {
		m[k] = true
	}
	return m
}

// highlightCode applies basic syntax highlighting to one line. Comments
// dim the rest of the line; strings get green; keywords get magenta.
// Unrecognised languages render as plain code-fg text.
func highlightCode(line, lang string) string {
	spec, ok := langs[strings.ToLower(lang)]
	if !ok {
		return codeFg + line + ansiReset
	}

	var out strings.Builder
	i := 0
	for i < len(line) {
		// Line comment — color from here to end.
		if spec.lineComment != "" && strings.HasPrefix(line[i:], spec.lineComment) {
			out.WriteString(colDim)
			out.WriteString(line[i:])
			out.WriteString(ansiReset)
			return out.String()
		}
		// String literal.
		if i < len(line) && strings.IndexByte(spec.stringChars, line[i]) >= 0 {
			delim := line[i]
			j := i + 1
			for j < len(line) {
				if line[j] == '\\' && j+1 < len(line) {
					j += 2
					continue
				}
				if line[j] == delim {
					j++
					break
				}
				j++
			}
			out.WriteString(colGreen)
			out.WriteString(line[i:j])
			out.WriteString(ansiReset)
			out.WriteString(codeFg)
			i = j
			continue
		}
		// Identifier / keyword.
		if isIdentStart(line[i]) {
			j := i + 1
			for j < len(line) && isIdentPart(line[j]) {
				j++
			}
			word := line[i:j]
			if spec.keywords[word] {
				out.WriteString(colMag)
				out.WriteString(word)
				out.WriteString(ansiReset)
				out.WriteString(codeFg)
			} else {
				out.WriteString(word)
			}
			i = j
			continue
		}
		out.WriteByte(line[i])
		i++
	}
	return codeFg + out.String() + ansiReset
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
