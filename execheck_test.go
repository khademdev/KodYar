package main

// چک باینری ویندوزی: نسخهٔ 1.0.2 نشان داد که می‌توان نسخهٔ درست در UI و CHANGELOG
// داشت ولی exe بدون مانیفست (و در نتیجه بدون DPI per-monitor) یا با مانیفست
// قالب‌دارِ جاسازی‌شده منتشر شود. این تست همان دو شکست را پیش از انتشار می‌گیرد.
//
// با `KODYAR_EXE` مسیر exe داده می‌شود (CI بعد از بیلد) و با
// `KODYAR_EXPECTED_VERSION` نسخهٔ انتظاری (مثل `1.0.3`). بدون این متغیرها
// (مثلاً `go test ./...` معمولی) تست skip می‌شود، چون exe ساخته نشده است.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var vsFixedFileInfoSignature = []byte{0xBD, 0x04, 0xEF, 0xFE}

// exeFileVersion چهار جزء FileVersion را از resource نسخه (VS_FIXEDFILEINFO)
// می‌خواند؛ همان چیزی که Properties ویندوز نشان می‌دهد.
func exeFileVersion(data []byte) (major, minor, patch, build uint16, ok bool) {
	idx := bytes.Index(data, vsFixedFileInfoSignature)
	if idx < 0 || idx+16 > len(data) {
		return 0, 0, 0, 0, false
	}
	ms := binary.LittleEndian.Uint32(data[idx+8:])
	ls := binary.LittleEndian.Uint32(data[idx+12:])
	return uint16(ms >> 16), uint16(ms), uint16(ls >> 16), uint16(ls), true
}

func TestWindowsExecutableManifestAndVersion(t *testing.T) {
	path := os.Getenv("KODYAR_EXE")
	if path == "" {
		path = filepath.Join("build", "bin", "KodYar.exe")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no Windows executable at %s (build it first or set KODYAR_EXE)", path)
	}
	if len(data) < 1024 {
		t.Fatalf("%s looks truncated (%d bytes)", path, len(data))
	}

	// ۱) مانیفست جاسازی‌شده: بدون آن ویندوز exe را non-DPI-aware می‌داند.
	start := bytes.Index(data, []byte("<assembly"))
	if start < 0 {
		t.Fatalf("%s has no embedded application manifest (build.sh must pass -manifest)", path)
	}
	end := bytes.Index(data[start:], []byte("</assembly>"))
	if end < 0 {
		t.Fatalf("%s has a truncated manifest", path)
	}
	manifest := data[start : start+end]
	if !bytes.Contains(manifest, []byte("dpiAware")) {
		t.Errorf("manifest has no dpiAware setting")
	}

	// ۲) مانیفست باید رندرشده باشد؛ قالب خام باعث خطای side-by-side می‌شود.
	if bytes.Contains(manifest, []byte("{{")) {
		t.Errorf("manifest still contains template placeholders: %q",
			manifest[bytes.Index(manifest, []byte("{{")):][:24])
	}

	// ۳) FileVersion واقعی در resource نسخه.
	major, minor, patch, build, ok := exeFileVersion(data)
	if !ok {
		t.Fatalf("%s has no VS_FIXEDFILEINFO version resource", path)
	}
	if major == 0 && minor == 0 && patch == 0 && build == 0 {
		t.Fatalf("%s reports an empty FileVersion", path)
	}
	if want := os.Getenv("KODYAR_EXPECTED_VERSION"); want != "" {
		want = strings.TrimPrefix(want, "v")
		parts := strings.Split(want, ".")
		if len(parts) != 3 {
			t.Fatalf("KODYAR_EXPECTED_VERSION=%q must be MAJOR.MINOR.PATCH", want)
		}
		got := fmt.Sprintf("%d.%d.%d", major, minor, patch)
		if got != want {
			t.Errorf("FileVersion is %s (%.4d.%d.%d.%d), want %s", got, major, minor, patch, build, want)
		}
	}
}
