// کالیبراسیون تخمین توکن با مصرف واقعی.
//
// estimateTokens تخمینی سرانگشتی است و برای فارسی (خط عربی) نزدیک به
// ۰٫۴ توکن به‌ازای هر نویسه معمولاً کم‌برآورد می‌شود. اینجا نسبتِ
// «توکن واقعی ÷ تخمین» به‌ازای هر گفتگو با میانگین متحرک نمایی (EMA)
// ردیابی می‌شود و فقط «درون منطق بودجه» اعمال می‌شود — آستانه‌ی
// فشرده‌سازی خودکار و بازیابی سرریز (chat.go با بودجه‌ی کالیبره‌شده
// compactHistoryForBudget را صدا می‌زند). خروجی estimateTokens برای
// بقیه‌ی مصرف‌کننده‌ها (نوار مصرف و …) دست‌نخورده می‌ماند.
//
// قفل: calibMu قفلی برگ (leaf) است — در زمان نگه‌داشتِ آن هیچ قفل دیگری
// گرفته نمی‌شود و هرگز در طول I/O نگه داشته نمی‌شود (در سند ترتیب قفل‌ها در
// ARCHITECTURE.md آمده). اندازه: LRU با سقف calibLRUCap گفتگو.
package main

import (
	"sync"
)

const (
	// calibMinRatio/calibMaxRatio محدوده‌ی اعتماد نسبت واقعی/تخمین
	calibMinRatio = 0.7
	calibMaxRatio = 2.5
	// calibAlpha وزن نمونه‌ی تازه در EMA (۰٫۳ → همگرایی سریع ولی نرم)
	calibAlpha = 0.3
	// calibDefaultPersian پیش‌فرضِ محافظه‌کارانه برای تاریخچه‌ی فارسی‌heavy
	// تا پیش از رسیدن اولین usage واقعی
	calibDefaultPersian = 1.15
	// minCalibEstimate تخمین‌های کوچک‌تر از این نادیده گرفته می‌شوند (نویز)
	minCalibEstimate = 200
	// calibLRUCap حداکثر گفتگوهای نگه‌داری‌شده در حافظه
	calibLRUCap = 64
	// calibArabicShare آستانه‌ی «فارسی‌heavy» برای پیش‌فرض (سهم نویسه‌های خط عربی)
	calibArabicShare = 0.30
)

// tokenCalib نسبتِ کالیبراسیون تخمین توکن به‌ازای هر گفتگو.
// نکته‌ی زبانی: سنجش «فارسی‌heavy» فقط شمارش نویسه است — هیچ نرمال‌سازی
// حروفی (ي/ی، ك/ک) یا ارقام انجام نمی‌شود.
type tokenCalib struct {
	mu    sync.Mutex
	order []string           // LRU: قدیمی‌ترین اول
	ratio map[string]float64 // convID → نسبت کالیبره‌شده
}

// globalTokenCalib حافظه‌ی بین‌نوبتی (تا restart)؛ مشترک بین گوروتین‌ها.
var globalTokenCalib = &tokenCalib{ratio: map[string]float64{}}

// touchLocked کلید را در LRU به انتها می‌برد و سرریز را اخراج می‌کند.
// باید با calibMu قفل‌شده صدا زده شود.
func (c *tokenCalib) touchLocked(id string) {
	for i, k := range c.order {
		if k == id {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, id)
	if len(c.order) > calibLRUCap {
		evict := c.order[0]
		c.order = c.order[1:]
		delete(c.ratio, evict)
	}
}

// observe یک دور واقعی را ثبت می‌کند: real = prompt_tokens همان درخواست،
// est = تخمین ما برای همان تاریخچه‌ی ارسال‌شده. دورهای بدون usage، با
// تخمین خیلی کوچک یا گفتگوی ناشناس نادیده گرفته می‌شوند. نمونه و نتیجه
// هر دو به [۰٫۷، ۲٫۵] کلمپ می‌شوند.
func (c *tokenCalib) observe(convID string, real, est int64) {
	if convID == "" || real <= 0 || est < minCalibEstimate {
		return
	}
	sample := float64(real) / float64(est)
	if sample < calibMinRatio {
		sample = calibMinRatio
	} else if sample > calibMaxRatio {
		sample = calibMaxRatio
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ratio == nil {
		c.ratio = map[string]float64{}
	}
	r, ok := c.ratio[convID]
	if !ok {
		r = sample // شروع EMA از خودِ نمونه‌ی اول
	} else {
		r = calibAlpha*sample + (1-calibAlpha)*r
	}
	if r < calibMinRatio {
		r = calibMinRatio
	} else if r > calibMaxRatio {
		r = calibMaxRatio
	}
	c.ratio[convID] = r
	c.touchLocked(convID)
}

// ratioFor نسبت کالیبراسیون برای این گفتگو: EMA اگر موجود است؛ وگرنه
// پیش‌فرض محافظه‌کارانه برای تاریخچه‌ی فارسی‌heavy و ۱٫۰ برای بقیه.
// نتیجه همیشه در [۰٫۷، ۲٫۵] است.
func (c *tokenCalib) ratioFor(convID string, history []chatMessage) float64 {
	if convID != "" {
		c.mu.Lock()
		r, ok := c.ratio[convID]
		if ok {
			c.touchLocked(convID)
		}
		c.mu.Unlock()
		if ok {
			return r
		}
	}
	if arabicScriptShare(history) > calibArabicShare {
		return calibDefaultPersian
	}
	return 1.0
}

// resetTokenCalibForTest حافظه‌ی کالیبراسیون را برای تست‌ها پاک می‌کند و
// مقدار قبلی را برای بازگرداندن برمی‌گرداند (جلوگیری از نشت وضعیت بین
// تست‌های همان پکیج).
func resetTokenCalibForTest() *tokenCalib {
	globalTokenCalib.mu.Lock()
	old := &tokenCalib{order: globalTokenCalib.order, ratio: globalTokenCalib.ratio}
	globalTokenCalib.order = nil
	globalTokenCalib.ratio = map[string]float64{}
	globalTokenCalib.mu.Unlock()
	return old
}

// restoreTokenCalibForTest مقدار قبلی را برمی‌گرداند (cleanup تست).
func restoreTokenCalibForTest(old *tokenCalib) {
	globalTokenCalib.mu.Lock()
	globalTokenCalib.order = old.order
	globalTokenCalib.ratio = old.ratio
	globalTokenCalib.mu.Unlock()
}

// arabicScriptShare سهم نویسه‌های خط عربی (U+0600–U+06FF) از کل نویسه‌های
// متن‌های تاریخچه — معیار «فارسی‌heavy بودن». یک پاس بدون تخصیص؛
// ZWNJ و نویسه‌های صفر-عرض جزو هیچ‌کدام از دو شمارش بلوک عربی نیستند.
func arabicScriptShare(history []chatMessage) float64 {
	var total, arabic int64
	count := func(s string) {
		for _, r := range s {
			total++
			if r >= 0x0600 && r <= 0x06FF {
				arabic++
			}
		}
	}
	for _, m := range history {
		count(historyContentString(m.Content))
		for _, tc := range m.ToolCalls {
			count(tc.Function.Name)
			count(tc.Function.Arguments)
		}
	}
	if total == 0 {
		return 0
	}
	return float64(arabic) / float64(total)
}

// calibratedBudget بودجه‌ی مؤثر پس از کالیبراسیون:
// مقایسه‌ی «تخمین × نسبت» با بودجه‌ی خام هم‌ارزِ مقایسه‌ی «تخمین» با
// این بودجه است (تقسیم؛ نسبت همیشه ≥ ۰٫۷ و ≤ ۲٫۵). فقط منطق بودجه
// (آستانه‌ی فشرده‌سازی/بازیابی سرریز) از این بودجه استفاده می‌کند.
func calibratedBudget(budget int64, ratio float64) int64 {
	if budget <= 0 || ratio <= 0 {
		return budget
	}
	b := int64(float64(budget) / ratio)
	if b < 1 {
		b = 1
	}
	return b
}
