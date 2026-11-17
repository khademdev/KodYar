// ابزارهای حالت چت (بدون ایجنت).
//
// حالت چت «مستقیم با مدل حرف می‌زنیم» است، اما کاربر انتظار دارد از همان‌جا هم
// بتواند بگوید «این را در اینترنت جستجو کن» یا «این فایل/پوشه را پیدا کن».
// بنابراین در حالت چت فقط ابزارهای **خواندنی** به مدل داده می‌شوند:
//
//	web_search, web_fetch                → جستجو در اینترنت و خواندن صفحه
//	list_dir, read_file, search_files,
//	glob, find_symbol                    → جستجو در فایل‌های رایانه (workspace)
//
// نوشتن فایل، اجرای فرمان، پچ، git و ابزارهای MCP/سفارشی عمداً در چت نیستند؛
// آن‌ها کار ایجنت‌اند. هر ابزار وب هم مثل همیشه مودال اجازه‌ی دامنه دارد.
package main

// chatToolAllowlist نام ابزارهای مجاز در حالت چت.
var chatToolAllowlist = map[string]bool{
	"web_search":   true,
	"web_fetch":    true,
	"list_dir":     true,
	"read_file":    true,
	"search_files": true,
	"glob":         true,
	"find_symbol":  true,
}

// filterChatTools فقط ابزارهای خواندنی را نگه می‌دارد (ترتیب اصلی حفظ می‌شود).
func filterChatTools(tools []toolDef) []toolDef {
	out := make([]toolDef, 0, len(tools))
	for _, t := range tools {
		if chatToolAllowlist[t.Function.Name] {
			out = append(out, t)
		}
	}
	return out
}

// chatToolsPromptBlock توضیح کوتاه ابزارهای در دسترس در حالت چت است.
// ws خالی یعنی هیچ ابزاری برای فایل‌ها نیست (پوشهٔ کاری تعیین نشده).
func chatToolsPromptBlock(ws string) string {
	var b []byte
	add := func(s string) { b = append(b, s...) }
	add("You are in plain chat mode: answer directly and concisely. ")
	add("You do have read-only tools for searching, so do not guess when the user asks you to look something up:\n")
	add("- web_search: search the internet for a query; web_fetch: read one URL (the user approves the domain each time).\n")
	if ws != "" {
		add("- list_dir, read_file, search_files, glob, find_symbol: search and read files inside the user's workspace. ")
		add("Paths are relative to the workspace root. You cannot modify, create or run anything in chat mode; ")
		add("if the user asks for changes, say that agent mode is needed.\n")
	} else {
		add("No workspace folder is set, so file tools are unavailable; you can still use web_search/web_fetch.\n")
	}
	return string(b)
}
