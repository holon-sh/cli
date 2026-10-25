// holon-generated-apps-v1 app=checkout
export interface Inputs {
  "checkout": {
  };
  "orders": {
    "orders://summary": {  };
  };
}
export interface Outputs {
  "checkout": {
  };
  "orders": {
    "orders://summary": { "total": number; [key: string]: unknown; };
  };
}
export interface InputTypes {
  "checkout": {
  };
  "orders": {
    "orders://summary": {  };
  };
}
export interface OutputTypes {
  "checkout": {
  };
  "orders": {
    "orders://summary": {  };
  };
}
