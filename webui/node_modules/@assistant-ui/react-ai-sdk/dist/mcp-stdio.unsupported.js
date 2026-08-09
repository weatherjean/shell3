//#region src/mcp-stdio.unsupported.ts
var UnsupportedStdioMCPTransport = class {
	constructor() {
		throw new Error("stdio MCP transport requires a runtime that can spawn a subprocess, such as Node, Bun, or Deno (with --allow-run). Use an HTTP or SSE MCP server config in browser, React Native, edge, or worker runtimes.");
	}
};
const Experimental_StdioMCPTransport = UnsupportedStdioMCPTransport;
//#endregion
export { Experimental_StdioMCPTransport };

//# sourceMappingURL=mcp-stdio.unsupported.js.map