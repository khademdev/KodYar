// مخزن گفتگوها — هر گفتگو یک فایل JSON جداگانه در پوشه‌ی تنظیمات است.
//
// عملکرد: برای اینکه GetConversations در هر بار (که بعد از هر پیام صدا زده
// می‌شود) مجبور به خواندن/پارس کامل همه‌ی فایل‌های گفتگو نباشد، یک فایل
// ایندکس سبک (‎.index.json‎) کنار گفتگوها نگه‌داری می‌شود. این ایندکس فقط
// متادیتا (ID، عنوان، زمان به‌روزرسانی، تعداد پیام) را نگه می‌دارد و در
// صورت خرابی/نبودن، به‌صورت خودکار از روی فایل‌ها بازسازی می‌شود (self-healing).
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// toolCallRecord فراخوانی ابزار مدل که روی پیام دستیار ذخیره می‌شود
// تا هنگام ساخت تاریخچه برای API، فرمت tool_calls بازسازی شود.
type toolCallRecord struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

// Message یک پیام داخل گفتگو است.
// Role یکی از: user | assistant | tool
type Message struct {
	ID        string   `json:"id"`
	Role      string   `json:"role"` // user | assistant | tool
	Content   string   `json:"content"`
	Thinking  string   `json:"thinking,omitempty"`  // استدلال مدل (reasoning / <think>) — نمایش جمع‌شونده در UI
	Attached  string   `json:"attached,omitempty"`  // کانتکست @mention — فقط برای مدل، در UI نمایش داده نمی‌شود
	Images    []string `json:"images,omitempty"`    // تصاویر پیام (data URL پایه۶۴) — Vision
	Pinned    bool     `json:"pinned,omitempty"`    // پیام سنجاق‌شده — همیشه در کانتکست مدل می‌ماند
	Compacted bool     `json:"compacted,omitempty"` // نتیجه‌ی ابزار هرس‌شده (نمایش در UI)
	At        int64    `json:"at"`                  // unix seconds
	Tokens    int64    `json:"tokens"`
	Aborted   bool     `json:"aborted,omitempty"`
	// Unfinished یعنی نوبت نه با توقف کاربر، بلکه با پر شدن سقف دورهای
	// ایجنت تمام شد و کار نیمه‌مانده است؛ UI برای همین حالت دکمهٔ
	// «ادامه بده» را نشان می‌دهد (بدون برچسب «متوقف شد»).
	Unfinished bool `json:"unfinished,omitempty"`
	// Mode حالت تولید پیام است: "agent" یا "chat" (پیام‌های قدیمی خالی‌اند).
	// UI از آن برای متمایزکردن حباب ایجنت از پاسخ معمولی استفاده می‌کند.
	Mode       string           `json:"mode,omitempty"`
	ToolName   string           `json:"toolName,omitempty"`   // فقط role=tool
	ToolArgs   string           `json:"toolArgs,omitempty"`   // فقط role=tool (JSON خام آرگومان‌ها)
	ToolCallID string           `json:"toolCallId,omitempty"` // فقط role=tool
	ToolOK     bool             `json:"toolOk,omitempty"`     // فقط role=tool
	ToolMs     int64            `json:"toolMs,omitempty"`     // فقط role=tool (زمان اجرا)
	ToolCalls  []toolCallRecord `json:"toolCalls,omitempty"`  // فقط role=assistant (فراخوانی‌های ابزار)
}

// Conversation یک گفتگوی کامل.
type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt int64     `json:"createdAt"`
	UpdatedAt int64     `json:"updatedAt"`
	Messages  []Message `json:"messages"`

	// TokensUsed مجموع توکن‌های مصرف‌شده فقط در «همین گفتگو»
	// (prompt+completion هر نوبت). از Providerها جداست و با چت جدید صفر است.
	TokensUsed int64 `json:"tokensUsed,omitempty"`
	// TokensExact اگر true باشد، TokensUsed فقط از usage رسمی API آمده
	// (نه تخمین محلی). اگر false/غایب → حداقل بخشی تخمینی است.
	TokensExact bool `json:"tokensExact,omitempty"`
	// ContextTokens تخمین حجم کانتکست فعلی گفتگو — همان عددِ نوار مصرف
	// (پر شدن پنجرهٔ کانتکست). یک مقدار مشتق‌شده است و در هر بارگذاری و هر
	// ذخیره از روی پیام‌ها بازمحاسبه می‌شود تا هرگز کهنه به UI نرسد؛
	// برخلاف TokensUsed صورتحساب API نیست.
	ContextTokens int64 `json:"contextTokens,omitempty"`

	// Todos چک‌لیست کارهای ایجنت (ابزار todo_write) — فیلد اختیاری:
	// گفتگوهای قدیمی که این فیلد را ندارند بدون خطا بارگذاری می‌شوند
	// (json.Unmarshal فیلد غایب را نادیده می‌گیرد) و گفتگوی تازه خالی است.
	Todos []TodoItem `json:"todos,omitempty"`

	// TurnSummaries خلاصه‌ی واقعیِ نوبت‌ها (۴.۲) — حداکثر ۵۰ موردِ اخیر؛
	// فیلد اختیاری برای سازگاری با گفتگوهای قدیمی.
	TurnSummaries []TurnSummary `json:"turnSummaries,omitempty"`
}

// ConversationMeta خلاصه‌ی گفتگو برای سایدبار.
type ConversationMeta struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt int64  `json:"updatedAt"`
	Count     int    `json:"count"`
}

// ---------------------------------------------------------------- ایندکس سبک

// conversationIndex فایل ایندکس متادیتای همه‌ی گفتگوها است.
//
// فلسفه‌ی طراحی:
// - فایل‌های گفتگو ممکن است بزرگ باشند (tool-resultهای ۱۶KB، متن‌های بلند).
// - GetConversations فقط به ID/عنوان/زمان/تعداد پیام نیاز دارد.
// - خواندن/پارس همه‌ی این فایل‌ها در هر بار → O(تعداد × اندازه) I/O.
// - یک ایندکس کوچک این کار را به O(۱ فایل کوچک + ۱ ReadDir) کاهش می‌دهد.
//
// اگر این فایل نباشد، خراب باشد یا نسخه‌اش قدیمی باشد، در GetConversations
// از روی فایل‌های گفتگو بازسازی می‌شود (self-healing).
type conversationIndex struct {
	Version int                `json:"version"`
	Entries []ConversationMeta `json:"entries"`
}

const (
	convIndexVersion = 1
	convIndexFile    = ".index.json"
)

func conversationsIndexPath(dir string) string {
	return filepath.Join(dir, convIndexFile)
}

// loadConversationIndex فایل ایندکس را می‌خواند.
// nil برمی‌گرداند اگر فایل نباشد، خراب باشد یا نسخه‌اش قدیمی باشد (یعنی
// GetConversations باید از روی فایل‌ها بازسازی کند).
func loadConversationIndex(dir string) *conversationIndex {
	raw, err := os.ReadFile(conversationsIndexPath(dir))
	if err != nil {
		return nil
	}
	var idx conversationIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil
	}
	if idx.Version != convIndexVersion {
		return nil
	}
	return &idx
}

// saveConversationIndex ایندکس را اتمیک می‌نویسد (tmp+fsync+rename — رفع F2).
// در صورت هر خطا، فایل ایندکس را پاک می‌کند تا GetConversations بعدی،
// از صفر بازسازی کند (self-healing).
func saveConversationIndex(dir string, idx *conversationIndex) {
	idx.Version = convIndexVersion
	raw, err := json.Marshal(idx) // MarshalIndent لازم نیست؛ ایندکس کوچک و ماشینی است
	if err != nil {
		_ = os.Remove(conversationsIndexPath(dir))
		return
	}
	final := conversationsIndexPath(dir)
	if err := atomicWriteFile(final, raw, 0o600); err != nil {
		// self-healing: ایندکس پاک شود تا GetConversations بعدی از روی فایل‌ها بسازد
		_ = os.Remove(final)
	}
}

// deleteConversationIndex فایل ایندکس را پاک می‌کند (برای self-healing).
func deleteConversationIndex(dir string) {
	_ = os.Remove(conversationsIndexPath(dir))
}

// buildIndexFromFiles ایندکس را از صفر با خواندن همه‌ی فایل‌های گفتگو می‌سازد.
// فقط در اولین اجرا یا هنگام خرابی/نبودِ ایندکس اجرا می‌شود.
func buildIndexFromFiles(dir string) *conversationIndex {
	entries, _ := os.ReadDir(dir)
	idx := &conversationIndex{
		Version: convIndexVersion,
		Entries: make([]ConversationMeta, 0, len(entries)),
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == convIndexFile || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var c Conversation
		if json.Unmarshal(raw, &c) != nil || !validConvID(c.ID) {
			// شناسه‌ی خالی یا نامعتبر (فایل کاشته‌شده) — چون app.js مقدار
			// data-id را بدون escape درج می‌کند، فقط شناسه‌های الگو‌دار
			// (حرف/رقم/-/_) به ایندکس راه پیدا می‌کنند.
			continue
		}
		idx.Entries = append(idx.Entries, ConversationMeta{
			ID: c.ID, Title: c.Title, UpdatedAt: c.UpdatedAt, Count: len(c.Messages),
		})
	}
	return idx
}

// ---------------------------------------------------------------- CRUD

func (a *App) conversationsDir() (string, error) {
	dir, err := a.configDir()
	if err != nil {
		return "", err
	}
	cdir := filepath.Join(dir, "conversations")
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		return "", err
	}
	return cdir, nil
}

// sweepStaleTempFiles فایل‌های موقتیِ جامانده از نوشتن‌های اتمیک نیم‌کاره را
// پاک می‌کند: اگر برنامه وسطِ «نوشتن tmp → rename» کرش کند، فایل.tmp روی
// دیسک می‌ماند. بی‌ضررند (خواننده‌ها فقط نام نهایی را می‌خوانند) ولی کلافه‌کننده
// و فضاخوری‌اند؛ راه‌اندازی، یک‌بار کل درخت config را بازگشتی جارو می‌کند.
// فقط فایل‌های با پسوند.tmp حذف می‌شوند (پوشه‌ها هرگز). best-effort —
// خطای I/O فقط همان مسیر را رد می‌کند. بازگشت: تعداد حذف‌شده‌ها.
func sweepStaleTempFiles(root string) int {
	removed := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // best-effort: خطا/پوشه → ادامه‌ی جارو
		}
		if strings.HasSuffix(d.Name(), ".tmp") {
			if os.Remove(path) == nil {
				removed++
			}
		}
		return nil
	})
	return removed
}

// validConvID فقط شناسه‌های ساخته‌شده توسط newID (حروف/عدد/خط‌تیره/زیرخط) را می‌پذیرد
// تا مقدار ورودی از فرانت‌اند (مثل "../x") نتواند از پوشه‌ی گفتگوها خارج شود.
func validConvID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return true
}

func convPath(dir, id string) string {
	return filepath.Join(dir, id+".json")
}

// loadConversation خواندن یک گفتگو از دیسک.
func (a *App) loadConversation(id string) (*Conversation, error) {
	if !validConvID(id) {
		return nil, errors.New("گفتگو پیدا نشد")
	}
	dir, err := a.conversationsDir()
	if err != nil {
		return nil, err
	}
	path := convPath(dir, id)
	raw, err := os.ReadFile(path)
	fromBackup := false
	if err != nil {
		// چرخش نیمه‌کاره‌ی .bak (کرش بین renameها) — فایل اصلی برمی‌گردد.
		if bak, bakErr := os.ReadFile(backupPath(path)); bakErr == nil {
			raw, fromBackup = bak, true
		} else {
			return nil, errors.New("گفتگو پیدا نشد")
		}
	}
	var c Conversation
	if jerr := json.Unmarshal(raw, &c); jerr != nil {
		// ۱) نسخه‌ی پشتیبان سالم؟ ۲) بازیابی پیام‌های کامل از فایل نیمه‌خراب.
		if bak, bakErr := os.ReadFile(backupPath(path)); bakErr == nil {
			var bc Conversation
			if json.Unmarshal(bak, &bc) == nil && bc.ID == id {
				c = bc
				moveToCorrupt(path)
			} else {
				return nil, jerr
			}
		} else {
			sv, n := salvageConversation(raw)
			if sv == nil || n == 0 {
				moveToCorrupt(path)
				return nil, jerr
			}
			c = *sv
			if c.ID == "" {
				c.ID = id
			}
			moveToCorrupt(path)
			// نسخه‌ی سالم‌شده بی‌درنگ روی دیسک نوشته می‌شود تا دفعه‌ی بعد
			// دوباره بازیابی لازم نباشد (فایل خراب در .corrupt-* می‌ماند).
			if raw2, merr := json.Marshal(&c); merr == nil {
				_ = atomicWriteFile(path, raw2, 0o600)
			}
			emitRecoveryNotice(a, id, conversationRecoveredNotice(n))
		}
	}
	if fromBackup {
		// فایل اصلی از دست رفته بود — از پشتیبان بازسازی می‌شود.
		if raw2, merr := json.Marshal(&c); merr == nil {
			_ = atomicWriteFile(path, raw2, 0o600)
		}
	}
	// بازگردانی تصاویر از فایل‌های جانبی (img://…) به data URL
	a.hydrateImagesLocked(dir, &c)
	// عدد نمایشی نوار مصرف — همیشه از روی پیام‌های همین لحظه محاسبه
	// می‌شود (مقدار ذخیره‌شده روی دیسک فقط نسخهٔ قبلی است).
	c.ContextTokens = conversationContextTokens(&c)
	return &c, nil
}

// imgRefPrefix شناسهٔ مرجع تصویر ذخیره‌شده روی دیسک (به‌جای data URL در JSON).
const imgRefPrefix = "img://"

// externalizeImagesLocked data URLهای بزرگ را به فایل‌های جدا در
// conversations/img/ می‌نویسد و در JSON فقط مرجع کوتاه نگه می‌دارد.
// نوشتن اتمیک است (atomicWriteFile — مثل خود JSON گفتگو): فایل موقت در
// همان پوشه → fsync → rename؛ پس از کرش سیستم یا در حین نوشتنِ هم‌زمان،
// خواننده همیشه تصویر کامل (قدیمی یا جدید) می‌بیند، نه نیمی‌نوشته.
// باید با a.mu قفل‌شده صدا زده شود.
func (a *App) externalizeImagesLocked(dir string, c *Conversation) {
	imgDir := filepath.Join(dir, "img")
	for i := range c.Messages {
		m := &c.Messages[i]
		if len(m.Images) == 0 {
			continue
		}
		for j, img := range m.Images {
			if img == "" || strings.HasPrefix(img, imgRefPrefix) {
				continue
			}
			if !strings.HasPrefix(img, "data:image/") {
				continue
			}
			if err := os.MkdirAll(imgDir, 0o755); err != nil {
				continue
			}
			name := fmt.Sprintf("%s-%d.b64", m.ID, j)
			if name == "-0.b64" || strings.HasPrefix(name, "-") {
				name = fmt.Sprintf("%s-%d.b64", c.ID, j)
			}
			path := filepath.Join(imgDir, name)
			if werr := atomicWriteFile(path, []byte(img), 0o600); werr != nil {
				continue
			}
			m.Images[j] = imgRefPrefix + name
		}
	}
}

// hydrateImagesLocked مرجعهای img:// را دوباره به data URL تبدیل می‌کند.
func (a *App) hydrateImagesLocked(dir string, c *Conversation) {
	imgDir := filepath.Join(dir, "img")
	for i := range c.Messages {
		m := &c.Messages[i]
		if len(m.Images) == 0 {
			continue
		}
		for j, img := range m.Images {
			if !strings.HasPrefix(img, imgRefPrefix) {
				continue
			}
			name := strings.TrimPrefix(img, imgRefPrefix)
			// فقط نام فایل ساده — جلوگیری از path traversal
			name = filepath.Base(name)
			raw, err := os.ReadFile(filepath.Join(imgDir, name))
			if err != nil {
				m.Images[j] = ""
				continue
			}
			m.Images[j] = string(raw)
		}
	}
}

// saveConversation نوشتن گفتگو روی دیسک (اتمیک؛ ابتدا در فایل موقت و سپس
// rename تا خواننده‌ی همزمان هرگز JSON ناقص نبیند) و به‌روزرسانی ایندکس
// تا GetConversations بعدی نیازی به خواندن این فایل نداشته باشد.
//
// سقف سخت حجم (MaxConvKB): عبور از سقف → فشرده‌سازی خودکار نتایج ابزار
// قدیمی؛ اگر باز هم بزرگ بود → حذف قدیمی‌ترین پیام‌ها (حداقل ۱۲ پیام
// می‌ماند). هشدار از ۷۵٪ سقف با رویداد conv:size_warn به UI می‌رسد.
//
// باید با a.mu قفل‌شده فراخوانی شود (همه‌ی callerهای فعلی این شرط را دارند).
func (a *App) saveConversation(c *Conversation) error {
	dir, err := a.conversationsDir()
	if err != nil {
		return err
	}
	c.UpdatedAt = time.Now().Unix()

	// تصاویر base64 را به فایل جانبی منتقل کن تا JSON گفتگو متورم نشود
	a.externalizeImagesLocked(dir, c)

	// سقف حجم — قبل از Marshal اعمال می‌شود
	capBytes := a.settings.MaxConvKB * 1024
	if capBytes > 0 {
		// تخمین سریع بدون Marshal کامل: اگر پیام‌ها زیاد و محتوا بلند است، فشرده کن
		if raw, merr := json.Marshal(c); merr == nil && len(raw) > capBytes {
			// مرحله ۱: فشرده‌سازی خودکار نتایج ابزار قدیمی
			a.compactConversationLocked(c)
			raw2, merr2 := json.Marshal(c)
			if merr2 == nil && len(raw2) > capBytes && len(c.Messages) > minKeepMessages {
				// مرحله ۲: حذف قدیمی‌ترین پیام‌ها تا زیر سقف (یک Marshal در هر دور)
				for len(c.Messages) > minKeepMessages {
					c.Messages = c.Messages[1:]
					raw3, merr3 := json.Marshal(c)
					if merr3 != nil || len(raw3) <= capBytes {
						break
					}
				}
			}
		}
	}

	// عدد نمایشی کانتکست پس از فشرده‌سازی/هرس احتمالی بازمحاسبه می‌شود تا
	// فایل ذخیره‌شده هرگز مقدار کهنه نداشته باشد.
	c.ContextTokens = conversationContextTokens(c)

	// Marshal به‌جای MarshalIndent: ۲-۳× سریع‌تر و فایل ۲۰-۳۰٪ کوچک‌تر.
	// (پیش‌تر MarshalIndent برای دیباگ انسانی بود، ولی هزینه‌اش در ذخیره‌ی
	// مکرر در دورهای ایجنت محسوس می‌شد.)
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	// نوشتن اتمیک با fsync (atomicWriteFile — رفع F2): فایل موقت در همان
	// پوشه → sync → rename؛ داده‌ی گفتگو پس از کرش سیستم سالم می‌ماند و
	// خواننده‌ی هم‌زمان هرگز JSON ناقص نمی‌بیند.
	if err := atomicWriteFileWithBackup(convPath(dir, c.ID), raw, 0o600); err != nil {
		// پیام فارسی واضح؛ فایل قبلی و گفتگوی در حافظه دست‌نخورده می‌مانند.
		return fmt.Errorf("ذخیره‌ی گفتگو روی دیسک ناموفق بود (فایل قبلی دست‌نخورده است): %w", err)
	}

	// هشدار نزدیک شدن به سقف (فقط یک‌بار برای هر گفتگو در هر اجرا)
	if capBytes > 0 && len(raw) > int(float64(capBytes)*sizeWarnFraction) {
		if a.sizeWarned == nil {
			a.sizeWarned = map[string]bool{}
		}
		// سقف اندازهٔ نقشه تا در نشست‌های طولانی بی‌رویه رشد نکند
		const sizeWarnedCap = 64
		if len(a.sizeWarned) >= sizeWarnedCap {
			a.sizeWarned = map[string]bool{}
		}
		if !a.sizeWarned[c.ID] {
			a.sizeWarned[c.ID] = true
			// emit بیرون از a.mu — saveConversation همیشه با قفل صدا زده
			// می‌شود و EventsEmit زیر قفل خلاف قاعده‌ی معماری است. این
			// goroutine یک‌بارمصرف است (حداکثر یک‌بار برای هر گفتگو در هر
			// اجرا؛ نقشه‌ی sizeWarned هم سقف ۶۴ دارد) و بلافاصله تمام می‌شود.
			go runtimeEmit(a, "conv:size_warn", map[string]interface{}{
				"conversationId": c.ID,
				"kb":             len(raw) / 1024,
				"capKb":          a.settings.MaxConvKB,
			})
		}
	}

	// به‌روزرسانی ایندکس (best-effort: در صورت خطا، self-healing انجام می‌شود).
	a.updateConversationIndexLocked(dir, c)

	// پس از ذخیره، data URLها را به حافظه برگردان تا UI و استریم بعدی
	// همچنان تصاویر واقعی داشته باشند (روی دیسک فقط مرجع img:// مانده).
	a.hydrateImagesLocked(dir, c)
	return nil
}

// updateConversationIndexLocked متای گفتگو را در ایندکس upsert می‌کند.
// باید با a.mu قفل‌شده فراخوانی شود.
//
// اگر ایندکس موجود نباشد (اولین ذخیره‌ی نسخه‌ی جدید، یا پس از self-healing)،
// فقط همین یک entry درج می‌شود. فایل‌های قدیمی‌تر که در ایندکس نیستند، در
// GetConversations بعدی به‌صورت خودکار (و به‌صورت افزایشی) کشف و اضافه می‌شوند.
func (a *App) updateConversationIndexLocked(dir string, c *Conversation) {
	idx := loadConversationIndex(dir)
	if idx == nil {
		idx = &conversationIndex{
			Version: convIndexVersion,
			Entries: []ConversationMeta{},
		}
	}
	meta := ConversationMeta{
		ID: c.ID, Title: c.Title, UpdatedAt: c.UpdatedAt, Count: len(c.Messages),
	}
	found := false
	for i := range idx.Entries {
		if idx.Entries[i].ID == c.ID {
			idx.Entries[i] = meta
			found = true
			break
		}
	}
	if !found {
		idx.Entries = append(idx.Entries, meta)
	}
	saveConversationIndex(dir, idx)
}

// GetConversations فهرست گفتگوها (جدیدترین اول).
//
// عملکرد بهینه:
// 1. یک os.ReadDir (ارزان: یک syscall) برای لیست نام فایل‌ها
// 2. خواندن فایل ایندکس کوچک (نه همه‌ی گفتگوها)
// 3. reconcile: فقط فایل‌های جدید خوانده و entryهای مرده حذف می‌شوند
// 4. اگر ایندکس خراب/نبود → بازسازی کامل (فقط یک‌بار)
func (a *App) GetConversations() []ConversationMeta {
	defer a.recoverBound("GetConversations")
	a.mu.Lock()
	defer a.mu.Unlock()
	dir, err := a.conversationsDir()
	if err != nil {
		return []ConversationMeta{}
	}

	// ۱. لیست فایل‌های موجود (بدون خواندن محتوا)
	dirEntries, _ := os.ReadDir(dir)
	fileIDs := make(map[string]bool, len(dirEntries))
	for _, e := range dirEntries {
		if e.IsDir() || e.Name() == convIndexFile || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		fileIDs[strings.TrimSuffix(e.Name(), ".json")] = true
	}

	// ۲. ایندکس؛ در صورت خرابی/نبود، بازسازی کامل
	idx := loadConversationIndex(dir)
	if idx == nil {
		idx = buildIndexFromFiles(dir)
		saveConversationIndex(dir, idx)
	} else {
		// ۳. reconcile کم‌هزینه
		changed := false

		// حذف entryهای بی‌فایل (فایلشان دستی پاک شده)
		kept := idx.Entries[:0]
		for _, m := range idx.Entries {
			if fileIDs[m.ID] {
				kept = append(kept, m)
			} else {
				changed = true
			}
		}
		idx.Entries = kept

		// افزودن فایل‌های ناشناخته (دستی اضافه شده یا ایندکس ناقص است).
		// فقط این فایل‌ها خوانده و پارس می‌شوند — نه همه‌ی گفتگوها.
		indexed := make(map[string]bool, len(idx.Entries))
		for _, m := range idx.Entries {
			indexed[m.ID] = true
		}
		for id := range fileIDs {
			if indexed[id] {
				continue
			}
			raw, rerr := os.ReadFile(convPath(dir, id))
			if rerr != nil {
				continue
			}
			var c Conversation
			if json.Unmarshal(raw, &c) != nil || !validConvID(c.ID) {
				continue // همان گارد مرز داده‌ی buildIndexFromFiles
			}
			idx.Entries = append(idx.Entries, ConversationMeta{
				ID: c.ID, Title: c.Title, UpdatedAt: c.UpdatedAt, Count: len(c.Messages),
			})
			changed = true
		}

		if changed {
			saveConversationIndex(dir, idx)
		}
	}

	// ۴. مرتب‌سازی و برگرداندن کپی (فرانت‌اند نباید به اسلایس داخلی دست بزند)
	out := make([]ConversationMeta, len(idx.Entries))
	copy(out, idx.Entries)
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// GetConversation یک گفتگوی کامل را برمی‌گرداند.
func (a *App) GetConversation(id string) (*Conversation, error) {
	defer a.recoverBound("GetConversation")
	a.mu.Lock()
	conv, err := a.loadConversation(id)
	var summaries []TurnSummary
	if err == nil && len(conv.TurnSummaries) > 0 {
		summaries = append([]TurnSummary(nil), conv.TurnSummaries...)
	}
	a.mu.Unlock()
	// با بازکردن گفتگو، خلاصه‌ی نوبت‌های ذخیره‌شده هم به UI می‌رسد تا
	// کارت «خلاصه‌ی واقعی این نوبت» دوباره ساخته شود (۴.۲). emit بیرون
	// از a.mu — قاعده‌ی معماری.
	if len(summaries) > 0 {
		runtimeEmit(a, "chat:turn_summaries", map[string]interface{}{
			"conversationId": id,
			"summaries":      summaries,
		})
	}
	return conv, err
}

// DeleteConversation حذف کامل یک گفتگو: فایل گفتگو، entry مربوطه در
// ایندکس و هشدار حجم آن از هر جا (دیسک + حافظه) پاک می‌شود.
//
// اگر همین گفتگو در حال پاسخ‌دهی باشد، ابتدا استریم متوقف و تا خروج کامل
// گوروتین منتظر می‌مانیم تا هیچ ذخیره‌ی دیرهنگام نتواند فایل را دوباره بسازد.
func (a *App) DeleteConversation(id string) error {
	defer a.recoverBound("DeleteConversation")
	if !validConvID(id) {
		return errors.New("شناسه‌ی گفتگو نامعتبر است")
	}
	// ابتدا (بدون نگه‌داشتن a.mu) پاسخ‌دهی فعال را متوقف کن تا قفل‌گذاری
	// معکوس (genMu → a.mu در SendMessage) باعث بن‌بست نشود.
	if id == a.activeConvID() {
		a.stopGenerationAndWait(5 * time.Second)
	}
	a.mu.Lock()
	dir, err := a.conversationsDir()
	if err != nil {
		a.mu.Unlock()
		return err
	}
	err = os.Remove(convPath(dir, id))
	if err != nil && !os.IsNotExist(err) {
		a.mu.Unlock()
		return err
	}
	// فایل موقت نیمه‌کاره‌ی احتمالی هم پاک شود
	_ = os.Remove(convPath(dir, id) + ".tmp")
	// تصاویر جانبی مربوط به این گفتگو (نام فایل با ID پیام/گفتگو)
	imgDir := filepath.Join(dir, "img")
	if entries, rerr := os.ReadDir(imgDir); rerr == nil {
		prefix := id + "-"
		for _, e := range entries {
			n := e.Name()
			if strings.HasPrefix(n, prefix) || strings.Contains(n, id) {
				_ = os.Remove(filepath.Join(imgDir, n))
			}
		}
	}
	// حذف از ایندکس (اگر موجود باشد)
	if idx := loadConversationIndex(dir); idx != nil {
		kept := idx.Entries[:0]
		for _, m := range idx.Entries {
			if m.ID != id {
				kept = append(kept, m)
			}
		}
		idx.Entries = kept
		saveConversationIndex(dir, idx)
	}
	// حذف هشدار حجم از حافظه
	delete(a.sizeWarned, id)
	a.mu.Unlock()

	// بیرون از a.mu: پاک‌سازی تاریخچهٔ تغییرات فایل و بکاپ‌های همان گفتگو
	// (فایل‌های واقعی پروژه دست نخورده می‌مانند) و رکوردهای «خوانده‌شده‌ی»
	// همان گفتگو (حافظه‌ی کران‌دار fileread.go).
	a.purgeChangesForConversation(id)
	a.readStore.dropConversation(id)
	return nil
}

// DeleteAllConversations حذف کامل همه‌ی گفتگوها: همه‌ی فایل‌ها، ایندکس و
// هشدارهای حجم — پاسخ‌دهی فعال هم مثل حذف تکی، با انتظار برای خروج کامل
// گوروتین متوقف می‌شود.
func (a *App) DeleteAllConversations() error {
	defer a.recoverBound("DeleteAllConversations")
	a.stopGenerationAndWait(5 * time.Second)
	a.mu.Lock()
	dir, err := a.conversationsDir()
	if err != nil {
		a.mu.Unlock()
		return err
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() && e.Name() != convIndexFile && (strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), ".json.tmp")) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	// پوشهٔ تصاویر جانبی Vision
	_ = os.RemoveAll(filepath.Join(dir, "img"))
	// ایندکس را پاک کن؛ GetConversations بعدی، آن را به‌صورت خالی بازسازی می‌کند.
	deleteConversationIndex(dir)
	// هشدارهای حجم از حافظه پاک شوند
	a.sizeWarned = map[string]bool{}
	a.mu.Unlock()

	// بیرون از a.mu: پاک‌سازی کامل تاریخچهٔ تغییرات فایل و بکاپ‌ها +
	// رکوردهای خواندن همه‌ی گفتگوها.
	a.purgeAllFileChanges()
	a.readStore.reset()
	return nil
}

// newID ساخت شناسه‌ی کوتاه یکتا.
func newID() string {
	return time.Now().Format("20060102-150405") + "-" + randSuffix(4)
}

// titleFromContent عنوان خودکار گفتگو از متن اولین پیام.
func titleFromContent(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 42 {
		r = r[:42]
		r = append(r, []rune("…")...)
	}
	t := strings.TrimSpace(string(r))
	if t == "" {
		t = "گفتگوی جدید"
	}
	return t
}

// conversationContextTokens تخمین حجم کانتکست فعلی گفتگو (جمع توکن پیام‌ها).
// برای نوار «پر شدن پنجرهٔ کانتکست» استفاده می‌شود — نه صورت‌حساب API.
func conversationContextTokens(c *Conversation) int64 {
	if c == nil {
		return 0
	}
	// فقط چیزی که واقعاً به مدل فرستاده می‌شود حساب می‌شود (نزدیک به prepareStream):
	//   - thinking ارسال نمی‌شود → شمرده نمی‌شود؛
	//   - Attached فقط برای آخرین پیام کاربر و پیام‌های سنجاق‌شده فرستاده می‌شود؛
	//   - آرگومان‌های tool_calls پیام دستیار بخشی از درخواست بعدی‌اند → شمرده می‌شوند.
	lastUser := -1
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == "user" {
			lastUser = i
			break
		}
	}
	var n int64
	for i, m := range c.Messages {
		// همیشه از خودِ متن تخمین زده می‌شود: m.Tokens صورتحسابِ همان پاسخ
		// (completion کل نوبت) است و برای نوبت‌های چنددوری ایجنت چند برابرِ
		// حجم واقعیِ متن می‌شد → دایره باد می‌کرد.
		n += estimateTokens(m.Content)
		if m.Attached != "" && (i == lastUser || m.Pinned) {
			n += estimateTokens(m.Attached)
		}
		for _, tc := range m.ToolCalls {
			n += estimateTokens(tc.Name) + estimateTokens(tc.Args)
		}
	}
	return n
}
