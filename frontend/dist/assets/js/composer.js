/* KodYar — حالت گفتگو و پوشه‌ی کاری، داخل چت‌باکس.
 *
 * - تعیین حالت گفتگو («ایجنت» یا «چت») از سایدبار/نوار بالا به چت‌باکس منتقل
 *   شده است؛ همین دکمه وضعیت KodYarState.view.agentMode را عوض می‌کند و از طریق
 *   SetAgentMode ذخیره می‌شود (اگر ذخیره شکست بخورد، وضعیت برمی‌گردد).
 * - مسیر پروژه دیگر در نوار بالای صفحه نیست: دکمه‌ی پوشه در چت‌باکس مسیر فعلی
 *   را در tooltip نشان می‌دهد و تب «پوشه‌ی کاری» تنظیمات را باز می‌کند.
 * - app.js در پایان هر رندر، sync() را صدا می‌زند تا برچسب/وضعیت هم‌گام بماند.
 */
(function () {
  "use strict";

  function el(id) {
    return document.getElementById(id);
  }
  function backend() {
    return (window.go && window.go.main && window.go.main.App) || null;
  }
  function view() {
    return (window.KodYarState && window.KodYarState.view) || null;
  }
  function toast(text, kind) {
    if (typeof window.toast === "function") window.toast(text, kind);
  }
  function icon(name, size) {
    if (!window.Icons) return "";
    try {
      return Icons.get(name, size);
    } catch (e) {
      return "";
    }
  }

  // آخرین مقدارهای رندرشده — تا sync() (که آخر هر رندر صدا زده می‌شود)
  // آیکون SVG و attributeهای بدون تغییر را دوباره نسازد/ننویسد.
  var lastModeIcon = "";
  var lastWsTitle = "";

  function sync() {
    var v = view();
    var on = !!(v && v.agentMode);
    var btn = el("composer-mode");
    var label = el("composer-mode-label");
    var ic = el("composer-mode-ic");
    if (btn) {
      btn.classList.toggle("on", on);
      btn.setAttribute("aria-pressed", on ? "true" : "false");
      btn.title = on
        ? "حالت ایجنت روشن است — خواندن، نوشتن و ویرایش فایل‌های پروژه + اجرای دستور. کلیک: چت معمولی"
        : "حالت چت معمولی — فقط گفتگو. کلیک: حالت ایجنت";
    }
    if (label) label.textContent = on ? "ایجنت" : "چت";
    if (ic) {
      var want = on ? "bolt" : "chat";
      if (want !== lastModeIcon) {
        ic.innerHTML = icon(want, 14);
        lastModeIcon = want;
      }
    }

    var ws = el("composer-workspace");
    if (ws) {
      var path = String((v && v.workspace) || "").trim();
      var has = !!path;
      ws.classList.toggle("unset", !has);
      var wsTitle = has
        ? "پوشه‌ی کاری: " + path + " — کلیک: تغییر مسیر پروژه"
        : "پوشه‌ی کاری تعیین نشده — کلیک: انتخاب پروژه";
      if (wsTitle !== lastWsTitle) {
        lastWsTitle = wsTitle;
        ws.title = wsTitle;
        ws.setAttribute("aria-label", wsTitle);
      }
    }
  }

  function toggleMode() {
    var v = view();
    var be = backend();
    if (!v || !be) return;
    var next = !v.agentMode;
    v.agentMode = next;
    sync();
    if (typeof window.renderAll === "function") {
      try {
        window.renderAll();
      } catch (e) {
        /* رندر مجدد نباید تعویض حالت را بشکند */
      }
    }
    be.SetAgentMode(next)
      .then(function () {
        toast(
          next
            ? "حالت ایجنت فعال شد — KodYar می‌تواند فایل‌های پروژه را بخواند و ویرایش کند"
            : "حالت چت معمولی فعال شد",
          next ? "ok" : "info"
        );
        sync();
      })
      .catch(function (err) {
        v.agentMode = !next;
        sync();
        toast(String(err && err.message ? err.message : err), "err");
      });
  }

  function openWorkspace() {
    if (window.KodYarModals && window.KodYarModals.openSettings) {
      window.KodYarModals.openSettings("folder");
    }
  }

  function init() {
    var modeBtn = el("composer-mode");
    if (modeBtn && !modeBtn.dataset.bound) {
      modeBtn.dataset.bound = "1";
      modeBtn.addEventListener("click", toggleMode);
    }
    var wsBtn = el("composer-workspace");
    if (wsBtn && !wsBtn.dataset.bound) {
      wsBtn.dataset.bound = "1";
      wsBtn.addEventListener("click", openWorkspace);
    }
    var wsIc = el("composer-workspace-ic");
    if (wsIc) wsIc.innerHTML = icon("folder", 16);
    var dnIc = el("prov-name-ic");
    if (dnIc) dnIc.innerHTML = icon("pencil", 15);
    sync();
  }

  window.KodYarComposer = { sync: sync, toggle: toggleMode };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
