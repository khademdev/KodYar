// تست‌های تجزیه‌گر مشترک SSE (readSSEChunks) — همان کدی که هم streamChat
// (گفتگوی اصلی) و هم runSubagent (زیرایجنت) استفاده می‌کنند.
//
// پوشش: چند chunk، [DONE]، خطوط آشغال، خط بلندتر از سقف ۱MB، لغو میان‌استریم
// و عبور بایت‌به‌بایت متن فارسی/نیم‌فاصله (ZWNJ).
package main

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"testing"
)

// sseTestLine یک خط data: با payload خام می‌سازد.
func sseTestLine(payload string) string { return "data: " + payload + "\n\n" }

func TestReadSSEChunksMultiChunkAndDone(t *testing.T) {
	stream := sseTestLine(`{"choices":[{"delta":{"content":"سلام `+"\u200c"+`دنیا"}}]}`) +
		": keep-alive comment\n" + // خط بدون data: → نادیده
		sseTestLine(`not-json at all`) + // JSON نامعتبر → نادیده
		sseTestLine(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"t1","function":{"name":"read_file","arguments":"{\"path\":\"a\"}"}}]},"finish_reason":"tool_calls"}]}`) +
		sseTestLine(`{"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7}}`) +
		sseTestLine(`[DONE]`) +
		sseTestLine(`{"choices":[{"delta":{"content":"AFTER-DONE"}}]}`)

	var contents []string
	var toolNames []string
	var prompt, completion int64
	finish := ""
	err := readSSEChunks(context.Background(), strings.NewReader(stream), func(c sseChunk) bool {
		if c.Usage != nil {
			prompt, completion = c.Usage.PromptTokens, c.Usage.CompletionTokens
		}
		for _, ch := range c.Choices {
			if ch.FinishReason != nil {
				finish = *ch.FinishReason
			}
			if ch.Delta.Content != "" {
				contents = append(contents, ch.Delta.Content)
			}
			for _, tc := range ch.Delta.ToolCalls {
				toolNames = append(toolNames, tc.Function.Name)
			}
		}
		return false
	})
	if err != nil {
		t.Fatalf("readSSEChunks: %v", err)
	}
	if len(contents) != 1 || contents[0] != "سلام \u200cدنیا" {
		t.Fatalf("persian payload must pass through byte-exact, got %q", contents)
	}
	if len(toolNames) != 1 || toolNames[0] != "read_file" {
		t.Fatalf("tool call chunk lost: %v", toolNames)
	}
	if prompt != 11 || completion != 7 {
		t.Fatalf("usage chunk lost: prompt=%d completion=%d", prompt, completion)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish_reason = %q", finish)
	}
	// هرچه بعد از [DONE] آمده نباید خوانده شود
	if strings.Contains(strings.Join(contents, ""), "AFTER-DONE") {
		t.Fatal("[DONE] must stop the reader")
	}
}

func TestReadSSEChunksStopCallback(t *testing.T) {
	stream := sseTestLine(`{"error":{"message":"boom"}}`) +
		sseTestLine(`{"choices":[{"delta":{"content":"never"}}]}`)
	n := 0
	stopErr := readSSEChunks(context.Background(), strings.NewReader(stream), func(c sseChunk) bool {
		n++
		return c.Error != nil
	})
	if stopErr != nil {
		t.Fatalf("stop callback must not produce an error: %v", stopErr)
	}
	if n != 1 {
		t.Fatalf("reader must stop after the callback returned true, chunks=%d", n)
	}
}

func TestReadSSEChunksOverlongLine(t *testing.T) {
	// خط بلندتر از سقف ۱MB اسکنر — خطا (bufio.ErrTooLong) باید بازگردد
	huge := sseTestLine(`{"choices":[{"delta":{"content":"` + strings.Repeat("a", 1024*1024+64) + `"}}]}`)
	err := readSSEChunks(context.Background(), strings.NewReader(huge), func(sseChunk) bool { return false })
	if err == nil {
		t.Fatal("an over-long SSE line must fail, not be silently truncated")
	}
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("want bufio.ErrTooLong, got %v", err)
	}
}

func TestReadSSEChunksContextCancel(t *testing.T) {
	stream := sseTestLine(`{"choices":[{"delta":{"content":"one"}}]}`) +
		sseTestLine(`{"choices":[{"delta":{"content":"two"}}]}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := 0
	err := readSSEChunks(ctx, strings.NewReader(stream), func(sseChunk) bool {
		n++
		cancel() // لغو پس از نخستین chunk — خط بعدی نباید خوانده شود
		return false
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation must surface as context.Canceled, got %v", err)
	}
	if n != 1 {
		t.Fatalf("reader must stop at the next line after cancellation, chunks=%d", n)
	}

	// کانتکست از پیش لغوشده → هیچ chunkی تحویل داده نمی‌شود
	pre, preCancel := context.WithCancel(context.Background())
	preCancel()
	n = 0
	if err := readSSEChunks(pre, strings.NewReader(stream), func(sseChunk) bool { n++; return false }); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled ctx must return context.Canceled, got %v", err)
	}
	if n != 0 {
		t.Fatalf("pre-cancelled ctx must not deliver chunks, got %d", n)
	}
}
