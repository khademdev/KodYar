// سخت‌سازی فراخوانی ابزار.
//
// این فایل چهار قطعه‌ی ایمنیِ حلقه‌ی ایجنت را در خود دارد:
// 1. parseToolArgs — پیام خطای دقیق و قابل‌اقدام (انگلیسی، برای مدل) به‌جای
// پیام عمومی قبلی برای JSON آرگومان‌های ناقص/نامعتبر. حالت «برش‌خورده»
// (finish_reason == "length") از طریق context تشخیص داده می‌شود.
// 2. validateToolArgs — اعتبارسنجی فیلدهای الزامی/نوع‌ها برای ابزارهای داخلی،
// قبل از اجرا و با خطاهای انگلیسی یکدست. تبدیل نوع با همان توابعی انجام
// می‌شود که مجری (gs/gi/gb در agent.go) استفاده می‌کند (coerce*،
// تا اعتبارسنجی هرگز سخت‌گیرتر یا سست‌تر از اجرا نشود.
// 3. canonicalToolSignature + toolLoopGuard — محافظ حلقه‌ی تکرار (doom-loop):
// فراخوانیِ «یکسانِ متوالی» (همان ابزار + همان آرگومان‌ها، مستقل از
// ترتیب کلیدها/فاصله‌گذاری JSON) بشمارده می‌شود؛ بار سوم اجرا نمی‌شود و
// بار پنجم نوبت را متوقف می‌کند.
// 4. حافظه‌ی درون‌حافظه‌ایِ پشتیبانی stream_options per-provider — برای
// عقب‌نشینی یک‌باره در chat.go وقتی سرویس‌دهنده ۴۰۰ می‌دهد.
//
// نکته‌ی زبانی (بخش ۲ پرامپت): همه‌ی خطاهای این فایل «رو به مدل» و انگلیسی
// هستند؛ اعلان توقف نوبت (doomLoopStopNotice) «رو به کاربر» و فارسی است.
// هیچ‌جا نویسه‌های صفر-عرض و حروف عربی/فارسی نرمال نمی‌شوند — امضای فراخوانی
// باید بایت‌به‌بایت به متن اصلی وفادار بماند (فقط ترتیب کلیدها و
// فاصله‌گذاری JSON استاندارد می‌شود).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// ---------------------------------------------------------------- برش‌خوردگی آرگومان‌ها

// ctxKeyToolTruncation کلید context برای سرایت hint برش‌خوردگی از حلقه‌ی
// استریم (finish_reason == "length") به execToolCtx — همان قرارداد
// withToolCallInfo در shell.go.
type ctxKeyToolTruncation struct{}

// withTruncationHint پاسخ مدل در این نوبت به‌خاطر سقف توکن نیمه‌بریده است.
func withTruncationHint(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyToolTruncation{}, true)
}

// truncationHinted آیا ctx حامل hint برش‌خوردگی است؟
func truncationHinted(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyToolTruncation{}).(bool)
	return v
}

// maxArgsExcerpt حداکثر طول گزیده‌ی آرگومان‌ها در پیام خطا (بایت). با cutUTF8
// روی مرز کاراکتر بریده می‌شود تا متن فارسی نصف نشود.
const maxArgsExcerpt = 120

// parseToolArgs آرگومان‌های JSON یک فراخوانی ابزار را parse می‌کند و در صورت
// خطا، پیام دقیق و قابل‌اقدام برمی‌گرداند:
// - truncated=true (finish_reason == "length"): به مدل می‌گوید آرگومان‌ها
// برش خورده‌اند و باید بارِ کوچک‌تر یا چند write بفرستد.
// - غیر از آن: خطای JSON خام + همان توصیه‌ی عملی.
//
// رشته‌ی خالی/فقط‌فاصله → map خالی بدون خطا (ابزارهای بدون آرگومان مثل
// git_status و list_dir بدون path همین‌طور کار می‌کنند).
func parseToolArgs(argsJSON string, truncated bool) (map[string]interface{}, error) {
	s := strings.TrimSpace(argsJSON)
	if s == "" {
		return map[string]interface{}{}, nil
	}
	// اول unmarshal کلی تا نوع واقعی مشخص شود (آرایه/رشته/عدد نباید به
	// «cannot unmarshal array into Go value of type map» خطا بدهند — برای
	// مدل گمراه‌کننده است).
	var raw interface{}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		excerpt := argsExcerpt(s)
		if truncated {
			return nil, fmt.Errorf(
				"ERROR: tool arguments are invalid JSON because your response was truncated (finish_reason=length): %v. "+
					"The arguments were cut off before the JSON was complete. Do NOT resend the same payload: "+
					"retry with a smaller payload (write a smaller portion of the file per call, or split the file into several writes), "+
					"so the whole call fits within your output limit. Arguments received (beginning): %s", err, excerpt)
		}
		return nil, fmt.Errorf(
			"ERROR: tool arguments are not valid JSON: %v. "+
				"The arguments must be one complete JSON object with the fields this tool expects. "+
				"Fix the JSON and call again; for large payloads use a smaller payload or split the file into several writes. "+
				"Arguments received (beginning): %s", err, excerpt)
	}
	args, ok := raw.(map[string]interface{})
	if !ok || args == nil {
		// null / آرایه / رشته / عدد — همه «آبجکت نیستند».
		return nil, fmt.Errorf(
			"ERROR: tool arguments must be a JSON object, got %s. "+
				"Pass one JSON object with the fields this tool expects (use {} when there are none).", jsonTypeName(raw))
	}
	return args, nil
}

// argsExcerpt گزیده‌ی rune-safe و UTF-8-معتبرِ ابتدای آرگومان‌ها برای پیام
// خطا: ابتدا سقف بایت روی مرز کاراکتر اعمال می‌شود (cutUTF8) و اگر ورودی
// خودش وسط یک کاراکتر چندبایتی تمام شده باشد (حالت واقعیِ برشِ
// finish_reason=length)، دنباله‌ی ناقص انتهایی حذف می‌شود تا پیام خطا
// هرگز UTF-8 خراب تولید نکند (بخش ۲: تست جاروی مرزها این را تضمین می‌کند).
func argsExcerpt(s string) string {
	excerpt := cutUTF8(s, maxArgsExcerpt)
	if !utf8.ValidString(excerpt) {
		excerpt = strings.ToValidUTF8(excerpt, "")
	}
	if len(s) > maxArgsExcerpt {
		excerpt += "…"
	}
	return excerpt
}

// ---------------------------------------------------------------- اعتبارسنجی آرگومان‌ها

// toolArgSpec یک فیلد مورد انتظار یک ابزار داخلی.
type toolArgSpec struct {
	key      string
	typ      string // "string" | "integer" | "boolean"
	required bool
	// nonEmpty: رشته‌ی الزامی نباید خالی باشد (فیلدهای «محل‌یاب» مثل path/ref؛
	// برخلافش content و new_string که خالی‌شان معنادار است).
	nonEmpty bool
}

// toolArgSpecs جدول اعتبارسنجی ابزارهای داخلی — همیشه با agentToolDefs هم‌قدم
// نگه داشته شود. ابزارهای سفارشی (custom_*) و ناشناخته اعتبارسنجی نمی‌شوند
// (شمای خودشان دست کاربر است) و به execCustomTool واگذار می‌شوند.
var toolArgSpecs = map[string][]toolArgSpec{
	"list_dir": {},
	"diagnostics": {
		{key: "path", typ: "string"}, // خالی = ریشهٔ workspace
	},
	"read_file": {
		{key: "path", typ: "string", required: true, nonEmpty: true},
		{key: "start_line", typ: "integer"},
		{key: "end_line", typ: "integer"},
	},
	"write_file": {
		{key: "path", typ: "string", required: true, nonEmpty: true},
		{key: "content", typ: "string", required: true}, // خالی = فایل خالی؛ مجاز
	},
	"edit_file": {
		{key: "path", typ: "string", required: true, nonEmpty: true},
		// شکل تک‌ویرایش: old_string/new_string در جدول «نوع‌دار»‌اند ولی
		// required نیستند چون شکل چندویرایشی (edits) آن‌ها را نمی‌فرستد —
		// بررسیِ either/or با پیام دقیق در toolEditFileArgs است.
		{key: "old_string", typ: "string", nonEmpty: true},
		{key: "new_string", typ: "string"}, // خالی = حذف متن؛ مجاز
		{key: "replace_all", typ: "boolean"},
		{key: "edits", typ: "edits"}, // آرایهٔ چندویرایشی — اعتبارسنجی عمیق در parseEditOps
	},
	"apply_patch": {
		{key: "patch", typ: "string", required: true, nonEmpty: true},
		{key: "dry_run", typ: "boolean"},
	},
	"move_path": {
		{key: "from", typ: "string", required: true, nonEmpty: true},
		{key: "to", typ: "string", required: true, nonEmpty: true},
	},
	"delete_path": {
		{key: "path", typ: "string", required: true, nonEmpty: true},
	},
	"create_dir": {
		{key: "path", typ: "string", required: true, nonEmpty: true},
	},
	"search_files": {
		{key: "query", typ: "string", required: true, nonEmpty: true},
		{key: "path", typ: "string"},
		{key: "regex", typ: "boolean"},
		{key: "is_regex", typ: "boolean"},
		{key: "include", typ: "string"},
		{key: "case_sensitive", typ: "boolean"},
		{key: "context", typ: "integer"},
		{key: "max_results", typ: "integer"},
	},
	"glob": {
		{key: "pattern", typ: "string", required: true, nonEmpty: true},
		{key: "path", typ: "string"},
	},
	"find_symbol": {
		{key: "query", typ: "string", required: true, nonEmpty: true},
		{key: "kind", typ: "string"},
		{key: "limit", typ: "integer"},
	},
	"find_references": {
		{key: "symbol", typ: "string", required: true, nonEmpty: true},
		{key: "include", typ: "string"},
		{key: "limit", typ: "integer"},
	},
	"run_command": {
		{key: "command", typ: "string", required: true, nonEmpty: true},
		{key: "timeout_sec", typ: "integer"},
		{key: "background", typ: "boolean"},
	},
	"git_status": {},
	"git_diff": {
		{key: "path", typ: "string"},
		{key: "staged", typ: "boolean"},
	},
	"git_log": {
		{key: "path", typ: "string"},
		{key: "limit", typ: "integer"},
	},
	"git_show": {
		{key: "ref", typ: "string", required: true, nonEmpty: true},
	},
	"git_create_branch": {
		{key: "name", typ: "string", required: true, nonEmpty: true},
		{key: "from", typ: "string"},
	},
	"git_commit": {
		{key: "message", typ: "string", required: true, nonEmpty: true},
		{key: "paths", typ: "paths"},
	},
	"task": {
		{key: "description", typ: "string", required: true, nonEmpty: true},
	},
	// todo_write: آرگمان `todos` آرایه است — با تایپ اختصاصی "todos"
	// در همین جدول پذیرفته می‌شود (آرایه یا رشتهٔ JSON حاوی آرایه؛
	// اعتبارسنجی عمیقِ آیتم‌ها در parseTodoItems — todos.go).
	"todo_write": {
		{key: "todos", typ: "todos", required: true},
	},
	"web_fetch": {
		{key: "url", typ: "string", required: true, nonEmpty: true},
		{key: "max_chars", typ: "integer"},
	},
	"process_output": {
		{key: "id", typ: "string", required: true, nonEmpty: true},
		{key: "offset", typ: "integer"},
		{key: "max_chars", typ: "integer"},
	},
	"process_stop": {
		{key: "id", typ: "string", required: true, nonEmpty: true},
	},
	"process_list": {},
	"process_input": {
		{key: "id", typ: "string", required: true, nonEmpty: true},
		{key: "text", typ: "string"}, // خالی + newline = فقط Enter
		{key: "newline", typ: "boolean"},
	},
	"process_wait": {
		{key: "id", typ: "string", required: true, nonEmpty: true},
		{key: "timeout_sec", typ: "integer"},
	},
	"wait_for_port": {
		{key: "target", typ: "string", required: true, nonEmpty: true},
		{key: "timeout_sec", typ: "integer"},
	},
	"web_search": {
		{key: "query", typ: "string", required: true, nonEmpty: true},
		{key: "max_results", typ: "integer"},
	},
}

// jsonTypeName نام نوع JSON مقدار برای پیام خطا.
func jsonTypeName(v interface{}) string {
	switch v.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	case nil:
		return "null"
	}
	return "unknown"
}

// ---------------------------------------------------------------- تبدیل نوع مشترک

// coercePathList فهرست مسیرهای آرگومان را به []string تبدیل می‌کند
// (آرایه یا رشتهٔ JSON حاوی آرایه؛ آیتم‌های غیررشته‌ای نادیده گرفته می‌شوند —
// اعتبارسنجی سخت‌گیرانه‌تر در validateToolArgs است).
func coercePathList(v interface{}) []string {
	arr, ok := coerceJSONArray(v)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, it := range arr {
		s, ok := coerceString(it)
		if !ok {
			continue
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// coerceString/coerceInt/coerceBool تنها مرجع تبدیل نوعِ مقدارهای آرگومان ابزار
// هستند — هم validateToolArgs (اینجا) و هم gs/gi/gb در execToolCtx (agent.go)
// از همین‌ها صدا می‌زنند تا اعتبارسنجی و اجرا هرگز از هم جدا نیفتند
// (خطای: اعتبارسنجی از مجری سخت‌گیرتر بود و «60» به‌جای 60 یا
// "true" به‌جای true را رد می‌کرد؛ مدل‌های GLM و متن‌باز این را زیاد می‌فرستند).
//
// قرارداد: ok=false یعنی این مقدار با هیچ شکل قابل‌قبولی به آن نوع نمی‌رسد؛
// در آن صورت مقدار برگشتی صفرِ نوع است. هیچ نرمال‌سازی متنی انجام نمی‌شود —
// رشته‌ی فارسی با ZWNJ بایت‌به‌بایت برمی‌گردد.
func coerceString(v interface{}) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case float64: // همان تبدیلی که gs همیشه انجام می‌داد
		return itoa(int(t)), true
	case bool:
		if t {
			return "true", true
		}
		return "false", true
	}
	return "", false
}

// coerceInt عدد صحیح JSON یا رشته‌ای که strconv.Atoi بعد از TrimSpace می‌پذیرد.
// عدد اعشاری (مثل 1.5) و رشته‌ی غیرعددی رد می‌شوند.
func coerceInt(v interface{}) (int, bool) {
	switch t := v.(type) {
	case float64:
		if t != float64(int64(t)) {
			return 0, false
		}
		return int(t), true
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// coerceBool بول JSON، رشته‌ی "true"/"false" (غیرحساس به case، با trim) یا
// عدد JSON (صفر/ناصفر) — همان چیزی که gb همیشه می‌پذیرفت.
func coerceBool(v interface{}) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		s := strings.TrimSpace(t)
		if strings.EqualFold(s, "true") {
			return true, true
		}
		if strings.EqualFold(s, "false") {
			return false, true
		}
	case float64:
		return t != 0, true
	}
	return false, false
}

// validateToolArgs فیلدهای الزامی و نوع‌ها را برای ابزارهای داخلی بررسی
// می‌کند. خطاها انگلیسی و یکدست‌اند: «ERROR: invalid arguments for <tool>: …».
// پذیرش نوع‌ها عیناً همان چیزی است که مجری اجرا می‌کند (coerce*) — یعنی
// integer: عدد صحیح یا رشته‌ی عددی؛ boolean: بول یا "true"/"false" یا ۰/۱؛
// string: رشته یا عدد/بول. فیلدهای اضافی رد نمی‌شوند (مدل‌ها گاهی فیلد اضافه
// می‌فرستند). ابزار سفارشی/ناشناخته → بدون خطا (مسیر execCustomTool).
func validateToolArgs(name string, args map[string]interface{}) error {
	specs, ok := toolArgSpecs[name]
	if !ok {
		return nil // custom_* یا ابزار ناشناخته — شمای آزاد
	}
	for _, sp := range specs {
		v, present := args[sp.key]
		if !present || v == nil {
			if sp.required {
				return fmt.Errorf("ERROR: invalid arguments for %s: missing required field %q.", name, sp.key)
			}
			continue
		}
		switch sp.typ {
		case "string":
			if _, ok := coerceString(v); !ok {
				return fmt.Errorf("ERROR: invalid arguments for %s: field %q must be a string, got %s.", name, sp.key, jsonTypeName(v))
			}
		case "integer":
			if _, ok := coerceInt(v); !ok {
				got := jsonTypeName(v)
				if _, isNum := v.(float64); isNum {
					got = "non-integer number" // مثل 1.5
				}
				return fmt.Errorf("ERROR: invalid arguments for %s: field %q must be an integer (a whole number or a numeric string such as \"60\"), got %s.", name, sp.key, got)
			}
		case "boolean":
			if _, ok := coerceBool(v); !ok {
				return fmt.Errorf("ERROR: invalid arguments for %s: field %q must be a boolean (true/false, \"true\"/\"false\" or 0/1), got %s.", name, sp.key, jsonTypeName(v))
			}
		case "todos":
			// آرایهٔ todo_write با تحمل نوع ضعیف (رشتهٔ JSON هم قبول) —
			// عمق اعتبارسنجی در parseTodoItems است (todos.go).
			if _, ok := coerceTodoList(v); !ok {
				return fmt.Errorf("ERROR: invalid arguments for %s: field %q must be an array of todo items (a JSON string containing the array is also accepted), got %s.", name, sp.key, jsonTypeName(v))
			}
		case "edits":
			// آرایهٔ edit_file با تحمل نوع ضعیف (رشتهٔ JSON هم قبول) —
			// عمق اعتبارسنجی در parseEditOps است (agent.go).
			if _, ok := coerceJSONArray(v); !ok {
				return fmt.Errorf("ERROR: invalid arguments for %s: field %q must be an array of edit items (a JSON string containing the array is also accepted), got %s.", name, sp.key, jsonTypeName(v))
			}
		case "paths":
			// فهرست مسیر: آرایهٔ رشته (رشتهٔ JSON هم قبول — همان تحمل ابزارهای دیگر).
			arr, ok := coerceJSONArray(v)
			if !ok {
				return fmt.Errorf("ERROR: invalid arguments for %s: field %q must be an array of path strings, got %s.", name, sp.key, jsonTypeName(v))
			}
			for _, it := range arr {
				if _, ok := coerceString(it); !ok {
					return fmt.Errorf("ERROR: invalid arguments for %s: field %q must contain only strings, got %s.", name, sp.key, jsonTypeName(it))
				}
			}
		}
		if sp.nonEmpty {
			if s, ok := coerceString(v); ok && strings.TrimSpace(s) == "" {
				return fmt.Errorf("ERROR: invalid arguments for %s: field %q must not be empty.", name, sp.key)
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------- امضای فراخوانی و محافظ حلقه

// canonicalToolSignature امضای پایدار (نام ابزار + آرگومان‌های استانداردشده).
//
// استانداردسازی فقط «ساختاری» است: JSON parse و marshal دوباره (کلیدها با
// sort داخلی encoding/json مرتب می‌شوند و فاصله‌گذاری یکدست می‌شود). به
// متن‌ها هیچ دست نمی‌زنیم — نویسه‌های صفر-عرض (ZWNJ)، حروف عربی/فارسی و
// ارقام بایت‌به‌بایت حفظ می‌شوند (بخش ۲: نرمال‌سازی ممنوع). JSON خراب به
// رشته‌ی خامِ خودش هش می‌شود تا تکرارِ همان JSON خراب هم بشمارده شود.
// خروجی: هش FNV-1a در مبنای ۳۶ (کوتاه و قابل مقایسه).
func canonicalToolSignature(name, argsJSON string) string {
	h := fnv.New64a()
	h.Write([]byte(name))
	h.Write([]byte{0})
	if s := strings.TrimSpace(argsJSON); s != "" {
		var args map[string]interface{}
		if json.Unmarshal([]byte(s), &args) == nil && args != nil {
			// Marshal یک map کلیدها را مرتب می‌کند؛ خروجی برای ورودیِ
			// semantically یکسان، یکسان است (حتی با ترتیب/فاصله‌ی متفاوت).
			if canon, err := json.Marshal(args); err == nil {
				h.Write(canon)
				return strconv.FormatUint(h.Sum64(), 36)
			}
		}
		h.Write([]byte(s))
	}
	return strconv.FormatUint(h.Sum64(), 36)
}

// آستانه‌های محافظ حلقه (طبق مشخصات.
const (
	toolRepeatBlockRun = 3 // سومین فراخوانی یکسانِ متوالی → اجرا نمی‌شود؛ خطا به مدل
	toolRepeatStopRun  = 5 // پنجمین → نوبت با اعلان فارسی متوقف می‌شود
)

// toolLoopGuard شمارنده‌ی فراخوانی‌های «یکسانِ متوالی» در یک نوبت ایجنت.
//
// مالکیت: فقط گوروتین streamChat (تک‌نخی) آن را می‌سازد و صدا می‌زند؛
// نیازی به قفل ندارد. «متوالی» یعنی پشت‌سرهم در همان نوبت — فراخوانیِ
// متفاوتِ وسط، شمارنده را صفر می‌کند.
type toolLoopGuard struct {
	lastSig  string
	run      int
	lastName string
}

// observe یک فراخوانی را ثبت می‌کند و تعداد تکرارهای متوالی (شامل همین
// فراخوانی) را برمی‌گرداند: ۱ یعنی نخستین بار.
func (g *toolLoopGuard) observe(name, argsJSON string) int {
	sig := canonicalToolSignature(name, argsJSON)
	if sig == g.lastSig {
		g.run++
	} else {
		g.lastSig = sig
		g.run = 1
	}
	g.lastName = name
	return g.run
}

// repeatBlockedToolError پیام خطای بار سوم/چهارم — انگلیسی، رو به مدل، با
// دستور روشن برای تغییر رویکرد.
func repeatBlockedToolError(tool string, run int) string {
	return fmt.Sprintf(
		"ERROR: identical repeated tool call blocked. The exact same call to %s with the exact same arguments has now been made %d times in a row; re-running it cannot change the result. "+
			"Read the previous tool output above and continue with a DIFFERENT approach: different arguments, a different tool, or explain the blocker to the user.",
		tool, run)
}

// toolTurnStoppedError پیام نتیجه‌ی ابزاری که بعد از توقف نوبت می‌آید
// (بقیه‌ی فراخوانی‌های همان دسته) — جفت‌بندی tool_calls/tool حفظ می‌شود.
const toolTurnStoppedError = "ERROR: this turn was stopped by the loop guard: the same tool call (identical tool and arguments) was repeated too many times in a row. The turn has ended; changes already applied are kept."

// doomLoopStopNotice اعلان فارسی پایان نوبت به کاربر (پیام نهایی دستیار).
// faRun ارقام فارسیِ تعداد تکرار است (با faDigits آماده می‌شود).
func doomLoopStopNotice(tool, faRun string) string {
	return "\n\n⚠️ فراخوانی ابزار «" + tool + "» با همان آرگومان‌ها " + faRun +
		" بار پشت‌سرهم تکرار شد؛ برای جلوگیری از حلقه‌ی بی‌پایان، این پاسخ متوقف شد. " +
		"کارِ انجام‌شده تا همین‌جا ذخیره است؛ لطفاً درخواست را دقیق‌تر یا متفاوت تکرار کنید."
}

// ---------------------------------------------------------------- حافظه‌ی stream_options per-provider

// streamUsageMem حافظه‌ی درون‌حافظه‌ای «این سرویس‌دهنده stream_options را
// نمی‌پذیرد». فقط تا restart عمر دارد (مطابق مشخصات: remember in memory).
// کلید: baseURL با TrimRight("/") — همان نرمال‌سازیِ ساخت URL درخواست.
var streamUsageMem struct {
	mu     sync.Mutex
	broken map[string]bool
}

// streamUsageProviderKey کلید یکسان‌سازی baseURL برای حافظه.
func streamUsageProviderKey(baseURL string) string {
	return strings.TrimSpace(strings.TrimRight(baseURL, "/"))
}

// providerSupportsStreamUsage آیا برای این سرویس‌دهنده هنوز stream_options
// می‌فرستیم؟ (پیش‌فرض بله تا اولین ۴۰۰ِ مربوط به آن.)
func providerSupportsStreamUsage(baseURL string) bool {
	k := streamUsageProviderKey(baseURL)
	streamUsageMem.mu.Lock()
	defer streamUsageMem.mu.Unlock()
	if streamUsageMem.broken == nil {
		return true
	}
	return !streamUsageMem.broken[k]
}

// markStreamUsageBroken سرویس‌دهنده را در حافظه نشانه‌گذاری می‌کند.
func markStreamUsageBroken(baseURL string) {
	k := streamUsageProviderKey(baseURL)
	streamUsageMem.mu.Lock()
	defer streamUsageMem.mu.Unlock()
	if streamUsageMem.broken == nil {
		streamUsageMem.broken = map[string]bool{}
	}
	streamUsageMem.broken[k] = true
}

// resetStreamUsageMemoryForTest حافظه‌ی per-provider را برای تست‌ها پاک
// می‌کند و مقدار قبلی را برمی‌گرداند تا در cleanup بازگردانده شود (جلوگیری
// از نشت وضعیت بین تست‌های همان پکیج).
func resetStreamUsageMemoryForTest() map[string]bool {
	streamUsageMem.mu.Lock()
	old := streamUsageMem.broken
	streamUsageMem.broken = map[string]bool{}
	streamUsageMem.mu.Unlock()
	return old
}

// restoreStreamUsageMemoryForTest مقدار قبلی حافظه را برمی‌گرداند (cleanup).
func restoreStreamUsageMemoryForTest(old map[string]bool) {
	streamUsageMem.mu.Lock()
	streamUsageMem.broken = old
	streamUsageMem.mu.Unlock()
}

// isStreamOptionsUnsupported آیا این پاسخ ۴۰۰ دقیقاً به‌خاطر stream_options
// است؟ (متن خطا باید به آن اشاره کند — همان قرارداد isMaxTokensError برای
// max_tokens در quickPing.)
func isStreamOptionsUnsupported(status int, body string) bool {
	if status != 400 {
		return false
	}
	b := strings.ToLower(body)
	for _, k := range []string{"stream_options", "include_usage", "stream options", "include usage"} {
		if strings.Contains(b, k) {
			return true
		}
	}
	return false
}
