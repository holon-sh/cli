export declare const VIEW_MIME_TYPE = "text/html;profile=mcp-app";
export interface ViewOptions {
    csp?: {
        connectDomains?: string[];
        resourceDomains?: string[];
    };
}
type ViewRegistration = [
    name: string,
    uri: string,
    metadata: {
        mimeType: string;
        _meta: {
            ui: {
                csp: {
                    connectDomains: string[];
                    resourceDomains: string[];
                };
            };
        };
    },
    read: () => Promise<{
        ttlMs: number;
        cacheScope: "private";
        contents: {
            uri: string;
            mimeType: string;
            text: string;
        }[];
    }>
];
export declare function appView(name: string, uri: string, file: URL | string, options?: ViewOptions): ViewRegistration;
export {};
