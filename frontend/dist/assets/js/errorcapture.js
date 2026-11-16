/* MyAI — ثبت خطاهای مهارنشدهٔ فرانت‌اند در «لاگ خطاها».
 *
 * مشکل: هر خطای JS یا Promise رد‌شده فقط در کنسول WebView2 می‌ماند و
 * برنامه‌نویس (و کاربر) هیچ ردی از آن در errors.log نداشت.
 *
 * این ماژول سه منبع را می‌گیرد:
 *   ۱) window.onerror (خطاهای اجرا + خطای اسکریپت‌های بارگذاری‌نشده)
 *   ۲) unhandledrejection (Promise بدون catch)
 *   ۳) MyAIErrors.report(...) برای نقاطی که خودمان catch می‌کنیم
 *
 * و همه را به ReportFrontendError در بک‌اند می‌فرستد (که با همان مُهر زمانی
 * در errors.log می‌نویسد). محافظت‌ها: حذف تکراری‌ها (۱۰ ثانیه)، سقف ۴۰
 * رخداد در هر نشست، و نادیده‌گرفتن خطاهای بی‌ضرر (ResizeObserver، abort،
 * بستن WebSocket).
 *
 * هیچ متن HTML ای درج نمی‌شود؛ این فایل به DOM دست نمی‌زند.
 */
(function () {
  "use strict";

  var DEDUPE_MS = 10000;
  var MAX_PER_SESSION = 40;
  var seen = Object.create(null);
  var sentCount = 0;

  // خطاهایی که تقریباً همیشه نویزند و ثبتشان لاگ را شلوغ می‌کند.
  var IGNORE = [
    /ResizeObserver loop/i,
    /^Script error\.?$/,
    /AbortError/i,
    /The operation was aborted/i,
    /operation was cancelled/i,
    /WebSocket is (already )?closed/i,
    /The user aborted a request/i
  ];

  function str(v) {
    if (v === null || v === undefined) return "";
    if (typeof v === "string") return v;
    try {
      return String(v);
    } catch (e) {
      return "";
    }
  }

  function stackOf(v) {
    if (!v) return "";
    if (typeof v === "object" && typeof v.stack === "string") return v.stack;
    return "";
  }

  function backend() {
    var be = window.go && window.go.main && window.go.main.App;
    return be && typeof be.ReportFrontendError === "function" ? be : null;
  }

  function report(kind, message, stack) {
    try {
      message = str(message).trim();
      stack = str(stack).trim();
      if (!message && !stack) return false;
      for (var i = 0; i < IGNORE.length; i++) {
        if (IGNORE[i].test(message)) return false;
      }
      var now = Date.now();
      var sig = kind + "|" + message + "|" + stack.slice(0, 120);
      if (seen[sig] && now - seen[sig] < DEDUPE_MS) return false;
      if (sentCount >= MAX_PER_SESSION) return false;
      seen[sig] = now;
      sentCount++;

      var be = backend();
      if (be) {
        // خطای ارسال هرگز نباید خطای اصلی را بپوشاند.
        Promise.resolve(be.ReportFrontendError(str(kind), message, stack)).catch(function () {});
      }
      if (window.console && typeof console.warn === "function") {
        console.warn("[myai " + kind + "] " + message);
      }
      return true;
    } catch (e) {
      return false; // خودِ گیرنده هرگز خطا نمی‌سازد
    }
  }

  // ۱) خطاهای اجرا و منابع (در فاز capture تا منابع از دست نروند).
  window.addEventListener("error", function (ev) {
    try {
      // خطای بارگذاری منبع (img/script/link) رویداد را روی خود عنصر می‌فرستد.
      var t = ev && ev.target;
      if (t && t !== window && t.tagName) {
        var tag = str(t.tagName).toLowerCase();
        var src = t.src || t.href || "";
        report("resource", "failed to load " + tag + (src ? ": " + src : ""));
        return;
      }
      var msg = ev && ev.message ? ev.message : "unknown error";
      var where = "";
      if (ev && ev.filename) {
        where = " @ " + ev.filename + ":" + (ev.lineno || 0) + ":" + (ev.colno || 0);
      }
      report("error", msg + where, stackOf(ev && ev.error));
    } catch (e) { /* بی‌صدا */ }
  }, true);

  // ۲) Promiseهای رد‌شده.
  window.addEventListener("unhandledrejection", function (ev) {
    try {
      var reason = ev && ev.reason;
      var msg = "";
      if (reason && typeof reason === "object") {
        msg = reason.message ? str(reason.message) : str(reason);
        if (!reason.message && reason.name) msg = str(reason.name);
      } else {
        msg = str(reason);
      }
      report("unhandledrejection", msg || "promise rejected without reason", stackOf(reason));
    } catch (e) { /* بی‌صدا */ }
  });

  // ۳) API دستی برای نقاطی که خودمان خطا را می‌گیریم ولی می‌خواهیم ثبت شود.
  window.MyAIErrors = {
    report: report,
    sent: function () { return sentCount; }
  };
})();
