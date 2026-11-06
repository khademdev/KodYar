// @ts-check
// Cynhyrchwyd y ffeil hon yn awtomatig. PEIDIWCH Â MODIWLU
// This file mirrors the Wails v2 bindings format, but is HAND-MAINTAINED
// in this project because MyAI builds with `-skipbindings`.
// Protocol (see ARCHITECTURE.md): every new exported App method must be
// added here (App.js + App.d.ts, plus any new struct types in models.ts).
// Regenerate deterministically with: scripts/gen_wailsjs.py (repo root).

export function ApprovePlan(arg1) {
  return window['go']['main']['App']['ApprovePlan'](arg1);
}

export function ClearErrorLog() {
  return window['go']['main']['App']['ClearErrorLog']();
}

export function CompactConversation(arg1) {
  return window['go']['main']['App']['CompactConversation'](arg1);
}

export function DeleteAllConversations() {
  return window['go']['main']['App']['DeleteAllConversations']();
}

export function DeleteConversation(arg1) {
  return window['go']['main']['App']['DeleteConversation'](arg1);
}

export function DeleteMessage(arg1, arg2) {
  return window['go']['main']['App']['DeleteMessage'](arg1, arg2);
}

export function DeleteProvider(arg1) {
  return window['go']['main']['App']['DeleteProvider'](arg1);
}

export function DiscoverModels(arg1, arg2, arg3) {
  return window['go']['main']['App']['DiscoverModels'](arg1, arg2, arg3);
}

export function EditMessage(arg1, arg2, arg3) {
  return window['go']['main']['App']['EditMessage'](arg1, arg2, arg3);
}

export function ForkConversation(arg1, arg2) {
  return window['go']['main']['App']['ForkConversation'](arg1, arg2);
}

export function GetConversation(arg1) {
  return window['go']['main']['App']['GetConversation'](arg1);
}

export function GetConversationExport(arg1, arg2) {
  return window['go']['main']['App']['GetConversationExport'](arg1, arg2);
}

export function GetConversations() {
  return window['go']['main']['App']['GetConversations']();
}

export function GetCustomTools() {
  return window['go']['main']['App']['GetCustomTools']();
}

export function GetEnvironmentPresets() {
  return window['go']['main']['App']['GetEnvironmentPresets']();
}

export function GetErrorLog() {
  return window['go']['main']['App']['GetErrorLog']();
}

export function GetFileChangeDiff(arg1) {
  return window['go']['main']['App']['GetFileChangeDiff'](arg1);
}

export function GetFileChanges() {
  return window['go']['main']['App']['GetFileChanges']();
}

export function GetProviders() {
  return window['go']['main']['App']['GetProviders']();
}

export function GetRulesInfo() {
  return window['go']['main']['App']['GetRulesInfo']();
}

export function GetSettings() {
  return window['go']['main']['App']['GetSettings']();
}

export function GetTurnChanges(arg1, arg2) {
  return window['go']['main']['App']['GetTurnChanges'](arg1, arg2);
}

export function MCPStatus() {
  return window['go']['main']['App']['MCPStatus']();
}

export function MentionSearch(arg1) {
  return window['go']['main']['App']['MentionSearch'](arg1);
}

export function OpenSupportPage() {
  return window['go']['main']['App']['OpenSupportPage']();
}

export function PermissionsView() {
  return window['go']['main']['App']['PermissionsView']();
}

export function PreviewCheckpointRestore(arg1, arg2) {
  return window['go']['main']['App']['PreviewCheckpointRestore'](arg1, arg2);
}

export function Regenerate(arg1) {
  return window['go']['main']['App']['Regenerate'](arg1);
}

export function RemoveCommandRule(arg1) {
  return window['go']['main']['App']['RemoveCommandRule'](arg1);
}

export function RemoveWebHostRule(arg1) {
  return window['go']['main']['App']['RemoveWebHostRule'](arg1);
}

export function ResetPermissions() {
  return window['go']['main']['App']['ResetPermissions']();
}

export function RespondFileConfirm(arg1, arg2) {
  return window['go']['main']['App']['RespondFileConfirm'](arg1, arg2);
}

export function RespondFileConfirmScope(arg1, arg2, arg3) {
  return window['go']['main']['App']['RespondFileConfirmScope'](arg1, arg2, arg3);
}

export function RespondPermissionConfirm(arg1, arg2) {
  return window['go']['main']['App']['RespondPermissionConfirm'](arg1, arg2);
}

export function RestoreCheckpoint(arg1, arg2) {
  return window['go']['main']['App']['RestoreCheckpoint'](arg1, arg2);
}

export function ResumeAgent(arg1) {
  return window['go']['main']['App']['ResumeAgent'](arg1);
}

export function RevertAllFileChanges() {
  return window['go']['main']['App']['RevertAllFileChanges']();
}

export function RevertFileChange(arg1) {
  return window['go']['main']['App']['RevertFileChange'](arg1);
}

export function RevertTurnChanges(arg1, arg2) {
  return window['go']['main']['App']['RevertTurnChanges'](arg1, arg2);
}

export function RunCommand(arg1) {
  return window['go']['main']['App']['RunCommand'](arg1);
}

export function SaveConnection(arg1, arg2, arg3, arg4) {
  return window['go']['main']['App']['SaveConnection'](arg1, arg2, arg3, arg4);
}

export function SaveCustomTools(arg1) {
  return window['go']['main']['App']['SaveCustomTools'](arg1);
}

export function SaveGenerationSettings(arg1, arg2, arg3) {
  return window['go']['main']['App']['SaveGenerationSettings'](arg1, arg2, arg3);
}

export function SaveMCPServers(arg1) {
  return window['go']['main']['App']['SaveMCPServers'](arg1);
}

export function SaveProvider(arg1, arg2) {
  return window['go']['main']['App']['SaveProvider'](arg1, arg2);
}

export function SaveWorkspace(arg1) {
  return window['go']['main']['App']['SaveWorkspace'](arg1);
}

export function SelectFolder() {
  return window['go']['main']['App']['SelectFolder']();
}

export function SendMessage(arg1, arg2, arg3) {
  return window['go']['main']['App']['SendMessage'](arg1, arg2, arg3);
}

export function SetAgentMode(arg1) {
  return window['go']['main']['App']['SetAgentMode'](arg1);
}

export function SetAllowCommands(arg1) {
  return window['go']['main']['App']['SetAllowCommands'](arg1);
}

export function SetAppearance(arg1, arg2) {
  return window['go']['main']['App']['SetAppearance'](arg1, arg2);
}

export function SetConfirmFileWrites(arg1) {
  return window['go']['main']['App']['SetConfirmFileWrites'](arg1);
}

export function SetEditMode(arg1) {
  return window['go']['main']['App']['SetEditMode'](arg1);
}

export function SetMaxAgentRounds(arg1) {
  return window['go']['main']['App']['SetMaxAgentRounds'](arg1);
}

export function SetMaxSubagentRounds(arg1) {
  return window['go']['main']['App']['SetMaxSubagentRounds'](arg1);
}

export function SetMaxTurnTokens(arg1) {
  return window['go']['main']['App']['SetMaxTurnTokens'](arg1);
}

export function SetMessagePinned(arg1, arg2, arg3) {
  return window['go']['main']['App']['SetMessagePinned'](arg1, arg2, arg3);
}

export function SetPlanMode(arg1) {
  return window['go']['main']['App']['SetPlanMode'](arg1);
}

export function SetProjectRules(arg1) {
  return window['go']['main']['App']['SetProjectRules'](arg1);
}

export function SetProxySettings(arg1, arg2) {
  return window['go']['main']['App']['SetProxySettings'](arg1, arg2);
}

export function SetRulesEnabled(arg1) {
  return window['go']['main']['App']['SetRulesEnabled'](arg1);
}

export function SetTheme(arg1) {
  return window['go']['main']['App']['SetTheme'](arg1);
}

export function SetToolPermission(arg1, arg2) {
  return window['go']['main']['App']['SetToolPermission'](arg1, arg2);
}

export function SettingsView() {
  return window['go']['main']['App']['SettingsView']();
}

export function StopGeneration() {
  return window['go']['main']['App']['StopGeneration']();
}

export function TestProvider(arg1, arg2) {
  return window['go']['main']['App']['TestProvider'](arg1, arg2);
}

export function ToggleFullscreen() {
  return window['go']['main']['App']['ToggleFullscreen']();
}
