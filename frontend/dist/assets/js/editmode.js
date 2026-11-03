/* MyAI — تنظیم «حالت ویرایش» و «سقف توکن هر نوبت» (Stage 2/3 — تب پوشه‌ی کاری)
 *
 * فایل خوانا و جدا (الگوی agentsettings.js)؛ فایل‌های minified دست نمی‌خورند.
 *   - «حالت ویرایش» (۴.۱): دو گزینه با توضیح یک‌خطی:
 *       «بازبینی هر تغییر» (review_each — مودال Diff برای هر نوشتن/ویرایش)
 *       «ویرایش خودکار + بازبینی در پایان نوبت» (auto — پیش‌فرض ۱.۸.۰)
 *   - «سقف توکن هر نوبت» (۵.۳): عددی، ۰ = خاموش، ۵۰ هزار تا ۱۰ میلیون.
 *   - ذخیره با دکمه‌ی «ذخیره»‌ی تب پوشه (#folder-save) — شنونده‌ی stack‌شده.
 *   - حالت Mock: فیلدها نمایش داده می‌شوند ولی ذخیره با پیام رد می‌شود.
 */
(function () {
  "use strict";

  function backend() {
    return (window.go && window.go.main && window.go.main.App) || null;
  }

  function fa(n) {
    return String(n).replace(/\d/g, function (d) {
      return "۰۱۲۳۴۵۶۷۸۹"[d];
    });
  }

  var MODES = [
    { id: "auto", label: "ویرایش خودکار", desc: "تغییرات داخل پوشه بی‌درنگ اعمال می‌شوند؛ بازبینی در پایان نوبت" },
    { id: "review_each", label: "بازبینی هر تغییر", desc: "پیش از هر نوشتن/ویرایش/جابه‌جایی/حذف، مودال Diff نشان داده می‌شود" },
  ];

  // ---------------------------------------------------------- ساخت ردیف‌ها

  function buildEditModeRow() {
    var host = document.getElementById("editmode-extra-row");
    if (!host || host.dataset.built) return;
    host.dataset.built = "1";

    var field = document.createElement("div");
    field.className = "field";

    var label = document.createElement("label");
    label.className = "field-label";
    label.textContent = "حالت ویرایش فایل‌ها";
    field.appendChild(label);

    var seg = document.createElement("div");
    seg.className = "seg-row";
    seg.id = "editmode-seg";
    MODES.forEach(function (m) {
      var b = document.createElement("button");
      b.type = "button";
      b.className = "seg-btn";
      b.dataset.mode = m.id;
      b.textContent = m.label;
      b.title = m.desc;
      b.addEventListener("click", function () {
        selectMode(m.id);
      });
      seg.appendChild(b);
    });
    field.appendChild(seg);

    var hint = document.createElement("div");
    hint.className = "hint-row";
    hint.id = "editmode-hint";
    field.appendChild(hint);

    host.appendChild(field);
  }

  function currentMode() {
    var v = (window.MyAIState && window.MyAIState.view && window.MyAIState.view.editMode) || "";
    return v === "review_each" ? "review_each" : "auto";
  }

  function selectMode(mode) {
    var seg = document.getElementById("editmode-seg");
    if (!seg) return;
    seg.querySelectorAll(".seg-btn").forEach(function (b) {
      b.classList.toggle("active", b.dataset.mode === mode);
    });
    var hint = document.getElementById("editmode-hint");
    if (hint) {
      var m = MODES.filter(function (x) {
        return x.id === mode;
      })[0];
      // مسیرهای محافظت‌شده و حذف سنگین در هر دو حالت تأیید می‌خواهند
      hint.textContent = (m ? m.desc : "") + " — مسیرهای محافظت‌شده (مثل package.json و .env) و حذف پوشه/فایل بزرگ در هر دو حالت تأیید می‌خواهند.";
    }
    pendingMode = mode;
  }

  var pendingMode = null;

  function saveEditMode() {
    if (!pendingMode) return;
    var be = backend();
    if (!be || typeof be.SetEditMode !== "function") {
      if (window.toast) window.toast("در حالت پیش‌نمایش مرورگر، «حالت ویرایش» ذخیره نمی‌شود", "info");
      return;
    }
    var mode = pendingMode;
    be.SetEditMode(mode)
      .then(function () {
        if (window.MyAIState && window.MyAIState.view) {
          window.MyAIState.view.editMode = mode;
          window.MyAIState.view.confirmFileWrites = mode === "review_each";
        }
      })
      .catch(function (err) {
        if (window.toast) window.toast(String(err && err.message ? err.message : err), "err");
      });
  }

  // ---------------------------------------------------------- init

  function populate() {
    selectMode(currentMode());
    var cap = document.getElementById("gen-maxturntokens");
    if (cap) {
      var v = (window.MyAIState && window.MyAIState.view && window.MyAIState.view.maxTurnTokens) || 0;
      cap.value = String(v || 2000000);
    }
  }

  function saveTurnTokens() {
    var cap = document.getElementById("gen-maxturntokens");
    if (!cap) return;
    var be = backend();
    if (!be || typeof be.SetMaxTurnTokens !== "function") {
      if (window.toast) window.toast("در حالت پیش‌نمایش مرورگر، «سقف توکن هر نوبت» ذخیره نمی‌شود", "info");
      return;
    }
    var n = parseInt(cap.value, 10);
    if (isNaN(n) || n < 0) n = 0;
    if (n > 0) {
      if (n < 50000) n = 50000;
      if (n > 10000000) n = 10000000;
    }
    be.SetMaxTurnTokens(n)
      .then(function () {
        if (window.MyAIState && window.MyAIState.view) window.MyAIState.view.maxTurnTokens = n;
        cap.value = String(n);
      })
      .catch(function (err) {
        if (window.toast) window.toast(String(err && err.message ? err.message : err), "err");
      });
  }

  function init() {
    buildEditModeRow();
    populate();

    var tab = document.querySelector('.modal-tab[data-tab="folder"]');
    if (tab) tab.addEventListener("click", populate);
    var mtab = document.querySelector('.modal-tab[data-tab="model"]');
    if (mtab) mtab.addEventListener("click", populate);

    var saveBtn = document.getElementById("folder-save");
    if (saveBtn) saveBtn.addEventListener("click", saveEditMode);
    var genSave = document.getElementById("gen-save");
    if (genSave) genSave.addEventListener("click", saveTurnTokens);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
