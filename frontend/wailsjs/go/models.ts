export namespace main {
	
	export class toolCallRecord {
	    id: string;
	    name: string;
	    args: string;
	
	    static createFrom(source: any = {}) {
	        return new toolCallRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.args = source["args"];
	    }
	}
	export class Message {
	    id: string;
	    role: string;
	    content: string;
	    thinking?: string;
	    attached?: string;
	    images?: string[];
	    pinned?: boolean;
	    compacted?: boolean;
	    at: number;
	    tokens: number;
	    aborted?: boolean;
	    toolName?: string;
	    toolArgs?: string;
	    toolCallId?: string;
	    toolOk?: boolean;
	    toolMs?: number;
	    toolCalls?: toolCallRecord[];
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.role = source["role"];
	        this.content = source["content"];
	        this.thinking = source["thinking"];
	        this.attached = source["attached"];
	        this.images = source["images"];
	        this.pinned = source["pinned"];
	        this.compacted = source["compacted"];
	        this.at = source["at"];
	        this.tokens = source["tokens"];
	        this.aborted = source["aborted"];
	        this.toolName = source["toolName"];
	        this.toolArgs = source["toolArgs"];
	        this.toolCallId = source["toolCallId"];
	        this.toolOk = source["toolOk"];
	        this.toolMs = source["toolMs"];
	        this.toolCalls = this.convertValues(source["toolCalls"], toolCallRecord);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Conversation {
	    id: string;
	    title: string;
	    createdAt: number;
	    updatedAt: number;
	    messages: Message[];
	
	    static createFrom(source: any = {}) {
	        return new Conversation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.messages = this.convertValues(source["messages"], Message);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ConversationMeta {
	    id: string;
	    title: string;
	    updatedAt: number;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new ConversationMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.updatedAt = source["updatedAt"];
	        this.count = source["count"];
	    }
	}
	export class CustomTool {
	    id: string;
	    name: string;
	    description: string;
	    parameters: string;
	    command: string;
	    timeoutSec: number;
	    confirm: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CustomTool(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.parameters = source["parameters"];
	        this.command = source["command"];
	        this.timeoutSec = source["timeoutSec"];
	        this.confirm = source["confirm"];
	    }
	}
	export class FileChange {
	    id: string;
	    convId: string;
	    path: string;
	    kind: string;
	    movedTo?: string;
	    added: number;
	    removed: number;
	    diff?: string;
	    backupFile?: string;
	    at: number;
	    rejected?: boolean;
	    reverted?: boolean;
	    revertedAt?: number;
	
	    static createFrom(source: any = {}) {
	        return new FileChange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.convId = source["convId"];
	        this.path = source["path"];
	        this.kind = source["kind"];
	        this.movedTo = source["movedTo"];
	        this.added = source["added"];
	        this.removed = source["removed"];
	        this.diff = source["diff"];
	        this.backupFile = source["backupFile"];
	        this.at = source["at"];
	        this.rejected = source["rejected"];
	        this.reverted = source["reverted"];
	        this.revertedAt = source["revertedAt"];
	    }
	}
	export class MentionItem {
	    path: string;
	    label: string;
	    isDir: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MentionItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.label = source["label"];
	        this.isDir = source["isDir"];
	    }
	}
	
	export class PermissionRule {
	    pattern: string;
	    decision: string;
	
	    static createFrom(source: any = {}) {
	        return new PermissionRule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.pattern = source["pattern"];
	        this.decision = source["decision"];
	    }
	}
	export class PermissionSettings {
	    tools?: Record<string, string>;
	    commandRules?: PermissionRule[];
	    sensitiveGlobs?: string[];
	
	    static createFrom(source: any = {}) {
	        return new PermissionSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tools = source["tools"];
	        this.commandRules = this.convertValues(source["commandRules"], PermissionRule);
	        this.sensitiveGlobs = source["sensitiveGlobs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Provider {
	    id: string;
	    name: string;
	    baseUrl: string;
	    color: string;
	    models: string[];
	    contextWindow: number;
	    noKey: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Provider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.baseUrl = source["baseUrl"];
	        this.color = source["color"];
	        this.models = source["models"];
	        this.contextWindow = source["contextWindow"];
	        this.noKey = source["noKey"];
	    }
	}
	export class RulesInfo {
	    enabled: boolean;
	    userRules: string;
	    filePath: string;
	    fileExists: boolean;
	    fileContent: string;
	
	    static createFrom(source: any = {}) {
	        return new RulesInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.userRules = source["userRules"];
	        this.filePath = source["filePath"];
	        this.fileExists = source["fileExists"];
	        this.fileContent = source["fileContent"];
	    }
	}
	export class Settings {
	    provider: string;
	    model: string;
	    environment: string;
	    providers: Provider[];
	    workspacePath: string;
	    allowCommands: boolean;
	    agentMode: boolean;
	    apiKeys: Record<string, string>;
	    systemPrompt: string;
	    temperature: number;
	    maxHistory: number;
	    tokenLimit: number;
	    tokensUsed: number;
	    recentUsage: number[];
	    confirmFileWrites: boolean;
	    planMode: boolean;
	    contextFiles: number;
	    projectRules: string;
	    rulesEnabled: boolean;
	    maxTokens: number;
	    maxAgentRounds: number;
	    maxSubagentRounds: number;
	    toolResultKeep: number;
	    maxConvKb: number;
	    theme: string;
	    fontSize: number;
	    density: string;
	    customTools: CustomTool[];
	    permissions: PermissionSettings;
	    defaultsRemoved: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.environment = source["environment"];
	        this.providers = this.convertValues(source["providers"], Provider);
	        this.workspacePath = source["workspacePath"];
	        this.allowCommands = source["allowCommands"];
	        this.agentMode = source["agentMode"];
	        this.apiKeys = source["apiKeys"];
	        this.systemPrompt = source["systemPrompt"];
	        this.temperature = source["temperature"];
	        this.maxHistory = source["maxHistory"];
	        this.tokenLimit = source["tokenLimit"];
	        this.tokensUsed = source["tokensUsed"];
	        this.recentUsage = source["recentUsage"];
	        this.confirmFileWrites = source["confirmFileWrites"];
	        this.planMode = source["planMode"];
	        this.contextFiles = source["contextFiles"];
	        this.projectRules = source["projectRules"];
	        this.rulesEnabled = source["rulesEnabled"];
	        this.maxTokens = source["maxTokens"];
	        this.maxAgentRounds = source["maxAgentRounds"];
	        this.maxSubagentRounds = source["maxSubagentRounds"];
	        this.toolResultKeep = source["toolResultKeep"];
	        this.maxConvKb = source["maxConvKb"];
	        this.theme = source["theme"];
	        this.fontSize = source["fontSize"];
	        this.density = source["density"];
	        this.customTools = this.convertValues(source["customTools"], CustomTool);
	        this.permissions = this.convertValues(source["permissions"], PermissionSettings);
	        this.defaultsRemoved = source["defaultsRemoved"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

