import { vi } from "vitest";
//#region src/tests/setup.ts
const OriginalDate = globalThis.Date;
const fixedDate = new OriginalDate("2023-01-01");
globalThis.Date = vi.fn(() => fixedDate);
globalThis.Date.now = vi.fn(() => fixedDate.getTime());
//#endregion

//# sourceMappingURL=setup.js.map