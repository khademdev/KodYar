// تست زندهٔ «میزان فکر کردن» — اختیاری و فقط با Provider واقعی.
//
//	default go test ./...  → skip (بدون شبکه)
//	KODYAR_EFFORT_BASE_URL=https://api.openai.com/v1 \
//	KODYAR_EFFORT_API_KEY=sk-... \
//	KODYAR_EFFORT_MODEL=gpt-5 \
//	go test -run TestThinkingEffortLive -v .
//
// همان وظیفهٔ کوتاه ایجنت دو بار اجرا می‌شود — یک‌بار با reasoning_effort=low
// و یک‌بار high — و توکن فکر کردن هر دو گزارش می‌شود تا معلوم شود این اهرم
// واقعاً چه مقدار صرفه‌جویی می‌کند. اگر Provider تعداد reasoning را در usage
// برنگرداند، از متن استدلال تخمین زده می‌شود؛ اگر هیچ استدلالی برنگردد،
// صفر گزارش می‌شود (خودِ نبودِ استدلال هم یک داده است).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type effortLiveResult struct {
	effort     string
	reasoning  int64
	completion int64
	answer     string
}

func TestThinkingEffortLive(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("KODYAR_EFFORT_BASE_URL"))
	apiKey := strings.TrimSpace(os.Getenv("KODYAR_EFFORT_API_KEY"))
	model := strings.TrimSpace(os.Getenv("KODYAR_EFFORT_MODEL"))
	if baseURL == "" || apiKey == "" || model == "" {
		t.Skip("live thinking-effort test: set KODYAR_EFFORT_BASE_URL, KODYAR_EFFORT_API_KEY and KODYAR_EFFORT_MODEL")
	}

	a := newTestApp(t)
	// همان پرامپت سیستمی و همان وظیفهٔ کوتاه در هر دو اجرا — فقط effort عوض می‌شود.
	task := "Reply with exactly: OK"
	messages := []chatMessage{
		{Role: "system", Content: agentSystemPrompt(a.settings.WorkspacePath)},
		{Role: "user", Content: task},
	}

	low := runEffortLive(t, baseURL, apiKey, model, messages, thinkingEffortLow)
	high := runEffortLive(t, baseURL, apiKey, model, messages, thinkingEffortHigh)

	t.Logf("reasoning_effort=low  → thinking tokens ~%d (completion %d, answer %q)", low.reasoning, low.completion, low.answer)
	t.Logf("reasoning_effort=high → thinking tokens ~%d (completion %d, answer %q)", high.reasoning, high.completion, high.answer)
	if high.reasoning > 0 {
		saved := float64(high.reasoning-low.reasoning) / float64(high.reasoning) * 100
		t.Logf("low vs high: %.0f%% fewer thinking tokens at low (%d vs %d)", saved, low.reasoning, high.reasoning)
	}
}

// runEffortLive یک درخواست واقعی (غیراستریمی) با effort مشخص می‌فرستد و
// توکن استدلال را برمی‌گرداند.
func runEffortLive(t *testing.T, baseURL, apiKey, model string, messages []chatMessage, effort string) effortLiveResult {
	t.Helper()
	body, err := json.Marshal(chatRequest{
		Model:           model,
		Messages:        messages,
		Stream:          false,
		MaxTokens:       200,
		ReasoningEffort: effort,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("live request (%s) failed: %v", effort, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("provider rejected reasoning_effort=%s with HTTP %d: %s", effort, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				Reasoning        string `json:"reasoning"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			CompletionTokens        int64 `json:"completion_tokens"`
			CompletionTokensDetails struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode provider reply (%s): %v (%s)", effort, err, strings.TrimSpace(string(raw)))
	}
	if out.Error != nil && out.Error.Message != "" {
		t.Fatalf("provider error (%s): %s", effort, out.Error.Message)
	}
	if len(out.Choices) == 0 {
		t.Fatalf("provider returned no choices (%s): %s", effort, strings.TrimSpace(string(raw)))
	}
	msg := out.Choices[0].Message
	reasoning := out.Usage.CompletionTokensDetails.ReasoningTokens
	if reasoning == 0 {
		// Providerهایی که reasoning جدا برنمی‌گردانند: تخمین از متن استدلال.
		reasoning = estimateTokens(msg.ReasoningContent + msg.Reasoning)
	}
	return effortLiveResult{
		effort:     effort,
		reasoning:  reasoning,
		completion: out.Usage.CompletionTokens,
		answer:     strings.TrimSpace(msg.Content),
	}
}
