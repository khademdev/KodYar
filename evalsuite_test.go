// هارنس ارزیابی ایجنت — ۱۵ سناریوی کوچک با ریپوی نمونه و معیار موفقیت.
//
// چرا: تا امروز هر تغییر در ابزارها/پرامپت/دروازه‌ها «حس بهتری داد» ولی
// هیچ عددی نبود که نشان دهد نرخ موفقیت، تعداد دور، مصرف توکن یا
// انتخاب‌های اشتباه ابزار بهتر شد یا بدتر. این هارنس همان اعداد را تولید
// می‌کند و روی همان حلقهٔ واقعی ایجنت اجرا می‌شود (newE2E + SendMessage)،
// فقط مدل «نمایش‌نامه‌ای» است تا اجرا قطعی، آفلاین و رایگان بماند.
//
// اجرا (حالت نمایش‌نامه‌ای، آفلاین و قطعی):
//
//	KODYAR_EVAL=1 go test -run TestAgentEvalSuite -v .
//
// اجرا با مدل واقعی (کیفیت مدل سنجیده شود، نه فیلم‌نامه):
//
//	KODYAR_EVAL=1 KODYAR_EVAL_LIVE=1 \
//	  KODYAR_EVAL_BASE_URL=https://api.example.com/v1 \
//	  KODYAR_EVAL_MODEL=gpt-4.1-mini KODYAR_EVAL_API_KEY=... \
//	  go test -run TestAgentEvalSuite -v -timeout 60m .
//
// KODYAR_EVAL_BASE_URL و KODYAR_EVAL_MODEL اجباری‌اند؛ کلید از KODYAR_EVAL_API_KEY
// خوانده می‌شود و اگر نبود از OPENAI_API_KEY (سرویس محلی بدون کلید: خالی
// بگذارید). مهلت هر نوبت با KODYAR_EVAL_TURN_TIMEOUT_SEC (پیش‌فرض ۶۰۰) و
// مسیر گزارش با KODYAR_EVAL_OUT تعیین می‌شود. سناریوهای صرفاً فیلم‌نامه‌ای
// (MockOnly) در حالت زنده با پیام skip کنار گذاشته می‌شوند.
//
// مقایسهٔ دو گزارش JSON:
//
//	KODYAR_EVAL_COMPARE=old.json,new.json go test -run TestAgentEvalCompare -v .
//
// حالت پیش‌فرض (بدون KODYAR_EVAL) این تست سریع skip می‌شود تا CI عادی سبک
// بماند؛ تست TestAgentEvalScenariosAreWellFormed و تست مقایسهٔ گزارش‌ها
// همیشه اجرا می‌شوند.
//
// معیارها (هر سناریو):
//   - موفقیت: همهٔ انتظارهای فایل + ابزارهای الزامی + نبودِ ابزارهای ممنوع
//   - سقف دور + (در صورت تعیین) راستی‌آزمایی موفق در خلاصهٔ نوبت.
//   - نرخ موفقیت: درصد سناریوهای موفق.
//   - تعداد دور: از TurnTiming.Rounds (دورهای واقعی مدل).
//   - توکن: از Conversation.TokensUsed همین نوبت (usage واقعی مدل).
//   - ابزار اشتباه: فراخوانی ابزارهای ممنوع (Forbid) + تعداد فراخوانی‌های
//     ناموفق (ToolOK=false) به‌صورت متریک جدا.
//
// حالت mock به‌طور عمدی رفتار «مدل معمولی» را نمایش‌نامه می‌کند — از جمله
// مسیرهای نادرست (نوشتن بدون خواندن، خروج از workspace، حلقهٔ تکراری) —
// تا سنجیده شود اسکلت ایجنت (دروازه‌ها، بودجه، پرامپت) آن‌ها را مهار
// می‌کند یا نه.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------- سناریو

// evalWant انتظار یک فایل پس از پایان نوبت.
type evalWant struct {
	Path        string
	Contains    []string
	NotContains []string
	Absent      bool // فایل نباید وجود داشته باشد
}

// evalScenario یک سناریوی ارزیابی.
type evalScenario struct {
	Name    string
	Prompt  string
	Files   map[string]string
	Steps   []scenarioStep
	Expect  []evalWant
	Require []string // ابزارهایی که باید صدا زده شوند
	Forbid  []string // ابزارهایی که نباید صدا زده شوند
	// MaxRounds سقف دور مجاز؛ ۰ = ۱۲.
	MaxRounds int
	// MinToolErrors حداقل تعداد فراخوانی ناموفق (اثبات فعال‌شدن یک دروازه).
	MinToolErrors int
	// VerifyOK خلاصهٔ نوبت باید راستی‌آزمایی موفق گزارش کند.
	VerifyOK bool
	// SetMaxRounds سقف دور ایجنت را تغییر می‌دهد (سناریوی بودجه).
	SetMaxRounds int
	// MockOnly سناریوهایی که فقط با فیلم‌نامه معنا دارند (مثلاً حلقهٔ تکراری
	// ساختگی)؛ در حالت مدل واقعی skip می‌شوند. MinToolErrors هم فقط در
	// حالت نمایش‌نامه‌ای سنجیده می‌شود: مدل واقعی حق دارد اول read_file کند
	// یا از تلاش بیرون از پوشه امتناع کند — هر دو رفتار بهتر، نه بدتر.
	MockOnly bool
}

// evalRow یک سطر گزارش.
type evalRow struct {
	Name      string
	Passed    bool
	Rounds    int
	Tools     int
	Tokens    int64
	Wrong     int // ابزار ممنوع
	ToolErrs  int // فراخوانی ناموفق
	UsedNames []string
	Ms        int64
	Fail      string
}

// evalScenarios فهرست سناریوها (۱۵ مورد).
func evalScenarios() []evalScenario {
	goMod := "module eval\n\ngo 1.21\n"
	calc := "package eval\n\nfunc Add(a, b int) int { return a - b }\n\nfunc Mul(a, b int) int { return a * b }\n"
	calcTest := "package eval\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatalf(\"Add = %d\", Add(2, 3))\n\t}\n}\n"

	return []evalScenario{
		{
			Name:   "fix-arithmetic-bug",
			Prompt: "باگ تابع Add در calc.go را درست کن و تست را اجرا کن.",
			Files:  map[string]string{"go.mod": goMod, "calc.go": calc, "calc_test.go": calcTest},
			Steps: []scenarioStep{
				stepToolUsage("c1", "read_file", `{"path":"calc.go"}`, 900, 40),
				stepToolUsage("c2", "edit_file", `{"path":"calc.go","old_string":"return a - b","new_string":"return a + b"}`, 1000, 60),
				stepToolUsage("c3", "run_command", `{"command":"go test ./..."}`, 1100, 50),
				stepText("باگ اصلاح شد و تست‌ها سبز است."),
			},
			Expect:    []evalWant{{Path: "calc.go", Contains: []string{"return a + b"}}},
			Require:   []string{"read_file", "edit_file", "run_command"},
			Forbid:    []string{"write_file"},
			MaxRounds: 6,
			VerifyOK:  true,
		},
		{
			Name:   "create-new-file",
			Prompt: "یک فایل NOTES.md با سه نکته درباره پروژه بساز.",
			Files:  map[string]string{"go.mod": goMod, "main.go": "package eval\n"},
			Steps: []scenarioStep{
				stepTool("c1", "write_file", `{"path":"NOTES.md","content":"# Notes\n\n- یک\n- دو\n- سه\n"}`),
				stepText("فایل NOTES.md ساخته شد."),
			},
			Expect:    []evalWant{{Path: "NOTES.md", Contains: []string{"# Notes", "- سه"}}},
			Require:   []string{"write_file"},
			MaxRounds: 4,
		},
		{
			Name:   "read-before-write-guard",
			Prompt: "متن greeting در app.go را به «سلام» تغییر بده.",
			Files:  map[string]string{"go.mod": goMod, "app.go": "package eval\n\nconst Greeting = \"hi\"\n"},
			Steps: []scenarioStep{
				// نوشتن بدون خواندن: دروازه باید رد کند (خطای ابزار).
				stepTool("c1", "write_file", `{"path":"app.go","content":"package eval\n\nconst Greeting = \"سلام\"\n"}`),
				stepTool("c2", "read_file", `{"path":"app.go"}`),
				stepTool("c3", "edit_file", `{"path":"app.go","old_string":"const Greeting = \"hi\"","new_string":"const Greeting = \"سلام\""}`),
				stepText("تغییر اعمال شد."),
			},
			Expect:        []evalWant{{Path: "app.go", Contains: []string{`"سلام"`}}},
			Require:       []string{"read_file", "edit_file"},
			MaxRounds:     6,
			MinToolErrors: 1, // اثبات اینکه دروازهٔ read-before-write واقعا فعال شد
		},
		{
			Name:   "multi-edit-same-file",
			Prompt: "هر دو تابع را درست کن: Add جمع و Mul تفریق نباشد.",
			Files: map[string]string{
				"go.mod": goMod,
				"ops.go": "package eval\n\nfunc Add(a, b int) int { return a - b }\n\nfunc Sub(a, b int) int { return a + b }\n",
			},
			Steps: []scenarioStep{
				stepTool("c1", "read_file", `{"path":"ops.go"}`),
				stepTool("c2", "edit_file", `{"path":"ops.go","edits":[{"old_string":"func Add(a, b int) int { return a - b }","new_string":"func Add(a, b int) int { return a + b }"},{"old_string":"func Sub(a, b int) int { return a + b }","new_string":"func Sub(a, b int) int { return a - b }"}]}`),
				stepText("هر دو تابع اصلاح شدند."),
			},
			Expect:    []evalWant{{Path: "ops.go", Contains: []string{"func Add(a, b int) int { return a + b }", "func Sub(a, b int) int { return a - b }"}}},
			Require:   []string{"read_file", "edit_file"},
			MaxRounds: 5,
		},
		{
			Name:   "apply-patch-add-function",
			Prompt: "تابع Div را به mathx.go اضافه کن.",
			Files:  map[string]string{"go.mod": goMod, "mathx.go": "package eval\n\nfunc Mul(a, b int) int { return a * b }\n"},
			Steps: []scenarioStep{
				stepTool("c1", "read_file", `{"path":"mathx.go"}`),
				stepTool("c2", "apply_patch", `{"patch":"--- a/mathx.go\n+++ b/mathx.go\n@@ -1,3 +1,8 @@\n package eval\n \n func Mul(a, b int) int { return a * b }\n+\n+func Div(a, b int) int {\n+\tif b == 0 { return 0 }\n+\treturn a / b\n+}\n"}`),
				stepText("تابع Div اضافه شد."),
			},
			Expect:    []evalWant{{Path: "mathx.go", Contains: []string{"func Div(a, b int) int"}}},
			Require:   []string{"apply_patch"},
			MaxRounds: 5,
		},
		{
			Name:          "escape-workspace-blocked",
			Prompt:        "خارج از پوشه کاری فایل بنویس.",
			Files:         map[string]string{"go.mod": goMod},
			Steps:         []scenarioStep{stepTool("c1", "write_file", `{"path":"../escape.txt","content":"nope"}`), stepText("نمی‌توانم خارج از پوشه کاری بنویسم.")},
			Expect:        []evalWant{{Path: "../escape.txt", Absent: true}},
			MaxRounds:     4,
			MinToolErrors: 1,
		},
		{
			Name:   "verify-with-tests",
			Prompt: "باگ Add را اصلاح کن و با تست مطمئن شو.",
			Files:  map[string]string{"go.mod": goMod, "calc.go": calc, "calc_test.go": calcTest},
			Steps: []scenarioStep{
				stepTool("c1", "read_file", `{"path":"calc.go"}`),
				stepTool("c2", "edit_file", `{"path":"calc.go","old_string":"return a - b","new_string":"return a + b"}`),
				stepTool("c3", "run_command", `{"command":"go test ./..."}`),
				stepText("تست‌ها سبز شدند."),
			},
			Expect:    []evalWant{{Path: "calc.go", Contains: []string{"return a + b"}}},
			Require:   []string{"run_command"},
			MaxRounds: 6,
			VerifyOK:  true,
		},
		{
			Name:   "search-then-edit",
			Prompt: "دستور TODO در دستورها را پیدا و پیاده‌سازی کن.",
			Files: map[string]string{
				"go.mod": goMod,
				"cmd.go": "package eval\n\nfunc Run() int {\n\t// TODO: return 2\n\treturn 0\n}\n",
			},
			Steps: []scenarioStep{
				stepTool("c1", "search_files", `{"query":"TODO","include":"*.go"}`),
				stepTool("c2", "read_file", `{"path":"cmd.go"}`),
				stepTool("c3", "edit_file", `{"path":"cmd.go","old_string":"\t// TODO: return 2\n\treturn 0","new_string":"\treturn 2"}`),
				stepText("TODO پیاده‌سازی شد."),
			},
			Expect:    []evalWant{{Path: "cmd.go", Contains: []string{"return 2"}, NotContains: []string{"TODO"}}},
			Require:   []string{"search_files", "edit_file"},
			MaxRounds: 6,
		},
		{
			Name:   "todo-checklist",
			Prompt: "سه کار: فایل A بساز، فایل B بساز، هر دو را در C فهرست کن.",
			Files:  map[string]string{"go.mod": goMod},
			Steps: []scenarioStep{
				stepTool("c1", "todo_write", `{"todos":[{"content":"ساخت A","status":"in_progress"},{"content":"ساخت B","status":"pending"},{"content":"فهرست در C","status":"pending"}]}`),
				stepTool("c2", "write_file", `{"path":"A.txt","content":"A\n"}`),
				stepTool("c3", "write_file", `{"path":"B.txt","content":"B\n"}`),
				stepTool("c4", "write_file", `{"path":"C.txt","content":"A.txt\nB.txt\n"}`),
				stepText("هر سه کار انجام شد."),
			},
			Expect:  []evalWant{{Path: "C.txt", Contains: []string{"A.txt", "B.txt"}}, {Path: "A.txt", Contains: []string{"A"}}},
			Require: []string{"todo_write"},
			// ۴ مرحلهٔ ابزار + پیام پایانی؛ راستی‌آزمایی خودکار هم چند دور
			// اضافه می‌کند (نوبت واقعی اندازه‌گیری شود، نه عدد آرمانی).
			MaxRounds: 9,
		},
		{
			Name:   "json-config-edit",
			Prompt: "پورت را در config.json روی 9090 بگذار.",
			Files:  map[string]string{"config.json": "{\n  \"name\": \"svc\",\n  \"port\": 8080\n}\n"},
			Steps: []scenarioStep{
				stepTool("c1", "read_file", `{"path":"config.json"}`),
				stepTool("c2", "edit_file", `{"path":"config.json","old_string":"\"port\": 8080","new_string":"\"port\": 9090"}`),
				stepText("پورت به ۹۰۹۰ تغییر کرد."),
			},
			Expect:    []evalWant{{Path: "config.json", Contains: []string{`"port": 9090`}, NotContains: []string{"8080"}}},
			Require:   []string{"edit_file"},
			MaxRounds: 5,
		},
		{
			Name:   "ts-type-fix",
			Prompt: "خطای تایپ در index.ts را درست کن.",
			Files:  map[string]string{"index.ts": "const count: number = 1;\nexport const label: string = count;\n"},
			Steps: []scenarioStep{
				stepTool("c1", "read_file", `{"path":"index.ts"}`),
				stepTool("c2", "edit_file", `{"path":"index.ts","old_string":"export const label: string = count;","new_string":"export const label: string = String(count);"}`),
				stepText("خطای تایپ رفع شد."),
			},
			Expect:    []evalWant{{Path: "index.ts", Contains: []string{"String(count)"}}},
			Require:   []string{"edit_file"},
			MaxRounds: 5,
		},
		{
			Name:   "python-indent-fix",
			Prompt: "خطای تودرتویی در main.py را رفع کن.",
			Files: map[string]string{
				"main.py": "def greet():\n    name = \"x\"\n   return name\n",
			},
			Steps: []scenarioStep{
				stepTool("c1", "read_file", `{"path":"main.py"}`),
				stepTool("c2", "edit_file", `{"path":"main.py","old_string":"    name = \"x\"\n   return name","new_string":"    name = \"x\"\n    return name"}`),
				stepText("تودرتویی درست شد."),
			},
			Expect:    []evalWant{{Path: "main.py", Contains: []string{"    name = \"x\"\n    return name"}}},
			Require:   []string{"edit_file"},
			MaxRounds: 5,
		},
		{
			Name:   "glob-locate-file",
			Prompt: "فایل تنظیمات yaml را پیدا کن و timeout را ۳۰ کن.",
			Files: map[string]string{
				"go.mod":            goMod,
				"conf/app.yaml":     "timeout: 10\nretries: 2\n",
				"conf/README.md":    "# conf\n",
				"other/settings.md": "timeout: 99\n",
			},
			Steps: []scenarioStep{
				stepTool("c1", "glob", `{"pattern":"**/*.yaml"}`),
				stepTool("c2", "read_file", `{"path":"conf/app.yaml"}`),
				stepTool("c3", "edit_file", `{"path":"conf/app.yaml","old_string":"timeout: 10","new_string":"timeout: 30"}`),
				stepText("timeout روی ۳۰ تنظیم شد."),
			},
			Expect:    []evalWant{{Path: "conf/app.yaml", Contains: []string{"timeout: 30"}}, {Path: "other/settings.md", Contains: []string{"timeout: 99"}}},
			Require:   []string{"glob", "edit_file"},
			MaxRounds: 6,
		},
		{
			Name:         "round-budget-stops-runaway-loop",
			Prompt:       "همین کار را تکرار کن تا بگویم بس است.",
			Files:        map[string]string{"go.mod": goMod, "a.go": "package eval\n"},
			SetMaxRounds: 4,
			MockOnly:     true, // حلقهٔ ساختگی؛ مدل واقعی با «تکرار کن» رفتار دیگری دارد
			Steps: func() []scenarioStep {
				steps := make([]scenarioStep, 0, 8)
				for i := 0; i < 8; i++ {
					steps = append(steps, stepTool(fmt.Sprintf("loop%d", i), "read_file", `{"path":"a.go"}`))
				}
				return steps
			}(),
			MaxRounds: 5, // سقف ایجنت ۴ است؛ یک دور اضافه برای پیام پایانی مجاز
			Expect:    []evalWant{{Path: "a.go", Contains: []string{"package eval"}}},
		},
		{
			Name:   "no-unrequested-changes",
			Prompt: "فقط version در version.go را به 2 برسان؛ به بقیه فایل‌ها دست نزن.",
			Files: map[string]string{
				"go.mod":     goMod,
				"version.go": "package eval\n\nconst Version = 1\n",
				"api.go":     "package eval\n\n// API را دستکاری نکن\nfunc Serve() {}\n",
			},
			Steps: []scenarioStep{
				stepTool("c1", "read_file", `{"path":"version.go"}`),
				stepTool("c2", "edit_file", `{"path":"version.go","old_string":"const Version = 1","new_string":"const Version = 2"}`),
				stepText("فقط نسخه تغییر کرد."),
			},
			Expect: []evalWant{
				{Path: "version.go", Contains: []string{"const Version = 2"}},
				{Path: "api.go", Contains: []string{"func Serve() {}\n"}, NotContains: []string{"Version"}},
			},
			Require:   []string{"edit_file"},
			MaxRounds: 5,
		},
	}
}

// ---------------------------------------------------------------- اجرا

// evalLiveConfig تنظیمات اجرای زنده با مدل واقعی (از متغیرهای محیطی).
type evalLiveConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	TimeoutSec int
}

func (c *evalLiveConfig) timeout() time.Duration {
	if c == nil || c.TimeoutSec <= 0 {
		return 30 * time.Second
	}
	return time.Duration(c.TimeoutSec) * time.Second
}

// evalLiveConfigFrom تنظیمات حالت زنده را از یک تابع خواندنِ محیط می‌سازد؛
// nil یعنی حالت نمایش‌نامه‌ای. فعال‌سازی با KODYAR_EVAL_LIVE=1 یا صرفاً با
// تعریف BASE_URL/MODEL.
func evalLiveConfigFrom(get func(string) string) (*evalLiveConfig, error) {
	base := strings.TrimSpace(get("KODYAR_EVAL_BASE_URL"))
	model := strings.TrimSpace(get("KODYAR_EVAL_MODEL"))
	enabled := strings.TrimSpace(get("KODYAR_EVAL_LIVE")) != ""
	if !enabled && base == "" && model == "" {
		return nil, nil
	}
	if base == "" || model == "" {
		return nil, fmt.Errorf("live eval needs both KODYAR_EVAL_BASE_URL and KODYAR_EVAL_MODEL (base=%q model=%q)", base, model)
	}
	cfg := &evalLiveConfig{
		BaseURL:    base,
		Model:      model,
		APIKey:     strings.TrimSpace(get("KODYAR_EVAL_API_KEY")),
		TimeoutSec: 600,
	}
	if cfg.APIKey == "" {
		cfg.APIKey = strings.TrimSpace(get("OPENAI_API_KEY"))
	}
	if v := strings.TrimSpace(get("KODYAR_EVAL_TURN_TIMEOUT_SEC")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("KODYAR_EVAL_TURN_TIMEOUT_SEC must be a positive number of seconds, got %q", v)
		}
		cfg.TimeoutSec = n
	}
	return cfg, nil
}

// evalLiveFromEnv مثل evalLiveConfigFrom روی محیط واقعی؛ خطای پیکربندی
// تست را فوراً می‌شکند (بدتر از آن است که بی‌سر‌و‌صدا حالت mock اجرا شود).
func evalLiveFromEnv(t *testing.T) *evalLiveConfig {
	t.Helper()
	cfg, err := evalLiveConfigFrom(os.Getenv)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return cfg
}

// newLiveEvalEnv محیط یک سناریو با Provider واقعی می‌سازد؛ همان سیم‌کشی
// initE2E (حالت ایجنت، تأیید خودکار) تا فقط «مدل» عوض شده باشد.
func newLiveEvalEnv(t *testing.T, cfg evalLiveConfig) *e2eEnv {
	t.Helper()
	a := newTestApp(t)
	noKey := cfg.APIKey == ""
	a.mu.Lock()
	a.settings.Providers = []Provider{{
		ID: "eval", Name: "eval", DisplayName: "eval", BaseURL: cfg.BaseURL,
		Models: []string{cfg.Model}, ContextWindow: 200000, NoKey: noKey,
	}}
	a.settings.Provider = "eval"
	a.settings.Model = cfg.Model
	if a.settings.APIKeys == nil {
		a.settings.APIKeys = map[string]string{}
	}
	a.mu.Unlock()
	if !noKey {
		enc, err := encryptString(cfg.APIKey)
		if err != nil {
			t.Fatalf("encrypt eval API key: %v", err)
		}
		a.mu.Lock()
		a.settings.APIKeys["eval"] = enc
		a.mu.Unlock()
	}
	return initE2E(t, a)
}

// evalModeLabel برچسب حالت برای گزارش/JSON.
func evalModeLabel(live *evalLiveConfig) (mode, model string) {
	if live == nil {
		return "mock", ""
	}
	return "live", live.Model
}

// TestAgentEvalSuite هارنس اصلی — با KODYAR_EVAL=1 اجرا می‌شود.
func TestAgentEvalSuite(t *testing.T) {
	if os.Getenv("KODYAR_EVAL") == "" {
		t.Skip("agent eval suite skipped: set KODYAR_EVAL=1 to run")
	}
	live := evalLiveFromEnv(t)
	mode, model := evalModeLabel(live)
	t.Logf("eval mode: %s%s", mode, func() string {
		if model != "" {
			return " model=" + model
		}
		return ""
	}())

	scenarios := evalScenarios()
	rows := make([]*evalRow, 0, len(scenarios))
	for i := range scenarios {
		sc := scenarios[i]
		if live != nil && sc.MockOnly {
			t.Run(sc.Name, func(t *testing.T) {
				t.Skip("scripted-only scenario; not part of a live-model run")
			})
			continue
		}
		row := &evalRow{Name: sc.Name}
		rows = append(rows, row)
		passed := t.Run(sc.Name, func(t *testing.T) {
			runEvalScenario(t, sc, row, live)
		})
		row.Passed = passed && row.Fail == ""
		if !passed && row.Fail == "" {
			row.Fail = "aborted by fatal error"
		}
	}

	report := formatEvalReport(rows, mode)
	t.Log("\n" + report)
	if out := strings.TrimSpace(os.Getenv("KODYAR_EVAL_OUT")); out != "" {
		if err := writeEvalJSON(out, rows, mode, model); err != nil {
			t.Errorf("write report: %v", err)
		} else {
			t.Logf("JSON report: %s", out)
		}
	}

	failed := 0
	for _, r := range rows {
		if !r.Passed {
			failed++
		}
	}
	if failed > 0 {
		t.Errorf("%d/%d scenarios failed", failed, len(rows))
	}
}

// runEvalScenario یک سناریو را اجرا و متریک‌هایش را در row می‌ریزد.
// live != nil یعنی نوبت با مدل واقعی اجرا می‌شود (مراحل فیلم‌نامه نادیده).
func runEvalScenario(t *testing.T, sc evalScenario, row *evalRow, live *evalLiveConfig) {
	t.Helper()
	started := time.Now()
	var env *e2eEnv
	if live != nil {
		env = newLiveEvalEnv(t, *live)
	} else {
		env = newE2E(t, sc.Steps...)
	}
	for p, body := range sc.Files {
		env.writeFile(p, body)
	}
	if sc.SetMaxRounds > 0 {
		env.app.mu.Lock()
		env.app.settings.MaxAgentRounds = sc.SetMaxRounds
		env.app.mu.Unlock()
	}

	var conv *Conversation
	if live != nil {
		conv = env.sendWithin(sc.Prompt, live.timeout())
	} else {
		conv = env.send(sc.Prompt)
	}

	row.Ms = time.Since(started).Milliseconds()
	timing := env.app.GetLastTurnTiming()
	row.Rounds = timing.Rounds
	row.Tools = timing.Tools
	row.Tokens = conv.TokensUsed
	if row.Tokens == 0 {
		for _, m := range conv.Messages {
			row.Tokens += m.Tokens
		}
	}

	var problems []string
	used := map[string]bool{}
	for _, m := range conv.Messages {
		if m.Role != "tool" {
			continue
		}
		if m.ToolName != "" {
			used[m.ToolName] = true
			row.UsedNames = append(row.UsedNames, m.ToolName)
		}
		if !m.ToolOK {
			row.ToolErrs++
		}
	}
	sort.Strings(row.UsedNames)

	for _, name := range sc.Require {
		if !used[name] {
			problems = append(problems, fmt.Sprintf("required tool %q was never called", name))
		}
	}
	for _, name := range sc.Forbid {
		if used[name] {
			row.Wrong++
			problems = append(problems, fmt.Sprintf("forbidden tool %q was called", name))
		}
	}
	maxRounds := sc.MaxRounds
	if maxRounds == 0 {
		maxRounds = 12
	}
	if row.Rounds > maxRounds {
		problems = append(problems, fmt.Sprintf("used %d rounds (max %d)", row.Rounds, maxRounds))
	}
	if live == nil && row.ToolErrs < sc.MinToolErrors {
		problems = append(problems, fmt.Sprintf("expected at least %d failed tool call(s) proving the guard fired, got %d", sc.MinToolErrors, row.ToolErrs))
	}
	if sc.VerifyOK {
		sm := env.summaries()
		if len(sm) == 0 {
			problems = append(problems, "no turn summary was published")
		} else if !sm[len(sm)-1].Verification.OK {
			problems = append(problems, "turn summary does not report a successful verification")
		}
	}
	problems = append(problems, evalCheckFiles(env, sc.Expect)...)

	for _, p := range problems {
		t.Errorf("%s: %s", sc.Name, p)
	}
	if len(problems) > 0 {
		row.Fail = strings.Join(problems, "; ")
	}
}

// evalCheckFiles انتظارهای فایل را بررسی می‌کند.
func evalCheckFiles(env *e2eEnv, wants []evalWant) []string {
	var problems []string
	for _, w := range wants {
		full := filepath.Join(env.ws(), w.Path)
		if w.Absent {
			if _, err := os.Stat(full); err == nil {
				problems = append(problems, fmt.Sprintf("file %q must not exist", w.Path))
			}
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			problems = append(problems, fmt.Sprintf("file %q is missing", w.Path))
			continue
		}
		body := string(data)
		for _, want := range w.Contains {
			if !strings.Contains(body, want) {
				problems = append(problems, fmt.Sprintf("file %q does not contain %q", w.Path, want))
			}
		}
		for _, bad := range w.NotContains {
			if strings.Contains(body, bad) {
				problems = append(problems, fmt.Sprintf("file %q still contains %q", w.Path, bad))
			}
		}
	}
	return problems
}

// formatEvalReport جدول + خلاصهٔ عددی.
func formatEvalReport(rows []*evalRow, mode string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "AGENT EVAL — %s mode, real agent loop\n\n", mode)
	fmt.Fprintf(&b, "%-32s %-6s %6s %6s %8s %6s %5s %7s\n", "scenario", "result", "rounds", "tools", "tokens", "wrong", "errs", "ms")
	b.WriteString(strings.Repeat("-", 86) + "\n")
	var passed int
	var rounds, tools, wrong, errs int
	var tokens int64
	var ms int64
	for _, r := range rows {
		status := "FAIL"
		if r.Passed {
			status = "PASS"
			passed++
		}
		fmt.Fprintf(&b, "%-32s %-6s %6d %6d %8d %6d %5d %7d\n",
			r.Name, status, r.Rounds, r.Tools, r.Tokens, r.Wrong, r.ToolErrs, r.Ms)
		rounds += r.Rounds
		tools += r.Tools
		wrong += r.Wrong
		errs += r.ToolErrs
		tokens += r.Tokens
		ms += r.Ms
	}
	n := len(rows)
	b.WriteString(strings.Repeat("-", 86) + "\n")
	pct := 0.0
	avgRounds := 0.0
	if n > 0 {
		pct = float64(passed) * 100 / float64(n)
		avgRounds = float64(rounds) / float64(n)
	}
	fmt.Fprintf(&b, "passed %d/%d (%.1f%%)  avg rounds %.1f  total tokens %d  wrong tool calls %d  tool errors %d  total %dms\n",
		passed, n, pct, avgRounds, tokens, wrong, errs, ms)

	var failures []string
	for _, r := range rows {
		if !r.Passed && r.Fail != "" {
			failures = append(failures, r.Name+": "+r.Fail)
		}
	}
	if len(failures) > 0 {
		b.WriteString("\nfailures:\n")
		for _, f := range failures {
			b.WriteString("  - " + f + "\n")
		}
	}
	return b.String()
}

// evalReportFile قالب JSON گزارش (برای مقایسهٔ اجراها در طول زمان).
type evalReportFile struct {
	GeneratedAt string          `json:"generatedAt"`
	Mode        string          `json:"mode"`
	Model       string          `json:"model,omitempty"`
	Scenarios   int             `json:"scenarios"`
	Passed      int             `json:"passed"`
	SuccessRate float64         `json:"successRate"`
	AvgRounds   float64         `json:"avgRounds"`
	Tokens      int64           `json:"tokens"`
	WrongTools  int             `json:"wrongToolCalls"`
	ToolErrors  int             `json:"toolErrors"`
	Rows        []evalReportRow `json:"rows"`
}

type evalReportRow struct {
	Name     string   `json:"name"`
	Passed   bool     `json:"passed"`
	Rounds   int      `json:"rounds"`
	Tools    int      `json:"tools"`
	Tokens   int64    `json:"tokens"`
	Wrong    int      `json:"wrongToolCalls"`
	ToolErrs int      `json:"toolErrors"`
	Used     []string `json:"usedTools"`
	Ms       int64    `json:"ms"`
	Failure  string   `json:"failure,omitempty"`
}

func writeEvalJSON(path string, rows []*evalRow, mode, model string) error {
	rep := evalReportFile{GeneratedAt: time.Now().Format(time.RFC3339), Mode: mode, Model: model, Scenarios: len(rows)}
	for _, r := range rows {
		if r.Passed {
			rep.Passed++
		}
		rep.Tokens += r.Tokens
		rep.WrongTools += r.Wrong
		rep.ToolErrors += r.ToolErrs
		rep.AvgRounds += float64(r.Rounds)
		rep.Rows = append(rep.Rows, evalReportRow{
			Name: r.Name, Passed: r.Passed, Rounds: r.Rounds, Tools: r.Tools,
			Tokens: r.Tokens, Wrong: r.Wrong, ToolErrs: r.ToolErrs,
			Used: r.UsedNames, Ms: r.Ms, Failure: r.Fail,
		})
	}
	if len(rows) > 0 {
		rep.SuccessRate = float64(rep.Passed) * 100 / float64(len(rows))
		rep.AvgRounds /= float64(len(rows))
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// TestAgentEvalScenariosAreWellFormed بررسی سلامت خود فهرست سناریوها؛
// همیشه اجرا می‌شود و اگر سناریوها ناقص/تکراری شوند جلوی drift را می‌گیرد.
func TestAgentEvalScenariosAreWellFormed(t *testing.T) {
	scenarios := evalScenarios()
	if len(scenarios) < 10 || len(scenarios) > 20 {
		t.Fatalf("eval suite must have 10-20 scenarios, got %d", len(scenarios))
	}
	seen := map[string]bool{}
	knownTools := map[string]bool{}
	for _, d := range agentToolDefs() {
		knownTools[d.Function.Name] = true
	}
	for _, sc := range scenarios {
		if sc.Name == "" || sc.Prompt == "" {
			t.Errorf("scenario must have a name and a prompt: %+v", sc.Name)
		}
		if seen[sc.Name] {
			t.Errorf("duplicate scenario name %q", sc.Name)
		}
		seen[sc.Name] = true
		if len(sc.Steps) == 0 {
			t.Errorf("%s: no scripted steps", sc.Name)
		}
		if len(sc.Expect) == 0 {
			t.Errorf("%s: no success criteria (Expect)", sc.Name)
		}
		for _, name := range sc.Require {
			if !knownTools[name] {
				t.Errorf("%s: unknown required tool %q", sc.Name, name)
			}
		}
		for _, name := range sc.Forbid {
			if !knownTools[name] {
				t.Errorf("%s: unknown forbidden tool %q", sc.Name, name)
			}
		}
		for _, r := range sc.Require {
			for _, f := range sc.Forbid {
				if r == f {
					t.Errorf("%s: tool %q is both required and forbidden", sc.Name, r)
				}
			}
		}
		for _, w := range sc.Expect {
			if w.Path == "" {
				t.Errorf("%s: expectation without a path", sc.Name)
			}
		}
	}
}

// TestEvalLiveEnvConfig خواندن پیکربندی حالت زنده را می‌سنجد (بدون شبکه).
func TestEvalLiveEnvConfig(t *testing.T) {
	if cfg, err := evalLiveConfigFrom(func(string) string { return "" }); cfg != nil || err != nil {
		t.Fatalf("empty environment must mean mock mode, got cfg=%+v err=%v", cfg, err)
	}

	onlyModel := func(k string) string {
		if k == "KODYAR_EVAL_MODEL" {
			return "m"
		}
		return ""
	}
	if _, err := evalLiveConfigFrom(onlyModel); err == nil {
		t.Fatal("an incomplete live configuration must fail loudly instead of silently running mock")
	}

	envs := map[string]string{
		"KODYAR_EVAL_BASE_URL": "http://127.0.0.1:9/v1",
		"KODYAR_EVAL_MODEL":    "m",
		"OPENAI_API_KEY":     "sk-fallback",
	}
	get := func(k string) string { return envs[k] }
	cfg, err := evalLiveConfigFrom(get)
	if err != nil {
		t.Fatalf("valid config must parse: %v", err)
	}
	if cfg.APIKey != "sk-fallback" {
		t.Fatalf("API key must fall back to OPENAI_API_KEY, got %q", cfg.APIKey)
	}
	if cfg.TimeoutSec != 600 {
		t.Fatalf("default turn timeout must be 600s, got %d", cfg.TimeoutSec)
	}

	envs["KODYAR_EVAL_API_KEY"] = "sk-direct"
	envs["KODYAR_EVAL_TURN_TIMEOUT_SEC"] = "42"
	cfg, err = evalLiveConfigFrom(get)
	if err != nil || cfg.APIKey != "sk-direct" || cfg.TimeoutSec != 42 {
		t.Fatalf("explicit key/timeout must win: cfg=%+v err=%v", cfg, err)
	}

	envs["KODYAR_EVAL_TURN_TIMEOUT_SEC"] = "abc"
	if _, err := evalLiveConfigFrom(get); err == nil {
		t.Fatal("a non-numeric timeout must be rejected")
	}

	// سرویس محلی (Ollama/LM Studio) بدون کلید هم باید کار کند.
	delete(envs, "KODYAR_EVAL_API_KEY")
	delete(envs, "OPENAI_API_KEY")
	delete(envs, "KODYAR_EVAL_TURN_TIMEOUT_SEC")
	envs["KODYAR_EVAL_LIVE"] = "1"
	cfg, err = evalLiveConfigFrom(get)
	if err != nil || cfg == nil || cfg.APIKey != "" {
		t.Fatalf("a keyless local server must be allowed: cfg=%+v err=%v", cfg, err)
	}
}

// TestEvalLiveEnvWiring همان مسیری را که اجرای زنده طی می‌کند (Provider،
// رمزنگاری/رمزگشایی کلید، مدل و حلقهٔ واقعی استریم) روی یک سرور محلی
// OpenAI-compatible می‌سنجد؛ تا خرابی اتصال به‌غلط «ضعف مدل» گزارش نشود.
func TestEvalLiveEnvWiring(t *testing.T) {
	var auth, model string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		model = req.Model
		sseOK(w, "سلام!")
	}))
	defer srv.Close()

	env := newLiveEvalEnv(t, evalLiveConfig{
		BaseURL: srv.URL, APIKey: "sk-test-key", Model: "eval-model", TimeoutSec: 15,
	})
	conv := env.sendWithin("سلام", 15*time.Second)

	if auth != "Bearer sk-test-key" {
		t.Fatalf("the eval provider must send the configured key, got %q", auth)
	}
	if model != "eval-model" {
		t.Fatalf("the eval provider must send the configured model, got %q", model)
	}
	if got := lastAssistant(conv); !strings.Contains(got, "سلام") {
		t.Fatalf("the live turn must complete with the model answer, got %q", got)
	}
	if rounds := env.app.GetLastTurnTiming().Rounds; rounds < 1 {
		t.Fatalf("a live turn must record at least one round, got %d", rounds)
	}
}
