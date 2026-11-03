// تست‌های کالیبراسیون تخمین توکن.
//
// پوشش:
// 1. همگرایی EMA به نسبت واقعی و کلمپ [۰٫۷، ۲٫۵].
// 2. نادیده‌گرفتن دورهای بدون usage یا با تخمین کوچک.
// 3. پیش‌فرض‌ها: ۱٫۱۵ برای تاریخچه‌ی فارسی‌heavy، ۱٫۰ برای لاتین.
// 4. LRU: اخراج قدیمی‌ترین گفتگوها با سقف ۶۴.
// 5. بودجه‌ی کالیبره‌شده: هم‌ارزی «تخمین×نسبت در برابر بودجه».
// 6. فارسی‌heavy بودن آستانه‌ی فشرده‌سازی را زودتر می‌گذراند (نسبت به
// لاتین با شمارش نویسه‌ی یکسان).
// 7. هم‌زمانی: race-clean با گوروتین‌های موازی observe/ratioFor.
package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestTokenCalibConvergesAndClamps(t *testing.T) {
	old := resetTokenCalibForTest()
	defer restoreTokenCalibForTest(old)

	// همگرایی به نسبت ۲ (واقعی = دو برابر تخمین)
	c := &tokenCalib{ratio: map[string]float64{}}
	for i := 0; i < 30; i++ {
		c.observe("c1", 2000, 1000)
	}
	r := c.ratioFor("c1", nil)
	if r < 1.9 || r > 2.1 {
		t.Fatalf("ratio must converge to ~2.0, got %v", r)
	}

	// کلمپ بالا: واقعی = ۱۰ برابر → ۲٫۵
	c2 := &tokenCalib{ratio: map[string]float64{}}
	for i := 0; i < 10; i++ {
		c2.observe("c2", 10000, 1000)
	}
	if got := c2.ratioFor("c2", nil); got != calibMaxRatio {
		t.Fatalf("ratio must clamp to %v, got %v", calibMaxRatio, got)
	}

	// کلمپ پایین: واقعی = یک‌دهم → ۰٫۷
	c3 := &tokenCalib{ratio: map[string]float64{}}
	for i := 0; i < 10; i++ {
		c3.observe("c3", 100, 1000)
	}
	if got := c3.ratioFor("c3", nil); got != calibMinRatio {
		t.Fatalf("ratio must clamp to %v, got %v", calibMinRatio, got)
	}

	// usage غایب یا تخمین کوچک → نادیده
	before := c.ratioFor("c1", nil)
	c.observe("c1", 0, 1000)
	c.observe("c1", 500, 10)
	c.observe("", 1000, 1000)
	if after := c.ratioFor("c1", nil); after != before {
		t.Fatalf("ignored rounds must not change the ratio: %v -> %v", before, after)
	}
}

func TestTokenCalibDefaults(t *testing.T) {
	old := resetTokenCalibForTest()
	defer restoreTokenCalibForTest(old)

	// فارسی‌heavy (ZWNJ جزو شمارش بلوک عربی نیست ولی «سلام» هست) → ۱٫۱۵
	fa := []chatMessage{{Role: "user", Content: strings.Repeat("سلام می\u200cشود ", 50)}}
	if got := globalTokenCalib.ratioFor("unknown-fa", fa); got != calibDefaultPersian {
		t.Fatalf("Persian-heavy history must default to %v, got %v", calibDefaultPersian, got)
	}
	// لاتین → ۱٫۰
	en := []chatMessage{{Role: "user", Content: strings.Repeat("hello world ", 50)}}
	if got := globalTokenCalib.ratioFor("unknown-en", en); got != 1.0 {
		t.Fatalf("Latin history must default to 1.0, got %v", got)
	}
	// مخلوط زیر آستانه‌ی ۳۰٪ (۲ نویسه‌ی عربی از ۱۰) → ۱٫۰
	mixed := []chatMessage{{Role: "user", Content: strings.Repeat("س", 2) + strings.Repeat("x", 8)}}
	if got := globalTokenCalib.ratioFor("unknown-mix", mixed); got != 1.0 {
		t.Fatalf("history just under the Arabic-share threshold must default to 1.0, got %v", got)
	}
	// EMA موجود بر پیش‌فرض مقدم است
	c := &tokenCalib{ratio: map[string]float64{"known": 1.8}}
	if got := c.ratioFor("known", en); got != 1.8 {
		t.Fatalf("EMA must take precedence over the default, got %v", got)
	}
}

func TestTokenCalibLRUEviction(t *testing.T) {
	old := resetTokenCalibForTest()
	defer restoreTokenCalibForTest(old)

	c := &tokenCalib{ratio: map[string]float64{}}
	for i := 0; i < calibLRUCap+10; i++ {
		c.observe(fmt.Sprintf("c%d", i), 2000, 1000)
	}
	c.mu.Lock()
	size := len(c.ratio)
	_, oldest := c.ratio["c0"]
	_, newest := c.ratio[fmt.Sprintf("c%d", calibLRUCap+9)]
	c.mu.Unlock()
	if size > calibLRUCap {
		t.Fatalf("map must stay bounded at %d, got %d", calibLRUCap, size)
	}
	if oldest {
		t.Fatal("the oldest conversation must have been evicted")
	}
	if !newest {
		t.Fatal("the newest conversation must have been kept")
	}
}

func TestCalibratedBudgetEquivalence(t *testing.T) {
	// بودجه‌ی صفر/منفی دست‌نخورده؛ نسبت نامعتبر هم
	if b := calibratedBudget(0, 1.5); b != 0 {
		t.Fatalf("zero budget must stay zero, got %d", b)
	}
	if b := calibratedBudget(-5, 1.5); b != -5 {
		t.Fatalf("negative budget must stay, got %d", b)
	}
	if b := calibratedBudget(1000, 0); b != 1000 {
		t.Fatalf("zero ratio must not change the budget, got %d", b)
	}
	// r=1.0 باید دقیقاً بدون تغییر باشد (هر مقدار بودجه/آستانه)
	for _, budget := range []int64{1, 999, 2663, 50000, 1 << 20} {
		if b := calibratedBudget(budget, 1.0); b != budget {
			t.Fatalf("r=1.0 must be an exact no-op for budget %d, got %d", budget, b)
		}
	}
	// هم‌ارزی آستانه با r=1.0: est ≥ budget·۸۰٪ ⟺ est ≥ eff·۸۰٪ (بدون استثنا)
	budget := int64(50000)
	eff := calibratedBudget(budget, 1.0)
	for _, est := range []int64{1000, 20000, 40000, 60000} {
		if (est >= budget*compactTriggerPercent/100) != (est >= eff*compactTriggerPercent/100) {
			t.Fatalf("r=1.0 trigger equivalence broken at est=%d", est)
		}
	}
	// نسبت >۱ بودجه را کوچک می‌کند (آستانه زودتر می‌گذرد) و <۱ بزرگ
	if calibratedBudget(1000, 2.0) >= 1000 {
		t.Fatal("ratio 2.0 must shrink the effective budget")
	}
	if calibratedBudget(1000, 0.7) <= 1000 {
		t.Fatal("ratio 0.7 must grow the effective budget")
	}
	// کلمپ‌ها: نسبت مجاز همیشه در [۰٫۷، ۲٫۵] است پس بودجه در [budget/2.5, budget/0.7]
	if eff := calibratedBudget(1000, 2.5); eff < 1000/3 || eff > 1000/2 {
		t.Fatalf("r=2.5 must roughly halve the budget, got %d", eff)
	}
}

// TestTokenCalibPersianTriggersEarlier دو تاریخچه با شمارش نویسه‌ی یکسان:
// فارسی (پیش‌فرض ۱٫۱۵) آستانه‌ی فشرده‌سازی را رد می‌کند ولی لاتین (۱٫۰) نه.
func TestTokenCalibPersianTriggersEarlier(t *testing.T) {
	old := resetTokenCalibForTest()
	defer restoreTokenCalibForTest(old)

	fa := strings.Repeat("س", 1000)
	en := strings.Repeat("s", 1000)
	estFA := estimateTokens(fa) // 400
	estEN := estimateTokens(en) // 375
	if estFA <= estEN {
		t.Fatalf("sanity: Persian estimate must not be below Latin for the same rune count: %d vs %d", estFA, estEN)
	}
	histFA := []chatMessage{{Role: "user", Content: fa}}
	histEN := []chatMessage{{Role: "user", Content: en}}
	rFA := globalTokenCalib.ratioFor("persian-conv", histFA)
	rEN := globalTokenCalib.ratioFor("latin-conv", histEN)
	if rFA <= rEN {
		t.Fatalf("Persian default ratio must exceed Latin: %v vs %v", rFA, rEN)
	}
	// بودجه‌ای که فارسی را فعال می‌کند ولی لاتین را نه:
	// کالیبره‌ی فارسی = 400×1.15 = 460 ≥ آستانه؛ لاتین = 375 < آستانه.
	budget := int64(550)
	trig := budget * compactTriggerPercent / 100 // 440
	if !(int64(float64(estFA)*rFA) >= trig) {
		t.Fatalf("Persian-heavy history must trigger compaction: est=%d r=%v trig=%d", estFA, rFA, trig)
	}
	if int64(float64(estEN)*rEN) >= trig {
		t.Fatalf("Latin history with identical rune count must NOT trigger yet: est=%d r=%v trig=%d", estEN, rEN, trig)
	}
}

// TestTokenCalibRace هم‌زمانی observe/ratioFor روی همان کلیدها — race-clean.
func TestTokenCalibRace(t *testing.T) {
	old := resetTokenCalibForTest()
	defer restoreTokenCalibForTest(old)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := fmt.Sprintf("race-%d", g%3)
			for i := 0; i < 200; i++ {
				globalTokenCalib.observe(id, int64(1000+i), 1000)
				globalTokenCalib.ratioFor(id, nil)
				globalTokenCalib.ratioFor("other", []chatMessage{{Role: "user", Content: "سلام"}})
			}
		}(g)
	}
	wg.Wait()
}
