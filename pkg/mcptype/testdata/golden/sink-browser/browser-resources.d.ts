// holon-generated-apps-v1 app=chéck"out
export interface Inputs {
  "123": {
  };
  "_private": {
  };
  "for": {
  };
  "orders": {
    "res://plain": {  };
    "res://typed": {  };
    "res://é/ü": {  };
  };
  "orders!": {
    "res://plain": {  };
    "res://typed": {  };
    "res://é/ü": {  };
  };
  "with-hyphen": {
  };
  "ünï": {
  };
}
export interface Outputs {
  "123": {
  };
  "_private": {
  };
  "for": {
  };
  "orders": {
    "res://plain": string;
    "res://typed": { "items": Array<{ "id": string; }>; };
    "res://é/ü": { "n"?: number; [key: string]: unknown; };
  };
  "orders!": {
    "res://plain": string;
    "res://typed": { "items": Array<{ "id": string; }>; };
    "res://é/ü": { "n"?: number; [key: string]: unknown; };
  };
  "with-hyphen": {
  };
  "ünï": {
  };
}
export interface InputTypes {
  "123": {
  };
  "_private": {
  };
  "for": {
  };
  "orders": {
    "res://plain": {  };
    "res://typed": {  };
    "res://é/ü": {  };
  };
  "orders!": {
    "res://plain": {  };
    "res://typed": {  };
    "res://é/ü": {  };
  };
  "with-hyphen": {
  };
  "ünï": {
  };
}
export interface OutputTypes {
  "123": {
  };
  "_private": {
  };
  "for": {
  };
  "orders": {
    "res://plain": {  };
    "res://typed": { "Item": { "id": string; }; };
    "res://é/ü": {  };
  };
  "orders!": {
    "res://plain": {  };
    "res://typed": { "Item": { "id": string; }; };
    "res://é/ü": {  };
  };
  "with-hyphen": {
  };
  "ünï": {
  };
}
