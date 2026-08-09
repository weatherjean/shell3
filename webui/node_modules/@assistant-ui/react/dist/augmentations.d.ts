//#region src/augmentations.d.ts
/**
 * Module augmentation namespace for assistant-ui type extensions.
 *
 * @example
 * ```typescript
 * declare module "@assistant-ui/react" {
 *   namespace Assistant {
 *     interface Commands {
 *       myCustomCommand: {
 *         type: "my-custom-command";
 *         data: string;
 *       };
 *     }
 *
 *     interface ExternalState {
 *       myCustomState: {
 *         foo: string;
 *       };
 *     }
 *   }
 * }
 * ```
 */
declare namespace Assistant {
  interface Commands {}
  interface ExternalState {}
}
type UserCommands = Assistant.Commands[keyof Assistant.Commands];
type UserExternalState = keyof Assistant.ExternalState extends never ? Record<string, unknown> : Assistant.ExternalState[keyof Assistant.ExternalState];
//#endregion
export { Assistant, UserCommands, UserExternalState };
//# sourceMappingURL=augmentations.d.ts.map