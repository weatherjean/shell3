//#region src/resumable/errors.d.ts
type ResumableStreamErrorCode = "missing" | "exists" | "finalized" | "invalid-id";
declare class ResumableStreamError extends Error {
  readonly code: ResumableStreamErrorCode;
  constructor(code: ResumableStreamErrorCode, message: string);
}
declare function validateStreamId(streamId: string): void;
//#endregion
export { ResumableStreamError, ResumableStreamErrorCode, validateStreamId };
//# sourceMappingURL=errors.d.ts.map