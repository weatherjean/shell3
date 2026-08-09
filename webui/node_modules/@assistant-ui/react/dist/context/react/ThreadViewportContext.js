"use client";
import { createContextHook } from "./utils/createContextHook.js";
import { createContextStoreHook } from "./utils/createContextStoreHook.js";
import { createContext } from "@assistant-ui/tap/react-shim";
//#region src/context/react/ThreadViewportContext.ts
const ThreadViewportContext = createContext(null);
const { useThreadViewport, useThreadViewportStore } = createContextStoreHook(createContextHook(ThreadViewportContext, "ThreadPrimitive.Viewport"), "useThreadViewport");
//#endregion
export { ThreadViewportContext, useThreadViewport, useThreadViewportStore };

//# sourceMappingURL=ThreadViewportContext.js.map