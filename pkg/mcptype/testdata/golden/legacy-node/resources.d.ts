export interface Inputs {
  "empty": {
  };
  "mysql": {
  };
  "orders": {
    "orders://summary": {  };
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
  "with-hyphen": {
    "orders://summary": {  };
  };
}
