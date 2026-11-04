/* MyAI — مودال دسترسی (فاز ۱: سیستم Permissions)
 *
 * رویداد agent:permission_ask از بک‌اند می‌آید و سه نوع دارد:
 *   - kind=command       → «اجازه یک‌بار / در این نشست / همیشه اجازه بده / رد»
 *     (اگر ask.noAlways یا ask.pattern خالی باشد، گزینه‌های نشست/همیشه
 *      نمایش داده نمی‌شوند — مثلاً ارجاع به فایل حساس؛ ۳.۲.۴)
 *   - kind=sensitive_read→ «اجازه خواندن / رد» (فایل حساس مثل .env)
 *   - kind=tool          → «اجازه اجرای ابزار / رد»
 *   - kind=web_host      → web_fetch: «یک‌بار / در این نشست / همیشه برای
 *     همین دامنه / رد» — هرگز اجازه‌ی سراسری برای کل ابزار (C.1)
 *
 * پاسخ از طریق RespondPermissionConfirm(id, decision) برمی‌گردد:
 *   allow | session | always | reject
 *
 * نکات ایمنی:
 *   - همه‌ی متن‌های پویا با textContent درج می‌شوند (هرگز innerHTML خام).
 *   - در حالت Mock (بدون بک‌اند واقعی) مودال بدون فراخوانی بک‌اند بسته می‌شود.
 *   - بستن با Esc/کلیک بیرون/دکمه‌ی بستن = رد (همان مهلت خودکار بک‌اند).
 */
(function () {
  "use strict";

  function backend() {
    return (window.go && window.go.main && window.go.main.App) || null;
  }

  function respond(id, decision) {
    var b = backend();
    if (b && typeof b.RespondPermissionConfirm === "function") {
      try {
        b.RespondPermissionConfirm(id, decision).catch(function () { /* منقضی شده — بی‌خطر */ });
      } catch (e) { /* noop */ }
    }
  }

  function makeModal(ask) {
    var overlay = document.createElement("div");
    // کلاس اضافهٔ perm-ask: هندلر سراسری Esc در app.js با دیدن آن،
    // closeTop را صدا نمی‌زند تا مودال پشت‌زمینه (تنظیمات و…) بسته نشود.
    overlay.className = "modal-overlay perm-ask";

    var modal = document.createElement("div");
    modal.className = "modal modal-sm";
    modal.setAttribute("role", "alertdialog");
    modal.setAttribute("aria-modal", "true");

    // ---- سربرگ
    var head = document.createElement("div");
    head.className = "modal-head";
    var title = document.createElement("div");
    title.className = "modal-title";
    title.textContent = "تأیید دسترسی";
    var close = document.createElement("button");
    close.className = "modal-close";
    close.title = "بستن";
    head.appendChild(title);
    head.appendChild(close);

    // ---- بدنه
    var body = document.createElement("div");
    body.className = "modal-body";
    var msg = document.createElement("p");
    msg.style.cssText = "font-size:13px;line-height:1.9;color:var(--text);margin:0 0 10px;";
    msg.textContent = ask.message || "";
    body.appendChild(msg);

    var detail = document.createElement("div");
    detail.style.cssText =
      "font-family:var(--font-code);font-size:12px;direction:ltr;unicode-bidi:isolate;" +
      "text-align:left;background:var(--bg2,#111318);border:1px solid var(--border);" +
      "border-radius:8px;padding:8px 10px;word-break:break-all;max-height:120px;overflow:auto;";
    if (ask.kind === "command" && ask.command) {
      detail.textContent = "$ " + ask.command;
    } else if (ask.kind === "sensitive_read" && ask.path) {
      detail.textContent = ask.path;
    } else if ((ask.kind === "tool" || ask.kind === "web_host") && ask.url) {
      // web_fetch — آدرس درخواستی، LTR و ایزوله از جمله‌ی فارسی مودال
      detail.textContent = ask.url;
    } else if (ask.kind === "tool" && ask.command) {
      detail.textContent = ask.command;
    }
    body.appendChild(detail);

    if (ask.kind === "command" && ask.pattern) {
      var hint = document.createElement("div");
      hint.className = "hint-row";
      hint.style.marginTop = "8px";
      var hintTxt = document.createElement("span");
      hintTxt.textContent = "«همیشه اجازه بده» قاعده‌ی " + ask.pattern + " را ذخیره می‌کند؛ «در این نشست» فقط تا بستن برنامه فعال می‌ماند.";
      hint.appendChild(hintTxt);
      body.appendChild(hint);
    }
    if (ask.kind === "web_host" && ask.pattern) {
      var whint = document.createElement("div");
      whint.className = "hint-row";
      whint.style.marginTop = "8px";
      var wtxt = document.createElement("span");
      wtxt.textContent =
        "اجازه فقط برای دامنه‌ی «" +
        ask.pattern +
        "» ذخیره می‌شود (نه برای همه‌ی آدرس‌ها)؛ «در این نشست» تا بستن برنامه فعال می‌ماند.";
      whint.appendChild(wtxt);
      body.appendChild(whint);
    }

    // ---- پاورقی
    var foot = document.createElement("div");
    foot.className = "modal-foot";
    var spacer = document.createElement("span");
    spacer.className = "spacer";
    foot.appendChild(spacer);

    function btn(text, cls, decision) {
      var b = document.createElement("button");
      b.className = cls;
      b.type = "button";
      b.textContent = text;
      b.addEventListener("click", function () {
        respond(ask.id, decision);
        close_();
      });
      return b;
    }

    if (ask.kind === "command") {
      foot.appendChild(btn("رد", "btn btn-danger", "reject"));
      // بدون الگو (حساس/محافظت‌شده) فقط «اجازه یک‌بار» — «همیشه» و «نشست»
      // برای این دستورها هرگز پیشنهاد نمی‌شود (۳.۲.۴).
      if (ask.pattern && !ask.noAlways) {
        foot.appendChild(btn("در این نشست", "btn btn-ghost", "session"));
        foot.appendChild(btn("همیشه اجازه بده", "btn btn-ghost", "always"));
      }
      foot.appendChild(btn("اجازه یک‌بار", "btn btn-primary", "allow"));
    } else if (ask.kind === "web_host") {
      // C.1: گزینه‌ی سراسری وجود ندارد؛ فقط همین دامنه
      foot.appendChild(btn("رد", "btn btn-danger", "reject"));
      if (ask.pattern) {
        foot.appendChild(btn("در این نشست", "btn btn-ghost", "session"));
        foot.appendChild(btn("همیشه برای این دامنه", "btn btn-ghost", "always"));
      }
      foot.appendChild(btn("اجازه یک‌بار", "btn btn-primary", "allow"));
    } else {
      foot.appendChild(btn("رد", "btn btn-danger", "reject"));
      foot.appendChild(btn("اجازه", "btn btn-primary", "allow"));
    }

    modal.appendChild(head);
    modal.appendChild(body);
    modal.appendChild(foot);
    overlay.appendChild(modal);
    document.body.appendChild(overlay);
    // .modal-overlay به‌طور پیش‌فرض opacity:0 و pointer-events:none است؛
    // بدون کلاس open مودال نامرئی و غیرقابل‌کلیک می‌ماند و درخواست اجازه
    // پس از مهلت ۳ دقیقه‌ای بک‌اند به‌صورت خودکار «رد» می‌شد.
    overlay.classList.add("open");

    var closed = false;
    function close_(reject) {
      if (closed) return;
      closed = true;
      if (reject) respond(ask.id, "reject");
      document.removeEventListener("keydown", onKey);
      document.body.removeChild(overlay);
    }
    function onKey(e) {
      if (e.key === "Escape") {
        e.preventDefault();
        close_(true);
      }
    }
    close.addEventListener("click", function () { close_(true); });
    overlay.addEventListener("mousedown", function (e) {
      if (e.target === overlay) close_(true);
    });
    document.addEventListener("keydown", onKey);
    return { close: close_ };
  }

  var active = null;
  function onAsk(data) {
    var ask = data && data.ask;
    if (!ask || !ask.id) return;
    // فقط یک مودال هم‌زمان — درخواست جدید، قبلی را رد می‌کند
    if (active) active.close(true);
    active = makeModal(ask);
  }

  if (window.runtime && typeof window.runtime.EventsOn === "function") {
    window.runtime.EventsOn("agent:permission_ask", onAsk);
  }
})();
