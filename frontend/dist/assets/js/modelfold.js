/* MyAI — جمع‌شدن گروه مدل‌های هر Provider در انتخاب‌گر مدلِ چت‌باکس
 * (modelfold.js → #mini-model / #model-popover)
 *
 * چرا: بعضی Providerها ده‌ها مدل دارند و فهرست مدل‌ها شلوغ می‌شود. حالا
 * عنوان هر Provider یک دکمه‌ی باز/بسته است (فلش کوچک کنار نام) و وضعیت
 * جمع‌بودن در localStorage می‌ماند، پس بار بعد هم همان گروه‌ها بسته‌اند.
 *
 * تعامل با جستجو (popovers.js): تا وقتی در جعبه‌ی جستجو متنی هست همه‌ی
 * گروه‌ها باز می‌شوند تا نتیجه‌ی جستجو در گروه جمع‌شده پنهان نماند؛ با
 * خالی‌شدن جستجو، وضعیت قبلی برمی‌گردد. گروه‌های بی‌نتیجه را همان فیلتر
 * قبلی پنهان می‌کند (ما display آن‌ها را دست نمی‌زنیم؛ فقط hiddenِ آپشن‌ها).
 */
(function () {
	"use strict";

	var KEY = "myai.modelFold";
	var pop = document.getElementById("model-popover");
	if (!pop) return;

	var collapsed = readState();

	function readState() {
		var out = Object.create(null);
		try {
			var raw = localStorage.getItem(KEY);
			var arr = raw ? JSON.parse(raw) : [];
			if (Array.isArray(arr)) {
				for (var i = 0; i < arr.length; i++) out[String(arr[i])] = true;
			}
		} catch (e) {
			/* وضعیت خراب = همه باز */
		}
		return out;
	}

	function persist() {
		try {
			var arr = [];
			for (var k in collapsed) {
				if (collapsed[k]) arr.push(k);
			}
			localStorage.setItem(KEY, JSON.stringify(arr));
		} catch (e) {
			/* حافظه‌ی محلی در دسترس نیست — فقط همین نشست */
		}
	}

	function searchQuery() {
		var inp = document.getElementById("model-search");
		return inp ? String(inp.value || "").trim() : "";
	}

	// groupName نام Provider از عنوان گروه (بدون نقطه‌ی رنگی و فلش).
	function groupName(lab) {
		var spans = lab.querySelectorAll("span");
		for (var i = 0; i < spans.length; i++) {
			var cls = String(spans[i].className || "");
			if (cls.indexOf("prov-dot") !== -1 || cls.indexOf("fold-caret") !== -1) continue;
			var t = String(spans[i].textContent || "").trim();
			if (t) return t;
		}
		return String(lab.textContent || "").trim();
	}

	function optsOf(lab) {
		var out = [];
		var el = lab.nextElementSibling;
		while (el && !el.classList.contains("group-label") && !el.classList.contains("foot-link")) {
			if (el.classList.contains("opt")) out.push(el);
			el = el.nextElementSibling;
		}
		return out;
	}

	function ensureCaret(lab) {
		var c = lab.querySelector(".fold-caret");
		if (c) return c;
		c = document.createElement("span");
		c.className = "fold-caret";
		c.setAttribute("aria-hidden", "true");
		if (window.Icons && typeof window.Icons.get === "function") {
			c.innerHTML = window.Icons.get("chevron", 13);
		} else {
			c.textContent = "▾";
		}
		lab.appendChild(c);
		return c;
	}

	function apply() {
		if (!pop.classList.contains("open")) return;
		var q = searchQuery();
		var labels = pop.querySelectorAll(".group-label");
		for (var i = 0; i < labels.length; i++) {
			var lab = labels[i];
			var name = groupName(lab);
			ensureCaret(lab);
			var folded = !!collapsed[name];
			if (folded) lab.classList.add("collapsed");
			else lab.classList.remove("collapsed");
			lab.setAttribute("role", "button");
			lab.setAttribute("tabindex", "0");
			lab.setAttribute("aria-expanded", folded ? "false" : "true");
			lab.setAttribute("title", folded ? "باز کردن مدل‌های " + name : "بستن مدل‌های " + name);
			var opts = optsOf(lab);
			for (var j = 0; j < opts.length; j++) {
				opts[j].hidden = folded && !q;
			}
		}
	}

	function toggle(lab) {
		var name = groupName(lab);
		if (!name) return;
		if (collapsed[name]) delete collapsed[name];
		else collapsed[name] = true;
		persist();
		apply();
	}

	// کلیک/کیبورد روی عنوان گروه؛ کلیک روی #mini-model پنجره را باز می‌کند و
	// بعد از رندر popovers.js (۳۰ms) وضعیت جمع‌شده‌ها اعمال می‌شود.
	document.addEventListener(
		"click",
		function (ev) {
			var t = ev.target;
			if (!t || !t.closest) return;
			var lab = t.closest("#model-popover .group-label");
			if (lab) {
				ev.preventDefault();
				toggle(lab);
				return;
			}
			if (t.closest("#mini-model")) setTimeout(apply, 60);
		},
		true
	);

	document.addEventListener(
		"keydown",
		function (ev) {
			if (ev.key !== "Enter" && ev.key !== " ") return;
			var t = ev.target;
			if (!t || !t.closest) return;
			var lab = t.closest("#model-popover .group-label");
			if (!lab) return;
			ev.preventDefault();
			toggle(lab);
		},
		true
	);

	// تایپ در جستجو: فیلتر popovers.js اول اجرا می‌شود، بعد وضعیت گروه‌ها.
	document.addEventListener(
		"input",
		function (ev) {
			if (ev.target && ev.target.id === "model-search") setTimeout(apply, 0);
		},
		true
	);

	// باز شدن پنجره و بازسازی innerHTML توسط popovers.js.
	var scheduled = false;
	new MutationObserver(function () {
		if (scheduled) return;
		scheduled = true;
		setTimeout(function () {
			scheduled = false;
			apply();
		}, 40);
	}).observe(pop, { childList: true, attributes: true, attributeFilter: ["class"] });
})();
