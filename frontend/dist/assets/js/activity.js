// KodYar — نوار «گزارش زنده» (که پیش‌تر «فعالیت ایجنت» نام داشت).
//
// هدف: دقیقاً همان چیزی که کاربر از یک ایجنت حرفه‌ای می‌بیند — جمله‌های
// انسانی فارسی برای هر کاری که ایجنت انجام می‌دهد: «در حال خواندن فایل
// x»، «فایل x ویرایش شد»، «حذف x ناموفق بود» — با وضعیت موفق/ناموفق و زمان.
//
// اتصال داده (کاملاً فرانت‌اندی، بدون تغییر بک‌اند):
//  - رویداد «chat:tool» ← شروع کار (callId، name، args)؛
//  - رویداد «chat:tool_result» ← پایان همان کار (ok، result، ms)؛
//  - «chat:start» نوبت تازه را شروع می‌کند (نوار پاک می‌شود) و
//    «chat:done» با شیء گفتگو، نوار را از پیام‌های role=tool بازمی‌سازد؛
//  - رویداد «chat:error» کارهای در جریان را «ناموفق» می‌کند؛
//  - پچ GetConversation (مثل todos.js، با تعویق یک macrotask) برای
//    بازگردانی فعالیت آخرین نوبت هنگام باز کردن گفتگوی ذخیره‌شده؛
//  - MutationObserver روی #empty-state برای پاک‌شدن با «گفتگوی تازه».
//
// امنیت متن: همه‌چیز با textContent درج می‌شود (هیچ innerHTMLای نیست) چون
// مسیر/دستور/پیام خطا از مدل می‌آید؛ مسیرها داخل <bdi dir="ltr"> ایزوله
// می‌شوند تا در چیدمان RTL به‌هم نریزند. طول متن‌ها کران‌دار است.
(function () {
	"use strict";

	var utils = window.KodYarUtils || {};
	var $ = utils.$ || function (sel) { return document.querySelector(sel); };
	var fa = utils.fa || function (n) { return String(n).replace(/\d/g, function (d) { return "۰۱۲۳۴۵۶۷۸۹"[d]; }); };

	var MAX_ROWS = 40;    // سقف ردیف‌های نگه‌داری‌شده (حافظه/UI)
	var MAX_DETAIL = 80;  // سقف طول مسیر/دستور در متن ردیف
	var MAX_ERROR = 120;  // سقف طول پیام خطا در ردیف ناموفق

	// «فعل» هر ابزار: پیشوند حالت جاری، اجزای حالت موفق و پیشوند شکست.
	// detailFor هر ابزار «جزئیات» را می‌سازد (مسیر یا عبارت، از قبل با «»).
	var VERBS = {
		list_dir:     { run: "در حال بررسی پوشه", okPre: "پوشه", okPost: "بررسی شد", fail: "بررسی پوشه" },
		read_file:    { run: "در حال خواندن", okPre: "", okPost: "خوانده شد", fail: "خواندن" },
		write_file:   { run: "در حال نوشتن", okPre: "", okPost: "نوشته شد", fail: "نوشتن" },
		edit_file:    { run: "در حال ویرایش", okPre: "", okPost: "ویرایش شد", fail: "ویرایش" },
		move_path:    { run: "در حال جابه‌جایی", okPre: "", okPost: "جابه‌جا شد", fail: "جابه‌جایی" },
		delete_path:  { run: "در حال حذف", okPre: "", okPost: "حذف شد", fail: "حذف" },
		create_dir:   { run: "در حال ساخت پوشه", okPre: "", okPost: "ساخته شد", fail: "ساخت پوشه" },
		search_files: { run: "در حال جست‌وجوی", okPre: "جست‌وجوی", okPost: "انجام شد", fail: "جست‌وجوی" },
		glob:         { run: "در حال بررسی الگوی", okPre: "الگوی", okPost: "بررسی شد", fail: "بررسی الگوی" },
		find_symbol:     { run: "در حال یافتن نماد", okPre: "نماد", okPost: "یافته شد", fail: "یافتن نماد" },
		find_references: { run: "در حال یافتن کاربردهای", okPre: "کاربردهای", okPost: "یافته شد", fail: "یافتن کاربردهای" },
		run_command:  { run: "در حال اجرای دستور", okPre: "دستور", okPost: "اجرا شد", fail: "اجرای دستور" },
		process_output: { run: "در حال خواندن لاگ", okPre: "لاگ", okPost: "خوانده شد", fail: "خواندن لاگ" },
		process_stop:   { run: "در حال توقف", okPre: "فرایند", okPost: "متوقف شد", fail: "توقف" },
		process_list:   { run: "در حال خواندن فهرست فرایندها", okPre: "فهرست فرایندها", okPost: "خوانده شد", fail: "خواندن فهرست فرایندها" },
		git_status:   { run: "در حال بررسی وضعیت گیت", okPre: "وضعیت گیت", okPost: "بررسی شد", fail: "بررسی وضعیت گیت" },
		git_diff:     { run: "در حال گرفتن diff", okPre: "diff", okPost: "گرفته شد", fail: "گرفتن diff" },
		git_log:      { run: "در حال خواندن تاریخچه", okPre: "تاریخچه", okPost: "خوانده شد", fail: "خواندن تاریخچه" },
		git_show:     { run: "در حال نمایش", okPre: "کامیت", okPost: "نمایش داده شد", fail: "نمایش" },
		task:         { run: "در حال سپردن زیرکار", okPre: "زیرکار", okPost: "انجام شد", fail: "سپردن زیرکار" },
		todo_write:   { run: "در حال به‌روزرسانی برنامهٔ کار", okPre: "برنامهٔ کار", okPost: "به‌روزرسانی شد", fail: "به‌روزرسانی برنامهٔ کار" },
		web_search:   { run: "در حال جست‌وجوی وب", okPre: "جست‌وجوی", okPost: "انجام شد", fail: "جست‌وجوی" },
		web_fetch:    { run: "در حال دریافت", okPre: "", okPost: "دریافت شد", fail: "دریافت" }
	};
	var MCP_VERBS = { run: "در حال اجرای ابزار MCP", okPre: "ابزار MCP", okPost: "اجرا شد", fail: "اجرای ابزار MCP" };
	var CUSTOM_VERBS = { run: "در حال اجرای ابزار سفارشی", okPre: "ابزار", okPost: "اجرا شد", fail: "اجرای ابزار" };
	var GENERIC_VERBS = { run: "در حال اجرا", okPre: "کار", okPost: "انجام شد", fail: "اجرای" };

	var rows = [];        // آخرین فعالیت‌ها (قدیمی → جدید)
	var byCall = Object.create(null); // callId → ردیف (برای به‌روزرسانی نتیجه)
	// collapsed فقط حافظهٔ همین نشست است. manualCollapse یعنی کاربر خودش
	// جمع/باز کرده؛ در این حالت جمع‌شدن خودکارِ پایان نوبت آن را نمی‌بندد.
	var collapsed = false;
	var manualCollapse = false;
	var enabledOverride = null; // null = از تنظیمات بخوان
	var panel, listEl, countEl, chevBtn;

	// ---------------------------------------------------------------- کمکی‌ها

	function verbsFor(name) {
		if (VERBS[name]) return VERBS[name];
		if (name && name.indexOf("mcp_") === 0) return MCP_VERBS;
		if (name && name.indexOf("custom_") === 0) return CUSTOM_VERBS;
		return GENERIC_VERBS;
	}

	function parseArgs(s) {
		try { return s ? JSON.parse(s) : {}; } catch (e) { return {}; }
	}

	function q(s) { // مسیر/عبارت را با گیومه و کران‌دار برمی‌گرداند
		var t = String(s == null ? "" : s).trim();
		if (!t) return "";
		if (t.length > MAX_DETAIL) t = t.slice(0, MAX_DETAIL) + "…";
		return "«" + t + "»";
	}

	// wsLabel نام پوشه‌ی کاری (ریشه‌ی پروژه) است. ابزارهای مسیرمحور ریشه را
	// با «.» هدف می‌گیرند و نوار فعالیت عیناً «.» نشان می‌داد؛ کاربر نام
	// پوشه/آدرس را نمی‌فهمید. حالا «.» و «./x» با نام واقعی پوشه نمایش
	// داده می‌شوند.
	function wsLabel() {
		var st = window.KodYarState;
		var ws = st && st.view && typeof st.view.workspace === "string" ? st.view.workspace.trim() : "";
		ws = ws.replace(/[\\/]+$/, "");
		if (!ws) return "ریشه‌ی پروژه";
		var parts = ws.split(/[\\/]/);
		return parts[parts.length - 1] || ws;
	}

	function prettyPath(p) {
		var t = String(p == null ? "" : p).trim();
		if (!t || t === "." || t === "./" || t === ".\\") return wsLabel();
		if (t.indexOf("./") === 0) return wsLabel() + "/" + t.slice(2);
		if (t.indexOf(".\\") === 0) return wsLabel() + "\\" + t.slice(2);
		return t;
	}

	// detailFor جزئیاتِ نمایشیِ هر کار را می‌سازد (همه از آرگومان‌های مدل).
	function detailFor(name, a) {
		a = a || {};
		var s = function (k) { return a[k] == null ? "" : String(a[k]); };
		switch (name) {
			case "list_dir":
				return q(prettyPath(s("path")));
			case "read_file":
			case "write_file":
			case "delete_path":
			case "create_dir":
				return q(prettyPath(s("path")));
			case "edit_file": {
				var n = Array.isArray(a.edits) ? a.edits.length : 0;
				return q(prettyPath(s("path"))) + (n > 1 ? " (" + fa(n) + " ویرایش)" : "");
			}
			case "move_path":
				return q(s("from")) + " → " + q(s("to"));
			case "search_files": {
				var inc = s("include");
				return q(s("query")) + (inc ? " در " + q(inc) : "");
			}
			case "glob":
				return q(s("pattern"));
			case "find_symbol":
				return q(s("query"));
			case "find_references":
				return q(s("symbol"));
			case "run_command":
			case "custom_command":
				return q(s("command"));
			case "process_output":
			case "process_stop":
				return q(s("id"));
			case "git_show":
				return q(s("ref"));
			case "git_diff": {
				var p = s("path");
				if (p) return q(prettyPath(p));
				return a.staged === true || a.staged === "true" ? "تغییرات stage شده" : "";
			}
			case "git_log":
				return q(s("path"));
			case "task":
				return q(s("description"));
			case "todo_write": {
				var items = Array.isArray(a.todos) ? a.todos : null;
				if (!items && typeof a.todos === "string") {
					try { items = JSON.parse(a.todos); } catch (e) { items = null; }
				}
				if (!Array.isArray(items)) return "";
				var done = 0;
				for (var i = 0; i < items.length; i++) {
					if (items[i] && items[i].status === "completed") done++;
				}
				// «۲ از ۵» — همان زبانی که پنل «برنامهٔ کار» استفاده می‌کند
				return fa(done) + " از " + fa(items.length);
			}
			case "web_search":
				return q(s("query"));
			case "web_fetch":
				return q(s("url"));
			case "todo_list":
				return "";
			default:
				if (name && (name.indexOf("mcp_") === 0 || name.indexOf("custom_") === 0)) {
					return q(name.replace(/^(mcp|custom)_/, ""));
				}
				return "";
		}
	}

	// shortError یک جملهٔ کوتاه از نتیجهٔ ناموفق می‌سازد: آخرین خط خطا اگر
	// بود، وگرنه اولین خط معنادار؛ پیشوند فنی حذف و طول کران‌دار می‌شود.
	function shortError(result) {
		var raw = String(result == null ? "" : result);
		if (!raw.trim()) return "";
		var lines = raw.split(/\r?\n/);
		var picked = "";
		for (var i = lines.length - 1; i >= 0; i--) {
			var l = lines[i].trim();
			if (!l) continue;
			if (/^(ERROR\b|\[command failed|error:)/i.test(l)) { picked = l; break; }
		}
		if (!picked) {
			for (var j = 0; j < lines.length; j++) {
				if (lines[j].trim()) { picked = lines[j].trim(); break; }
			}
		}
		picked = picked.replace(/^ERROR:\s*/i, "").replace(/\s+/g, " ").trim();
		if (picked.length > MAX_ERROR) picked = picked.slice(0, MAX_ERROR) + "…";
		return picked;
	}

	function joinWords(parts) {
		var out = [];
		for (var i = 0; i < parts.length; i++) {
			var p = String(parts[i] == null ? "" : parts[i]).trim();
			if (p) out.push(p);
		}
		return out.join(" ");
	}

	// activityEnabled آیا نوار «گزارش زنده» روشن است؟ (تنظیم سراسری،
	// پیش‌فرض روشن). خاموش‌کردن فقط نمایش را می‌بندد؛ داده‌ها می‌مانند.
	function activityEnabled() {
		if (enabledOverride !== null) return enabledOverride;
		var st = window.KodYarState;
		if (st && st.view && typeof st.view.showActivity === "boolean") return st.view.showActivity;
		return true;
	}

	// taskChip ارجاع کوتاه به وظیفه‌ی در جریان (از پنل «برنامهٔ کار»).
	// اگر چک‌لیستی نباشد یا هیچ وظیفه‌ای در جریان نباشد، خالی است.
	function taskChip() {
		var api = window.KodYarTodos;
		if (!api || typeof api.currentTask !== "function") return "";
		var t = null;
		try { t = api.currentTask(); } catch (e) { return ""; }
		if (!t || !t.total) return "";
		return "وظیفهٔ " + fa(t.index) + " از " + fa(t.total);
	}

	// sentence متن نهایی یک ردیف را (بدون جزئیات، برای title) می‌سازد.
	function sentence(r) {
		// ردیف‌های برنامه متن اختصاصی دارند (نه فعل عمومی ابزار)
		if (r.name === "todo_write") {
			if (r.state === "running") return "در حال به‌روزرسانی برنامهٔ کار…";
			if (r.state === "failed") return "به‌روزرسانی برنامهٔ کار ناموفق بود" + (r.error ? " — " + r.error : "");
			return "برنامهٔ کار به‌روزرسانی شد" + (r.detail ? " — " + r.detail : "");
		}
		var v = verbsFor(r.name);
		if (r.state === "running") return joinWords([v.run, r.detail]) + "…";
		if (r.state === "failed") return joinWords([v.fail, r.detail, "ناموفق بود"]) + (r.error ? " — " + r.error : "");
		return joinWords([v.okPre, r.detail, v.okPost]);
	}

	// ---------------------------------------------------------------- ساخت پنل

	function ensureContainer() {
		if (panel) return true;
		var wrap = $("#composer-wrap");
		if (!wrap) return false;
		panel = document.createElement("section");
		panel.id = "activity-panel";
		panel.className = "act-panel";
		panel.setAttribute("dir", "rtl");
		panel.hidden = true;

		var head = document.createElement("div");
		head.className = "act-head";

		var title = document.createElement("span");
		title.className = "act-title";
		title.textContent = "گزارش زنده";

		countEl = document.createElement("span");
		countEl.className = "act-count";

		chevBtn = document.createElement("button");
		chevBtn.type = "button";
		chevBtn.className = "act-chev";
		chevBtn.title = "جمع/باز کردن فعالیت‌ها";
		chevBtn.setAttribute("aria-expanded", "true");
		chevBtn.addEventListener("click", function () {
			setCollapsed(!collapsed);
		});

		head.appendChild(title);
		head.appendChild(countEl);
		head.appendChild(chevBtn);
		head.addEventListener("click", function (ev) {
			if (ev.target === title || ev.target === head || ev.target === countEl) {
				setCollapsed(!collapsed);
			}
		});

		listEl = document.createElement("ul");
		listEl.className = "act-list";
		listEl.setAttribute("aria-live", "polite");

		panel.appendChild(head);
		panel.appendChild(listEl);
		// درست بالای composer و زیر پنل «برنامهٔ کار» (todos.js اول درج می‌کند).
		var composer = $("#composer");
		if (composer && composer.parentNode === wrap) wrap.insertBefore(panel, composer);
		else wrap.insertBefore(panel, wrap.firstChild);
		return true;
	}

	function appendText(el, s) {
		if (!s) return;
		el.appendChild(document.createTextNode(s));
	}

	function appendDetail(el, s) {
		if (!s) return;
		var b = document.createElement("bdi");
		b.setAttribute("dir", "ltr");
		b.className = "act-arg";
		b.textContent = s;
		el.appendChild(b);
	}

	// ساخت DOM یک ردیف — همه‌چیز textContent؛ جزئیات در bdi ایزوله.
	function buildRow(r) {
		var v = verbsFor(r.name);
		var li = document.createElement("li");
		li.className = "act-item " + r.state + (r.name === "todo_write" ? " plan" : "");
		li.title = sentence(r);

		var mark = document.createElement("span");
		mark.className = "act-mark";
		mark.setAttribute("aria-hidden", "true");
		mark.textContent = r.state === "ok" ? "✓" : r.state === "failed" ? "✕" : "◆";

		var txt = document.createElement("span");
		txt.className = "act-text";

		if (r.name === "todo_write") {
			// ردیف برنامه یک جملهٔ کامل و بسته است؛ حجزئیات (۲ از ۵) در bdi.
			if (r.state === "running") {
				appendText(txt, "در حال به‌روزرسانی برنامهٔ کار…");
			} else if (r.state === "failed") {
				appendText(txt, "به‌روزرسانی برنامهٔ کار ناموفق بود" + (r.error ? " — " + r.error : ""));
			} else {
				appendText(txt, "برنامهٔ کار به‌روزرسانی شد" + (r.detail ? " — " : ""));
				appendDetail(txt, r.detail);
			}
			var ms0 = document.createElement("span");
			ms0.className = "act-time";
			li.appendChild(mark);
			li.appendChild(txt);
			li.appendChild(ms0);
			return li;
		}

		if (r.state === "running") {
			appendText(txt, v.run + (r.detail ? " " : ""));
			appendDetail(txt, r.detail);
			appendText(txt, "…");
		} else if (r.state === "failed") {
			appendText(txt, v.fail + (r.detail ? " " : ""));
			appendDetail(txt, r.detail);
			appendText(txt, " ناموفق بود" + (r.error ? " — " + r.error : ""));
		} else {
			if (v.okPre) appendText(txt, v.okPre + (r.detail ? " " : ""));
			appendDetail(txt, r.detail);
			appendText(txt, (r.detail ? " " : "") + v.okPost);
		}

		var ms = document.createElement("span");
		ms.className = "act-time";
		if (r.state !== "running" && r.ms > 0) {
			ms.textContent = fa((r.ms / 1000).toFixed(1)) + "s";
		}

		li.appendChild(mark);
		// ارجاع به وظیفه‌ی جاری: همان لحظه‌ای که ردیف ساخته شد
		if (r.task) {
			var chip = document.createElement("span");
			chip.className = "act-task";
			chip.textContent = r.task;
			if (r.taskText) chip.title = r.taskText;
			li.appendChild(chip);
		}
		li.appendChild(txt);
		li.appendChild(ms);
		return li;
	}

	// به‌روزرسانی سربرگ/شمارنده — ارزان و بدون دست‌زدن به ردیف‌ها.
	function updateHeader() {
		if (!ensureContainer()) return;
		var n = rows.length;
		panel.hidden = n === 0;
		listEl.hidden = collapsed;
		// تنظیم «گزارش زنده» خاموش است ← نوار اصلاً دیده نمی‌شود (داده‌ها حفظ
		// می‌شوند؛ روشن‌کردن دوباره همه را برمی‌گرداند).
		var on = activityEnabled();
		panel.hidden = n === 0 || !on;
		if (n === 0 || !on) {
			listEl.textContent = "";
			countEl.textContent = "";
			panel.classList.remove("busy");
			syncSetting();
			return;
		}
		var running = 0;
		var failed = 0;
		for (var i = 0; i < n; i++) {
			if (rows[i].state === "running") running++;
			else if (rows[i].state === "failed") failed++;
		}
		panel.classList.toggle("busy", running > 0);
		// «عملیات» به‌جای «کار» تا با «برنامهٔ کار» (فهرست وظایف) اشتباه نشود.
		countEl.textContent = failed > 0
			? fa(n) + " عملیات · " + fa(failed) + " ناموفق"
			: fa(n) + " عملیات";
		chevBtn.textContent = collapsed ? "▸" : "▾";
		chevBtn.setAttribute("aria-expanded", String(!collapsed));
		syncSetting();
	}

	// رندر کامل — فقط برای بازسازی از تاریخچه، پاک‌کردن یا بازکردن نوار جمع‌شده.
	function renderAll() {
		if (!ensureContainer()) return;
		updateHeader();
		listEl.textContent = "";
		if (collapsed || !rows.length) return;
		var frag = document.createDocumentFragment();
		for (var i = 0; i < rows.length; i++) {
			rows[i].node = buildRow(rows[i]);
			frag.appendChild(rows[i].node);
		}
		listEl.appendChild(frag);
		listEl.scrollTop = listEl.scrollHeight; // جدیدترین ته فهرست است
	}

	// آیا کاربر در انتهای فهرست است؟ (تا اسکرول دستی وسط کار پاره نشود)
	function nearBottom() {
		return listEl.scrollHeight - listEl.scrollTop - listEl.clientHeight < 24;
	}

	// افزودن یک ردیف تازه — فقط یک گره می‌سازد، نه بازسازی کل فهرست.
	function appendRow(r) {
		if (!ensureContainer()) return;
		updateHeader();
		if (collapsed) return; // فهرست بسته است؛ ردیف فقط در داده می‌ماند
		var stick = nearBottom();
		r.node = buildRow(r);
		listEl.appendChild(r.node);
		// سقف ۴۰ ردیف: داده و گره هر دو حذف می‌شوند (بدون نشتی در byCall)
		while (rows.length > MAX_ROWS) {
			var old = rows.shift();
			if (old && old.callId) delete byCall[old.callId];
			if (old && old.node && old.node.parentNode) old.node.parentNode.removeChild(old.node);
			if (old) old.node = null;
		}
		while (listEl.children.length > MAX_ROWS) listEl.removeChild(listEl.firstChild);
		// اگر ردیف کهنه حذف شد، شمارنده دوباره محاسبه شود (نه یک عدد کهنه)
		if (rows.length >= MAX_ROWS) updateHeader();
		if (stick) listEl.scrollTop = listEl.scrollHeight;
	}

	// تغییر وضعیت یک ردیف موجود — فقط همان گره بازسازی می‌شود.
	function refreshRow(r) {
		if (!ensureContainer()) return;
		updateHeader();
		if (collapsed || !r.node || !r.node.parentNode) return;
		var li = buildRow(r);
		r.node.parentNode.replaceChild(li, r.node);
		r.node = li;
	}

	// جمع/باز کردن: در حالت جمع‌شده فهرست از DOM پاک می‌شود (کار/حافظهٔ صفر)
	// و هنگام باز شدن دوباره از داده بازسازی می‌شود. byUser=true یعنی کاربر
	// خودش انتخاب کرده؛ در آن صورت پایان نوبت آن را خودکار نمی‌بندد.
	function setCollapsed(v, byUser) {
		collapsed = !!v;
		if (byUser) manualCollapse = true;
		if (!ensureContainer()) return;
		if (!collapsed) {
			renderAll();
			return;
		}
		updateHeader();
		listEl.textContent = "";
	}

	function activeId() {
		var st = window.KodYarState;
		return st && st.activeId ? String(st.activeId) : "";
	}

	function clearAll() {
		rows = [];
		byCall = Object.create(null);
		renderAll();
	}

	// ---------------------------------------------------------------- رویدادها

	function addRow(callId, name, args, detailOverride) {
		var detail = detailOverride != null ? detailOverride : detailFor(name, parseArgs(args));
		// ابزار برنامه (todo_write) در هر نوبت فقط یک ردیف دارد: ردیف موجود
		// به‌جای ردیف تازه به‌روز می‌شود (بازپخش کل فهرست کار، نوار را شلوغ
		// می‌کند و همان چیزی است که پنل «برنامهٔ کار» یک‌جا نشان می‌دهد).
		if (name === "todo_write") {
			for (var i = rows.length - 1; i >= 0; i--) {
				if (rows[i].name !== "todo_write") continue;
				var prev = rows[i];
				prev.detail = detail;
				prev.state = "running";
				prev.ms = 0;
				prev.error = "";
				if (callId) byCall[callId] = prev;
				prev.reused = true;
				return prev;
			}
		}
		var task = taskChip();
		var r = {
			callId: callId || ("act-" + Math.random().toString(36).slice(2, 8)),
			name: name || "",
			detail: detail,
			state: "running",
			ms: 0,
			error: "",
			task: task,
			taskText: task && window.KodYarTodos && typeof window.KodYarTodos.currentTask === "function"
				? (window.KodYarTodos.currentTask() || {}).content || ""
				: ""
		};
		if (r.callId && byCall[r.callId]) return byCall[r.callId];
		rows.push(r);
		if (r.callId) byCall[r.callId] = r;
		return r;
	}

	function onToolStart(p) {
		if (!p || p.conversationId !== activeId()) return;
		var known = p.callId && byCall[p.callId];
		var r = addRow(p.callId, p.name, p.args);
		if (known) return; // رویداد تکراری — ردیف دومی ساخته نمی‌شود
		if (r.reused) { r.reused = false; refreshRow(r); return; } // ردیف برنامه به‌روز شد
		appendRow(r);
	}

	function onToolResult(p) {
		if (!p || p.conversationId !== activeId()) return;
		var r = p.callId ? byCall[p.callId] : null;
		if (!r) {
			// نتیجه بدون رویداد شروع (مثلاً اتصال دیرهنگام UI): یک ردیف می‌سازیم.
			r = addRow(p.callId, p.name, "");
		}
		if (p.name && !r.name) r.name = p.name;
		r.state = p.ok ? "ok" : "failed";
		r.ms = Number(p.ms) || 0;
		r.error = p.ok ? "" : shortError(p.result);
		refreshRow(r); // فقط همین ردیف دوباره ساخته می‌شود
	}

	// بازسازی از روی پیام‌های ذخیره‌شده: همهٔ ابزارهای «نوبت آخر» (بعد از
	// آخرین پیام کاربر). اگر خالی بود و keepIfEmpty روشن است، نوار پاک نمی‌شود.
	function rebuildFromConversation(conv, keepIfEmpty) {
		if (!conv || !conv.id) return;
		if (String(conv.id) !== activeId()) return;
		var msgs = Array.isArray(conv.messages) ? conv.messages : [];
		var start = -1;
		for (var i = msgs.length - 1; i >= 0; i--) {
			if (msgs[i] && msgs[i].role === "user") { start = i; break; }
		}
		var out = [];
		for (var j = start + 1; j < msgs.length; j++) {
			var m = msgs[j];
			if (!m || m.role !== "tool" || !m.toolName) continue;
			out.push({
				callId: m.id || ("hist-" + j),
				name: m.toolName,
				detail: detailFor(m.toolName, parseArgs(m.toolArgs)),
				state: m.toolOk ? "ok" : "failed",
				ms: Number(m.toolMs) || 0,
				error: m.toolOk ? "" : shortError(m.content)
			});
		}
		// در تاریخچه فقط آخرین ردیف برنامه بماند (بقیه تکرار همان فهرست‌اند)
		var lastPlan = -1;
		for (var p = 0; p < out.length; p++) {
			if (out[p].name === "todo_write") lastPlan = p;
		}
		if (lastPlan >= 0) {
			var kept = [];
			for (var q = 0; q < out.length; q++) {
				if (out[q].name !== "todo_write" || q === lastPlan) kept.push(out[q]);
			}
			out = kept;
		}
		if (!out.length && keepIfEmpty) return;
		rows = out.slice(-MAX_ROWS);
		byCall = Object.create(null);
		for (var k = 0; k < rows.length; k++) {
			if (rows[k].callId && !byCall[rows[k].callId]) byCall[rows[k].callId] = rows[k];
		}
		renderAll();
	}

	function markRunningAsFailed(reason) {
		var changed = false;
		for (var i = 0; i < rows.length; i++) {
			if (rows[i].state !== "running") continue;
			rows[i].state = "failed";
			rows[i].error = reason || "";
			changed = true;
		}
		if (changed) renderAll();
	}

	// ------------------------------------------- تنظیم «گزارش زنده» (تب مدل)
	// این ردیف در همین فایل ساخته می‌شود (مثل editmode.js) تا index.html
	// دست‌کاری نشود؛ دو دکمهٔ روشن/خاموش و ذخیرهٔ فوری در بک‌اند.
	var settingSeg = null;

	function buildSettingRow() {
		if (settingSeg) return;
		var host = document.querySelector("#model-panel .form-card");
		if (!host) return;
		var field = document.createElement("div");
		field.className = "field";
		field.id = "act-setting";
		var label = document.createElement("div");
		label.className = "field-label";
		label.textContent = "گزارش زنده‌ی فعالیت ایجنت";
		var hint = document.createElement("div");
		hint.className = "hint-row";
		hint.textContent = "نوار بالای جعبه‌ی پیام که هر عملیات ایجنت (خواندن، نوشتن، دستور) را با وضعیت و زمان نشان می‌دهد. خاموش‌کردنش پنل «برنامهٔ کار» را پنهان نمی‌کند.";
		var seg = document.createElement("div");
		seg.className = "seg-row";
		seg.id = "act-seg";
		[ ["on", "روشن"], ["off", "خاموش"] ].forEach(function (item) {
			var b = document.createElement("button");
			b.type = "button";
			b.className = "seg-btn";
			b.setAttribute("data-act", item[0]);
			b.textContent = item[1];
			b.addEventListener("click", function () { setEnabled(item[0] === "on"); });
			seg.appendChild(b);
		});
		field.appendChild(label);
		field.appendChild(seg);
		field.appendChild(hint);
		host.appendChild(field);
		settingSeg = seg;
		syncSetting();
	}

	function syncSetting() {
		if (!settingSeg) return;
		var on = activityEnabled();
		var btns = settingSeg.querySelectorAll(".seg-btn");
		for (var i = 0; i < btns.length; i++) {
			btns[i].classList.toggle("active", (btns[i].getAttribute("data-act") === "on") === on);
		}
	}

	// setEnabled روشن/خاموش + ذخیرهٔ ماندگار (در پیش‌نمایش مرورگر فقط حافظه).
	function setEnabled(on) {
		enabledOverride = null;
		if (window.KodYarState && window.KodYarState.view) window.KodYarState.view.showActivity = !!on;
		syncSetting();
		updateHeader();
		if (on) renderAll();
		var app = window.go && window.go.main && window.go.main.App;
		if (app && typeof app.SetShowActivity === "function") {
			try { app.SetShowActivity(!!on).catch(function () { /* UI نباید بخوابد */ }); } catch (e) { /* mock */ }
		}
	}

	function connect() {
		buildSettingRow();
		var rt = window.runtime;
		if (rt && typeof rt.EventsOn === "function") {
			try {
				rt.EventsOn("chat:tool", onToolStart);
				rt.EventsOn("chat:tool_result", onToolResult);
				rt.EventsOn("chat:start", function () {
					// نوبت تازه: باز و تازه (انتخاب دستی قبلی معتبر نیست)
					manualCollapse = false;
					collapsed = false;
					clearAll();
				});
				rt.EventsOn("chat:done", function (p) {
					if (p && p.conversation) rebuildFromConversation(p.conversation, true);
					// پایان کار = جمع‌شدن خودکار (اگر کاربر خودش تصمیم نگرفته)
					if (!manualCollapse) setCollapsed(true);
				});
				rt.EventsOn("chat:error", function (p) {
					if (!p || p.conversationId !== activeId()) return;
					markRunningAsFailed("نوبت متوقف شد");
					if (!manualCollapse) setCollapsed(true);
				});
			} catch (e) { /* بدون runtime (تست) — بی‌خطر */ }
		}

		// باز کردن گفتگوی ذخیره‌شده: پچ GetConversation با تأخیر یک macrotask
		// تا activeId توسط app.js به‌روز شده باشد (همان الگوی todos.js).
		var app = window.go && window.go.main && window.go.main.App;
		if (app && typeof app.GetConversation === "function" && !app.GetConversation.__activityPatched) {
			var orig = app.GetConversation;
			var wrapped = function () {
				var args = arguments;
				var self = this;
				return Promise.resolve(orig.apply(self, args)).then(function (conv) {
					setTimeout(function () {
						try { rebuildFromConversation(conv, false); } catch (e) { /* UI نباید بخوابد */ }
					}, 0);
					return conv;
				});
			};
			wrapped.__activityPatched = true;
			try { app.GetConversation = wrapped; } catch (e) { /* فقط‌حالت‌های عجیب */ }
		}

		// «گفتگوی تازه» ← صفحهٔ خالی نمایان می‌شود؛ نوار هم پاک شود.
		var empty = $("#empty-state");
		if (empty && window.MutationObserver) {
			new MutationObserver(function () {
				if (!empty.hidden) clearAll();
			}).observe(empty, { attributes: true, attributeFilter: ["hidden"] });
		}
	}

	if (document.readyState === "loading") {
		document.addEventListener("DOMContentLoaded", connect);
	} else {
		connect();
	}

	// API عمومی برای تست/دیباگ و استفادهٔ سایر ماژول‌ها
	window.KodYarActivity = {
		rebuildFromConversation: rebuildFromConversation,
		setEnabled: setEnabled,
		enabled: activityEnabled,
		detailFor: detailFor,
		sentenceFor: function (name, args, ok, ms, result) {
			var r = {
				name: name,
				detail: detailFor(name, args),
				state: ok ? "ok" : "failed",
				ms: Number(ms) || 0,
				error: ok ? "" : shortError(result)
			};
			return sentence(r);
		},
		clear: clearAll,
		render: renderAll
	};
})();
