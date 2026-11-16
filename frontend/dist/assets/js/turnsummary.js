/* MyAI — کارت «خلاصه‌ی واقعی این نوبت» (Stage 2 — ۴.۲)
 *
 * رویدادها:
 *   - chat:turn_summary  {summary}  → پایان هر نوبتِ ایجنت که فایل یا دستور داشت
 *   - chat:turn_summaries {conversationId, summaries} → بازکردن گفتگوی قبلی
 *
 * کارت پس از آخرین پیام دستیار در #msg-col اضافه می‌شود:
 *   - عنوان «خلاصه‌ی واقعی این نوبت» + فهرست فایل‌ها با +/− و وضعیت محافظت
 *   - کلیک روی فایل → دیافراگ diff (GetFileChangeDiff)
 *   - کارت فقط اطلاعات نوبت را نشان می‌دهد؛ هیچ دکمه‌ی بازگشتی ندارد.
 *   - چیپ هشدار «پس از آخرین تغییر، تست یا ساخت اجرا نشده» وقتی
 *     verification.ok=false ولی فایلی عوض شده
 *   - فهرست دستورات با کد خروج
 *
 * نکات ایمنی: همه‌ی متن‌های پویا با textContent؛ مسیرها/دستورات با
 * dir="ltr" و unicode-bidi:isolate از جمله‌ی فارسی جدا می‌شوند؛ حالت Mock
 * بدون بک‌اند واقعی بی‌خطا کار می‌کند (دکمه‌ها پیام پیش‌نمایش می‌دهند).
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

  var opLabel = { create: "ایجاد", modify: "ویرایش", delete: "حذف", move: "جابه‌جایی" };

  /* ---------------------------------------------------------------- کارت */

  function fileRow(f) {
    var row = document.createElement("button");
    row.type = "button";
    row.className = "ts-file" + (f.reverted ? " ts-reverted" : "");
    row.title = "نمایش تفاوت";

    var op = document.createElement("span");
    op.className = "ts-op ts-op-" + (f.op || "modify");
    op.textContent = opLabel[f.op] || f.op || "";

    var path = document.createElement("span");
    path.className = "ts-path";
    path.setAttribute("dir", "ltr");
    path.textContent = f.movedTo ? f.path + "  →  " + f.movedTo : f.path;

    var stat = document.createElement("span");
    stat.className = "ts-stat";
    stat.setAttribute("dir", "ltr");
    stat.textContent = "+" + fa(f.added || 0) + " / −" + fa(f.removed || 0);

    row.appendChild(op);
    row.appendChild(path);
    row.appendChild(stat);
    if (f.protected) {
      var shield = document.createElement("span");
      shield.className = "ts-protected";
      shield.textContent = "محافظت‌شده";
      row.appendChild(shield);
    }
    if (f.reverted) {
      var rev = document.createElement("span");
      rev.className = "ts-revtag";
      rev.textContent = "برگشت‌داده‌شده";
      row.appendChild(rev);
    }
    row.addEventListener("click", function () {
      showDiff(f);
    });
    return row;
  }

  function commandRow(c) {
    var row = document.createElement("div");
    row.className = "ts-cmd";
    var cmd = document.createElement("span");
    cmd.className = "ts-cmd-text";
    cmd.setAttribute("dir", "ltr");
    cmd.textContent = c.command || "";
    var code = document.createElement("span");
    code.className = "ts-exit" + (c.exitOk ? " ts-exit-ok" : "");
    code.textContent = c.exitOk ? "خروج ۰" : "خروج " + fa(c.exitCode == null ? "?" : c.exitCode);
    if (c.verified) {
      var v = document.createElement("span");
      v.className = "ts-verified";
      v.textContent = "راستی‌آزمایی";
      row.appendChild(v);
    }
    row.appendChild(cmd);
    row.appendChild(code);
    return row;
  }

  // آخرین زمان‌بندی نوبت (رویداد chat:timing از latency.go) — ردیف کوچک
  // «زمان‌ها» در پای همان کارت خلاصه. ارقام فارسی، هیچ HTML ای درج نمی‌شود.
  var lastTiming = null;

  function sec(ms) {
    var v = (Number(ms) || 0) / 1000;
    return fa(v >= 10 ? Math.round(v) : v.toFixed(1).replace(".", "٫")) + "s";
  }

  function timingRow(t) {
    var row = document.createElement("div");
    row.className = "ts-timing";
    var parts = [];
    parts.push("اولین توکن " + (Number(t.ttftMs) >= 0 ? sec(t.ttftMs) : "—"));
    parts.push(fa(t.rounds || 0) + " دور" + (t.maxRoundMs ? " (کندترین " + sec(t.maxRoundMs) + ")" : ""));
    if (t.tools) parts.push(fa(t.tools) + " ابزار / " + sec(t.toolMs));
    parts.push("کل " + sec(t.totalMs));
    if (t.promptCache) parts.push("کش روشن");
    row.textContent = parts.join(" · ");
    row.title = "زمان تا اولین توکن، زمان دورها، زمان ابزارها و کل نوبت";
    return row;
  }

  function makeCard(s) {
    var card = document.createElement("div");
    card.className = "ts-card";
    card.setAttribute("data-turn", String(s.turnId || 0));

    var head = document.createElement("div");
    head.className = "ts-head";
    var title = document.createElement("span");
    title.className = "ts-title";
    title.textContent = "خلاصه‌ی واقعی این نوبت";
    head.appendChild(title);
    var n = document.createElement("span");
    n.className = "ts-count";
    n.textContent = fa((s.files || []).length) + " فایل";
    head.appendChild(n);
    card.appendChild(head);

    // هشدار «بدون راستی‌آزمایی» — فقط وقتی فایلی عوض شده و تأیید نشده
    if ((s.files || []).length > 0 && s.verification && !s.verification.ok) {
      var warn = document.createElement("div");
      warn.className = "ts-warn";
      warn.textContent = "پس از آخرین تغییر، تست یا ساخت اجرا نشده";
      card.appendChild(warn);
    } else if (s.verification && s.verification.ok && s.verification.command) {
      var ok = document.createElement("div");
      ok.className = "ts-ok";
      var t1 = document.createElement("span");
      t1.textContent = "راستی‌آزمایی موفق: ";
      ok.appendChild(t1);
      var c = document.createElement("span");
      c.setAttribute("dir", "ltr");
      c.className = "ts-ok-cmd";
      c.textContent = s.verification.command;
      ok.appendChild(c);
      card.appendChild(ok);
    }

    var files = document.createElement("div");
    files.className = "ts-files";
    (s.files || []).forEach(function (f) {
      files.appendChild(fileRow(f));
    });
    if ((s.files || []).length > 0) card.appendChild(files);

    if ((s.commands || []).length > 0) {
      var cmdsTitle = document.createElement("div");
      cmdsTitle.className = "ts-cmds-title";
      cmdsTitle.textContent = "دستورهای اجراشده";
      card.appendChild(cmdsTitle);
      var cmds = document.createElement("div");
      cmds.className = "ts-cmds";
      s.commands.forEach(function (c) {
        cmds.appendChild(commandRow(c));
      });
      card.appendChild(cmds);
    }

    if (s.tokens && (s.tokens.prompt || s.tokens.completion)) {
      var tok = document.createElement("div");
      tok.className = "ts-tokens";
      tok.textContent =
        "توکن این نوبت: " + fa(s.tokens.prompt || 0) + " ورودی + " + fa(s.tokens.completion || 0) + " خروجی";
      card.appendChild(tok);
    }

    // زمان‌بندی نوبت (اگر رسیده باشد و مال همین نوبت باشد)
    if (lastTiming && lastTiming.t && (!s.turnId || lastTiming.turnId === s.turnId)) {
      card.appendChild(timingRow(lastTiming.t));
    }

    // سقف توکن (A.4): نوبت به‌خاطر سقف تنظیم‌شده متوقف شد
    if (s.tokenCapHit) {
      var cap = document.createElement("div");
      cap.className = "ts-warn ts-cap";
      cap.textContent =
        "سقف توکن این نوبت پر شد" +
        (s.tokenCap ? " (" + fa(s.tokenCap) + " توکن)" : "") +
        " — نوبت متوقف شد؛ می‌توانید ادامه بدهید";
      card.appendChild(cap);
    }

    return card;
  }

  /* -------------------------------------------------------------- diff box */

  function showDiff(f) {
    var be = backend();
    if (!be || typeof be.GetFileChangeDiff !== "function") {
      if (window.toast) window.toast("در حالت پیش‌نمایش مرورگر، تفاوت نمایش داده نمی‌شود", "info");
      return;
    }
    be.GetFileChangeDiff(f.changeId)
      .then(function (diff) {
        openDiffModal(f, diff);
      })
      .catch(function (err) {
        if (window.toast) window.toast(String(err && err.message ? err.message : err), "err");
      });
  }

  function openDiffModal(f, diff) {
    var overlay = document.createElement("div");
    overlay.className = "modal-overlay";
    var modal = document.createElement("div");
    modal.className = "modal modal-diff";
    modal.setAttribute("role", "dialog");
    modal.setAttribute("aria-modal", "true");

    var head = document.createElement("div");
    head.className = "modal-head";
    var title = document.createElement("div");
    title.className = "modal-title";
    title.textContent = "تفاوت فایل — نوبت " + fa(f.turnId || 0);
    var close = document.createElement("button");
    close.className = "modal-close";
    close.title = "بستن";
    head.appendChild(title);
    head.appendChild(close);

    var body = document.createElement("div");
    body.className = "modal-body";
    body.style.padding = "0";
    var pathRow = document.createElement("div");
    pathRow.className = "diff-file-row";
    var pathEl = document.createElement("span");
    pathEl.className = "diff-file-path";
    pathEl.setAttribute("dir", "ltr");
    pathEl.textContent = f.movedTo ? f.path + "  →  " + f.movedTo : f.path;
    pathRow.appendChild(pathEl);
    var view = document.createElement("div");
    view.className = "diff-view";
    view.setAttribute("dir", "ltr");
    // رندر خط‌به‌خط diff با کلاس‌های موجود (dl dl-add/dl-del/…)
    if (window.MyAIChanges && window.MyAIChanges.renderDiff) {
      window.MyAIChanges.renderDiff(view, diff || "");
    } else {
      var pre = document.createElement("pre");
      pre.style.cssText = "margin:0;padding:10px;font-family:var(--font-code);font-size:12px;white-space:pre-wrap;";
      pre.textContent = diff || "";
      view.appendChild(pre);
    }
    body.appendChild(pathRow);
    body.appendChild(view);

    var foot = document.createElement("div");
    foot.className = "modal-foot";
    var spacer = document.createElement("span");
    spacer.className = "spacer";
    var bClose = document.createElement("button");
    bClose.type = "button";
    bClose.className = "btn btn-primary";
    bClose.textContent = "بستن";
    foot.appendChild(spacer);
    foot.appendChild(bClose);

    modal.appendChild(head);
    modal.appendChild(body);
    modal.appendChild(foot);
    overlay.appendChild(modal);
    document.body.appendChild(overlay);
    requestAnimationFrame(function () {
      overlay.classList.add("open");
    });

    var closed = false;
    function close_() {
      if (closed) return;
      closed = true;
      document.removeEventListener("keydown", onKey);
      if (overlay.parentNode) document.body.removeChild(overlay);
    }
    function onKey(e) {
      if (e.key === "Escape") {
        e.preventDefault();
        close_();
      }
    }
    close.addEventListener("click", close_);
    bClose.addEventListener("click", close_);
    overlay.addEventListener("mousedown", function (e) {
      if (e.target === overlay) close_();
    });
    document.addEventListener("keydown", onKey);

  }

  // برای تست‌های قراردادی/مرورگر (Mock mode)
  window.MyAITurnSummary = { makeCard: makeCard };

  /* --------------------------------------------------------------- رویدادها */

  function activeConvId() {
    return (window.MyAIState && window.MyAIState.activeId) || null;
  }

  function appendCard(summary) {
    if (!summary) return;
    var col = document.getElementById("msg-col");
    if (!col || col.hidden) return;
    // فقط برای گفتگوی فعال
    if (activeConvId() && summary.conversationId && summary.conversationId !== activeConvId()) return;
    col.appendChild(makeCard(summary));
    col.scrollIntoView({ block: "end" });
  }

  function replaceAllCards(summaries) {
    var col = document.getElementById("msg-col");
    if (!col) return;
    col.querySelectorAll(".ts-card").forEach(function (el) {
      el.remove();
    });
    (summaries || []).forEach(function (s) {
      if (activeConvId() && s.conversationId && s.conversationId !== activeConvId()) return;
      col.appendChild(makeCard(s));
    });
  }

  function init() {
    if (window.runtime && typeof window.runtime.EventsOn === "function") {
      window.runtime.EventsOn("chat:turn_summary", function (data) {
        var s = data && data.summary;
        if (!s) return;
        // یک فریم صبر — پیام پایانی chat:done اول رندر شود
        setTimeout(function () {
          appendCard(s);
        }, 30);
      });
      window.runtime.EventsOn("chat:timing", function (data) {
        if (!data || !data.timing) return;
        lastTiming = { turnId: data.turnId, t: data.timing };
        // اگر کارت همان نوبت زودتر ساخته شده، همین‌جا ردیف را اضافه کن.
        var card = document.querySelector('.ts-card[data-turn="' + String(data.turnId || 0) + '"]');
        if (card && !card.querySelector(".ts-timing")) card.appendChild(timingRow(data.timing));
      });
      window.runtime.EventsOn("chat:turn_summaries", function (data) {
        if (!data) return;
        setTimeout(function () {
          replaceAllCards(data.summaries || []);
        }, 60);
      });
      // یادداشت «مسیر محافظت‌شده / حذف سنگین» روی مودال تأیید Diff
      // (changes.js مقدار را می‌گیرد ولی minified است — درج نوت اینجا، خوانا).
      window.runtime.EventsOn("agent:confirm", function (data) {
        var ch = data && data.change;
        if (!ch || (!ch.protected && !ch.confirmNote && !ch.heavyDelete)) return;
        setTimeout(function () {
          var overlay = document.getElementById("diff-overlay");
          if (!overlay || !overlay.classList.contains("open")) return;
          var old = overlay.querySelector(".ts-protect-note");
          if (old) old.remove();
          var row = document.querySelector(".diff-file-row");
          if (!row || !row.parentNode) return;
          var note = document.createElement("div");
          note.className = "ts-protect-note";
          note.textContent =
            ch.confirmNote ||
            "این تغییر به مسیر محافظت‌شده اشاره دارد و تأیید صریح می‌خواهد.";
          row.parentNode.insertBefore(note, row.nextSibling);
        }, 20);
      });
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
