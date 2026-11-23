/* KodYar — تنظیم «حداکثر مراحل ایجنت» و «حداکثر مراحل زیرایجنت» در تب «مدل و رفتار»
 *
 * این فایل خوانا و جدا است؛ فایل‌های minified دست‌کاری نمی‌شوند.
 * اتصال با حداقل قلاب:
 *   - کلیک روی تب «مدل و رفتار» → مقدار فعلی از KodYarState.view خوانده و در
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
  // مهلت سکون استریم — پیش‌تر «تنظیم پنهان» بود و بیشترین قطع‌های وسط کار
  // (سرویس‌دهنده‌ی واسط/مدل reasoning با سکوت >۲ دقیقه) از همین می‌آمد.
  var IDLE = { id: "gen-streamidle", view: "streamIdleTimeoutSec", set: "SetStreamIdleTimeout", def: 180, min: 30, max: 900, label: "مهلت سکون پاسخ (ثانیه)" };

  // سیاست ارسال تصویر (اسکرین‌شات) به مدل — سه حالت، پیش‌فرض auto.
  // auto = فقط مدل‌های تصویری شناخته‌شده؛ on = اجبار؛ off = هرگز.
  var VISION = { id: "gen-vision", view: "visionImages", set: "SetVisionImages", def: "auto", label: "تصاویر اسکرین‌شات برای مدل" };
  // «میزان فکر کردن» — پارامتر reasoning_effort؛ پیش‌فرض کم تا استدلال طولانی
  // توکن نسوزاند. Provider ناشناس یک‌بار ۴۰۰ می‌دهد و بعد از آن حذف می‌شود.
  var EFFORT = { id: "gen-effort", view: "thinkingEffort", set: "SetThinkingEffort", def: "low", label: "میزان فکر کردن مدل" };

  function backend() {
    return (window.go && window.go.main && window.go.main.App) || null;
  }

  function input(cfg) {
    return document.getElementById(cfg.id);
  }

  // خواندن مقدار فعلی از view (SettingsView بک‌اند؛ در Mock ممکن است نباشد)
  function currentValue(cfg) {
    var v = (window.KodYarState && window.KodYarState.view && window.KodYarState.view[cfg.view]) || 0;
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
        if (window.KodYarState && window.KodYarState.view) {
          window.KodYarState.view[cfg.view] = n;
        }
        el.value = String(n);
      })
      .catch(function (err) {
        if (window.toast) {
          window.toast("ذخیره‌ی " + cfg.label + " ناموفق بود: " + (err && err.message ? err.message : err), "err");
        }
      });
  }

  // فیلد مهلت سکون در index.html نیست (فایل مینیفای است)؛ همین‌جا با همان
  // ساختار .field ساخته می‌شود — همان الگوی editmode.js.
  function buildIdleField() {
    if (document.getElementById(IDLE.id)) return;
    var host = document.querySelector("#model-panel .form-card");
    if (!host) return;
    var field = document.createElement("div");
    field.className = "field";
    var label = document.createElement("label");
    label.className = "field-label";
    label.textContent = IDLE.label;
    var opt = document.createElement("span");
    opt.className = "optional";
    opt.textContent = " (۳۰ تا ۹۰۰ — پیش‌فرض ۱۸۰)";
    label.appendChild(opt);
    var wrap = document.createElement("div");
    wrap.className = "input-wrap";
    var ic = document.createElement("span");
    ic.id = "gen-streamidle-ic";
    var el = document.createElement("input");
    el.id = IDLE.id;
    el.type = "number";
    el.min = String(IDLE.min);
    el.max = String(IDLE.max);
    el.step = "30";
    el.value = String(IDLE.def);
    el.style.direction = "ltr";
    el.style.textAlign = "left";
    wrap.appendChild(ic);
    wrap.appendChild(el);
    var hint = document.createElement("div");
    hint.className = "hint-row";
    var hic = document.createElement("span");
    hic.id = "gen-streamidle-hint-ic";
    var ht = document.createElement("span");
    ht.textContent = "اگر سرویس‌دهنده در این مدت هیچ بایتی نفرستد، درخواست لغو و تا چهار بار دوباره تلاش می‌شود. واسط‌های واسط (aggregator) و مدل‌های reasoning گاهی بیش از دو دقیقه سکوت می‌کنند.";
    hint.appendChild(hic);
    hint.appendChild(ht);
    field.appendChild(label);
    field.appendChild(wrap);
    field.appendChild(hint);
    host.appendChild(field);
    if (window.Icons) ic.innerHTML = Icons.get("timer", 15);
    if (window.Icons && hic) hic.innerHTML = Icons.get("info", 14);
  }

  // سوییچ کش پرامپت (۱.۹) — پیش‌فرض روشن است. ردیف در تب «مدل و رفتار»
  // با همان ساختار .switch-row/.switch ساخته می‌شود (index.html مینیفای است).
  function buildCacheRow() {
    if (document.getElementById("gen-promptcache-row")) return;
    var host = document.querySelector("#model-panel .form-card");
    if (!host) return;
    var row = document.createElement("div");
    row.className = "switch-row";
    row.id = "gen-promptcache-row";
    row.title = "علامت‌گذاری بخش ثابت پرامپت برای کش سرویس‌دهنده";
    var title = document.createElement("span");
    title.className = "switch-title";
    title.textContent = "کَش پرامپت (سریع‌تر و کم‌هزینه‌تر)";
    var sw = document.createElement("div");
    sw.className = "switch";
    sw.id = "gen-promptcache-switch";
    row.appendChild(title);
    row.appendChild(sw);

    var hint = document.createElement("div");
    hint.className = "hint-row";
    hint.id = "gen-promptcache-hint";
    hint.textContent = "بخش ثابت پرامپت (system + تعریف ابزارها) برای سرویس‌دهنده نشانه می‌شود تا در دورهای بعدی از کش خوانده شود؛ زمان تا اولین پاسخ و هزینهٔ ورودی کم می‌شود. سرویس‌دهنده‌ای که پشتیبانی نکند یک‌بار خطا می‌دهد و خودکار خاموش می‌شود.";

    var anchor = document.getElementById(IDLE.hint);
    var field = anchor && anchor.parentElement; // فیلد مهلت سکون
    if (field && field.parentElement === host) {
      host.insertBefore(row, field.nextSibling);
      host.insertBefore(hint, row.nextSibling);
    } else {
      host.appendChild(row);
      host.appendChild(hint);
    }

    row.addEventListener("click", function () {
      var view = (window.KodYarState && window.KodYarState.view) || {};
      var next = view.promptCache === false;
      var be = backend();
      if (!be || typeof be.SetPromptCache !== "function") {
        if (window.toast) window.toast("در حالت پیش‌نمایش مرورگر، کش پرامپت ذخیره نمی‌شود", "info");
        return;
      }
      be.SetPromptCache(next)
        .then(function () {
          view.promptCache = next;
          syncCacheSwitch(next);
          if (window.toast) {
            window.toast(
              next ? "کش پرامپت روشن شد — دورهای بعدی از کش سرویس‌دهنده استفاده می‌کنند" : "کش پرامپت خاموش شد",
              next ? "ok" : "info"
            );
          }
        })
        .catch(function (err) {
          if (window.toast) {
            window.toast("ذخیرهٔ تنظیم کش پرامپت ناموفق بود: " + (err && err.message ? err.message : err), "err");
          }
        });
    });
  }

  function syncCacheSwitch(on) {
    var sw = document.getElementById("gen-promptcache-switch");
    if (sw) sw.classList.toggle("on", on !== false);
  }

  // سوییچ «راستی‌آزمایی خودکار» (گام ۵) — پیش‌فرض روشن؛ بعد از هر تغییر فایل
  // یک build/typecheck ثابت اجرا می‌شود و فقط خطاها به مدل داده می‌شوند.
  function buildAutoVerifyRow() {
    if (document.getElementById("gen-autoverify-row")) return;
    var host = document.querySelector("#model-panel .form-card");
    if (!host) return;
    var row = document.createElement("div");
    row.className = "switch-row";
    row.id = "gen-autoverify-row";
    row.title = "اجرای خودکار build/typecheck پس از تغییر فایل";
    var title = document.createElement("span");
    title.className = "switch-title";
    title.textContent = "راستی‌آزمایی خودکار پس از تغییر فایل";
    var sw = document.createElement("div");
    sw.className = "switch";
    sw.id = "gen-autoverify-switch";
    row.appendChild(title);
    row.appendChild(sw);

    var hint = document.createElement("div");
    hint.className = "hint-row";
    hint.id = "gen-autoverify-hint";
    hint.textContent =
      "بعد از هر دور که فایلی تغییر کند، یک دستور ساخت/تایپ‌چک ثابت (go build / go vet، tsc --noEmit یا py_compile) اجرا می‌شود؛ فقط اگر خطا باشد نتیجه به مدل داده می‌شود تا همان‌جا رفع کند. دستورها از پیش تعیین‌شده‌اند و ورودی مدل اجرا نمی‌شود.";

    var cacheHint = document.getElementById("gen-promptcache-hint");
    if (cacheHint && cacheHint.parentElement === host) {
      host.insertBefore(row, cacheHint.nextSibling);
      host.insertBefore(hint, row.nextSibling);
    } else {
      host.appendChild(row);
      host.appendChild(hint);
    }

    row.addEventListener("click", function () {
      var view = (window.KodYarState && window.KodYarState.view) || {};
      var next = view.autoVerify === false;
      var be = backend();
      if (!be || typeof be.SetAutoVerify !== "function") {
        if (window.toast) window.toast("در حالت پیش‌نمایش مرورگر، راستی‌آزمایی خودکار ذخیره نمی‌شود", "info");
        return;
      }
      be.SetAutoVerify(next)
        .then(function () {
          view.autoVerify = next;
          syncAutoVerifySwitch(next);
          if (window.toast) {
            window.toast(
              next ? "راستی‌آزمایی خودکار روشن شد — بعد از هر تغییر فایل build/typecheck اجرا می‌شود" : "راستی‌آزمایی خودکار خاموش شد",
              next ? "ok" : "info"
            );
          }
        })
        .catch(function (err) {
          if (window.toast) {
            window.toast("ذخیرهٔ تنظیم راستی‌آزمایی ناموفق بود: " + (err && err.message ? err.message : err), "err");
          }
        });
    });
  }

  function syncAutoVerifySwitch(on) {
    var sw = document.getElementById("gen-autoverify-switch");
    if (sw) sw.classList.toggle("on", on !== false);
  }

  // فیلد انتخاب سیاست تصویر — با ساختار .field/.input-wrap و <select> خودِ
  // استایل سراسری، بدون دست‌زدن به index.html مینیفای.
  function buildVisionField() {
    if (document.getElementById(VISION.id)) return;
    var host = document.querySelector("#model-panel .form-card");
    if (!host) return;
    var field = document.createElement("div");
    field.className = "field";
    var label = document.createElement("label");
    label.className = "field-label";
    label.textContent = VISION.label;
    var opt = document.createElement("span");
    opt.className = "optional";
    opt.textContent = " (browser_screenshot)";
    label.appendChild(opt);

    var wrap = document.createElement("div");
    wrap.className = "input-wrap";
    var ic = document.createElement("span");
    ic.id = "gen-vision-ic";
    var sel = document.createElement("select");
    sel.id = VISION.id;
    [
      ["auto", "خودکار — فقط مدل‌های تصویری شناخته‌شده"],
      ["on", "همیشه بفرست — مدل تصویر را می‌بیند"],
      ["off", "هرگز — فقط کاربر تصویر را می‌بیند"],
    ].forEach(function (pair) {
      var o = document.createElement("option");
      o.value = pair[0];
      o.textContent = pair[1];
      sel.appendChild(o);
    });
    sel.value = VISION.def;
    wrap.appendChild(ic);
    wrap.appendChild(sel);

    var hint = document.createElement("div");
    hint.className = "hint-row";
    var hic = document.createElement("span");
    hic.id = "gen-vision-hint-ic";
    var ht = document.createElement("span");
    ht.textContent =
      "اگر مدل ورودی تصویر داشته باشد، PNG اسکرین‌شات مستقیماً به مدل می‌رود تا صفحه را ببیند؛ وگرنه ابزار صریح می‌گوید فقط کاربر تصویر را می‌بیند. «خودکار» برای مدل‌های ناشناخته تصویر نمی‌فرستد تا نوبت با خطای سرویس‌دهنده نشکند.";
    hint.appendChild(hic);
    hint.appendChild(ht);
    field.appendChild(label);
    field.appendChild(wrap);
    field.appendChild(hint);

    var anchor = document.getElementById("gen-autoverify-hint");
    if (anchor && anchor.parentElement === host) {
      host.insertBefore(field, anchor.nextSibling);
    } else {
      host.appendChild(field);
    }
    if (window.Icons) {
      if (ic) ic.innerHTML = Icons.get("image", 15);
      if (hic) hic.innerHTML = Icons.get("info", 14);
    }
  }

  function visionValue() {
    var v = String(((window.KodYarState && window.KodYarState.view) || {}).visionImages || "").toLowerCase();
    return v === "on" || v === "off" ? v : VISION.def;
  }

  function populateVision() {
    var el = document.getElementById(VISION.id);
    if (el) el.value = visionValue();
  }

  function saveVision() {
    var el = document.getElementById(VISION.id);
    if (!el) return;
    var mode = el.value === "on" || el.value === "off" ? el.value : "auto";
    var be = backend();
    if (!be || typeof be[VISION.set] !== "function") {
      if (window.toast) window.toast("در حالت پیش‌نمایش مرورگر، «" + VISION.label + "» ذخیره نمی‌شود", "info");
      return;
    }
    be[VISION.set](mode)
      .then(function () {
        if (window.KodYarState && window.KodYarState.view) window.KodYarState.view[VISION.view] = mode;
        el.value = mode;
      })
      .catch(function (err) {
        if (window.toast) {
          window.toast("ذخیره‌ی " + VISION.label + " ناموفق بود: " + (err && err.message ? err.message : err), "err");
        }
      });
  }

  function buildEffortField() {
    if (document.getElementById(EFFORT.id)) return;
    var host = document.querySelector("#model-panel .form-card");
    if (!host) return;
    var field = document.createElement("div");
    field.className = "field";
    var label = document.createElement("label");
    label.className = "field-label";
    label.textContent = EFFORT.label;
    var opt = document.createElement("span");
    opt.className = "optional";
    opt.textContent = " (توکن کمتر)";
    label.appendChild(opt);

    var wrap = document.createElement("div");
    wrap.className = "input-wrap";
    var sel = document.createElement("select");
    sel.id = EFFORT.id;
    [
      ["low", "کم — پیش‌فرض؛ سریع و کم‌هزینه"],
      ["medium", "متوسط"],
      ["high", "زیاد — فقط برای مسائل پیچیده"],
      ["auto", "خودکار — پارامتر فرستاده نشود"],
    ].forEach(function (pair) {
      var o = document.createElement("option");
      o.value = pair[0];
      o.textContent = pair[1];
      sel.appendChild(o);
    });
    sel.value = EFFORT.def;
    wrap.appendChild(sel);

    var hint = document.createElement("div");
    hint.className = "hint-row";
    var ht = document.createElement("span");
    ht.textContent =
      "مدل کمتر فکر می‌کند و مستقیم سراغ ابزار/کد می‌رود، پس توکن استدلال کم می‌شود بدون آنکه کارِ انجام‌شده کم شود. فقط برای Providerهایی فرستاده می‌شود که پارامتر reasoning_effort را می‌شناسند؛ اگر سرویس‌دهنده آن را رد کند، همان یک‌بار بدون پارامتر تکرار می‌شود.";
    hint.appendChild(ht);
    field.appendChild(label);
    field.appendChild(wrap);
    field.appendChild(hint);

    var vision = document.getElementById(VISION.id);
    var visionField = vision && vision.closest ? vision.closest(".field") : null;
    if (visionField && visionField.parentElement === host) {
      host.insertBefore(field, visionField.nextSibling);
    } else {
      host.appendChild(field);
    }
  }

  function effortValue() {
    var v = String(((window.KodYarState && window.KodYarState.view) || {}).thinkingEffort || "").toLowerCase();
    return ["low", "medium", "high", "auto"].indexOf(v) >= 0 ? v : EFFORT.def;
  }

  function populateEffort() {
    var el = document.getElementById(EFFORT.id);
    if (el) el.value = effortValue();
  }

  function saveEffort() {
    var el = document.getElementById(EFFORT.id);
    if (!el) return;
    var mode = el.value;
    if (["low", "medium", "high", "auto"].indexOf(mode) < 0) mode = EFFORT.def;
    var be = backend();
    if (!be || typeof be[EFFORT.set] !== "function") {
      if (window.toast) window.toast("در حالت پیش‌نمایش مرورگر، «" + EFFORT.label + "» ذخیره نمی‌شود", "info");
      return;
    }
    be[EFFORT.set](mode)
      .then(function () {
        if (window.KodYarState && window.KodYarState.view) window.KodYarState.view[EFFORT.view] = mode;
        el.value = mode;
      })
      .catch(function (err) {
        if (window.toast) {
          window.toast("ذخیره‌ی " + EFFORT.label + " ناموفق بود: " + (err && err.message ? err.message : err), "err");
        }
      });
  }

  function init() {
    buildIdleField();
    buildCacheRow();
    syncCacheSwitch((window.KodYarState && window.KodYarState.view || {}).promptCache);
    buildAutoVerifyRow();
    syncAutoVerifySwitch((window.KodYarState && window.KodYarState.view || {}).autoVerify);
    buildVisionField();
    populateVision();
    buildEffortField();
    populateEffort();
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
        populate(IDLE);
        syncCacheSwitch((window.KodYarState && window.KodYarState.view || {}).promptCache);
        syncAutoVerifySwitch((window.KodYarState && window.KodYarState.view || {}).autoVerify);
        populateVision();
        populateEffort();
      });
    populate(AGENT);
    populate(SUB);
    populate(IDLE);

    var saveBtn = document.getElementById("gen-save");
    if (saveBtn)
      saveBtn.addEventListener("click", function () {
        save(AGENT);
        save(SUB);
        save(IDLE);
        saveVision();
        saveEffort();
      });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
