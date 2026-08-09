//#region src/primitives/actionBar/ActionBarInteractionContext.d.ts
type ActionBarInteractionContextValue = {
  acquireInteractionLock: () => () => void;
};
declare const ActionBarInteractionContext: import("react").Context<ActionBarInteractionContextValue | null>;
declare const useActionBarInteractionContext: () => ActionBarInteractionContextValue | null;
//#endregion
export { ActionBarInteractionContext, ActionBarInteractionContextValue, useActionBarInteractionContext };
//# sourceMappingURL=ActionBarInteractionContext.d.ts.map