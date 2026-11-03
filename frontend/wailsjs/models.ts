// Cynhyrchwyd y ffeil hon yn awtomatig. PEIDIWCH Â MODIWLU
// This file mirrors the Wails v2 bindings format, but is HAND-MAINTAINED
// in this project because MyAI builds with `-skipbindings`.
// Protocol (see ARCHITECTURE.md): keep in sync with App.js and models.ts.
// Regenerate deterministically with: scripts/gen_wailsjs.py (repo root).
export namespace main {

	export class Settings {
		provider: string;
		model: string;
		environment: string;
		providers: Provider[];
		workspacePath: string;
		allowCommands: boolean;
		agentMode: boolean;
		apiKeys: {[key: string]: string};
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
	}

	export class PermissionSettings {
		tools?: {[key: string]: string};
		commandRules?: PermissionRule[];
		sensitiveGlobs?: string[];
	}

	export class PermissionRule {
		pattern: string;
		decision: string;
	}

	export class Provider {
		id: string;
		name: string;
		baseUrl: string;
		color: string;
		models: string[];
		contextWindow: number;
		noKey: boolean;
	}

	export class CustomTool {
		id: string;
		name: string;
		description: string;
		parameters: string;
		command: string;
		timeoutSec: number;
		confirm: boolean;
	}

	export class Conversation {
		id: string;
		title: string;
		createdAt: number;
		updatedAt: number;
		messages: Message[];
		todos?: TodoItem[];
	}

	export class TodoItem {
		id: string;
		content: string;
		status: string;
	}

	export class ConversationMeta {
		id: string;
		title: string;
		updatedAt: number;
		count: number;
	}

	export class MCPServerConfig {
		name: string;
		command: string;
		args?: string[];
		env?: Record<string, string>;
		enabled: boolean;
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
	}

	export class toolCallRecord {
		id: string;
		name: string;
		args: string;
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
	}

	export class TurnChangesReport {
		reverted: string[];
		conflicts: string[];
		errors: string[];
	}

	export class RulesInfo {
		enabled: boolean;
		userRules: string;
		filePath: string;
		fileExists: boolean;
		fileContent: string;
	}

	export class MentionItem {
		path: string;
		label: string;
		isDir: boolean;
	}

}
