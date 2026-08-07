// Syntax highlighting for the Files view and chat code blocks.
//
// highlight.js with an explicit language set rather than the "common" bundle:
// a shell3 config directory is markdown, YAML, shell, and the odd script, and
// registering only those keeps the payload honest. Colours come from
// index.css, so both themes stay in step with the rest of the interface.

import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import diff from "highlight.js/lib/languages/diff";
import dockerfile from "highlight.js/lib/languages/dockerfile";
import go from "highlight.js/lib/languages/go";
import ini from "highlight.js/lib/languages/ini";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import markdown from "highlight.js/lib/languages/markdown";
import python from "highlight.js/lib/languages/python";
import sql from "highlight.js/lib/languages/sql";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";

for (const [name, language] of Object.entries({
  bash,
  diff,
  dockerfile,
  go,
  ini,
  javascript,
  json,
  markdown,
  python,
  sql,
  typescript,
  xml,
  yaml,
})) {
  hljs.registerLanguage(name, language);
}

// Aliases hljs does not register for us, keyed the way file extensions read.
const BY_EXTENSION: Record<string, string> = {
  md: "markdown",
  markdown: "markdown",
  yaml: "yaml",
  yml: "yaml",
  sh: "bash",
  bash: "bash",
  zsh: "bash",
  env: "ini",
  toml: "ini",
  ini: "ini",
  conf: "ini",
  json: "json",
  jsonl: "json",
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  ts: "typescript",
  tsx: "typescript",
  go: "go",
  py: "python",
  sql: "sql",
  html: "xml",
  svg: "xml",
  xml: "xml",
  diff: "diff",
  patch: "diff",
};

// Files with no useful extension that still have an obvious language.
const BY_NAME: Record<string, string> = {
  dockerfile: "dockerfile",
  makefile: "bash",
  ".env": "ini",
  ".gitignore": "ini",
};

/** Picks a language for a path, or undefined to leave the text unhighlighted. */
export const languageFor = (path: string): string | undefined => {
  const name = path.split("/").pop()?.toLowerCase() ?? "";
  if (BY_NAME[name]) return BY_NAME[name];

  const ext = name.includes(".") ? name.split(".").pop()! : "";
  return BY_EXTENSION[ext];
};

/**
 * Returns highlighted HTML, or null when the language is unknown or
 * highlighting fails — callers render the raw text in that case rather than
 * showing nothing.
 */
export const highlight = (code: string, language: string | undefined): string | null => {
  if (!language || !hljs.getLanguage(language)) return null;
  try {
    return hljs.highlight(code, { language, ignoreIllegals: true }).value;
  } catch {
    return null;
  }
};
