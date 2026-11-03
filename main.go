// MyAI — دستیار هوشمند کدنویسی (اپلیکیشن دسکتاپ native)
//
// ساخته‌شده با Wails v2 (Go + WebView سیستمی؛ بدون Electron و بدون وابستگی
// به مرورگر خارجی). رابط کاربری کاملاً فارسی و راست‌به‌چپ است و از
// frontend/dist سرو می‌شود.
package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// version در زمان ساخت با ldflags قابل بازنویسی است:
//
// -ldflags "-X main.version=1.0.0"
var version = "1.7.1"

func main() {
	app := NewApp()

	// مسیر دادهٔ WebView2 را داخل همان پوشه‌ی config قرار می‌دهیم تا
	// Wails پوشه‌ی جداگانه‌ی %APPDATA%\MyAI.exe نسازد و کش کنار settings بماند.
	webviewDataPath := ""
	if cfg, err := computeConfigDir(); err == nil {
		webviewDataPath = filepath.Join(cfg, "webview")
		_ = os.MkdirAll(webviewDataPath, 0o755)
	}

	err := wails.Run(&options.App{
		Title:     "MyAI — دستیار هوشمند کدنویسی",
		Width:     1280,
		Height:    832,
		MinWidth:  980,
		MinHeight: 640,
		// شروع تمام‌صفحه — پنجره در بزرگ‌ترین اندازه‌ی مانیتور باز
		// می‌شود (مثل VS Code). F11 هم به‌صورت همه‌جانبه در دسترس است
		// (ToggleFullscreen از فرانت‌اند صدا زده می‌شود).
		WindowStartState: options.Maximised,
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 23, A: 1},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
			Theme:                windows.Dark,
			// مسیر سفارشی تا %APPDATA%\MyAI.exe ساخته نشود
			WebviewUserDataPath: webviewDataPath,
		},
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "MyAI: %v\n", err)
		os.Exit(1)
	}

	_ = version
}
