export interface ExtractedTool {
  name: string;
  inputSchema: Record<string, unknown>;
  outputSchema?: Record<string, unknown>;
}
export interface ExtractedResource { uri: string; outputSchema: Record<string, unknown> }
export type Extraction = {
  tools: ExtractedTool[];
  resources: ExtractedResource[];
  usesApps: boolean;
  sources: Record<string, string>;
  diagnostic?: undefined;
} | { diagnostic: string; usesApps: boolean; tools?: undefined; resources?: undefined; sources?: undefined };
/** Analyze an MCP handler without executing application initialization. */
export declare function extract(root: string, entry?: string): Extraction;
export declare function clientUsage(paths: string[]): { usesApps: boolean; references: string[]; imports: string[] };
