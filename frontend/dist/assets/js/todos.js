// MyAI — پنل «برنامهٔ کار» (ابزار todo_write؛ پیش‌تر «کارهای ایجنت»).
//
// چک‌لیست جمع‌وجمع‌شونده‌ای که بالای composer نمایش داده می‌شود و وضعیت
// کارهای اعلام‌شدهٔ ایجنت را زنده نشان می‌دهد. وقتی فهرست خالی است، پنل
// کاملاً پنهان است.
//
// اتصال داده (بدون دست‌بردن به app.js مینیفای‌شده):
//  - رویداد «chat:todos» ← به‌روزرسانی لحظه‌ای (فقط برای گفتگوی فعال؛
//    رویداد گفتگوهای دیگر نادیده گرفته می‌شود)؛
//  - رویداد «chat:start» و «chat:done» ← payload شامل conversation با
//    فیلد todos است (بازسازی هنگام شروع/پایان نوبت)؛
//  - پچ جمع‌وشکل دادن به App.GetConversation ← بازگردانی چک‌لیست هنگام
//    باز کردن گفتگوی ذخیره‌شده (با تأخیر یک macrotask تا activeId توسط
//    app.js به‌روز شده باشد — جزئیات در کامنت خود پچ)؛
//  - MutationObserver روی #empty-state ← کلیک «گفتگوی تازه» پنل را پاک
//    می‌کند (گفتگوی تازه بدون todo شروع می‌شود).
//
// حالت Mock (پیش‌نمایش مرورگر): mock.js همین رویدادها را با runtime
// ساختگی منتشر می‌کند؛ همهٔ مسیرها دفاعی نوشته شده‌اند تا بدون بک‌اند
// هم خطا ندهند. همهٔ متن‌ها با textContent درج می‌شوند (نه innerHTML) و
// متن هر آیتم dir="auto" دارد تا متن انگلیسی/فارسی هرکدام درست نمایش
// داده شوند. اعداد فارسی با MyAIUtils.fa (هماهنگ با بقیهٔ UI).
(function () {
	"use strict";

	var utils = window.MyAIUtils || {};
	var $ = utils.$ || function (sel) { return document.querySelector(sel); };
	var fa = utils.fa || function (n) { return String(n).replace(/\d/g, function (d) { return "۰۱۲۳۴۵۶۷۸۹"[d]; }); };

	var todos = [];        // آخرین وضعیت فهرست
	// پیش‌فرض جمع‌شده: این پنل «برنامه» است، نه گزارش زندهٔ کار. باز‌کردنش
	// درخواستی است تا با نوار «گزارش زنده» که همیشه فعال است اشتباه نشود.
	var collapsed = true;
	var panel, listEl, countEl, chevBtn;
	var lastKey = "";      // امضای آخرین رندر — برای رد کردن به‌روزرسانی تکراری

	// امضای سبک فهرست (وضعیت + متن)؛ اگر رویدادی با همان داده دوباره
	// برسد، DOM بازسازی نمی‌شود (چک‌لیست در هر نوبت چندبار emit می‌شود).
	function keyOf(list) {
		var out = "";
		for (var i = 0; i < list.length; i++) {
			var it = list[i] || {};
			out += String(it.status) + "\u0001" + String(it.content == null ? "" : it.content) + "\u0002";
		}
		return out;
	}

	// برچسب وضعیت هر آیتم (فارسی، هم‌لحن با بقیهٔ پیام‌های برنامه)
	function statusLabel(s) {
		if (s === "completed") return "انجام شد";
		if (s === "in_progress") return "در حال انجام";
		return "در انتظار";
	}

	function ensureContainer() {
		if (panel) return true;
		var wrap = $("#composer-wrap");
		if (!wrap) return false;
		panel = document.createElement("section");
		panel.id = "todos-panel";
		panel.className = "todos-panel";
		panel.setAttribute("dir", "rtl");
		panel.hidden = true;

		var head = document.createElement("div");
		head.className = "todos-head";

		var title = document.createElement("span");
		title.className = "todos-title";
		title.textContent = "برنامهٔ کار";

		countEl = document.createElement("span");
		countEl.className = "todos-count";

		chevBtn = document.createElement("button");
		chevBtn.type = "button";
		chevBtn.className = "todos-chev";
		chevBtn.title = "جمع/باز کردن فهرست";
		chevBtn.setAttribute("aria-expanded", "true");
		chevBtn.addEventListener("click", function () {
			collapsed = !collapsed;
			render(true);
		});

		head.appendChild(title);
		head.appendChild(countEl);
		head.appendChild(chevBtn);
		head.addEventListener("click", function (ev) {
			// کلیک روی خودِ سربرگ هم پنل را جمع/باز می‌کند (دکمه فقط برای
			// دسترس‌پذیری keyboard است)
			if (ev.target === title || ev.target === head || ev.target === countEl) {
				collapsed = !collapsed;
				render(true);
			}
		});

		listEl = document.createElement("ul");
		listEl.className = "todos-list";

		panel.appendChild(head);
		panel.appendChild(listEl);
		wrap.insertBefore(panel, wrap.firstChild);
		return true;
	}

	// رندر کامل پنل از روی todos — بدون innerHTML؛ همه‌چیز textContent.
	// force=true برای تغییرهای UI (جمع/باز) که داده عوض نشده است.
	function render(force) {
		if (!ensureContainer()) return;
		var key = keyOf(todos);
		if (!force && key === lastKey) return;
		lastKey = key;
		var n = todos.length;
		panel.hidden = n === 0;
		if (n === 0) {
			listEl.textContent = "";
			countEl.textContent = "";
			return;
		}
		var done = 0;
		for (var i = 0; i < n; i++) if (todos[i].status === "completed") done++;

		// «۲ از ۵» — ارقام فارسی هماهنگ با بقیهٔ UI
		countEl.textContent = fa(done) + " از " + fa(n);
		chevBtn.textContent = collapsed ? "▸" : "▾";
		chevBtn.setAttribute("aria-expanded", String(!collapsed));
		listEl.hidden = collapsed;

		// بازسازی لیست (ساده و بی‌حافظه — آیتم‌ها دکمه/اینپوت ندارند)
		listEl.textContent = "";
		for (var j = 0; j < n; j++) {
			var it = todos[j] || {};
			var li = document.createElement("li");
			li.className = "todo-item " + (it.status === "completed" ? "done" : it.status === "in_progress" ? "active" : "wait");

			var mark = document.createElement("span");
			mark.className = "todo-mark";
			mark.setAttribute("aria-hidden", "true");
			mark.textContent = it.status === "completed" ? "✓" : it.status === "in_progress" ? "◆" : "○";

			var txt = document.createElement("span");
			txt.className = "todo-text";
			txt.setAttribute("dir", "auto"); // متن مدل ممکن است فارسی یا انگلیسی باشد
			txt.textContent = String(it.content == null ? "" : it.content);

			var lbl = document.createElement("span");
			lbl.className = "todo-status " + (it.status === "completed" ? "s-done" : it.status === "in_progress" ? "s-active" : "s-wait");
			lbl.textContent = statusLabel(it.status);

			li.appendChild(mark);
			li.appendChild(txt);
			li.appendChild(lbl);
			listEl.appendChild(li);
		}
	}

	function activeId() {
		var st = window.MyAIState;
		return st && st.activeId ? String(st.activeId) : "";
	}

	// رویداد chat:todos — فقط گفتگوی فعال
	function onTodosEvent(p) {
		if (!p || p.conversationId !== activeId()) return;
		todos = Array.isArray(p.todos) ? p.todos : [];
		render();
	}

	// بازگردانی از روی شیء گفتگو (chat:start / chat:done / GetConversation)
	//
	// گارد «فقط گفتگوی فعال» اکید است: وقتی هیچ گفتگویی باز نیست (activeId
	// خالی — مثلاً «گفتگوی تازه») یا گفتگوی دیگری باز است، چک‌لیستِ آن گفتگو
	// نباید بالای composer ظاهر شود. پیش‌تر با activeId خالی گارد رد می‌شد و
	// chat:done یک گفتگوی پس‌زمینه، فهرست کارهایش را روی صفحهٔ خالی نشان
	// می‌داد. به همین دلیل chat:start یک macrotask به تعویق می‌افتد تا app.js
	// (که بعد از ما همان رویداد را می‌گیرد) activeId را ست کرده باشد.
	function setFromConversation(conv) {
		if (!conv || !conv.id) return;
		if (String(conv.id) !== activeId()) return;
		todos = Array.isArray(conv.todos) ? conv.todos : [];
		render();
	}

	// پنل را پاک کن (گفتگوی تازه)
	function clearAll() {
		todos = [];
		render(true);
	}

	// ---- اتصال رویدادها ----
	function connect() {
		var rt = window.runtime;
		if (rt && typeof rt.EventsOn === "function") {
			try {
				rt.EventsOn("chat:todos", onTodosEvent);
				rt.EventsOn("chat:start", function (p) {
					// تعویق تا پس از ست‌شدن activeId توسط app.js (همان رویداد،
					// ثبت‌شده بعد از ما) — وگرنه گارد گفتگوی فعال رد می‌کند.
					if (!p || !p.conversation) return;
					var conv = p.conversation;
					setTimeout(function () {
						try { setFromConversation(conv); } catch (e) { /* UI نباید بخوابد */ }
					}, 0);
				});
				rt.EventsOn("chat:done", function (p) {
					if (p && p.conversation) setFromConversation(p.conversation);
				});
			} catch (e) { /* بدون runtime (تست) — بی‌خطر */ }
		}

		// بازکردن گفتگو: نتیجهٔ GetConversation را می‌بینیم و todos را
		// بازگردانی می‌کنیم. نکتهٔ ترتیب: این پچ «قبل از» app.js نصب
		// می‌شود، پس .then ما زودتر از .then خودِ app.js اجرا می‌شود —
		// یعنی هنوز MyAIState.activeId روی شناسهٔ گفتگوی قبلی است و
		// گارد «فقط گفتگوی فعال» گفتگوی تازه را رد می‌کند. تأخیر به
		// یک macrotask (setTimeout 0) تضمین می‌کند activeId توسط
		// app.js به‌روز شده باشد؛ پاسخ‌های واقعاً کهنه (تعویض سریع
		// گفتگو) همچنان توسط همان گارد رد می‌شوند. پچ idempotent است.
		var app = window.go && window.go.main && window.go.main.App;
		if (app && typeof app.GetConversation === "function" && !app.GetConversation.__todosPatched) {
			var orig = app.GetConversation;
			var wrapped = function () {
				var args = arguments;
				var self = this;
				return Promise.resolve(orig.apply(self, args)).then(function (conv) {
				setTimeout(function () {
					try { setFromConversation(conv); } catch (e) { /* UI نباید بخوابد */ }
				}, 0);
				return conv;
			});
			};
			wrapped.__todosPatched = true;
			try { app.GetConversation = wrapped; } catch (e) { /* فقطحالت‌های عجیب */ }
		}

		// «گفتگوی تازه» ← صفحهٔ خالی نمایان می‌شود؛ چک‌لیست هم پاک شود.
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

	// currentTask کارِ در حال انجام را برمی‌گرداند (برای ارجاع خطوط «گزارش
	// زنده» به وظیفه‌ی متناطر): { index, total, content }. اگر هیچ کاری در
	// حال انجام نباشد، null.
	function currentTask() {
		var total = todos.length;
		for (var i = 0; i < total; i++) {
			var it = todos[i] || {};
			if (it.status === "in_progress") {
				return { index: i + 1, total: total, content: String(it.content == null ? "" : it.content) };
			}
		}
		return null;
	}

	// API عمومی برای تست/دیباگ و استفادهٔ سایر ماژول‌ها
	window.MyAITodos = {
		setFromConversation: setFromConversation,
		clear: clearAll,
		render: render,
		currentTask: currentTask
	};
})();
