import { AssistantCloudAPI } from "./AssistantCloudAPI.js";
import { AssistantCloudAuthTokens } from "./AssistantCloudAuthTokens.js";
import { AssistantCloudRuns } from "./AssistantCloudRuns.js";
import { AssistantCloudThreads } from "./AssistantCloudThreads.js";
import { AssistantCloudFiles } from "./AssistantCloudFiles.js";
//#region src/AssistantCloud.ts
var AssistantCloud = class {
	threads;
	auth;
	runs;
	files;
	telemetry;
	constructor(config) {
		const api = new AssistantCloudAPI(config);
		this.threads = new AssistantCloudThreads(api);
		this.auth = { tokens: new AssistantCloudAuthTokens(api) };
		this.runs = new AssistantCloudRuns(api);
		this.files = new AssistantCloudFiles(api);
		const t = config.telemetry;
		this.telemetry = t === false ? { enabled: false } : t === true || t === void 0 ? { enabled: true } : {
			enabled: t.enabled !== false,
			...t
		};
	}
};
//#endregion
export { AssistantCloud };

//# sourceMappingURL=AssistantCloud.js.map