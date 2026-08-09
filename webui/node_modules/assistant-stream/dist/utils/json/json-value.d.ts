//#region src/utils/json/json-value.d.ts
type ReadonlyJSONValue = null | string | number | boolean | ReadonlyJSONObject | ReadonlyJSONArray;
type ReadonlyJSONObject = {
  readonly [key: string]: ReadonlyJSONValue;
};
type ReadonlyJSONArray = readonly ReadonlyJSONValue[];
//#endregion
export { ReadonlyJSONArray, ReadonlyJSONObject, ReadonlyJSONValue };
//# sourceMappingURL=json-value.d.ts.map