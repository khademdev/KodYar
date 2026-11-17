// تست‌های ابزارهای حالت چت: فقط خواندنی‌ها به مدل می‌رسند.
package main

import (
	"strings"
	"testing"
)

func TestFilterChatToolsKeepsReadOnly(t *testing.T) {
	got := filterChatTools(agentToolDefs())
	if len(got) == 0 {
		t.Fatal("chat mode must expose read-only tools")
	}
	names := map[string]bool{}
	for _, td := range got {
		names[td.Function.Name] = true
	}
	for _, want := range []string{"web_search", "web_fetch", "search_files", "read_file", "list_dir", "glob"} {
		if !names[want] {
			t.Errorf("chat tools should include %s (got %v)", want, names)
		}
	}
	for _, forbidden := range []string{"write_file", "edit_file", "apply_patch", "run_command", "delete_path", "move_path"} {
		if names[forbidden] {
			t.Errorf("chat mode must NOT expose %s", forbidden)
		}
	}
}

func TestChatToolsPromptBlockMentionsWebAndFiles(t *testing.T) {
	withWS := chatToolsPromptBlock(`C:\work\proj`)
	if !strings.Contains(withWS, "web_search") || !strings.Contains(withWS, "search_files") {
		t.Fatalf("prompt block must name the search tools, got:\n%s", withWS)
	}
	noWS := chatToolsPromptBlock("")
	if strings.Contains(noWS, "search_files:") {
		t.Fatalf("without a workspace, file tools must not be advertised, got:\n%s", noWS)
	}
	if !strings.Contains(noWS, "web_search") {
		t.Fatalf("web search stays available without a workspace, got:\n%s", noWS)
	}
}
