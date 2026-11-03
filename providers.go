// مدیریت Providerهای کاربر — هر سرویس‌دهنده‌ی سازگار با API نوع OpenAI را
// کاربر خودش تعریف می‌کند (نام + Base URL + کلید API).
//
// از نسخه‌ی ۱.3.0 هیچ Provider از پیش تعریف‌شده‌ای وجود ندارد؛ نسخه‌های
// قدیمی که Providerهای پیش‌فرض (OpenAI، Groq و…) در تنظیماتشان مانده،
// یک‌بار هنگام بارگذاری مهاجرت می‌شوند (stripBuiltinProviders در settings.go).
package main

import (
	"errors"
	"strings"
)

// builtinProviderSignatures امضای Providerهای پیش‌فرض نسخه‌های قدیمی —
// فقط برای مهاجرت یک‌باره‌ی تنظیمات قدیمی به کار می‌رود.
// کلید: ID — مقدار: BaseURL (مقایسه‌ی دقیق؛ ورودی سفارشی‌شده با URL متفاوت دست نمی‌خورد).
func builtinProviderSignatures() map[string]string {
	return map[string]string{
		"openai":  "https://api.openai.com/v1",
		"groq":    "https://api.groq.com/openai/v1",
		"hormoz":  "https://api.hormoz.ai/v1",
		"ollama":  "http://localhost:11434/v1",
		"mistral": "https://api.mistral.ai/v1",
	}
}

// findProvider با شناسه، Provider را برمی‌گرداند.
func (a *App) findProvider(id string) *Provider {
	for i := range a.settings.Providers {
		if a.settings.Providers[i].ID == id {
			return &a.settings.Providers[i]
		}
	}
	return nil
}

// GetProviders فهرست Providerها را برمی‌گرداند (کپی امن بدون alias به state داخلی).
func (a *App) GetProviders() []Provider {
	a.mu.Lock()
	defer a.mu.Unlock()
	return copyProviders(a.settings.Providers)
}

// SaveProvider یک Provider جدید اضافه یا موجود را ویرایش می‌کند.
// apiKey: "" → کلید قبلی حفظ می‌شود
//
// "-" → کلید قبلی پاک می‌شود
// بقیه → رمزنگاری و ذخیره می‌شود (همه‌چیز همان‌جا که Provider تعریف می‌شود)
//
// Provider ذخیره‌شده برگردانده می‌شود تا فرانت‌اند بتواند آن را فعال کند.
func (a *App) SaveProvider(p Provider, apiKey string) (Provider, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimSpace(strings.TrimRight(p.BaseURL, "/"))
	if p.Name == "" {
		return Provider{}, errors.New("نام Provider الزامی است")
	}
	if p.BaseURL == "" {
		return Provider{}, errors.New("Base URL الزامی است (مثلاً https://api.example.com/v1)")
	}
	if !strings.HasPrefix(p.BaseURL, "http://") && !strings.HasPrefix(p.BaseURL, "https://") {
		return Provider{}, errors.New("Base URL باید با http:// یا https:// شروع شود")
	}
	// مرز داده: شناسه‌ی وارده (مسیر ویرایش) و رنگ باید همان الگوی امنِ
	// ساخته‌شده توسط UI باشند — درخواست ساختگی/فایل دست‌کاری‌شده رد می‌شود.
	if p.ID != "" && !providerIDSafe(p.ID) {
		return Provider{}, errors.New("شناسه‌ی Provider نامعتبر است")
	}
	if p.Color != "" && !providerColorSafe(p.Color) {
		return Provider{}, errors.New("رنگ Provider باید کد هگز معتبر باشد (مثل #7c6cf0)")
	}
	if p.Color == "" {
		p.Color = "#7c6cf0"
	}
	if p.ContextWindow <= 0 {
		p.ContextWindow = 200000
	}
	if len(p.Models) == 0 {
		return Provider{}, errors.New("حداقل یک نام مدل وارد کنید (مثلاً gpt-4o)")
	}

	// شناسه‌ی خودکار برای Provider تازه
	if p.ID == "" {
		slug := slugify(p.Name)
		id := slug
		for n := 2; a.findProvider(id) != nil; n++ {
			id = slug + "-" + itoa(n)
		}
		p.ID = id
		a.settings.Providers = append(a.settings.Providers, p)
	} else {
		idx := -1
		for i := range a.settings.Providers {
			if a.settings.Providers[i].ID == p.ID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return Provider{}, errors.New("Provider پیدا نشد")
		}
		a.settings.Providers[idx] = p
	}

	// کلید API — همان‌جا که Provider تعریف می‌شود ذخیره می‌شود
	if apiKey == "-" {
		a.settings.APIKeys[p.ID] = ""
	} else if strings.TrimSpace(apiKey) != "" {
		enc, err := encryptString(strings.TrimSpace(apiKey))
		if err != nil {
			return Provider{}, err
		}
		a.settings.APIKeys[p.ID] = enc
	}

	if err := a.saveSettings(); err != nil {
		return Provider{}, err
	}
	return p, nil
}

// DeleteProvider یک Provider را حذف می‌کند. حذف آخرین Provider هم مجاز است
// (کاربر خودش Providerهایش را تعریف می‌کند)؛ در این صورت سرویس‌دهنده‌ی فعال
// پاک می‌شود و تا تعریف Provider بعدی، ارسال پیام با پیام راهنما رد می‌شود.
func (a *App) DeleteProvider(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	idx := -1
	for i := range a.settings.Providers {
		if a.settings.Providers[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errors.New("Provider پیدا نشد")
	}
	a.settings.Providers = append(a.settings.Providers[:idx], a.settings.Providers[idx+1:]...)
	delete(a.settings.APIKeys, id)
	if a.settings.Provider == id {
		a.settings.Provider = ""
		a.settings.Model = ""
		// اگر Provider دیگری مانده، اولین مورد فعال می‌شود
		if len(a.settings.Providers) > 0 {
			first := a.settings.Providers[0]
			a.settings.Provider = first.ID
			if len(first.Models) > 0 {
				a.settings.Model = first.Models[0]
			}
		}
	}
	return a.saveSettings()
}

// TestProvider یک درخواست کوچک برای اطمینان از درستی آدرس و کلید می‌فرستد.
func (a *App) TestProvider(providerID, model string) error {
	a.mu.Lock()
	p := a.findProvider(providerID)
	if p == nil {
		a.mu.Unlock()
		return errors.New("Provider پیدا نشد")
	}
	base := p.BaseURL
	key := ""
	if !p.NoKey {
		key = a.decryptKey(providerID)
	}
	a.mu.Unlock()

	if model == "" {
		a.mu.Lock()
		model = a.settings.Model
		a.mu.Unlock()
	}
	if strings.TrimSpace(model) == "" {
		return errors.New("نام مدل را وارد کنید")
	}
	return quickPing(base, key, model)
}
