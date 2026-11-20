/* KodYar — دیالوگ تأیید تغییر فایل (Diff Preview) و رندر Diff.
 *
 * پنل «تغییرات فایل» و همه‌ی مسیرهای «برگرداندن تغییر» از پروژه حذف شده‌اند؛
 * این فایل فقط دو چیز را نگه می‌دارد:
 *   - مودال تأیید تغییر (#diff-overlay) با شمارش معکوس ۳ دقیقه‌ای،
 *     «اجازه تا پایان پاسخ/نشست» و پاسخ به بک‌اند (RespondFileConfirm)
 *   - renderDiff که کارت خلاصه‌ی نوبت هم از آن برای نمایش تفاوت استفاده می‌کند
 * رویدادها از app.js می‌آیند: agent:confirm / agent:change_applied /
 * agent:change_rejected / agent:change_reverted و پایان نسل (chat:done/error).
 */
(function () {
  "use strict";

  var U = window.KodYarUtils;
  var $ = U.$;
  var fa = U.fa;
  var esc = U.esc;
  var app = function () {
    return window.go.main.App;
  };

  var labels = {
    create: "ایجاد فایل جدید",
    modify: "ویرایش فایل",
    delete: "حذف فایل",
    move: "جابه‌جایی / تغییر نام"
  };
  var icons = { create: "plus", modify: "pencil", delete: "trash", move: "edit2" };

  var current = null;
  var timer = null;
  var left = 0;

  /* ------------------------------------------------------------- رندر Diff */

  function renderDiff(box, diff) {
    if (!box) return;
    var lines = String(diff || "").split("\n");
    var MAX = 2500;
    var html = "";
    var shown = 0;
    var truncated = 0;
    for (var i = 0; i < lines.length; i++) {
      var line = lines[i];
      if (line === "") continue;
      if (shown >= MAX) {
        truncated++;
        continue;
      }
      shown++;
      var cls =
        line.indexOf("+++") === 0 || line.indexOf("---") === 0
          ? "dl-file"
          : line.indexOf("@@") === 0
            ? "dl-hunk"
            : line.charAt(0) === "+"
              ? "dl-add"
              : line.charAt(0) === "-"
                ? "dl-del"
                : "dl-ctx";
      html += '<div class="dl ' + cls + '">' + esc(line) + "</div>";
    }
    if (truncated) html += '<div class="dl dl-ctx">… ' + truncated + " خط دیگر نمایش داده نشد</div>";
    box.innerHTML = html || '<div class="dl dl-ctx">(بدون تفاوت)</div>';
  }

  /* ------------------------------------------------------- پاسخ به بک‌اند */

  function stopCountdown() {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
    var c = $("#diff-countdown");
    if (c) c.hidden = true;
  }

  function respond(allow, scope) {
    if (!current) return;
    var id = current.id;
    current = null;
    stopCountdown();
    var overlay = $("#diff-overlay");
    if (overlay) overlay.classList.remove("open");
    var be = app();
    if (!be) return;
    var chosen = scope || "";
    var p = be.RespondFileConfirmScope
      ? be.RespondFileConfirmScope(id, allow, chosen)
      : be.RespondFileConfirm(id, allow);
    if (p && p.catch) p.catch(function () {});
  }

  function startCountdown() {
    stopCountdown();
    left = 180;
    var box = $("#diff-countdown");
    if (!box) return;
    box.hidden = false;
    // آیکون یک‌بار ساخته می‌شود و هر ثانیه فقط متن شمارنده عوض می‌شود
    // (قبلاً هر تیک innerHTML کامل دوباره parse می‌شد).
    box.innerHTML = Icons.get("clock", 13);
    var num = document.createElement("span");
    box.appendChild(num);
    function paint() {
      var m = Math.floor(left / 60);
      var s = left % 60;
      var next = fa(m) + ":" + fa(String(s).padStart(2, "0"));
      if (num.textContent !== next) num.textContent = next;
    }
    paint();
    timer = setInterval(function () {
      left--;
      if (left <= 0) {
        paint();
        respond(false);
        return;
      }
      paint();
    }, 1000);
  }

  /* ------------------------------------------------------------ مودال تأیید */

  function showConfirm(change) {
    if (!change || !change.id) return;
    if (current) respond(false);
    current = change;
    var kind = change.kind || "modify";
    var badge = $("#diff-kind-badge");
    if (badge) {
      badge.innerHTML = Icons.get(icons[kind] || "save", 20);
      badge.title = labels[kind] || "";
    }
    var pathEl = $("#diff-file-path");
    if (pathEl) {
      pathEl.textContent =
        kind === "move" && change.movedTo ? change.path + "  →  " + change.movedTo : change.path || "";
    }
    var stats = $("#diff-stats");
    if (stats) {
      stats.innerHTML =
        '<span class="ds-add">+' +
        fa(change.added || 0) +
        '</span><span class="ds-del">−' +
        fa(change.removed || 0) +
        "</span>";
    }
    renderDiff($("#diff-view"), change.diff);
    var overlay = $("#diff-overlay");
    if (overlay) overlay.classList.add("open");
    startCountdown();
    setTimeout(function () {
      var accept = $("#diff-accept");
      if (accept) accept.focus();
    }, 120);
  }

  function bind() {
    var accept = $("#diff-accept");
    if (accept) accept.addEventListener("click", function () { respond(true, ""); });
    var allowGen = $("#diff-allow-gen");
    if (allowGen) allowGen.addEventListener("click", function () { respond(true, "generation"); });
    var allowSession = $("#diff-allow-session");
    if (allowSession) allowSession.addEventListener("click", function () { respond(true, "session"); });
    var copy = $("#diff-copy");
    if (copy)
      copy.addEventListener("click", function () {
        if (current && window.KodYarMD) {
          window.KodYarMD.copyText(current.diff || "").then(function () {
            window.toast("متن Diff کپی شد");
          });
        }
      });
    var reject = $("#diff-reject");
    if (reject) reject.addEventListener("click", function () { respond(false); });
    var close = $("#diff-close");
    if (close) close.addEventListener("click", function () { respond(false); });
    var overlay = $("#diff-overlay");
    if (overlay)
      overlay.addEventListener("click", function (e) {
        if (e.target === overlay) respond(false);
      });
    document.addEventListener("keydown", function (e) {
      if (!current || e.key !== "Enter") return;
      var t = e.target;
      if (t && t.closest && t.closest("button, input, textarea, select, a[href], [contenteditable]") &&
          t.id !== "diff-accept") {
        return;
      }
      e.preventDefault();
      respond(true);
    });
  }

  function onGenerationEnd() {
    if (!current) return;
    current = null;
    stopCountdown();
    var overlay = $("#diff-overlay");
    if (overlay) overlay.classList.remove("open");
  }

  function handleEscape() {
    if (current) {
      respond(false);
      return true;
    }
    var overlay = $("#diff-overlay");
    if (overlay && overlay.classList.contains("open")) {
      stopCountdown();
      overlay.classList.remove("open");
      return true;
    }
    return false;
  }

  window.KodYarChanges = {
    bind: bind,
    onAgentConfirm: function (data) {
      if (data && data.change) showConfirm(data.change);
    },
    onChangeApplied: function (data) {
      var ch = data && data.change;
      if (ch && ch.path && window.toast) {
        window.toast("تغییر اعمال شد: " + ch.path + " (+‎" + fa(ch.added || 0) + "/−‎" + fa(ch.removed || 0) + ")");
      }
    },
    onChangeRejected: function () {},
    onChangeReverted: function () {},
    onGenerationEnd: onGenerationEnd,
    handleEscape: handleEscape,
    renderDiff: renderDiff
  };
})();
