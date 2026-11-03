// تست‌های همگامی بایندینگ‌های Wails — رفع F5).
//
// با build «-skipbindings»، فایل‌های frontend/wailsjs سورسِ دست‌نگهداری‌شده
// هستند (پروتکل ARCHITECTURE.md): هر متد exported جدیدِ App باید در App.js و
// App.d.ts اضافه شود و هر نوع main.X ارجاع‌شده در App.d.ts باید کلاسی در
// models.ts داشته باشد. این تست آن قاعده را دروازه‌ی تست می‌کند تا فراموشی
// در فازهای بعدی، همان‌جا fail شود — نه در ماشین کاربر.
package main

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

var (
	bindingsJSPath    = "frontend/wailsjs/go/main/App.js"
	bindingsTSPath    = "frontend/wailsjs/go/main/App.d.ts"
	bindingsModelPath = "frontend/wailsjs/models.ts"

	bindingsFnRe    = regexp.MustCompile(`(?m)^export function (\w+)\(`)
	bindingsClassRe = regexp.MustCompile(`(?m)^\texport class (\w+) \{`)
	bindingsTypeRef = regexp.MustCompile(`main\.(\w+)`)
)

// boundMethodNames نام متدهای exported را با reflection می‌گیرد — همان
// چیزی که Wails به فرانت‌اند بایند می‌کند (بدون پارس سورس؛ مقاوم به جابه‌جایی
// کد بین فایل‌ها).
func boundMethodNames(t *testing.T) map[string]bool {
	t.Helper()
	appType := reflect.TypeOf(&App{})
	out := map[string]bool{}
	for i := 0; i < appType.NumMethod(); i++ {
		out[appType.Method(i).Name] = true
	}
	if len(out) == 0 {
		t.Fatal("no exported App methods found via reflection")
	}
	return out
}

// TestWailsBindingsFilesExist ensures the F5 deliverable stays in the repo.
func TestWailsBindingsFilesExist(t *testing.T) {
	for _, p := range []string{bindingsJSPath, bindingsTSPath, bindingsModelPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing bindings file %s (protocol: ARCHITECTURE.md, -skipbindings build): %v", p, err)
		}
	}
}

// TestWailsBindingsInSync مجموعه‌ی متدهای App را با App.js و App.d.ts مقایسه
// می‌کند: هیچ متدی نباید در هیچ سمتی جا افتاده باشد.
func TestWailsBindingsInSync(t *testing.T) {
	goMethods := boundMethodNames(t)

	jsSrc, err := os.ReadFile(bindingsJSPath)
	if err != nil {
		t.Fatal(err)
	}
	tsSrc, err := os.ReadFile(bindingsTSPath)
	if err != nil {
		t.Fatal(err)
	}

	jsSet := map[string]bool{}
	for _, m := range bindingsFnRe.FindAllStringSubmatch(string(jsSrc), -1) {
		jsSet[m[1]] = true
	}
	tsSet := map[string]bool{}
	for _, m := range bindingsFnRe.FindAllStringSubmatch(string(tsSrc), -1) {
		tsSet[m[1]] = true
	}

	for name := range goMethods {
		if !jsSet[name] {
			t.Errorf("method %s: missing from %s — add it (protocol: ARCHITECTURE.md)", name, bindingsJSPath)
		}
		if !tsSet[name] {
			t.Errorf("method %s: missing from %s — add it (protocol: ARCHITECTURE.md)", name, bindingsTSPath)
		}
	}
	for name := range jsSet {
		if !goMethods[name] {
			t.Errorf("%s exports %q which is not a bound App method (stale binding?)", bindingsJSPath, name)
		}
	}
	for name := range tsSet {
		if !goMethods[name] {
			t.Errorf("%s exports %q which is not a bound App method (stale binding?)", bindingsTSPath, name)
		}
	}
	if len(goMethods) != len(jsSet) || len(goMethods) != len(tsSet) {
		t.Fatalf("count mismatch: go=%d js=%d ts=%d", len(goMethods), len(jsSet), len(tsSet))
	}
}

// TestWailsBindingsModelTypesCovered هر ارجاع main.X در App.d.ts باید کلاس
// متناظر در models.ts داشته باشد.
func TestWailsBindingsModelTypesCovered(t *testing.T) {
	tsSrc, err := os.ReadFile(bindingsTSPath)
	if err != nil {
		t.Fatal(err)
	}
	modelsSrc, err := os.ReadFile(bindingsModelPath)
	if err != nil {
		t.Fatal(err)
	}

	classSet := map[string]bool{}
	for _, m := range bindingsClassRe.FindAllStringSubmatch(string(modelsSrc), -1) {
		classSet[m[1]] = true
	}
	if len(classSet) == 0 {
		t.Fatalf("no model classes found in %s (parser drift?)", bindingsModelPath)
	}

	// ارجاع‌های نوع در دکلریشن‌ها: Promise<main.X> / arg:main.X / main.X[]
	for _, line := range strings.Split(string(tsSrc), "\n") {
		if !strings.HasPrefix(line, "export function ") {
			continue
		}
		for _, m := range bindingsTypeRef.FindAllStringSubmatch(line, -1) {
			if !classSet[m[1]] {
				t.Errorf("type main.%s referenced in App.d.ts but no `export class %s` in models.ts", m[1], m[1])
			}
		}
	}
}
