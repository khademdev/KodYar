// تست: تعویض مدل نباید «دستور سیستمی» سفارشی کاربر را پاک کند.
//
// SaveConnection از انتخاب‌گر مدل چت‌باکس با environment ذخیره‌شده صدا زده
// می‌شود (که همیشه غیرخالی است). اگر هر بار پرامپت پیش‌فرض همان محیط را
// دوباره بنویسد، هر تعویض مدل متن دست‌نویس کاربر را بی‌صدا از بین می‌برد.
package main

import "testing"

func TestSaveConnectionKeepsCustomSystemPrompt(t *testing.T) {
	a := newTestApp(t)

	const custom = "دستور سیستمی سفارشیِ من — این متن نباید با تعویض مدل عوض شود."
	a.mu.Lock()
	a.settings.Environment = "general"
	a.settings.SystemPrompt = custom
	a.mu.Unlock()

	// همان محیط (مسیر عادیِ انتخاب مدل از چت‌باکس) → پرامپت دست‌نخورده
	if err := a.SaveConnection("ollama", "qwen2.5-coder", "general", ""); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	got, model := a.settings.SystemPrompt, a.settings.Model
	a.mu.Unlock()
	if model != "qwen2.5-coder" {
		t.Fatalf("model must change, got %q", model)
	}
	if got != custom {
		t.Fatalf("switching model must keep the custom system prompt; got %q", got)
	}

	// تغییر واقعی محیط، پرامپت پیش‌فرض همان محیط را می‌نشاند (رفتار قبلی حفظ شود)
	if err := a.SaveConnection("ollama", "llama3.2", "developer", ""); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	env, prompt := a.settings.Environment, a.settings.SystemPrompt
	a.mu.Unlock()
	if env != "developer" {
		t.Fatalf("environment must switch to developer, got %q", env)
	}
	if prompt != promptForEnvironment("developer") {
		t.Fatalf("changing environment must install its preset prompt, got %q", prompt)
	}
}
