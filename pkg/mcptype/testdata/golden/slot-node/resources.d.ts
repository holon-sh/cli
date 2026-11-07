// holon-generated-apps-v1 app=checkout
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
    "orders://summary": { "total": number; };
  };
  "with-hyphen": {
    "orders://summary": { "total": number; };
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
