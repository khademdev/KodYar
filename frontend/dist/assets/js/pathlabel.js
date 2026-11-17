/* MyAI — برچسب مسیر در کارت‌های ابزار گفتگو (pathlabel.js)
 *
 * مشکل: ابزارهای مسیرمحور وقتی ریشه‌ی پوشه‌ی کاری را هدف می‌گیرند آرگومانشان
 * «.» است و کارت ابزار عیناً «.» نشان می‌داد؛ کاربر نام پوشه/آدرس را
 * نمی‌فهمید. برای search_files/glob هم «“عبارت”  in .» دیده می‌شد.
 *
 * راه‌حل کاملاً فرانت‌اندی (app.js مینیفای دست‌نخورده می‌ماند):
 *  - آرگومان خام هر فراخوانی از رویداد chat:tool در یک map کوچک نگه داشته
 *    می‌شود و برچسب همان کارت بازنویسی می‌شود:
 *      "."  / ""  → نام پوشه‌ی کاری،  "./x" → «نام/x»
 *  - کارت‌های تاریخی (که رویداد ندارند) از روی متن نمایش‌داده‌شده نرمال
 *    می‌شوند — همان قاعده‌ها.
 *
 * امنیت متن: همه‌جا textContent؛ هیچ innerHTMLای نیست (مسیر از مدل می‌آید).
 */
(function () {
	"use strict";

	var PATH_TOOLS = {
		list_dir: 1,
		read_file: 1,
		write_file: 1,
		edit_file: 1,
		delete_path: 1,
		create_dir: 1
	};
	var MAX_KEEP = 400; // سقف نگه‌داری آرگومان‌ها در یک نشست
	var argsByCall = Object.create(null);
	var kept = 0;

	function workspace() {
		var st = window.MyAIState;
		return st && st.view && typeof st.view.workspace === "string" ? st.view.workspace.trim() : "";
	}

	// wsLabel نام پوشه‌ی کاری؛ اگر پوشه‌ای انتخاب نشده باشد یک برچسب روشن.
	function wsLabel() {
		var ws = workspace().replace(/[\\/]+$/, "");
		if (!ws) return "ریشه‌ی پروژه";
		var parts = ws.split(/[\\/]/);
		return parts[parts.length - 1] || ws;
	}

	function pretty(p) {
		var t = p == null ? "" : String(p).trim();
		if (!t || t === "." || t === "./" || t === ".\\") return wsLabel();
		if (t.indexOf("./") === 0) return wsLabel() + "/" + t.slice(2);
		if (t.indexOf(".\\") === 0) return wsLabel() + "\\" + t.slice(2);
		return t;
	}

	// labelFromArgs همان قالب app.js را می‌سازد، فقط با مسیر خوانا.
	function labelFromArgs(tool, raw) {
		var a = null;
		try {
			a = raw ? JSON.parse(raw) : null;
		} catch (e) {
			return "";
		}
		if (!a || typeof a !== "object") return "";
		var s = function (k) {
			return a[k] == null ? "" : String(a[k]);
		};
		if (PATH_TOOLS[tool]) {
			return pretty(s("path")) + (tool === "edit_file" ? "  ↺" : "");
		}
		if (tool === "search_files") {
			return "“" + s("query") + "”" + (s("path") ? "  in " + pretty(s("path")) : "");
		}
		if (tool === "glob") {
			return "“" + s("pattern") + "”" + (s("path") ? "  in " + pretty(s("path")) : "");
		}
		return "";
	}

	// labelFromText برای کارت‌های بازسازی‌شده از تاریخچه (بدون رویداد خام).
	function labelFromText(tool, text) {
		var t = text == null ? "" : String(text);
		if (PATH_TOOLS[tool]) {
			if (!t) return tool === "list_dir" ? wsLabel() : "";
			var arrow = /\s+↺$/.test(t);
			var base = arrow ? t.replace(/\s+↺$/, "") : t;
			return pretty(base) + (arrow ? "  ↺" : "");
		}
		if (tool === "search_files" || tool === "glob") {
			return t.replace(/ in \.(?=$|[/\\])/g, " in " + wsLabel());
		}
		return "";
	}

	function fixCard(card) {
		if (!card || card.getAttribute("data-pl-done") === "1") return;
		var argsEl = card.querySelector(".tool-args");
		if (!argsEl) return;
		var nameEl = card.querySelector(".tool-name");
		var tool = nameEl ? String(nameEl.textContent || "").trim() : "";
		var callId = card.getAttribute("data-call") || "";
		var raw = callId && argsByCall[callId];
		card.setAttribute("data-pl-done", "1");
		var next = raw ? labelFromArgs(tool, raw) : "";
		if (!next) next = labelFromText(tool, argsEl.textContent);
		if (next && next !== argsEl.textContent) argsEl.textContent = next;
	}

	function scan(node) {
		if (!node || node.nodeType !== 1) return;
		if (node.classList && node.classList.contains("tool-card")) fixCard(node);
		if (!node.querySelectorAll) return;
		var cards = node.querySelectorAll(".tool-card");
		for (var i = 0; i < cards.length; i++) fixCard(cards[i]);
	}

	function remember(callId, args) {
		if (!callId || kept > MAX_KEEP) return;
		argsByCall[callId] = String(args == null ? "" : args);
		kept++;
	}

	// آرگومان خام: هندلر app.js کارت را می‌سازد و بعد نوبتِ این هندلر است؛
	// observer (microtask بعد از همین task) کارت را با آرگومان واقعی می‌سازد.
	if (window.runtime && typeof window.runtime.EventsOn === "function") {
		window.runtime.EventsOn("chat:tool", function (p) {
			if (p && p.callId) remember(p.callId, p.args);
		});
		window.runtime.EventsOn("chat:done", function () {
			argsByCall = Object.create(null);
			kept = 0;
		});
	}

	var host = document.getElementById("messages");
	if (!host) host = document.body;
	// صف گره‌های تازه: هیچ بچ‌ای در حین بسته‌بودن صف دور ریخته نمی‌شود
	// (چند کارت ابزار که پشت سر هم ساخته می‌شوند).
	var pending = false;
	var queue = [];
	function flushQueue() {
		pending = false;
		var nodes = queue;
		queue = [];
		for (var i = 0; i < nodes.length; i++) scan(nodes[i]);
	}
	new MutationObserver(function (muts) {
		for (var i = 0; i < muts.length; i++) {
			var added = muts[i].addedNodes;
			for (var j = 0; j < added.length; j++) queue.push(added[j]);
		}
		if (pending) return;
		pending = true;
		setTimeout(flushQueue, 0);
	}).observe(host, { childList: true, subtree: true });
})();
