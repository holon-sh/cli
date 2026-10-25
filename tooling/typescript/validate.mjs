import Ajv from "ajv";
import { Ajv2020 } from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

export function validator(schema) {
  const constructor = schema.$schema?.includes("draft-07") ? Ajv : Ajv2020;
  const ajv = new constructor({ strict: false, ownProperties: true, allErrors: true });
  addFormats(ajv);
  return ajv.compile(schema);
}
