// تست‌های بخش مشترک shell (پلتفرم-مستقل): throttle استریم خروجی و بافر کران‌دار.
package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// TestThrottledEmitterRate: ارسال سریع → فقط ارسال اول + flush؛
// بعد از فاصله‌ی مجاز، ارسال بعدی. نرخ هرگز از ۱۰/ثانیه بیشتر نمی‌شود.
func TestThrottledEmitterRate(t *testing.T) {
	var mu sync.Mutex
	var sends []string
	e := newThrottledEmitter(100*time.Millisecond, 8*1024, func(chunk string) {
		mu.Lock()
		sends = append(sends, chunk)
		mu.Unlock()
	})

	// ۳۰ فراخوانی فوری → فقط ارسال اول (بقیه بافر می‌شوند)
	for i := 0; i < 30; i++ {
		e.emit([]byte("x"))
	}
	e.flush() // باقی‌مانده → ارسال دوم
	mu.Lock()
	n := len(sends)
	mu.Unlock()
	if n != 2 {
		t.Fatalf("rapid emits must produce exactly 2 sends (first + flush), got %d", n)
	}

	time.Sleep(120 * time.Millisecond) // فاصله‌ی مجاز بگذرد
	e.emit([]byte("y"))
	e.flush()
	mu.Lock()
	n = len(sends)
	total := 0
	for _, s := range sends {
		total += len(s)
	}
	mu.Unlock()
	if n != 3 {
		t.Fatalf("after interval expect 3rd send, got %d", n)
	}
	if total != 31 { // ۳۰ x + ۱ y — هیچ داده‌ای گم نشده
		t.Fatalf("content lost: %d bytes sent, want 31", total)
	}
}

// TestBoundedOutputBufferKeepsHeadAndTail: واحد boundedOutput به‌صورت مستقیم.
func TestBoundedOutputBufferKeepsHeadAndTail(t *testing.T) {
	b := &boundedOutput{}
	head := strings.Repeat("H", shellHeadBytes)
	mid := strings.Repeat("M", 1024)
	tail := strings.Repeat("T", shellTailBytes)
	_, _ = b.Write([]byte(head))
	for i := 0; i < 100; i++ {
		_, _ = b.Write([]byte(mid)) // ۱۰۰KB اضافی → حذف می‌شود
	}
	_, _ = b.Write([]byte(tail))
	s := b.String()
	if !strings.Contains(s, "truncated") {
		t.Fatal("marker missing")
	}
	if !strings.HasPrefix(s, "H") {
		t.Fatal("head lost")
	}
	if !strings.HasSuffix(s, "T") {
		t.Fatal("tail lost")
	}
	if b.dropped != 100*1024 {
		t.Fatalf("dropped=%d want %d", b.dropped, 100*1024)
	}
}
