// MyAI — جمع/باز کردن نوار کناری.
//
// - دکمه‌ی toggle در ابتدای نوار بالا تزریق می‌شود (بدون تغییر app.js).
// - وضعیت در localStorage می‌ماند و کلاس اولیه در <head> پیش از رندر
//   اعمال می‌شود تا هنگام باز شدن برنامه، سایدبار یک لحظه باز و بسته نشود.
// - هنگام بسته‌شدن، `inert` و aria-hidden روی سایدبار گذاشته می‌شود تا
//   تب‌کردن روی دکمه‌های نامرئی گیر نکند؛ فوکوس به خود دکمه منتقل می‌شود.
// - میان‌بر Ctrl+B (یا Cmd+B) هم همین کار را می‌کند.
(function () {
	"use strict";

	var KEY = "myai.sidebarCollapsed";
	var root = document.documentElement;

	function isCollapsed() {
		return root.classList.contains("sidebar-collapsed");
	}

	function apply(collapsed, persist) {
		var sidebar = document.getElementById("sidebar");
		var toggle = document.getElementById("sidebar-toggle");

		// پاپ‌اور مدل/محیط اگر باز است، با جمع شدن سایدبار بسته شود.
		if (collapsed && window.MyAIPopovers && typeof window.MyAIPopovers.closePopovers === "function") {
			try { window.MyAIPopovers.closePopovers(); } catch (e) { /* noop */ }
		}

		// اگر فوکوس داخل سایدبار است، قبل از inert شدن به دکمه منتقل شود.
		if (collapsed && sidebar && toggle && sidebar.contains(document.activeElement)) {
			toggle.focus();
		}

		root.classList.toggle("sidebar-collapsed", collapsed);

		if (sidebar) {
			sidebar.toggleAttribute("inert", collapsed);
			sidebar.setAttribute("aria-hidden", collapsed ? "true" : "false");
		}
		if (toggle) {
			toggle.setAttribute("aria-expanded", collapsed ? "false" : "true");
			toggle.title = (collapsed ? "باز کردن" : "جمع کردن") + " نوار کناری (Ctrl+B)";
			toggle.setAttribute("aria-label", toggle.title);
		}

		if (persist) {
			try {
				localStorage.setItem(KEY, collapsed ? "1" : "0");
			} catch (e) {
				/* حالت خصوصی/محدود — فقط در همین نشست اعمال می‌شود */
			}
		}
	}

	function toggle() {
		apply(!isCollapsed(), true);
	}

	// «محیط پاسخ‌دهی» از رابط برداشته شد؛ سایدبار فقط گفتگوها، تنظیمات و
	// دکمه‌ی حمایت را نگه می‌دارد و دکمه‌ی تنظیمات به فوتر منتقل می‌شود.
	function mountControls() {
		var sidebar = document.getElementById("sidebar");
		if (!sidebar) return;

		var footer = sidebar.querySelector(".sidebar-footer");
		if (!footer) return;
		var util = document.createElement("div");
		util.className = "sidebar-utility";
		["btn-settings"].forEach(function (id) {
			var btn = document.getElementById(id);
			if (!btn) return;
			if (!btn.querySelector(".sidebar-util-label")) {
				var label = document.createElement("span");
				label.className = "sidebar-util-label";
				label.textContent = "تنظیمات";
				btn.appendChild(label);
			}
			util.appendChild(btn);
		});
		if (util.firstChild) footer.appendChild(util);
	}

	function mount() {
		var topbar = document.getElementById("topbar");
		if (!topbar || document.getElementById("sidebar-toggle")) return;

		var btn = document.createElement("button");
		btn.id = "sidebar-toggle";
		btn.type = "button";
		btn.className = "icon-btn sidebar-toggle";
		btn.setAttribute("aria-controls", "sidebar");
		btn.innerHTML =
			'<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" ' +
			'stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
			'<rect x="3" y="4.5" width="18" height="15" rx="2.5"/><path d="M15 4.5v15"/></svg>';
		topbar.insertBefore(btn, topbar.firstChild);
		btn.addEventListener("click", toggle);

		document.addEventListener("keydown", function (e) {
			if ((e.ctrlKey || e.metaKey) && (e.key === "b" || e.key === "B")) {
				e.preventDefault();
				toggle();
			}
		});

		mountControls();

		// کلاس اولیه در head ست شده؛ اینجا فقط aria/inert را هم‌گام می‌کنیم.
		apply(isCollapsed(), false);
	}

	if (document.readyState === "loading") {
		document.addEventListener("DOMContentLoaded", mount);
	} else {
		mount();
	}
})();
