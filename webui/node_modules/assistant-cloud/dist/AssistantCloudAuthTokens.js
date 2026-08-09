//#region src/AssistantCloudAuthTokens.ts
var AssistantCloudAuthTokens = class {
	cloud;
	constructor(cloud) {
		this.cloud = cloud;
	}
	async create() {
		return this.cloud.makeRequest("/auth/tokens", { method: "POST" });
	}
};
//#endregion
export { AssistantCloudAuthTokens };

//# sourceMappingURL=AssistantCloudAuthTokens.js.map