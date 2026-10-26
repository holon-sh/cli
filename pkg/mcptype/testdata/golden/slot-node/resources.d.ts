// holon-generated-apps-v1 app=checkout
export interface Inputs {
  "empty": {
  };
  "mysql": {
  };
  "orders": {
    "orders://summary": {  };
  };
  "schemas": {
    "res://plain": {  };
    "res://typed": {  };
    "res://é/ü": {  };
  };
  "with-hyphen": {
    "orders://summary": {  };
  };
}
export interface Outputs {
  "empty": {
  };
  "mysql": {
  };
  "orders": {
    "orders://summary": { "total": number; [key: string]: unknown; };
  };
  "schemas": {
    "res://plain": string;
    "res://typed": { "items": Array<{ "id": string; }>; };
    "res://é/ü": { "n"?: number; [key: string]: unknown; };
  };
  "with-hyphen": {
    "orders://summary": { "total": number; [key: string]: unknown; };
  };
}
export interface InputTypes {
  "empty": {
  };
  "mysql": {
  };
  "orders": {
    "orders://summary": {  };
  };
  "schemas": {
    "res://plain": {  };
    "res://typed": {  };
    "res://é/ü": {  };
  };
  "with-hyphen": {
    "orders://summary": {  };
  };
}
export interface OutputTypes {
  "empty": {
  };
  "mysql": {
  };
  "orders": {
    "orders://summary": {  };
  };
  "schemas": {
    "res://plain": {  };
    "res://typed": { "Item": { "id": string; }; };
    "res://é/ü": {  };
  };
  "with-hyphen": {
    "orders://summary": {  };
  };
}
