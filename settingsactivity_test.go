// تست تنظیم «گزارش زنده» — پیش‌فرض روشن، ذخیره‌ی ماندگار و سازگاری با
// فایل تنظیمات قدیمی که کلید showActivity ندارد.
package main

import (
	"os"
	"testing"
)

func TestShowActivityDefaultsOn(t *testing.T) {
	if !defaultSettings().ShowActivity {
		t.Fatal("گزارش زنده باید به‌صورت پیش‌فرض روشن باشد")
	}
	a := newTestApp(t)
	v, ok := a.SettingsView()["showActivity"].(bool)
	if !ok || !v {
		t.Fatalf("SettingsView باید showActivity=true بدهد، دریافت: %#v", a.SettingsView()["showActivity"])
	}
}

func TestShowActivityTogglePersists(t *testing.T) {
	a := newTestApp(t)
	if err := a.SetShowActivity(false); err != nil {
		t.Fatalf("SetShowActivity: %v", err)
	}
	if v := a.SettingsView()["showActivity"].(bool); v {
		t.Fatal("پس از خاموش‌کردن، SettingsView باید false بدهد")
	}
	// بارگذاری دوباره از دیسک باید همان مقدار را برگرداند
	if err := a.loadSettings(); err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if a.SettingsView()["showActivity"].(bool) {
		t.Fatal("مقدار خاموش باید ماندگار باشد")
	}
	if err := a.SetShowActivity(true); err != nil {
		t.Fatalf("SetShowActivity: %v", err)
	}
	if !a.SettingsView()["showActivity"].(bool) {
		t.Fatal("روشن‌کردن دوباره باید در SettingsView دیده شود")
	}
}

// فایل تنظیمات قدیمی (بدون کلید showActivity) نباید نوار را ناخواسته خاموش کند.
func TestShowActivityKeepsDefaultForOldSettingsFile(t *testing.T) {
	a := newTestApp(t)
	p, err := a.settingsPath()
	if err != nil {
		t.Fatalf("settingsPath: %v", err)
	}
	if err := os.WriteFile(p, []byte(`{"model":"m","temperature":0.5}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	if err := a.loadSettings(); err != nil {
		t.Fatalf("loadSettings: %v", err)
	}
	if !a.SettingsView()["showActivity"].(bool) {
		t.Fatal("نبودِ کلید در فایل قدیمی باید به پیش‌فرض روشن برگردد")
	}
}
