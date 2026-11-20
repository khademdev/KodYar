// Cynhyrchwyd y ffeil hon yn awtomatig. PEIDIWCH Â MODIWLU
// This file mirrors the Wails v2 bindings format, but is HAND-MAINTAINED
// in this project because KodYar builds with `-skipbindings`.
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
		tokensByProvider?: {[key: string]: number};
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
		permissions: PermissionSettings;
		defaultsRemoved: boolean;
	}

	export class PermissionSettings {
		tools?: {[key: string]: string};
		commandRules?: PermissionRule[];
		sensitiveGlobs?: string[];
		webHosts?: PermissionRule[];
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
		tokensUsed?: number;
		tokensExact?: boolean;
		contextTokens?: number;
		todos?: TodoItem[];
		turnSummaries?: TurnSummary[];
	}

	export class ConversationMeta {
		id: string;
		title: string;
		updatedAt: number;
		count: number;
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

	export class ModelInfo {
		id: string;
		displayName?: string;
		ownedBy?: string;
		contextWindow?: number;
		chatLikely: boolean;
	}

	export class TurnChangesReport {
		reverted: string[];
		conflicts: string[];
		errors: string[];
	}

	export class CheckpointRestorePreview {
		available: boolean;
		rewrite: string[];
		trash: string[];
		unchanged: string[];
		skipped: string[];
		error?: string;
	}

	export class CheckpointRestoreReport {
		restored: string[];
		trashed: string[];
		skipped: string[];
		errors: string[];
		trashDir?: string;
	}

}
