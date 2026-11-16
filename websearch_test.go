// تست‌های web_search — با سرورهای ساختگی (بدون تماس واقعی با اینترنت).
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withSearchGuard گارد SSRF را برای httptest (loopback) باز می‌کند؛ خودِ گارد
// واقعی در webfetch_test.go تست شده است.
func withSearchGuard(t *testing.T) {
	t.Helper()
	old := webFetchGuard
	webFetchGuard = func(network, address string) error { return nil }
	t.Cleanup(func() { webFetchGuard = old })
}

func withSearchEndpoints(t *testing.T, endpoints ...string) {
	t.Helper()
	old := webSearchEndpoints
	webSearchEndpoints = endpoints
	t.Cleanup(func() { webSearchEndpoints = old })
}

const searchHTMLFixture = `<html><body>
<div class="result results_links"><h2 class="result__title">
<a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F&amp;rut=abc">Go <b>Docs</b></a>
</h2><a class="result__snippet" href="https://go.dev/doc/">The official <b>Go</b> documentation.</a></div>
<div class="result results_links"><h2 class="result__title">
<a rel="nofollow" class="result__a" href="https://example.com/guide">Example guide</a>
</h2><a class="result__snippet">A second snippet.</a></div>
<div class="result results_links"><h2 class="result__title">
<a rel="nofollow" class="result__a" href="https://example.com/guide">Duplicate guide</a>
</h2><a class="result__snippet">duplicate</a></div>
</body></html>`

const searchLiteFixture = `<html><body><table>
<tr><td><a rel="nofollow" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fpkg.go.dev%2Fnet%2Fhttp" class='result-link'>net/http</a></td></tr>
<tr><td class='result-snippet'>Package http provides HTTP client and server implementations.</td></tr>
</table></body></html>`

func TestWebSearchParsesResultsAndCitesURLs(t *testing.T) {
	withSearchGuard(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "go docs" {
			t.Errorf("query forwarded = %q, want %q", got, "go docs")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(searchHTMLFixture))
	}))
	defer srv.Close()
	withSearchEndpoints(t, srv.URL+"/html/")

	a := newTestApp(t)
	out, ok := a.toolWebSearch(context.Background(), "go docs", 0)
	if !ok {
		t.Fatalf("web_search failed: %s", out)
	}
	// پوشش untrusted مثل web_fetch
	if !strings.Contains(out, "<search_results ") || !strings.Contains(out, `untrusted="true"`) {
		t.Fatalf("results must be wrapped in an untrusted envelope:\n%s", out)
	}
	// URL ریدایرکت DuckDuckGo باید باز شود و هر منبع URL کامل داشته باشد.
	if !strings.Contains(out, "URL: https://go.dev/doc/") {
		t.Fatalf("redirected result URL was not decoded:\n%s", out)
	}
	if !strings.Contains(out, "URL: https://example.com/guide") {
		t.Fatalf("plain result URL missing:\n%s", out)
	}
	if !strings.Contains(out, "Go Docs") || !strings.Contains(out, "The official Go documentation.") {
		t.Fatalf("title/snippet text missing:\n%s", out)
	}
	// تکراری حذف می‌شود.
	if n := strings.Count(out, "URL: https://example.com/guide"); n != 1 {
		t.Fatalf("duplicate result must appear once, got %d:\n%s", n, out)
	}
	// هیچ لینک داخلی موتور جست‌وجو نباید به‌عنوان منبع بیاید.
	if strings.Contains(out, "duckduckgo.com/l/") {
		t.Fatalf("search-engine redirect leaked into sources:\n%s", out)
	}
}

func TestWebSearchHonorsMaxResults(t *testing.T) {
	withSearchGuard(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(searchHTMLFixture))
	}))
	defer srv.Close()
	withSearchEndpoints(t, srv.URL+"/html/")

	a := newTestApp(t)
	out, ok := a.toolWebSearch(context.Background(), "x", 1)
	if !ok {
		t.Fatalf("web_search failed: %s", out)
	}
	if n := strings.Count(out, "URL: https://"); n != 1 {
		t.Fatalf("max_results=1 must return one source, got %d:\n%s", n, out)
	}
	// سقف سخت ۱۰ رعایت می‌شود (بدون خطا).
	if _, ok := a.toolWebSearch(context.Background(), "x", 999); !ok {
		t.Fatal("oversized max_results must be clamped, not rejected")
	}
}

func TestWebSearchFallsBackToLiteEngine(t *testing.T) {
	withSearchGuard(t)
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(searchLiteFixture))
	}))
	defer fallback.Close()
	withSearchEndpoints(t, primary.URL+"/html/", fallback.URL+"/lite/")

	a := newTestApp(t)
	out, ok := a.toolWebSearch(context.Background(), "net http", 0)
	if !ok {
		t.Fatalf("fallback engine must be tried: %s", out)
	}
	if !strings.Contains(out, "URL: https://pkg.go.dev/net/http") || !strings.Contains(out, "Package http provides") {
		t.Fatalf("lite results missing:\n%s", out)
	}
}

func TestWebSearchFailsWhenEveryEngineFails(t *testing.T) {
	withSearchGuard(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer srv.Close()
	withSearchEndpoints(t, srv.URL+"/html/", srv.URL+"/lite/")

	a := newTestApp(t)
	out, ok := a.toolWebSearch(context.Background(), "anything", 0)
	if ok || !strings.HasPrefix(out, "ERROR: web_search failed") {
		t.Fatalf("all-engine failure must return an ERROR result, got ok=%v: %s", ok, out)
	}
	if out, ok := a.toolWebSearch(context.Background(), "   ", 0); ok || !strings.Contains(out, "query is empty") {
		t.Fatalf("empty query must be rejected: %s", out)
	}
}

func TestWebSearchPermissionGateIsPerHost(t *testing.T) {
	a := newTestApp(t)
	// پیش‌فرض: بپرس (مثل web_fetch).
	if d := toolDefaultDecision(webSearchToolName, false); d != permAsk {
		t.Fatalf("default decision = %v, want ask", d)
	}
	// اجازه‌ی سراسری باید رد شود (همان کانال نشت داده‌ی web_fetch).
	if err := a.SetToolPermission(webSearchToolName, "allow"); err == nil {
		t.Fatal("tool-wide allow for web_search must be rejected")
	}
	a.settings.Permissions.Tools = map[string]string{webSearchToolName: "allow"}
	if d := a.toolPermDecisionLocked(webSearchToolName); d != permAsk {
		t.Fatalf("stored tool-wide allow must still evaluate to ask, got %v", d)
	}
	// deny باید کار کند.
	a.settings.Permissions.Tools = map[string]string{webSearchToolName: "deny"}
	if d := a.toolPermDecisionLocked(webSearchToolName); d != permDeny {
		t.Fatalf("stored deny must be honored, got %v", d)
	}
	// ابزار در فهرست موازی/زیرایجنت نیست (مثل web_fetch).
	if isParallelReadOnlyTool(webSearchToolName) || subagentAllowedTools[webSearchToolName] {
		t.Fatal("web_search must not be parallel/subagent-allowed")
	}
}

func TestWebSearchToolSpecAndValidation(t *testing.T) {
	found := false
	for _, d := range agentToolDefs() {
		if d.Function.Name == webSearchToolName {
			found = true
			if d.Function.Parameters == nil {
				t.Fatal("web_search parameters missing")
			}
		}
	}
	if !found {
		t.Fatal("web_search is not registered in agentToolDefs")
	}
	if _, ok := toolArgSpecs[webSearchToolName]; !ok {
		t.Fatal("web_search is missing toolArgSpecs validation")
	}
	if err := validateToolArgs(webSearchToolName, map[string]interface{}{"max_results": float64(3)}); err == nil {
		t.Fatal("missing required query must fail validation")
	}
	if err := validateToolArgs(webSearchToolName, map[string]interface{}{"query": "ok"}); err != nil {
		t.Fatalf("valid args rejected: %v", err)
	}
	if !strings.Contains(agentSystemPrompt("/tmp/ws"), "web_search") {
		t.Fatal("system prompt must advertise web_search")
	}
}

func TestNormalizeSearchResultURL(t *testing.T) {
	cases := map[string]string{
		"//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F": "https://go.dev/doc/",
		"https://example.com/a?b=1":                              "https://example.com/a?b=1",
		"//example.com/x":                                        "https://example.com/x",
		"javascript:alert(1)":                                    "",
		"https://user:pass@example.com/":                         "",
		"https://duckduckgo.com/about":                           "",
		"":                                                       "",
	}
	for in, want := range cases {
		if got := normalizeSearchResultURL(in); got != want {
			t.Errorf("normalizeSearchResultURL(%q) = %q, want %q", in, got, want)
		}
	}
	u := webSearchRequestURL("go docs")
	if !strings.HasPrefix(u, "https://html.duckduckgo.com/") || !strings.Contains(u, "q=go+docs") {
		t.Fatalf("request URL wrong: %s", u)
	}
}
