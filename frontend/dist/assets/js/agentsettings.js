/* MyAI — تنظیم «حداکثر مراحل ایجنت» و «حداکثر مراحل زیرایجنت» در تب «مدل و رفتار»
 *
 * این فایل خوانا و جدا است؛ فایل‌های minified دست‌کاری نمی‌شوند.
 * اتصال با حداقل قلاب:
 *   - کلیک روی تب «مدل و رفتار» → مقدار فعلی از MyAIState.view خوانده و در
 *     فیلدهای #gen-agentrounds و #gen-subagentrounds نشان داده می‌شود
 *     (شنونده‌ی ما روی همان دکمه‌ی تب، بعد از شنونده‌ی minified اجرا می‌شود —
 *     stack کردن addEventListener).
 *   - کلیک روی «ذخیره» (#gen-save) → علاوه بر ذخیره‌ی minified تنظیمات مدل،
 *     مقدار این فیلدها هم با SetMaxAgentRounds / SetMaxSubagentRounds
 *     ذخیره می‌شود.
 *
 * نکات:
 *   - حالت Mock (بدون بک‌اند واقعی): متدهای ذخیره وجود ندارند؛ فیلدها نشان
 *     داده می‌شوند ولی ذخیره‌شان با یک پیام وضعیت رد می‌شود.
 *   - محدوده‌ها سمت کلاینت هم اعمال می‌شوند (بک‌اند هم clamp می‌کند):
 *     ایجنت ۵ تا ۲۰۰، زیرایجنت ۳ تا ۵۰.
 *   - اعداد داخل input با direction:ltr نمایش می‌شوند (مثل بقیه‌ی فیلدهای
 *     عددی)؛ برچسب و راهنما فارسی و RTL هستند.
 *   - گام ۶: فیلد زیرایجنت به همین فایل اضافه شد (الگوی یکسان، بدون فایل جدید).
 */
(function () {
  "use strict";

  var AGENT = { id: "gen-agentrounds", view: "maxAgentRounds", set: "SetMaxAgentRounds", def: 25, min: 5, max: 200, ic: "gen-rounds-ic", hint: "gen-rounds-hint-ic", label: "حداکثر مراحل ایجنت" };
  var SUB = { id: "gen-subagentrounds", view: "maxSubagentRounds", set: "SetMaxSubagentRounds", def: 8, min: 3, max: 50, ic: "gen-subrounds-ic", hint: "gen-subrounds-hint-ic", label: "حداکثر مراحل زیرایجنت" };

  function backend() {
    return (window.go && window.go.main && window.go.main.App) || null;
  }

  function input(cfg) {
    return document.getElementById(cfg.id);
  }

  // خواندن مقدار فعلی از view (SettingsView بک‌اند؛ در Mock ممکن است نباشد)
  function currentValue(cfg) {
    var v = (window.MyAIState && window.MyAIState.view && window.MyAIState.view[cfg.view]) || 0;
    var n = parseInt(v, 10);
    if (isNaN(n) || n <= 0) return cfg.def;
    if (n < cfg.min) return cfg.min;
    if (n > cfg.max) return cfg.max;
    return n;
  }

  function populate(cfg) {
    var el = input(cfg);
    if (el) el.value = String(currentValue(cfg));
  }

  function clampClient(cfg, n) {
    if (isNaN(n) || n <= 0) return cfg.def;
    if (n < cfg.min) return cfg.min;
    if (n > cfg.max) return cfg.max;
    return n;
  }

  // ذخیره‌ی همراه دکمه‌ی «ذخیره»‌ی تب مدل — خطا در اینجا toast می‌شود و
  // ذخیره‌ی اصلی تنظیمات مدل (minified) تحت تأثیر قرار نمی‌گیرد.
  function save(cfg) {
    var el = input(cfg);
    if (!el) return;
    var be = backend();
    if (!be || typeof be[cfg.set] !== "function") {
      if (window.toast) {
        window.toast("در حالت پیش‌نمایش مرورگر، «" + cfg.label + "» ذخیره نمی‌شود", "info");
      }
      return;
    }
    var n = clampClient(cfg, parseInt(el.value, 10));
    be[cfg.set](n)
      .then(function () {
        if (window.MyAIState && window.MyAIState.view) {
          window.MyAIState.view[cfg.view] = n;
        }
        el.value = String(n);
      })
      .catch(function (err) {
        if (window.toast) {
          window.toast("ذخیره‌ی " + cfg.label + " ناموفق بود: " + (err && err.message ? err.message : err), "err");
        }
      });
  }

  function init() {
    // آیکون فیلدها — slots.js (minified) از IDهای شناخته‌شده آیکون می‌گذارد؛
    // فیلدهای جدید مال ما هستند، پس آیکونشان را همین‌جا می‌گذاریم (الگوی
    // permsettings.js). زیرایجنت آیکون «map» دارد (همان آیکون کارت ابزار task).
    [ [AGENT, "list"], [SUB, "map"] ].forEach(function (pair) {
      var ic = document.getElementById(pair[0].ic);
      if (ic && window.Icons) ic.innerHTML = Icons.get(pair[1], 15);
      var hic = document.getElementById(pair[0].hint);
      if (hic && window.Icons) hic.innerHTML = Icons.get("info", 14);
    });

    // populate در دو حالت: باز کردن تب مدل (شنونده‌ی stack‌شده) و بارگذاری اولیه
    var tab = document.querySelector('.modal-tab[data-tab="model"]');
    if (tab)
      tab.addEventListener("click", function () {
        populate(AGENT);
        populate(SUB);
      });
    populate(AGENT);
    populate(SUB);

    var saveBtn = document.getElementById("gen-save");
    if (saveBtn)
      saveBtn.addEventListener("click", function () {
        save(AGENT);
        save(SUB);
      });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
