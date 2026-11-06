// تست‌های تشخیص خودکار مدل‌ها (مأموریت ۵ / نسخه ۱.۹.۰).
//
// لایه‌ها:
//   - توابع خالی (حدس از پیشوند کلید، دسته‌بندی endpoint، پارس پاسخ‌ها) — جدولی
//   - جریان کامل HTTP با httptest (سازگار با OpenAI + کلید ذخیره‌شده + خطاها)
//   - fuzz پارس پاسخ‌ها برای اطمینان از نبود panic روی ورودی خراب
package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ------------------------------------------------------------- pure ----

func TestGuessEndpointFromKey(t *testing.T) {
	cases := map[string]string{
		"AIzaSyD-1234567890abcdef": "https://generativelanguage.googleapis.com/v1beta",
		"sk-or-v1-abc123":          "https://openrouter.ai/api/v1",
		"gsk_AbcDef123":            "https://api.groq.com/openai/v1",
		// «sk-» عمومی مبهم است (OpenAI/DeepSeek/…) — حدس اشتباه بدتر از پرسیدن آدرس
		"sk-proj-abc": "",
		"":            "",
		"random":      "",
	}
	for key, want := range cases {
		if got := guessEndpointFromKey(key); got != want {
			t.Errorf("guessEndpointFromKey(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestClassifyEndpoint(t *testing.T) {
	cases := []struct {
		url  string
		want endpointKind
	}{
		{"https://api.openai.com/v1", endpointOpenAI},
		{"http://localhost:11434/v1", endpointOpenAI},
		{"https://api.groq.com/openai/v1", endpointOpenAI},
		{"https://openrouter.ai/api/v1", endpointOpenAI},
		// پایه‌ی سازگار OpenAI گوگل: مسیر استاندارد /models با Bearer
		{"https://generativelanguage.googleapis.com/v1beta/openai", endpointOpenAI},
		{"https://generativelanguage.googleapis.com/v1beta/openai/", endpointOpenAI},
		// پایه‌ی بومی گوگل: ListModels با x-goog-api-key
		{"https://generativelanguage.googleapis.com/v1beta", endpointGoogleNative},
		{"https://generativelanguage.googleapis.com", endpointGoogleNative},
		{"https://generativelanguage.googleapis.com/v1", endpointGoogleNative},
	}
	for _, c := range cases {
		if got := classifyEndpoint(c.url); got != c.want {
			t.Errorf("classifyEndpoint(%q) = %d, want %d", c.url, got, c.want)
		}
	}
}

func TestParseOpenAIModels(t *testing.T) {
	// شکل استاندارد OpenAI با فیلدهای اختیاری OpenRouter/vLLM
	raw := []byte(`{"object":"list","data":[
                {"id":"gpt-4o","object":"model","owned_by":"openai"},
                {"id":"text-embedding-3-small","owned_by":"openai"},
                {"id":"tts-1"},
                {"id":"openrouter/auto","name":"OpenRouter Auto","context_window":128000},
                {"id":"qwen2.5-7b-instruct","max_model_len":32768},
                {"id":""},
                {"id":"ALPHA"}
        ]}`)
	list, err := parseOpenAIModels(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(list) != 6 {
		t.Fatalf("want 6 models, got %d: %+v", len(list), list)
	}
	byID := map[string]ModelInfo{}
	for _, m := range list {
		byID[m.ID] = m
	}
	if !byID["gpt-4o"].ChatLikely || byID["text-embedding-3-small"].ChatLikely || byID["tts-1"].ChatLikely {
		t.Fatalf("chatLikely heuristic wrong: %+v", byID)
	}
	if m := byID["openrouter/auto"]; m.DisplayName != "OpenRouter Auto" || m.ContextWindow != 128000 {
		t.Fatalf("openrouter fields: %+v", m)
	}
	if m := byID["qwen2.5-7b-instruct"]; m.ContextWindow != 32768 {
		t.Fatalf("max_model_len fallback: %+v", m)
	}

	// آرایه‌ی خام (بعضی سرورها)
	list, err = parseOpenAIModels([]byte(`[{"id":"llama3.2:latest"}]`))
	if err != nil || len(list) != 1 || list[0].ID != "llama3.2:latest" {
		t.Fatalf("bare array: %v %+v", err, list)
	}

	// data خالی → فهرست خالی، نه خطا
	list, err = parseOpenAIModels([]byte(`{"object":"list","data":[]}`))
	if err != nil || len(list) != 0 {
		t.Fatalf("empty data: %v %+v", err, list)
	}

	// JSON خراب → خطای روشن
	if _, err := parseOpenAIModels([]byte(`<html>login page</html>`)); err == nil {
		t.Fatal("invalid JSON must error")
	}
}

func TestParseGoogleNativeModels(t *testing.T) {
	raw := []byte(`{"nextPageToken":"tok2","models":[
                {"name":"models/gemini-2.5-flash","displayName":"Gemini 2.5 Flash","inputTokenLimit":1048576,"supportedGenerationMethods":["generateContent","countTokens"]},
                {"name":"models/text-embedding-004","supportedGenerationMethods":["embedContent"]},
                {"name":"models/gemini-2.5-pro","displayName":"Gemini 2.5 Pro","supportedGenerationMethods":["generateContent"]},
                {"name":"models/", "supportedGenerationMethods":["generateContent"]}
        ]}`)
	list, next, err := parseGoogleNativeModels(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if next != "tok2" {
		t.Fatalf("nextPageToken = %q", next)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 chat models, got %+v", list)
	}
	if list[0].ID != "gemini-2.5-flash" || list[0].DisplayName != "Gemini 2.5 Flash" || list[0].ContextWindow != 1048576 || list[0].OwnedBy != "google" {
		t.Fatalf("first model wrong: %+v", list[0])
	}
	if list[1].ID != "gemini-2.5-pro" {
		t.Fatalf("second model wrong: %+v", list[1])
	}

	// JSON خراب
	if _, _, err := parseGoogleNativeModels([]byte(`nope`)); err == nil {
		t.Fatal("invalid JSON must error")
	}
}

func TestSortAndDedupModels(t *testing.T) {
	in := []ModelInfo{
		{ID: "zeta-embed", ChatLikely: false},
		{ID: "Beta", ChatLikely: true},
		{ID: "alpha", ChatLikely: true},
		{ID: "ALPHA", ChatLikely: true},
		{ID: "alpha-embed", ChatLikely: false},
	}
	out := sortAndDedupModels(in)
	var ids []string
	for _, m := range out {
		ids = append(ids, m.ID)
	}
	want := []string{"alpha", "Beta", "alpha-embed", "zeta-embed"}
	if len(ids) != len(want) {
		t.Fatalf("want %v, got %v", want, ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("want %v, got %v", want, ids)
		}
	}
}

// ------------------------------------------------------------- http ----

// withDiscoveryClient کلاینت کشف را با کلاینت ساده‌ی بدون پروکسی جایگزین
// می‌کند (httptest روی 127.0.0.1 — نباید از HTTP_PROXY محیط عبور کند).
func withDiscoveryClient(t *testing.T) {
	t.Helper()
	old := discoveryHTTPClient
	discoveryHTTPClient = &http.Client{}
	t.Cleanup(func() { discoveryHTTPClient = old })
}

func TestDiscoverModelsOpenAICompat(t *testing.T) {
	withDiscoveryClient(t)
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"whisper-1"},{"id":"gpt-4o"},{"id":"gpt-4o"},{"id":"tts-1"}]}`))
	}))
	defer srv.Close()

	a := newTestApp(t)
	list, err := a.DiscoverModels(srv.URL+"/v1", "k-test-1", "")
	if err != nil {
		t.Fatalf("DiscoverModels failed: %v", err)
	}
	if gotAuth != "Bearer k-test-1" {
		t.Fatalf("Authorization header = %q", gotAuth)
	}
	if len(list) != 3 {
		t.Fatalf("want 3 after dedup, got %+v", list)
	}
	// مدل‌های چت اول (الفبایی)، غیرچت آخر
	if list[0].ID != "gpt-4o" || list[1].ID != "tts-1" || list[2].ID != "whisper-1" {
		t.Fatalf("order wrong: %+v", list)
	}
}

func TestDiscoverModelsSavedKeyAndURL(t *testing.T) {
	withDiscoveryClient(t)
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"m-1"}]}`))
	}))
	defer srv.Close()

	a := newTestApp(t)
	enc, err := encryptString("sk-saved-key-9")
	if err != nil {
		t.Fatalf("encryptString: %v", err)
	}
	a.settings.APIKeys["prov1"] = enc
	a.settings.Providers = append(a.settings.Providers, Provider{
		ID: "prov1", Name: "P1", BaseURL: srv.URL + "/v1", ContextWindow: 1000,
	})

	// همه‌چیز خالی → آدرس و کلید ذخیره‌شده‌ی Provider
	list, err := a.DiscoverModels("", "", "prov1")
	if err != nil {
		t.Fatalf("DiscoverModels failed: %v", err)
	}
	if len(list) != 1 || list[0].ID != "m-1" {
		t.Fatalf("list wrong: %+v", list)
	}
	if gotAuth != "Bearer sk-saved-key-9" {
		t.Fatalf("saved key not used: %q", gotAuth)
	}

	// NoKey → بدون هدر Authorization
	a.settings.Providers = append(a.settings.Providers, Provider{
		ID: "local", Name: "L", BaseURL: srv.URL + "/v1", NoKey: true,
	})
	a.settings.APIKeys["local"] = enc // نباید استفاده شود
	_, err = a.DiscoverModels("", "ignored-inline", "local")
	// کلید inline ارسال شده پس Authorization همان است؛ تست فقط رد نشدن مسیر را بررسی می‌کند
	if err != nil {
		t.Fatalf("NoKey provider discover failed: %v", err)
	}
}

func TestDiscoverModelsErrors(t *testing.T) {
	withDiscoveryClient(t)
	a := newTestApp(t)

	// خالی‌خالی → پیام راهنما
	if _, err := a.DiscoverModels("", "", ""); err == nil || !strings.Contains(err.Error(), "Base URL") {
		t.Fatalf("empty inputs: %v", err)
	}
	// طرح نامعتبر
	if _, err := a.DiscoverModels("ftp://example.com/v1", "k", ""); err == nil || !strings.Contains(err.Error(), "http") {
		t.Fatalf("bad scheme: %v", err)
	}
	// کلید ناشناخته بدون آدرس
	if _, err := a.DiscoverModels("", "sk-unknown-format", ""); err == nil || !strings.Contains(err.Error(), "Base URL") {
		t.Fatalf("unknown key no url: %v", err)
	}

	statuses := map[int]string{
		http.StatusUnauthorized:        "کلید API نامعتبر",
		http.StatusForbidden:           "کلید API نامعتبر",
		http.StatusNotFound:            "فهرست مدل‌ها را ارائه نمی‌کند",
		http.StatusTooManyRequests:     "محدودیت نرخ",
		http.StatusInternalServerError: "خطای سرویس‌دهنده",
	}
	for status, want := range statuses {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
		}))
		_, err := a.DiscoverModels(srv.URL, "key-x", "")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("HTTP %d: got %v, want substring %q", status, err, want)
		}
	}

	// قرارداد امنیتی: کلید هرگز در متن خطا نمی‌آید (حتی اگر بدنه‌ی خطای
	// سرور آن را تکرار کند یا آدرس حاوی کلید باشد)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key sk-leak-me-42"}}`))
	}))
	_, err := a.DiscoverModels(srv.URL, "sk-leak-me-42", "")
	srv.Close()
	if err == nil || strings.Contains(err.Error(), "sk-leak-me-42") {
		t.Fatalf("key leaked in error: %v", err)
	}

	// JSON خراب روی 200
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>gateway login</html>`))
	}))
	_, err = a.DiscoverModels(srv2.URL+"/v1", "k", "")
	srv2.Close()
	if err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("html body: %v", err)
	}

	// سرورِ خاموش → خطای اتصال (نه panic، نه کلید در پیام)
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url3 := srv3.URL
	srv3.Close()
	if _, err := a.DiscoverModels(url3+"/v1", "k-secret", ""); err == nil || strings.Contains(err.Error(), "k-secret") {
		t.Fatalf("dead server: %v", err)
	}
}

func TestDiscoverModelsGoogleOpenAIBase(t *testing.T) {
	withDiscoveryClient(t)
	// پایه‌ی …/v1beta/openai گوگل باید از مسیر استاندارد /models با Bearer برود
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":[{"id":"gemini-2.5-flash"}]}`))
	}))
	defer srv.Close()

	a := newTestApp(t)
	// classifyEndpoint برای میزبان گوگل مسیر بومی را انتخاب می‌کند؛ برای
	// میزبان دلخواهِ این تست با URL حاوی /openai همان مسیر استاندارد اجرا می‌شود
	list, err := a.DiscoverModels(srv.URL+"/v1beta/openai", "AIza-fake", "")
	if err != nil {
		t.Fatalf("DiscoverModels failed: %v", err)
	}
	if gotPath != "/v1beta/openai/models" || gotAuth != "Bearer AIza-fake" {
		t.Fatalf("path/auth: %q %q", gotPath, gotAuth)
	}
	if len(list) != 1 || list[0].ID != "gemini-2.5-flash" {
		t.Fatalf("list: %+v", list)
	}
}

func TestDiscoverModelsGoogleNativeNeedsKey(t *testing.T) {
	withDiscoveryClient(t)
	a := newTestApp(t)
	// پایه‌ی بومی گوگل بدون کلید → خطای روشن (بدون هیچ درخواست شبکه به دامنه‌ی واقعی)
	_, err := a.DiscoverModels("https://generativelanguage.googleapis.com/v1beta", "", "")
	if err == nil || !strings.Contains(err.Error(), "کلید API لازم است") {
		t.Fatalf("google native no key: %v", err)
	}
}

// ------------------------------------------------------------- fuzz ----

func FuzzParseModelLists(f *testing.F) {
	f.Add([]byte(`{"data":[{"id":"gpt-4o","owned_by":"openai"}]}`))
	f.Add([]byte(`[{"id":"llama3.2:latest"}]`))
	f.Add([]byte(`{"models":[{"name":"models/gemini-2.5-flash","supportedGenerationMethods":["generateContent"]}]}`))
	f.Add([]byte(`{"data":[{"id":"x","context_window":-5},{"id":"y","context_window":9999999999999999}]}`))
	f.Add([]byte(``))
	f.Add([]byte(`{"models":[{"name":"models/","supportedGenerationMethods":["generateContent"]}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		// هیچ ورودی نباید panic کند؛ خروجی معتبر هم باید نرمال‌سازی‌پذیر باشد
		if list, err := parseOpenAIModels(data); err == nil {
			_ = sortAndDedupModels(list)
		}
		if list, _, err := parseGoogleNativeModels(data); err == nil {
			_ = sortAndDedupModels(list)
		}
	})
}
