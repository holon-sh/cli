type Protocol4 = undefined | string | number;
type Protocol6 = {
    "taskId": string;
};
type Protocol5 = undefined | Protocol6;
type Protocol3 = {
    "progressToken"?: Protocol4;
    "io.modelcontextprotocol/related-task"?: Protocol5;
    [key: string]: unknown;
};
type Protocol2 = undefined | Protocol3;
type Protocol9 = undefined | number;
type Protocol8 = {
    "ttl"?: Protocol9;
};
type Protocol7 = undefined | Protocol8;
type Protocol11 = {
    [key: string]: unknown;
};
type Protocol10 = undefined | Protocol11;
type Protocol1 = {
    "name": string;
    "_meta"?: Protocol2;
    "task"?: Protocol7;
    "arguments"?: Protocol10;
};
type Protocol0 = {
    "method": "tools/call";
    "params": Protocol1;
};
type CallToolRequest = Protocol0;
type Protocol20 = "user" | "assistant";
type Protocol19 = Array<Protocol20>;
type Protocol18 = undefined | Protocol19;
type Protocol21 = undefined | string;
type Protocol17 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol16 = undefined | Protocol17;
type Protocol15 = {
    "type": "text";
    "text": string;
    "annotations"?: Protocol16;
    "_meta"?: Protocol10;
};
type Protocol24 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol23 = undefined | Protocol24;
type Protocol22 = {
    "type": "image";
    "data": string;
    "mimeType": string;
    "annotations"?: Protocol23;
    "_meta"?: Protocol10;
};
type Protocol27 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol26 = undefined | Protocol27;
type Protocol25 = {
    "type": "audio";
    "data": string;
    "mimeType": string;
    "annotations"?: Protocol26;
    "_meta"?: Protocol10;
};
type Protocol30 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol29 = undefined | Protocol30;
type Protocol32 = {
    [key: string]: unknown;
};
type Protocol31 = undefined | Protocol32;
type Protocol37 = Array<string>;
type Protocol36 = undefined | Protocol37;
type Protocol38 = undefined | "light" | "dark";
type Protocol35 = {
    "src": string;
    "mimeType"?: Protocol21;
    "sizes"?: Protocol36;
    "theme"?: Protocol38;
};
type Protocol34 = Array<Protocol35>;
type Protocol33 = undefined | Protocol34;
type Protocol28 = {
    "uri": string;
    "name": string;
    "type": "resource_link";
    "description"?: Protocol21;
    "mimeType"?: Protocol21;
    "size"?: Protocol9;
    "annotations"?: Protocol29;
    "_meta"?: Protocol31;
    "icons"?: Protocol33;
    "title"?: Protocol21;
};
type Protocol41 = {
    "uri": string;
    "text": string;
    "mimeType"?: Protocol21;
    "_meta"?: Protocol10;
};
type Protocol42 = {
    "uri": string;
    "blob": string;
    "mimeType"?: Protocol21;
    "_meta"?: Protocol10;
};
type Protocol40 = Protocol41 | Protocol42;
type Protocol44 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol43 = undefined | Protocol44;
type Protocol39 = {
    "type": "resource";
    "resource": Protocol40;
    "annotations"?: Protocol43;
    "_meta"?: Protocol10;
};
type Protocol14 = Protocol15 | Protocol22 | Protocol25 | Protocol28 | Protocol39;
type Protocol13 = Array<Protocol14>;
type Protocol51 = {
    "src": string;
    "mimeType"?: Protocol21;
    "sizes"?: Protocol36;
    "theme"?: Protocol38;
};
type Protocol50 = Array<Protocol51>;
type Protocol49 = undefined | Protocol50;
type Protocol48 = {
    "version": string;
    "name": string;
    "websiteUrl"?: Protocol21;
    "description"?: Protocol21;
    "icons"?: Protocol49;
    "title"?: Protocol21;
};
type Protocol47 = undefined | Protocol48;
type Protocol46 = {
    "io.modelcontextprotocol/serverInfo"?: Protocol47;
    [key: string]: unknown;
};
type Protocol45 = undefined | Protocol46;
type Protocol52 = undefined | false | true;
type Protocol12 = {
    "content": Protocol13;
    "_meta"?: Protocol45;
    "structuredContent"?: unknown;
    "isError"?: Protocol52;
    [key: string]: unknown;
};
type CallToolResult = Protocol12;
type Protocol64 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol63 = undefined | Protocol64;
type Protocol62 = {
    "type": "text";
    "text": string;
    "annotations"?: Protocol63;
    "_meta"?: Protocol10;
};
type Protocol67 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol66 = undefined | Protocol67;
type Protocol65 = {
    "type": "image";
    "data": string;
    "mimeType": string;
    "annotations"?: Protocol66;
    "_meta"?: Protocol10;
};
type Protocol70 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol69 = undefined | Protocol70;
type Protocol68 = {
    "type": "audio";
    "data": string;
    "mimeType": string;
    "annotations"?: Protocol69;
    "_meta"?: Protocol10;
};
type Protocol71 = {
    "type": "tool_use";
    "name": string;
    "id": string;
    "input": Protocol11;
    "_meta"?: Protocol10;
};
type Protocol77 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol76 = undefined | Protocol77;
type Protocol75 = {
    "type": "text";
    "text": string;
    "annotations"?: Protocol76;
    "_meta"?: Protocol10;
};
type Protocol80 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol79 = undefined | Protocol80;
type Protocol78 = {
    "type": "image";
    "data": string;
    "mimeType": string;
    "annotations"?: Protocol79;
    "_meta"?: Protocol10;
};
type Protocol83 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol82 = undefined | Protocol83;
type Protocol81 = {
    "type": "audio";
    "data": string;
    "mimeType": string;
    "annotations"?: Protocol82;
    "_meta"?: Protocol10;
};
type Protocol86 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol85 = undefined | Protocol86;
type Protocol89 = {
    "src": string;
    "mimeType"?: Protocol21;
    "sizes"?: Protocol36;
    "theme"?: Protocol38;
};
type Protocol88 = Array<Protocol89>;
type Protocol87 = undefined | Protocol88;
type Protocol84 = {
    "uri": string;
    "name": string;
    "type": "resource_link";
    "description"?: Protocol21;
    "mimeType"?: Protocol21;
    "size"?: Protocol9;
    "annotations"?: Protocol85;
    "_meta"?: Protocol31;
    "icons"?: Protocol87;
    "title"?: Protocol21;
};
type Protocol92 = {
    "uri": string;
    "text": string;
    "mimeType"?: Protocol21;
    "_meta"?: Protocol10;
};
type Protocol93 = {
    "uri": string;
    "blob": string;
    "mimeType"?: Protocol21;
    "_meta"?: Protocol10;
};
type Protocol91 = Protocol92 | Protocol93;
type Protocol95 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol94 = undefined | Protocol95;
type Protocol90 = {
    "type": "resource";
    "resource": Protocol91;
    "annotations"?: Protocol94;
    "_meta"?: Protocol10;
};
type Protocol74 = Protocol75 | Protocol78 | Protocol81 | Protocol84 | Protocol90;
type Protocol73 = Array<Protocol74>;
type Protocol72 = {
    "type": "tool_result";
    "toolUseId": string;
    "content": Protocol73;
    "structuredContent"?: unknown;
    "isError"?: Protocol52;
    "_meta"?: Protocol10;
};
type Protocol100 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol99 = undefined | Protocol100;
type Protocol98 = {
    "type": "text";
    "text": string;
    "annotations"?: Protocol99;
    "_meta"?: Protocol10;
};
type Protocol103 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol102 = undefined | Protocol103;
type Protocol101 = {
    "type": "image";
    "data": string;
    "mimeType": string;
    "annotations"?: Protocol102;
    "_meta"?: Protocol10;
};
type Protocol106 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol105 = undefined | Protocol106;
type Protocol104 = {
    "type": "audio";
    "data": string;
    "mimeType": string;
    "annotations"?: Protocol105;
    "_meta"?: Protocol10;
};
type Protocol107 = {
    "type": "tool_use";
    "name": string;
    "id": string;
    "input": Protocol11;
    "_meta"?: Protocol10;
};
type Protocol113 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol112 = undefined | Protocol113;
type Protocol111 = {
    "type": "text";
    "text": string;
    "annotations"?: Protocol112;
    "_meta"?: Protocol10;
};
type Protocol116 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol115 = undefined | Protocol116;
type Protocol114 = {
    "type": "image";
    "data": string;
    "mimeType": string;
    "annotations"?: Protocol115;
    "_meta"?: Protocol10;
};
type Protocol119 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol118 = undefined | Protocol119;
type Protocol117 = {
    "type": "audio";
    "data": string;
    "mimeType": string;
    "annotations"?: Protocol118;
    "_meta"?: Protocol10;
};
type Protocol122 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol121 = undefined | Protocol122;
type Protocol125 = {
    "src": string;
    "mimeType"?: Protocol21;
    "sizes"?: Protocol36;
    "theme"?: Protocol38;
};
type Protocol124 = Array<Protocol125>;
type Protocol123 = undefined | Protocol124;
type Protocol120 = {
    "uri": string;
    "name": string;
    "type": "resource_link";
    "description"?: Protocol21;
    "mimeType"?: Protocol21;
    "size"?: Protocol9;
    "annotations"?: Protocol121;
    "_meta"?: Protocol31;
    "icons"?: Protocol123;
    "title"?: Protocol21;
};
type Protocol128 = {
    "uri": string;
    "text": string;
    "mimeType"?: Protocol21;
    "_meta"?: Protocol10;
};
type Protocol129 = {
    "uri": string;
    "blob": string;
    "mimeType"?: Protocol21;
    "_meta"?: Protocol10;
};
type Protocol127 = Protocol128 | Protocol129;
type Protocol131 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol130 = undefined | Protocol131;
type Protocol126 = {
    "type": "resource";
    "resource": Protocol127;
    "annotations"?: Protocol130;
    "_meta"?: Protocol10;
};
type Protocol110 = Protocol111 | Protocol114 | Protocol117 | Protocol120 | Protocol126;
type Protocol109 = Array<Protocol110>;
type Protocol108 = {
    "type": "tool_result";
    "toolUseId": string;
    "content": Protocol109;
    "structuredContent"?: unknown;
    "isError"?: Protocol52;
    "_meta"?: Protocol10;
};
type Protocol97 = Protocol98 | Protocol101 | Protocol104 | Protocol107 | Protocol108;
type Protocol96 = Array<Protocol97>;
type Protocol61 = Protocol62 | Protocol65 | Protocol68 | Protocol71 | Protocol72 | Protocol96;
type Protocol60 = {
    "role": Protocol20;
    "content": Protocol61;
    "_meta"?: Protocol10;
};
type Protocol59 = Array<Protocol60>;
type Protocol135 = {
    "taskId": string;
};
type Protocol134 = undefined | Protocol135;
type Protocol133 = {
    "progressToken"?: Protocol4;
    "io.modelcontextprotocol/related-task"?: Protocol134;
    [key: string]: unknown;
};
type Protocol132 = undefined | Protocol133;
type Protocol137 = {
    "ttl"?: Protocol9;
};
type Protocol136 = undefined | Protocol137;
type Protocol142 = {
    "name"?: Protocol21;
};
type Protocol141 = Array<Protocol142>;
type Protocol140 = undefined | Protocol141;
type Protocol139 = {
    "hints"?: Protocol140;
    "costPriority"?: Protocol9;
    "speedPriority"?: Protocol9;
    "intelligencePriority"?: Protocol9;
};
type Protocol138 = undefined | Protocol139;
type Protocol143 = undefined | "none" | "thisServer" | "allServers";
type Protocol147 = Array<Protocol146>;
type Protocol146 = null | string | number | false | true | Protocol145 | Protocol147;
type Protocol145 = {
    [key: string]: Protocol146;
};
type Protocol144 = undefined | Protocol145;
type Protocol153 = {
    [key: string]: Protocol146;
};
type Protocol152 = undefined | Protocol153;
type Protocol151 = {
    "type": "object";
    "properties"?: Protocol152;
    "required"?: Protocol36;
    [key: string]: unknown;
};
type Protocol155 = {
    "$schema"?: Protocol21;
    [key: string]: unknown;
};
type Protocol154 = undefined | Protocol155;
type Protocol157 = {
    "title"?: Protocol21;
    "readOnlyHint"?: Protocol52;
    "destructiveHint"?: Protocol52;
    "idempotentHint"?: Protocol52;
    "openWorldHint"?: Protocol52;
};
type Protocol156 = undefined | Protocol157;
type Protocol160 = undefined | "optional" | "required" | "forbidden";
type Protocol159 = {
    "taskSupport"?: Protocol160;
};
type Protocol158 = undefined | Protocol159;
type Protocol163 = {
    "src": string;
    "mimeType"?: Protocol21;
    "sizes"?: Protocol36;
    "theme"?: Protocol38;
};
type Protocol162 = Array<Protocol163>;
type Protocol161 = undefined | Protocol162;
type Protocol150 = {
    "inputSchema": Protocol151;
    "name": string;
    "description"?: Protocol21;
    "outputSchema"?: Protocol154;
    "annotations"?: Protocol156;
    "execution"?: Protocol158;
    "_meta"?: Protocol10;
    "icons"?: Protocol161;
    "title"?: Protocol21;
};
type Protocol149 = Array<Protocol150>;
type Protocol148 = undefined | Protocol149;
type Protocol166 = undefined | "none" | "required" | "auto";
type Protocol165 = {
    "mode"?: Protocol166;
};
type Protocol164 = undefined | Protocol165;
type Protocol58 = {
    "messages": Protocol59;
    "maxTokens": number;
    "_meta"?: Protocol132;
    "task"?: Protocol136;
    "modelPreferences"?: Protocol138;
    "systemPrompt"?: Protocol21;
    "includeContext"?: Protocol143;
    "temperature"?: Protocol9;
    "stopSequences"?: Protocol36;
    "metadata"?: Protocol144;
    "tools"?: Protocol148;
    "toolChoice"?: Protocol164;
};
type Protocol57 = {
    "method": "sampling/createMessage";
    "params": Protocol58;
};
type Protocol173 = {
    "taskId": string;
};
type Protocol172 = undefined | Protocol173;
type Protocol171 = {
    "progressToken"?: Protocol4;
    "io.modelcontextprotocol/related-task"?: Protocol172;
    [key: string]: unknown;
};
type Protocol170 = undefined | Protocol171;
type Protocol169 = {
    "_meta"?: Protocol170;
};
type Protocol168 = undefined | Protocol169;
type Protocol167 = {
    "method": "roots/list";
    "params"?: Protocol168;
};
type Protocol180 = {
    "type": "string";
    "enum": Protocol37;
    "title"?: Protocol21;
    "description"?: Protocol21;
    "enumNames"?: Protocol36;
    "default"?: Protocol21;
};
type Protocol181 = {
    "type": "string";
    "enum": Protocol37;
    "title"?: Protocol21;
    "description"?: Protocol21;
    "default"?: Protocol21;
};
type Protocol184 = {
    "const": string;
    "title": string;
};
type Protocol183 = Array<Protocol184>;
type Protocol182 = {
    "type": "string";
    "oneOf": Protocol183;
    "title"?: Protocol21;
    "description"?: Protocol21;
    "default"?: Protocol21;
};
type Protocol186 = {
    "type": "string";
    "enum": Protocol37;
};
type Protocol185 = {
    "type": "array";
    "items": Protocol186;
    "title"?: Protocol21;
    "description"?: Protocol21;
    "minItems"?: Protocol9;
    "maxItems"?: Protocol9;
    "default"?: Protocol36;
};
type Protocol190 = {
    "const": string;
    "title": string;
};
type Protocol189 = Array<Protocol190>;
type Protocol188 = {
    "anyOf": Protocol189;
};
type Protocol187 = {
    "type": "array";
    "items": Protocol188;
    "title"?: Protocol21;
    "description"?: Protocol21;
    "minItems"?: Protocol9;
    "maxItems"?: Protocol9;
    "default"?: Protocol36;
};
type Protocol191 = {
    "type": "boolean";
    "title"?: Protocol21;
    "description"?: Protocol21;
    "default"?: Protocol52;
};
type Protocol193 = undefined | "email" | "uri" | "date" | "date-time";
type Protocol192 = {
    "type": "string";
    "title"?: Protocol21;
    "description"?: Protocol21;
    "minLength"?: Protocol9;
    "maxLength"?: Protocol9;
    "format"?: Protocol193;
    "default"?: Protocol21;
};
type Protocol195 = "number" | "integer";
type Protocol194 = {
    "type": Protocol195;
    "title"?: Protocol21;
    "description"?: Protocol21;
    "minimum"?: Protocol9;
    "maximum"?: Protocol9;
    "default"?: Protocol9;
};
type Protocol179 = Protocol180 | Protocol181 | Protocol182 | Protocol185 | Protocol187 | Protocol191 | Protocol192 | Protocol194;
type Protocol178 = {
    [key: string]: Protocol179;
};
type Protocol177 = {
    "type": "object";
    "properties": Protocol178;
    "required"?: Protocol36;
    [key: string]: unknown;
};
type Protocol199 = {
    "taskId": string;
};
type Protocol198 = undefined | Protocol199;
type Protocol197 = {
    "progressToken"?: Protocol4;
    "io.modelcontextprotocol/related-task"?: Protocol198;
    [key: string]: unknown;
};
type Protocol196 = undefined | Protocol197;
type Protocol201 = {
    "ttl"?: Protocol9;
};
type Protocol200 = undefined | Protocol201;
type Protocol202 = undefined | "form";
type Protocol176 = {
    "message": string;
    "requestedSchema": Protocol177;
    "_meta"?: Protocol196;
    "task"?: Protocol200;
    "mode"?: Protocol202;
};
type Protocol207 = {
    "taskId": string;
};
type Protocol206 = undefined | Protocol207;
type Protocol205 = {
    "progressToken"?: Protocol4;
    "io.modelcontextprotocol/related-task"?: Protocol206;
    [key: string]: unknown;
};
type Protocol204 = undefined | Protocol205;
type Protocol209 = {
    "ttl"?: Protocol9;
};
type Protocol208 = undefined | Protocol209;
type Protocol203 = {
    "mode": "url";
    "message": string;
    "elicitationId": string;
    "url": string;
    "_meta"?: Protocol204;
    "task"?: Protocol208;
};
type Protocol175 = Protocol176 | Protocol203;
type Protocol174 = {
    "method": "elicitation/create";
    "params": Protocol175;
};
type Protocol56 = Protocol57 | Protocol167 | Protocol174;
type Protocol55 = {
    [key: string]: Protocol56;
};
type Protocol54 = undefined | Protocol55;
type Protocol216 = {
    "src": string;
    "mimeType"?: Protocol21;
    "sizes"?: Protocol36;
    "theme"?: Protocol38;
};
type Protocol215 = Array<Protocol216>;
type Protocol214 = undefined | Protocol215;
type Protocol213 = {
    "version": string;
    "name": string;
    "websiteUrl"?: Protocol21;
    "description"?: Protocol21;
    "icons"?: Protocol214;
    "title"?: Protocol21;
};
type Protocol212 = undefined | Protocol213;
type Protocol211 = {
    "io.modelcontextprotocol/serverInfo"?: Protocol212;
    [key: string]: unknown;
};
type Protocol210 = undefined | Protocol211;
type Protocol53 = {
    "resultType": "input_required";
    "inputRequests"?: Protocol54;
    "requestState"?: Protocol21;
    "_meta"?: Protocol210;
    [key: string]: unknown;
};
type InputRequiredResult = Protocol53;
type Protocol223 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol222 = undefined | Protocol223;
type Protocol221 = {
    "type": "text";
    "text": string;
    "annotations"?: Protocol222;
    "_meta"?: Protocol10;
};
type Protocol226 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol225 = undefined | Protocol226;
type Protocol224 = {
    "type": "image";
    "data": string;
    "mimeType": string;
    "annotations"?: Protocol225;
    "_meta"?: Protocol10;
};
type Protocol229 = {
    "audience"?: Protocol18;
    "priority"?: Protocol9;
    "lastModified"?: Protocol21;
};
type Protocol228 = undefined | Protocol229;
type Protocol227 = {
    "type": "audio";
    "data": string;
    "mimeType": string;
    "annotations"?: Protocol228;
    "_meta"?: Protocol10;
};
type Protocol220 = Protocol221 | Protocol224 | Protocol227;
type Protocol236 = {
    "src": string;
    "mimeType"?: Protocol21;
    "sizes"?: Protocol36;
    "theme"?: Protocol38;
};
type Protocol235 = Array<Protocol236>;
type Protocol234 = undefined | Protocol235;
type Protocol233 = {
    "version": string;
    "name": string;
    "websiteUrl"?: Protocol21;
    "description"?: Protocol21;
    "icons"?: Protocol234;
    "title"?: Protocol21;
};
type Protocol232 = undefined | Protocol233;
type Protocol231 = {
    "io.modelcontextprotocol/serverInfo"?: Protocol232;
    [key: string]: unknown;
};
type Protocol230 = undefined | Protocol231;
type Protocol219 = {
    "model": string;
    "role": Protocol20;
    "content": Protocol220;
    "_meta"?: Protocol230;
    "stopReason"?: Protocol21;
    [key: string]: unknown;
};
type Protocol239 = {
    "uri": string;
    "name"?: Protocol21;
    "_meta"?: Protocol10;
};
type Protocol238 = Array<Protocol239>;
type Protocol246 = {
    "src": string;
    "mimeType"?: Protocol21;
    "sizes"?: Protocol36;
    "theme"?: Protocol38;
};
type Protocol245 = Array<Protocol246>;
type Protocol244 = undefined | Protocol245;
type Protocol243 = {
    "version": string;
    "name": string;
    "websiteUrl"?: Protocol21;
    "description"?: Protocol21;
    "icons"?: Protocol244;
    "title"?: Protocol21;
};
type Protocol242 = undefined | Protocol243;
type Protocol241 = {
    "io.modelcontextprotocol/serverInfo"?: Protocol242;
    [key: string]: unknown;
};
type Protocol240 = undefined | Protocol241;
type Protocol237 = {
    "roots": Protocol238;
    "_meta"?: Protocol240;
    [key: string]: unknown;
};
type Protocol248 = "cancel" | "accept" | "decline";
type Protocol255 = {
    "src": string;
    "mimeType"?: Protocol21;
    "sizes"?: Protocol36;
    "theme"?: Protocol38;
};
type Protocol254 = Array<Protocol255>;
type Protocol253 = undefined | Protocol254;
type Protocol252 = {
    "version": string;
    "name": string;
    "websiteUrl"?: Protocol21;
    "description"?: Protocol21;
    "icons"?: Protocol253;
    "title"?: Protocol21;
};
type Protocol251 = undefined | Protocol252;
type Protocol250 = {
    "io.modelcontextprotocol/serverInfo"?: Protocol251;
    [key: string]: unknown;
};
type Protocol249 = undefined | Protocol250;
type Protocol258 = string | number | false | true | Protocol37;
type Protocol257 = {
    [key: string]: Protocol258;
};
type Protocol256 = undefined | Protocol257;
type Protocol247 = {
    "action": Protocol248;
    "_meta"?: Protocol249;
    "content"?: Protocol256;
    [key: string]: unknown;
};
type Protocol218 = Protocol219 | Protocol237 | Protocol247;
type Protocol217 = {
    [key: string]: Protocol218;
};
type InputResponses = Protocol217;
type Protocol262 = {
    "type": "object";
    "properties"?: Protocol152;
    "required"?: Protocol36;
    [key: string]: unknown;
};
type Protocol264 = {
    "$schema"?: Protocol21;
    [key: string]: unknown;
};
type Protocol263 = undefined | Protocol264;
type Protocol266 = {
    "title"?: Protocol21;
    "readOnlyHint"?: Protocol52;
    "destructiveHint"?: Protocol52;
    "idempotentHint"?: Protocol52;
    "openWorldHint"?: Protocol52;
};
type Protocol265 = undefined | Protocol266;
type Protocol268 = {
    "taskSupport"?: Protocol160;
};
type Protocol267 = undefined | Protocol268;
type Protocol271 = {
    "src": string;
    "mimeType"?: Protocol21;
    "sizes"?: Protocol36;
    "theme"?: Protocol38;
};
type Protocol270 = Array<Protocol271>;
type Protocol269 = undefined | Protocol270;
type Protocol261 = {
    "inputSchema": Protocol262;
    "name": string;
    "description"?: Protocol21;
    "outputSchema"?: Protocol263;
    "annotations"?: Protocol265;
    "execution"?: Protocol267;
    "_meta"?: Protocol10;
    "icons"?: Protocol269;
    "title"?: Protocol21;
};
type Protocol260 = Array<Protocol261>;
type Protocol278 = {
    "src": string;
    "mimeType"?: Protocol21;
    "sizes"?: Protocol36;
    "theme"?: Protocol38;
};
type Protocol277 = Array<Protocol278>;
type Protocol276 = undefined | Protocol277;
type Protocol275 = {
    "version": string;
    "name": string;
    "websiteUrl"?: Protocol21;
    "description"?: Protocol21;
    "icons"?: Protocol276;
    "title"?: Protocol21;
};
type Protocol274 = undefined | Protocol275;
type Protocol273 = {
    "io.modelcontextprotocol/serverInfo"?: Protocol274;
    [key: string]: unknown;
};
type Protocol272 = undefined | Protocol273;
type Protocol259 = {
    "tools": Protocol260;
    "_meta"?: Protocol272;
    "nextCursor"?: Protocol21;
    [key: string]: unknown;
};
type ListToolsResult = Protocol259;
type Protocol283 = {
    "progress": number;
    "total"?: Protocol9;
    "message"?: Protocol21;
};
type Protocol282 = {
    (arg0: Protocol283): void;
};
type Protocol281 = undefined | Protocol282;
type Protocol284 = undefined | AbortSignal;
type Protocol280 = {
    "onprogress"?: Protocol281;
    "signal"?: Protocol284;
    "timeout"?: Protocol9;
    "resetTimeoutOnProgress"?: Protocol52;
    "maxTotalTimeout"?: Protocol9;
    "allowInputRequired"?: Protocol52;
};
type Protocol286 = undefined | string | number;
type Protocol288 = {
    (arg0: string): void;
};
type Protocol287 = undefined | Protocol288;
type Protocol290 = {
    (): void;
};
type Protocol289 = undefined | Protocol290;
type Protocol292 = {
    [key: string]: string;
};
type Protocol291 = undefined | Protocol292;
type Protocol285 = {
    "relatedRequestId"?: Protocol286;
    "resumptionToken"?: Protocol21;
    "onresumptiontoken"?: Protocol287;
    "requestSignal"?: Protocol284;
    "onRequestStreamEnd"?: Protocol289;
    "headers"?: Protocol291;
};
type Protocol279 = Protocol280 & Protocol285;
type RequestOptions = Protocol279;
export type AuthProvider = {
    token(): Promise<string | undefined>;
    onUnauthorized?(context: unknown): Promise<void>;
} | {
    tokens(): unknown;
    redirectUrl: unknown;
    clientMetadata: unknown;
};
export type CallOptions = Omit<RequestOptions, "allowInputRequired">;
export type CallMetadata = Omit<CallToolRequest["params"], "name" | "arguments"> & {
    inputResponses?: InputResponses;
    requestState?: string;
};
type Fields<T> = {
    [K in keyof T as string extends K ? never : number extends K ? never : K]: T[K];
};
export type ToolResult<Output> = (Omit<Fields<CallToolResult>, "structuredContent" | "isError"> & {
    resultType: "complete";
    isError?: false;
    structuredContent: Output;
}) | (Omit<Fields<CallToolResult>, "structuredContent" | "isError"> & {
    resultType: "complete";
    isError: true;
    structuredContent?: unknown;
}) | InputRequiredResult;
export declare class ToolCallError extends Error {
    readonly code: "tool_error" | "input_required";
    readonly app: string;
    readonly tool: string;
    readonly result: Extract<ToolResult<unknown>, {
        isError: true;
    } | {
        resultType: "input_required";
    }>;
    constructor(app: string, tool: string, result: ToolCallError["result"]);
}
export interface ToolClient<I, O, R> {
    readonly close: () => Promise<void>;
    readonly listTools: (options?: CallOptions) => Promise<ListToolsResult>;
    readonly resources: {
        readonly [U in keyof R]: {
            readonly read: (options?: CallOptions) => Promise<R[U]>;
            readonly watch: (onChange: (value: R[U]) => void | Promise<void>, options?: {
                signal?: AbortSignal;
            }) => Promise<void>;
        };
    };
    readonly tools: {
        readonly [K in Exclude<keyof I, "then" | "toJSON" | "toString" | "valueOf" | "toLocaleString">]: (input: I[K], options?: CallOptions) => Promise<K extends keyof O ? O[K] : unknown>;
    };
    readonly callTool: <K extends keyof I & string>(request: CallMetadata & {
        name: K;
        arguments: I[K];
    }, options?: CallOptions) => Promise<ToolResult<K extends keyof O ? O[K] : unknown>>;
}
