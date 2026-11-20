/* KodYar — تب تنظیمات «دسترسی‌ها» (مدیریت سیستم Permissions)
 *
 * وظایف:
 *   - نمایش override هر ابزار (پیش‌فرض / اجازه / بپرس / رد) و ذخیره‌ی فوری
 *   - فهرست قاعده‌های «همیشه اجازه بده» با دکمه‌ی حذف (ابطال اجازه)
 *   - نمایش فایل‌های حساس (فقط خواندنی) و دکمه‌ی بازنشانی کامل
 *
 * نکات ایمنی:
 *   - همه‌ی متن‌های پویا با textContent درج می‌شوند (هرگز innerHTML خام).
 *   - بازنشانی دو مرحله‌ای است (کلیک دوم تا ۴ ثانیه اعتبار دارد).
 *   - در حالت Mock (بدون بک‌اند) فقط پیام وضعیت نشان داده می‌شود.
 *   - ویرایش فایل‌های minified ممنوع است؛ این فایل خوانا و جدا است.
 */
(function () {
  "use strict";

  function backend() {
    return (window.go && window.go.main && window.go.main.App) || null;
  }

  // ---------- وضعیت کوچک محلی (بدون نشت به سایر ماژول‌ها)
  var state = {
    loaded: false,
    resetArmed: 0, // timestamp لحظه‌ی کلیک اولِ بازنشانی
    resetTimer: null
  };

  function status(text, bad) {
    var el = document.getElementById("perm-tools-note");
    if (!el) return;
    el.textContent = text || "";
    el.style.color = bad ? "var(--danger)" : "var(--text3)";
  }

  // برچسب فارسی تصمیم‌ها
  var decisionLabel = { default: "پیش‌فرض", allow: "اجازه", ask: "بپرس", deny: "رد" };

  // ---------- رندر ابزارها
  function renderTools(tools) {
    var wrap = document.getElementById("perm-tools");
    if (!wrap) return;
    wrap.textContent = "";
    if (!tools || !tools.length) {
      status("ابزاری برای نمایش نیست.");
      return;
    }
    tools.forEach(function (t) {
      var row = document.createElement("div");
      row.style.cssText =
        "display:flex;align-items:center;gap:10px;padding:5px 2px;";

      var name = document.createElement("span");
      name.style.cssText =
        "flex:1;min-width:0;font-family:var(--font-code);font-size:11.5px;" +
        "direction:ltr;unicode-bidi:isolate;text-align:left;word-break:break-all;";
      name.textContent = t.name;
      name.title = t.name;
      row.appendChild(name);

      var def = document.createElement("span");
      def.className = "setting-desc";
      def.style.cssText = "font-size:10.5px;white-space:nowrap;flex-shrink:0;";
      def.textContent = "پیش‌فرض: " + (decisionLabel[t.default] || t.default);
      row.appendChild(def);

      var seg = document.createElement("div");
      seg.className = "seg-row";
      seg.style.cssText = "flex-shrink:0;";
      var current = t.override && decisionLabel[t.override] ? t.override : "default";
      // C.1: web_fetch اجازه‌ی سراسری ندارد — گزینه‌ی «اجازه» حذف می‌شود
      // تا کاربر دامنه‌ها را هنگام درخواست تأیید کند.
      var opts = t.name === "web_fetch" ? ["default", "ask", "deny"] : ["default", "allow", "ask", "deny"];
      if (t.name === "web_fetch" && current === "allow") current = "ask";
      opts.forEach(function (d) {
        var b = document.createElement("button");
        b.type = "button";
        b.className = "seg-btn" + (d === current ? " active" : "");
        b.style.cssText = "padding:5px 8px;";
        b.textContent = decisionLabel[d];
        b.addEventListener("click", function () {
          var be = backend();
          if (!be || typeof be.SetToolPermission !== "function") return;
          b.disabled = true;
          be.SetToolPermission(t.name, d)
            .then(function () {
              state.loaded = false;
              load(true);
            })
            .catch(function (err) {
              b.disabled = false;
              status("ذخیره نشد: " + (err && err.message ? err.message : err), true);
            });
        });
        seg.appendChild(b);
      });
      row.appendChild(seg);
      wrap.appendChild(row);
    });
  }

  // ---------- رندر قواعد دستورات
  function renderRules(rules) {
    var wrap = document.getElementById("perm-rules");
    var empty = document.getElementById("perm-rules-empty");
    if (!wrap) return;
    wrap.textContent = "";
    if (empty) empty.hidden = !!(rules && rules.length);
    if (!rules || !rules.length) return;
    rules.forEach(function (r) {
      var row = document.createElement("div");
      row.style.cssText =
        "display:flex;align-items:center;gap:10px;padding:5px 2px;";

      var pat = document.createElement("span");
      pat.style.cssText =
        "flex:1;min-width:0;font-family:var(--font-code);font-size:11.5px;" +
        "direction:ltr;unicode-bidi:isolate;text-align:left;word-break:break-all;";
      pat.textContent = r.pattern;
      row.appendChild(pat);

      var dec = document.createElement("span");
      dec.className = "setting-desc";
      dec.style.cssText = "font-size:11px;white-space:nowrap;flex-shrink:0;";
      dec.textContent = decisionLabel[r.decision] || r.decision;
      row.appendChild(dec);

      var del = document.createElement("button");
      del.type = "button";
      del.className = "icon-btn";
      del.title = "حذف قاعده";
      del.style.cssText = "width:26px;height:26px;flex-shrink:0;";
      del.innerHTML = (window.Icons && Icons.get("trash", 14)) || "×";
      del.addEventListener("click", function () {
        var be = backend();
        if (!be || typeof be.RemoveCommandRule !== "function") return;
        del.disabled = true;
        be.RemoveCommandRule(r.pattern)
          .then(function () {
            state.loaded = false;
            load(true);
          })
          .catch(function (err) {
            del.disabled = false;
            status("حذف نشد: " + (err && err.message ? err.message : err), true);
          });
      });
      row.appendChild(del);
      wrap.appendChild(row);
    });
  }

  // ---------- رندر دامنه‌های مجاز web_fetch (C.1)
  function renderWebHosts(hosts) {
    var wrap = document.getElementById("perm-webhosts");
    var empty = document.getElementById("perm-webhosts-empty");
    if (!wrap) return;
    wrap.textContent = "";
    if (empty) empty.hidden = !!(hosts && hosts.length);
    if (!hosts || !hosts.length) return;
    hosts.forEach(function (h) {
      var row = document.createElement("div");
      row.style.cssText = "display:flex;align-items:center;gap:10px;padding:5px 2px;";
      var name = document.createElement("span");
      name.style.cssText =
        "flex:1;min-width:0;font-family:var(--font-code);font-size:11.5px;" +
        "direction:ltr;unicode-bidi:isolate;text-align:left;word-break:break-all;";
      name.textContent = h.pattern;
      row.appendChild(name);
      var del = document.createElement("button");
      del.type = "button";
      del.className = "icon-btn";
      del.title = "حذف اجازه‌ی این دامنه";
      del.style.cssText = "width:26px;height:26px;flex-shrink:0;";
      del.innerHTML = (window.Icons && Icons.get("trash", 14)) || "×";
      del.addEventListener("click", function () {
        var be = backend();
        if (!be || typeof be.RemoveWebHostRule !== "function") return;
        del.disabled = true;
        be.RemoveWebHostRule(h.pattern)
          .then(function () {
            state.loaded = false;
            load(true);
          })
          .catch(function (err) {
            del.disabled = false;
            status("حذف نشد: " + (err && err.message ? err.message : err), true);
          });
      });
      row.appendChild(del);
      wrap.appendChild(row);
    });
  }

  // ---------- رندر فایل‌های حساس
  function renderGlobs(globs) {
    var el = document.getElementById("perm-globs");
    if (!el) return;
    el.textContent = (globs && globs.length) ? globs.join("   ·   ") : "—";
  }

  // ---------- بارگذاری
  function load(force) {
    var be = backend();
    if (!be || typeof be.PermissionsView !== "function") {
      status("بک‌اند در دسترس نیست (حالت Mock).");
      return;
    }
    if (state.loaded && !force) return;
    status("در حال بارگذاری…");
    be.PermissionsView()
      .then(function (v) {
        if (!v) {
          status("پاسخی دریافت نشد.", true);
          return;
        }
        renderTools(v.tools);
        renderRules(v.commandRules);
        renderWebHosts(v.webHosts);
        renderGlobs(v.sensitiveGlobs);
        state.loaded = true;
        status("");
      })
      .catch(function (err) {
        status("خطا در خواندن دسترسی‌ها: " + (err && err.message ? err.message : err), true);
      });
  }

  // ---------- بازنشانی دو مرحله‌ای
  function armReset() {
    var label = document.getElementById("perm-reset-label");
    if (!label) return;
    state.resetArmed = Date.now();
    label.textContent = "مطمئنید؟ دوباره بزنید";
    if (state.resetTimer) clearTimeout(state.resetTimer);
    state.resetTimer = setTimeout(function () {
      state.resetArmed = 0;
      if (label) label.textContent = "بازنشانی همه\u200cی دسترسی\u200cها";
    }, 4000);
  }

  function onReset() {
    var be = backend();
    if (!be || typeof be.ResetPermissions !== "function") return;
    if (!state.resetArmed || Date.now() - state.resetArmed > 4000) {
      armReset();
      return;
    }
    if (state.resetTimer) clearTimeout(state.resetTimer);
    state.resetArmed = 0;
    var label = document.getElementById("perm-reset-label");
    if (label) label.textContent = "بازنشانی همه\u200cی دسترسی\u200cها";
    be.ResetPermissions()
      .then(function () {
        state.loaded = false;
        load(true);
        status("همه\u200cی دسترسی\u200cها به پیش\u200cفرض بازگشت.");
      })
      .catch(function (err) {
        status("بازنشانی نشد: " + (err && err.message ? err.message : err), true);
      });
  }

  // ---------- راه‌اندازی
  function init() {
    var tab = document.querySelector('.modal-tab[data-tab="perms"]');
    if (tab) {
      var ic = tab.querySelector(".tab-ic");
      if (ic && window.Icons) ic.innerHTML = Icons.get("shield", 14);
      // همیشه تازه بخوان — کش قدیمی پس از «همیشه اجازه بده»ی مودال اجرا
      // (ساخت قاعده در بک‌اند) کهنه می‌شد و تب، وضعیت واقعی را نشان نمی‌داد.
      tab.addEventListener("click", function () { load(true); });
    }
    var ric = document.getElementById("perm-reset-ic");
    if (ric && window.Icons) ric.innerHTML = Icons.get("undo", 15);
    var rbtn = document.getElementById("perm-reset");
    if (rbtn) rbtn.addEventListener("click", onReset);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
