export namespace main {
	
	export class CheckpointRestorePreview {
	    available: boolean;
	    rewrite: string[];
	    trash: string[];
	    unchanged: string[];
	    skipped: string[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new CheckpointRestorePreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.rewrite = source["rewrite"];
	        this.trash = source["trash"];
	        this.unchanged = source["unchanged"];
	        this.skipped = source["skipped"];
	        this.error = source["error"];
	    }
	}
	export class CheckpointRestoreReport {
	    restored: string[];
	    trashed: string[];
	    skipped: string[];
	    errors: string[];
	    trashDir?: string;
	
	    static createFrom(source: any = {}) {
	        return new CheckpointRestoreReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.restored = source["restored"];
	        this.trashed = source["trashed"];
	        this.skipped = source["skipped"];
	        this.errors = source["errors"];
	        this.trashDir = source["trashDir"];
	    }
	}
	export class TurnTokens {
	    prompt: number;
	    completion: number;
	
	    static createFrom(source: any = {}) {
	        return new TurnTokens(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.prompt = source["prompt"];
	        this.completion = source["completion"];
	    }
	}
	export class TurnVerification {
	    ran: boolean;
	    ok: boolean;
	    command?: string;
	
	    static createFrom(source: any = {}) {
	        return new TurnVerification(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ran = source["ran"];
	        this.ok = source["ok"];
	        this.command = source["command"];
	    }
	}
	export class turnCommandRecord {
	    command: string;
	    exitCode: number;
	    exitOk: boolean;
	    verified: boolean;
	    at: number;
	
	    static createFrom(source: any = {}) {
	        return new turnCommandRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.command = source["command"];
	        this.exitCode = source["exitCode"];
	        this.exitOk = source["exitOk"];
	        this.verified = source["verified"];
	        this.at = source["at"];
	    }
	}
	export class TurnSummaryFile {
	    path: string;
	    op: string;
	    added: number;
	    removed: number;
	    changeId: string;
	    protected: boolean;
	    reverted?: boolean;
	    movedTo?: string;
	
	    static createFrom(source: any = {}) {
	        return new TurnSummaryFile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.op = source["op"];
	        this.added = source["added"];
	        this.removed = source["removed"];
	        this.changeId = source["changeId"];
	        this.protected = source["protected"];
	        this.reverted = source["reverted"];
	        this.movedTo = source["movedTo"];
	    }
	}
	export class TurnSummary {
	    conversationId: string;
	    turnId: number;
	    files: TurnSummaryFile[];
	    commands: turnCommandRecord[];
	    verification: TurnVerification;
	    tokens: TurnTokens;
	    at: number;
	    tokenCapHit?: boolean;
	    tokenCap?: number;
	
	    static createFrom(source: any = {}) {
	        return new TurnSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.conversationId = source["conversationId"];
	        this.turnId = source["turnId"];
	        this.files = this.convertValues(source["files"], TurnSummaryFile);
	        this.commands = this.convertValues(source["commands"], turnCommandRecord);
	        this.verification = this.convertValues(source["verification"], TurnVerification);
	        this.tokens = this.convertValues(source["tokens"], TurnTokens);
	        this.at = source["at"];
	        this.tokenCapHit = source["tokenCapHit"];
	        this.tokenCap = source["tokenCap"];
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
	export class TodoItem {
	    id: string;
	    content: string;
	    status: string;
	
	    static createFrom(source: any = {}) {
	        return new TodoItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.content = source["content"];
	        this.status = source["status"];
	    }
	}
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
	    tokensUsed?: number;
	    tokensExact?: boolean;
	    contextTokens?: number;
	    todos?: TodoItem[];
	    turnSummaries?: TurnSummary[];
	
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
	        this.tokensUsed = source["tokensUsed"];
	        this.tokensExact = source["tokensExact"];
	        this.contextTokens = source["contextTokens"];
	        this.todos = this.convertValues(source["todos"], TodoItem);
	        this.turnSummaries = this.convertValues(source["turnSummaries"], TurnSummary);
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
	    turnId?: number;
	    protected?: boolean;
	    heavyDelete?: boolean;
	    contentSha256?: string;
	    confirmNote?: string;
	
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
	        this.turnId = source["turnId"];
	        this.protected = source["protected"];
	        this.heavyDelete = source["heavyDelete"];
	        this.contentSha256 = source["contentSha256"];
	        this.confirmNote = source["confirmNote"];
	    }
	}
	export class MCPServerConfig {
	    name: string;
	    command: string;
	    args?: string[];
	    env?: Record<string, string>;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MCPServerConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.command = source["command"];
	        this.args = source["args"];
	        this.env = source["env"];
	        this.enabled = source["enabled"];
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
	
	export class ModelInfo {
	    id: string;
	    displayName?: string;
	    ownedBy?: string;
	    contextWindow?: number;
	    chatLikely: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ModelInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.ownedBy = source["ownedBy"];
	        this.contextWindow = source["contextWindow"];
	        this.chatLikely = source["chatLikely"];
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
	    webHosts?: PermissionRule[];
	
	    static createFrom(source: any = {}) {
	        return new PermissionSettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tools = source["tools"];
	        this.commandRules = this.convertValues(source["commandRules"], PermissionRule);
	        this.sensitiveGlobs = source["sensitiveGlobs"];
	        this.webHosts = this.convertValues(source["webHosts"], PermissionRule);
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
	    displayName?: string;
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
	        this.displayName = source["displayName"];
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
	    tokensByProvider?: Record<string, number>;
	    tokensByModel?: Record<string, number>;
	    recentUsage: number[];
	    confirmFileWrites: boolean;
	    editMode?: string;
	    protectedGlobs?: string[];
	    maxTurnTokens?: number;
	    streamIdleTimeoutSec?: number;
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
	    mcpServers?: MCPServerConfig[];
	    proxyMode?: string;
	    proxyURL?: string;
	    proxySmartMigrated?: boolean;
	    permissions: PermissionSettings;
	    sandboxMode?: string;
	    sandboxNoNetwork?: boolean;
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
	        this.tokensByProvider = source["tokensByProvider"];
	        this.tokensByModel = source["tokensByModel"];
	        this.recentUsage = source["recentUsage"];
	        this.confirmFileWrites = source["confirmFileWrites"];
	        this.editMode = source["editMode"];
	        this.protectedGlobs = source["protectedGlobs"];
	        this.maxTurnTokens = source["maxTurnTokens"];
	        this.streamIdleTimeoutSec = source["streamIdleTimeoutSec"];
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
	        this.mcpServers = this.convertValues(source["mcpServers"], MCPServerConfig);
	        this.proxyMode = source["proxyMode"];
	        this.proxyURL = source["proxyURL"];
	        this.proxySmartMigrated = source["proxySmartMigrated"];
	        this.permissions = this.convertValues(source["permissions"], PermissionSettings);
	        this.sandboxMode = source["sandboxMode"];
	        this.sandboxNoNetwork = source["sandboxNoNetwork"];
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
	
	export class TurnChangesReport {
	    reverted: string[];
	    conflicts: string[];
	    errors: string[];
	
	    static createFrom(source: any = {}) {
	        return new TurnChangesReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.reverted = source["reverted"];
	        this.conflicts = source["conflicts"];
	        this.errors = source["errors"];
	    }
	}
	
	
	
	
	

}

