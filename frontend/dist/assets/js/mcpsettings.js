/* KodYar — تب تنظیمات «MCP» (سرورهای ابزار خارجی)
 *
 * فایل خوانا و جدا؛ کد مینیفای‌شدهٔ app.js دست نمی‌خورد. اتصال با همان الگوی
 * permsettings.js/agentsettings.js:
 *   - دکمه‌ی تب «MCP» (data-tab="mcp") که modals.js با data-attr سوییچ
 *     می‌کند؛ شنونده‌ی ما روی همان دکمه stack می‌شود و پنل را پر می‌کند.
 *   - محتوای پنل (#mcp-panel) کاملاً از همین فایل ساخته می‌شود —
 *     index.html فقط دکمه‌ی تب و یک div خالی دارد.
 *   - ذخیره: ویرایشگر JSON → SaveMCPServers(json) با خطاهای دقیق فارسی
 *     بک‌اند؛ وضعیت هر سرور با MCPStatus() («متصل»، «در حال اتصال»،
 *     «خطا: …»، «غیرفعال»).
 *
 * نکات:
 *   - مقادیر env از SettingsView «ماسک» می‌آیند (••••••••)؛ اگر همان
 *     ماسک بی‌ تغییر ذخیره شود بک‌اند مقدار قبلی را نگه می‌دارد.
 *   - حالت Mock: متدهای بک‌اند نیستند — پیام راهنما و دکمه‌ی غیرفعال.
 *   - همه‌ی متن‌های پویا با textContent درج می‌شوند؛ JSON داخل textarea
 *     با direction:ltr/فونت مونو نمایش داده می‌شود.
 */
(function () {
  "use strict";

  var utils = window.KodYarUtils || {};
  var $ = utils.$ || function (s) { return document.querySelector(s); };
  var fa = utils.fa || function (n) { return String(n).replace(/\d/g, function (d) { return "۰۱۲۳۴۵۶۷۸۹"[d]; }); };

  var SAMPLE = [
    {
      name: "my-server",
      command: "npx",
      args: ["-y", "@example/mcp-server"],
      enabled: false,
      env: { API_TOKEN: "your-token-here" }
    }
  ];

  function backend() {
    return (window.go && window.go.main && window.go.main.App) || null;
  }

  function toast(msg, kind) {
    if (window.toast) window.toast(msg, kind || "info");
  }

  // ---------------------------------------------------------- ساخت پنل

  function buildPanel(panel) {
    panel.textContent = "";

    var card = document.createElement("div");
    card.className = "form-card";

    var title = document.createElement("div");
    title.className = "setting-section-title";
    title.textContent = "سرورهای MCP (ابزارهای خارجی)";

    var desc = document.createElement("div");
    desc.className = "setting-desc";
    desc.textContent = "هر سرور MCP یک فرمان محلی است که ابزارهای اضافه را با پروتکل استاندارد MCP به ایجنت می‌دهد. ابزارهای هر سرور با پیشوند mcp_ به مدل معرفی می‌شوند و هر فراخوانی با تأیید شما اجرا می‌شود.";

    var field = document.createElement("div");
    field.className = "field";

    var label = document.createElement("label");
    label.className = "field-label";
    label.textContent = "پیکربندی (JSON)";

    var ta = document.createElement("textarea");
    ta.id = "mcp-json";
    ta.className = "rules-textarea";
    ta.rows = 9;
    ta.spellcheck = false;
    ta.setAttribute("dir", "ltr");
    ta.style.cssText = "direction:ltr;text-align:left;font-family:var(--font-code);font-size:calc(var(--ui-size) - 1px);";

    var hint = document.createElement("div");
    hint.className = "hint-row";
    var hintTxt = document.createElement("span");
    hintTxt.textContent = "مقادیر env رمزنگاری‌شده ذخیره می‌شوند؛ پس از ذخیره به‌صورت ماسک (••••••••) نمایش داده می‌شوند و اگر دست نزنید، مقدار قبلی حفظ می‌شود.";
    hint.appendChild(hintTxt);

    field.appendChild(label);
    field.appendChild(ta);
    field.appendChild(hint);

    // فهرست وضعیت سرورها
    var statusCard = document.createElement("div");
    statusCard.className = "form-card";
    var statusTitle = document.createElement("div");
    statusTitle.className = "setting-section-title";
    statusTitle.textContent = "وضعیت سرورها";
    var statusDesc = document.createElement("div");
    statusDesc.className = "setting-desc";
    statusDesc.textContent = "اتصال تنبل است: هر سرور هنگام اولین نیاز شروع می‌شود. این‌جا می‌توانید وضعیت فعلی را ببینید.";
    var statusList = document.createElement("div");
    statusList.id = "mcp-status-list";
    statusList.style.cssText = "display:flex;flex-direction:column;gap:6px;";
    var statusEmpty = document.createElement("div");
    statusEmpty.className = "setting-desc";
    statusEmpty.id = "mcp-status-empty";
    statusEmpty.textContent = "هنوز سروری تعریف نشده است.";
    statusList.appendChild(statusEmpty);
    statusCard.appendChild(statusTitle);
    statusCard.appendChild(statusDesc);
    statusCard.appendChild(statusList);

    // دکمه‌ها
    var actions = document.createElement("div");
    actions.style.cssText = "display:flex;gap:8px;justify-content:flex-start;flex-wrap:wrap;";
    var saveBtn = document.createElement("button");
    saveBtn.type = "button";
    saveBtn.className = "btn btn-primary";
    saveBtn.id = "mcp-save";
    saveBtn.textContent = "ذخیره‌ی پیکربندی";

    var refreshBtn = document.createElement("button");
    refreshBtn.type = "button";
    refreshBtn.className = "btn btn-ghost";
    refreshBtn.id = "mcp-refresh";
    refreshBtn.textContent = "بازخوانی وضعیت";

    actions.appendChild(saveBtn);
    actions.appendChild(refreshBtn);

    var saveHint = document.createElement("div");
    saveHint.className = "setting-desc";
    saveHint.id = "mcp-save-hint";
    saveHint.textContent = "پس از ذخیره، سرورهای تغییرکرده دوباره راه‌اندازی می‌شوند و سرورهای ناموفق دوباره تلاش می‌کنند.";

    card.appendChild(title);
    card.appendChild(desc);
    card.appendChild(field);
    card.appendChild(actions);
    card.appendChild(saveHint);

    panel.appendChild(card);
    panel.appendChild(statusCard);

    saveBtn.addEventListener("click", save);
    refreshBtn.addEventListener("click", function () { refreshStatus(true); });
  }

  // ---------------------------------------------------------- خواندن/نوشتن

  function currentServers() {
    var view = window.KodYarState && window.KodYarState.view;
    return (view && view.mcpServers) || [];
  }

  function serversToJSON(servers) {
    try {
      return JSON.stringify(servers && servers.length ? servers : SAMPLE, null, 2);
    } catch (e) {
      return JSON.stringify(SAMPLE, null, 2);
    }
  }

  function populateEditor() {
    var ta = $("#mcp-json");
    if (ta) ta.value = serversToJSON(currentServers());
  }

  // نمونهٔ نمایشی (SAMPLE) فقط برای راهنماست؛ اگر فهرست واقعی خالی باشد و
  // کاربر متن ویرایشگر را دست‌نخورده بگذارد، ذخیره باید «هیچ سروری» باشد
  // نه نوشتنِ نمونه به‌عنوان پیکربندی واقعی.
  function isUntouchedSample(parsed) {
    try {
      return JSON.stringify(parsed) === JSON.stringify(SAMPLE);
    } catch (e) {
      return false;
    }
  }

  function save() {
    var b = backend();
    var ta = $("#mcp-json");
    if (!ta) return;
    if (!b || typeof b.SaveMCPServers !== "function") {
      toast("در پیش‌نمایش مرورگر، ذخیره‌ی سرورهای MCP در دسترس نیست", "info");
      return;
    }
    var json = ta.value;
    var parsed = null;
    try { parsed = JSON.parse(json); } catch (e) { /* بک‌اند خطای دقیق فارسی می‌دهد */ }
    if (parsed && currentServers().length === 0 && isUntouchedSample(parsed)) {
      json = "[]";
    }
    // اعتبارسنجی سمت کلاینت — پیام خطای دقیق از بک‌اند فارسی می‌آید
    b.SaveMCPServers(json)
      .then(function () {
        toast("پیکربندی MCP ذخیره شد", "ok");
        // snapshot در KodYarState.view.mcpServers به‌روز نمی‌شد؛ در نتیجه
        // باز کردن دوبارهٔ تب، JSONِ «قبل از ذخیره» را نشان می‌داد و ذخیرهٔ
        // دوباره، ویرایش کاربر را بی‌صدا برمی‌گرداند. وضعیت رسمی را از
        // SettingsView می‌خوانیم (env ماسک‌شده) و ویرایشگر را با آن دوباره
        // پر می‌کنیم.
        if (typeof b.SettingsView === "function") {
          b.SettingsView()
            .then(function (view) {
              if (window.KodYarState && view) {
                try { window.KodYarState.view.mcpServers = view.mcpServers || []; } catch (e) { /* فقطحالت‌های عجیب */ }
                populateEditor();
              }
            })
            .catch(function () { /* ذخیره موفق بود — به‌روزرسانی نمایش در حد امکان است */ });
        }
        refreshStatus(true);
      })
      .catch(function (err) {
        toast(String(err && err.message ? err.message : err), "err");
      });
  }

  function statusChip(kind) {
    var chip = document.createElement("span");
    chip.className = "mcp-status-chip " + kind;
    return chip;
  }

  function renderStatus(rows) {
    var list = $("#mcp-status-list");
    var empty = $("#mcp-status-empty");
    if (!list) return;
    list.textContent = "";
    if (!rows || !rows.length) {
      if (empty) list.appendChild(empty);
      return;
    }
    rows.forEach(function (r) {
      var row = document.createElement("div");
      row.className = "mcp-status-row";

      var name = document.createElement("span");
      name.className = "mcp-status-name";
      name.setAttribute("dir", "ltr");
      name.textContent = String(r.name || "");

      var chip = statusChip(/خطا/.test(String(r.status)) ? "err" : r.status === "متصل" ? "ok" : r.status === "غیرفعال" ? "off" : "wait");
      chip.textContent = String(r.status || "—");

      var tools = document.createElement("span");
      tools.className = "mcp-status-tools";
      var n = Number(r.toolCount) || 0;
      tools.textContent = n > 0 ? fa(n) + " ابزار" : "";

      row.appendChild(name);
      row.appendChild(chip);
      row.appendChild(tools);
      list.appendChild(row);
    });
  }

  function refreshStatus(notify) {
    var b = backend();
    if (!b || typeof b.MCPStatus !== "function") {
      renderStatus(null);
      return;
    }
    b.MCPStatus()
      .then(function (rows) { renderStatus(rows); })
      .catch(function () {
        if (notify) toast("خواندن وضعیت MCP ناموفق بود", "err");
      });
  }

  function onTabOpened() {
    populateEditor();
    refreshStatus(false);
  }

  // ---------------------------------------------------------- اتصال

  function connect() {
    var panel = $("#mcp-panel");
    if (!panel) return;
    buildPanel(panel);

    var tabBtn = document.querySelector('.modal-tab[data-tab="mcp"]');
    if (tabBtn) {
      tabBtn.addEventListener("click", function () {
        // اجرا بعد از شنونده‌ی modals.js (سوییچ پنل)
        setTimeout(onTabOpened, 0);
      });
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", connect);
  } else {
    connect();
  }
})();
