/*
 * diagnostics.js — دکمهٔ «صدور بستهٔ عیب‌یابی» در تب «لاگ خطاها».
 *
 * فایل خوانا و مستقل است (بدنه‌های مینیفای‌شده دست نمی‌خورند). بک‌اند یک zip
 * امن می‌سازد (بدون کلید/توکن/محتوای گفتگو) و مسیرش را برمی‌گرداند؛ این فایل
 * فقط دکمه را به binding وصل و نتیجه را به کاربر نشان می‌دهد.
 */
(function () {
  "use strict";

  function backend() {
    return window.go && window.go.main && window.go.main.App;
  }

  function toast(msg, kind) {
    if (typeof window.toast === "function") window.toast(msg, kind || "info");
  }

  function fillIcons() {
    var slot = document.getElementById("log-diag-ic");
    if (slot && window.Icons && typeof window.Icons.get === "function") {
      slot.innerHTML = window.Icons.get("save", 15);
    }
  }

  function exportBundle(btn) {
    var be = backend();
    if (!be || typeof be.ExportDiagnosticBundle !== "function") {
      toast("در پیش‌نمایش مرورگر بستهٔ عیب‌یابی ساخته نمی‌شود", "info");
      return;
    }
    if (btn && btn.disabled) return;
    if (btn) btn.disabled = true;
    var label = btn ? btn.querySelector("span:last-child") : null;
    var old = label ? label.textContent : "";
    if (label) label.textContent = "در حال ساخت…";
    Promise.resolve(be.ExportDiagnosticBundle())
      .then(function (path) {
        if (!path) throw new Error("مسیر بسته برنگشت");
        toast("بستهٔ عیب‌یابی ساخته شد: " + path, "ok");
        if (navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
          navigator.clipboard.writeText(String(path)).catch(function () {});
        }
      })
      .catch(function (err) {
        var msg = err && err.message ? err.message : String(err || "خطای نامشخص");
        toast("ساخت بستهٔ عیب‌یابی ناموفق بود: " + msg, "err");
      })
      .then(function () {
        if (label) label.textContent = old || "صدور بستهٔ عیب‌یابی";
        if (btn) btn.disabled = false;
      });
  }

  function bind() {
    fillIcons();
    var btn = document.getElementById("log-diag");
    if (!btn || btn.dataset.bound === "1") return;
    btn.dataset.bound = "1";
    btn.addEventListener("click", function () {
      exportBundle(btn);
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", bind);
  } else {
    bind();
  }

  window.KodYarDiagnostics = { exportBundle: exportBundle, bind: bind };
})();
