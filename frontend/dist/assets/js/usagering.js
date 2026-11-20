/* KodYar — نشانگر مصرف توکن (نسخهٔ ۱.۱۱ — بازطراحی کامل).
 *
 * دو خواستهٔ کاربر که این ماژول پاسخ می‌دهد:
 *
 *  ۱) «دایرهٔ مصرف زیبا نیست» — دایرهٔ قبلی یک conic-gradient ساده با لبهٔ
 *     کِش (hard edge) بود. حالا یک دونات SVG با مسیر گرادیانی، کلاهِ گرد
 *     (rounded caps) و انیمیشن نرم است؛ عدد درصد وسط دایره با نیم‌علامت
 *     «٪» فارسی، برچسب «کانتکست» و کسرِ «۱۲هزار از ۲۰۰هزار» کنار آن.
 *
 *  ۲) «صفر کردن شمارنده نباید دست کاربر باشد» — دکمهٔ «صفر کن» حذف شد.
 *     صفر شدن فقط وقتی رخ می‌دهد که بک‌اند شارژ سرویس‌دهنده را خودکار
 *     تشخیص دهد (usageguard.go: افزایش موجودی، یا بازیابی پس از خطای سهمیه)
 *     و رویداد usage:auto-reset بفرستد؛ ما همان‌جا توست خبر می‌دهیم.
 *     پاپ‌اور فقط وضعیت پایش را نشان می‌دهد و دکمهٔ «بررسی شارژ» دارد که
 *     فقط موجودی را دوباره می‌خواند (هرگز مستقیم صفر نمی‌کند).
 *
 * امنیت متن: همه‌چیز از طریق textContent/createElementNS درج می‌شود
 * (هیچ درج مستقیم HTML ای در این ماژول مجاز نیست).
 */
(function () {
  "use strict";

  var utils = window.KodYarUtils || {};
  var fa = utils.fa || function (n) {
    return String(n).replace(/\d/g, function (d) { return "۰۱۲۳۴۵۶۷۸۹"[d]; });
  };
  function $(sel) { return document.querySelector(sel); }
  function num(n) { return Number(n) || 0; }

  var widget = null, ringEl = null, svgArc = null, SVG_NS = "http://www.w3.org/2000/svg";
  // محیط دایره برای r=15.5 در viewBox 36×36 — با JS هم محاسبه می‌شود ولی
  // ثابت بودنش Transition را پایدار نگه می‌دارد.
  var RADIUS = 15.5, CIRC = 2 * Math.PI * RADIUS;

  var pop = null, popOpen = false, probeBusy = false, pollTimer = null;

  // compact عدد را کوتاه و فارسی می‌کند: ۹۸۰ / ۱۲هزار / ۱٫۲م
  function compact(n) {
    n = num(n);
    if (n >= 1000000) return fa((n / 1000000).toFixed(1)) + " م";
    if (n >= 1000) return fa(Math.round(n / 1000)) + " هزار";
    return fa(Math.round(n));
  }

  // faMoney موجودی را با دو رقم اعشار و ممیز فارسی می‌کند: ۴٫۵۰ USD
  function faMoney(v, currency) {
    v = num(v);
    var s = fa(v.toFixed(2)).replace(".", "٫");
    return currency ? s + " " + currency : s;
  }

  // pctFromDom درصد را از متن #usage-pct (که app.js می‌نویسد) می‌خواند تا
  // عدد دایرهٔ SVG دقیقاً همان عددی باشد که کاربر می‌بیند؛ ارقام فارسی هم
  // پارس می‌شوند. خواندن ناموفق → null.
  function pctFromDom() {
    var pc = $("#usage-pct");
    if (!pc) return null;
    var txt = String(pc.textContent || "").replace(/[۰-۹]/g, function (d) {
      return "۰۱۲۳۴۵۶۷۸۹".indexOf(d);
    }).replace(/[^\d]/g, "");
    if (txt === "") return null;
    var v = parseInt(txt, 10);
    if (isNaN(v)) return null;
    return Math.max(0, Math.min(100, v));
  }

  // snapshot وضعیت فعلی از KodYarState (منبع UI).
  function snapshot() {
    var st = window.KodYarState || {};
    var v = st.view || {};
    var conv = st.activeConv;
    var limit = num(v.tokenLimit) || 200000;
    var ctx = 0;
    if (conv) ctx = num(conv.contextTokens);
    var live = st.liveUsage || {};
    if (st.streaming && num(live.ctx) > 0) ctx = num(live.ctx);
    var pct = Math.min(100, Math.round((ctx / Math.max(limit, 1)) * 100));
    var provName = v.provider || "";
    if (Array.isArray(v.providers)) {
      for (var i = 0; i < v.providers.length; i++) {
        if (v.providers[i] && v.providers[i].id === v.provider) {
          provName = v.providers[i].displayName || v.providers[i].name || provName;
          break;
        }
      }
    }
    return {
      ctx: ctx,
      limit: limit,
      pct: pct,
      streaming: !!st.streaming,
      provider: num(v.providerTokensUsed),
      total: num(v.tokensUsed),
      model: v.model || "",
      modelTokens: num(v.modelTokensUsed),
      since: num(v.usageSince),
      provName: provName,
      balance: v.providerBalance || null,
      lastAutoResetAt: num(v.lastAutoResetAt),
      lastAutoResetReason: v.lastAutoResetReason || "",
      autoUsageReset: v.autoUsageReset !== false
    };
  }

  function sinceText(unix) {
    if (!unix || unix <= 0) return "";
    var d = new Date(unix * 1000);
    try {
      return d.toLocaleDateString("fa-IR", { year: "numeric", month: "long", day: "numeric" });
    } catch (e) {
      return d.toLocaleDateString();
    }
  }

  function clockText(unix) {
    if (!unix || unix <= 0) return "";
    var d = new Date(unix * 1000);
    try {
      return d.toLocaleTimeString("fa-IR", { hour: "2-digit", minute: "2-digit" });
    } catch (e) {
      return d.toLocaleTimeString();
    }
  }

  // buildRing SVG دونات را داخل #usage-ring می‌سازد؛ مسیر گرادیانی با کلاهِ
  // گرد و انیمیشن نرم stroke-dashoffset. #usage-pct دست‌نخورده می‌ماند تا
  // app.js (که به آن نوشتن دارد) بی‌دردسر کارش را ادامه دهد.
  function buildRing() {
    if (!ringEl || ringEl.dataset.svg === "1") return;
    ringEl.dataset.svg = "1";
    var svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("class", "usage-ring-svg");
    svg.setAttribute("viewBox", "0 0 36 36");
    svg.setAttribute("aria-hidden", "true");

    var defs = document.createElementNS(SVG_NS, "defs");
    var grad = document.createElementNS(SVG_NS, "linearGradient");
    grad.setAttribute("id", "usageRingGrad");
    grad.setAttribute("gradientUnits", "userSpaceOnUse");
    grad.setAttribute("x1", "2"); grad.setAttribute("y1", "2");
    grad.setAttribute("x2", "34"); grad.setAttribute("y2", "34");
    var g1 = document.createElementNS(SVG_NS, "stop");
    g1.setAttribute("offset", "0"); g1.setAttribute("stop-color", "var(--accent)");
    var g2 = document.createElementNS(SVG_NS, "stop");
    g2.setAttribute("offset", "1"); g2.setAttribute("stop-color", "var(--accent-h, var(--accent))");
    grad.appendChild(g1); grad.appendChild(g2);
    defs.appendChild(grad);
    svg.appendChild(defs);

    var track = document.createElementNS(SVG_NS, "circle");
    track.setAttribute("class", "usage-ring-track");
    track.setAttribute("cx", "18"); track.setAttribute("cy", "18");
    track.setAttribute("r", String(RADIUS));
    svg.appendChild(track);

    var arc = document.createElementNS(SVG_NS, "circle");
    arc.setAttribute("class", "usage-ring-arc");
    arc.setAttribute("cx", "18"); arc.setAttribute("cy", "18");
    arc.setAttribute("r", String(RADIUS));
    arc.setAttribute("transform", "rotate(-90 18 18)");
    svg.appendChild(arc);

    ringEl.insertBefore(svg, ringEl.firstChild);
    svgArc = arc;
  }

  // setArc درصد را روی مسیر SVG می‌نویسد — transition در CSS نرم می‌کند.
  function setArc(pct) {
    if (!svgArc) return;
    var off = CIRC * (1 - Math.max(0, Math.min(100, pct)) / 100);
    svgArc.style.strokeDasharray = CIRC.toFixed(2);
    svgArc.style.strokeDashoffset = off.toFixed(2);
  }

  // render برچسب/کسر، درصد SVG و رنگ هشدار را تازه می‌کند. منبع عدد، متن
  // #usage-pct است (app.js می‌نویسد) تا هر دو نمای دایره همیشه یکی باشند.
  function render() {
    if (!widget) return;
    var s = snapshot();
    var pct = pctFromDom();
    if (pct === null) pct = s.pct;
    setArc(pct);
    widget.classList.add("usage-pill");
    widget.classList.toggle("usage-warn", pct >= 60 && pct < 85);
    widget.classList.toggle("usage-danger", pct >= 85);
    // رنگ کمان SVG هم باید با همین عدد عوض شود — کلاس‌ها را روی خود ring
    // هم می‌نویسیم (app.js همان منطق را دارد؛ هم‌ارزش و بدون تضاد است).
    if (ringEl) {
      ringEl.classList.toggle("warn", pct >= 60 && pct < 85);
      ringEl.classList.toggle("danger", pct >= 85);
    }
    if (popOpen) fillPopover(true);
  }

  function build() {
    widget = $("#usage-widget");
    if (!widget || widget.dataset.usagePill === "1") return !!widget;
    widget.dataset.usagePill = "1";
    widget.classList.add("usage-pill");
    ringEl = $("#usage-ring");
    buildRing();
    if (!widget.getAttribute("role")) {
      widget.setAttribute("role", "button");
      widget.setAttribute("tabindex", "0");
    }
    widget.setAttribute("aria-label", "مصرف توکن — برای جزئیات کلیک کنید");

    // فقط دایره و درصد — هیچ متن/برچسب اضافه‌ای روی نوار چت نمی‌نشیند.
    widget.addEventListener("click", function (ev) {
      ev.stopPropagation();
      togglePopover();
    });
    widget.addEventListener("keydown", function (ev) {
      if (ev.key === "Enter" || ev.key === " ") {
        ev.preventDefault();
        togglePopover();
      }
    });
    // title بومی app.js حذف می‌شود (پاپ‌اور جای آن است)
    if (window.MutationObserver) {
      new MutationObserver(function () {
        if (widget.getAttribute("title")) widget.removeAttribute("title");
      }).observe(widget, { attributes: true, attributeFilter: ["title", "hidden"] });
    }
    var pc = $("#usage-pct");
    if (pc && window.MutationObserver) {
      new MutationObserver(render).observe(pc, { childList: true, characterData: true, subtree: true });
    }
    return true;
  }

  // ------------------------------------------------ پاپ‌اور توضیحات

  function row(host, label, value, note) {
    var box = document.createElement("div");
    box.className = "usage-row";
    var l = document.createElement("div");
    l.className = "usage-row-label";
    l.textContent = label;
    var v = document.createElement("div");
    v.className = "usage-row-value";
    v.textContent = value;
    box.appendChild(l);
    box.appendChild(v);
    if (note) {
      var n = document.createElement("div");
      n.className = "usage-row-note";
      n.textContent = note;
      box.appendChild(n);
    }
    host.appendChild(box);
    return box;
  }

  function monitorText(s) {
    if (!s.autoUsageReset) {
      return "پایش خودکار شارژ خاموش است.";
    }
    var lines = [];
    var bal = s.balance;
    if (bal && num(bal.updatedAt) > 0) {
      var t = clockText(num(bal.updatedAt));
      lines.push("موجودی: " + faMoney(bal.total, bal.currency) + (t ? " — آخرین بررسی " + t : ""));
    } else {
      lines.push("هنوز موجودی این سرویس‌دهنده بررسی نشده است.");
    }
    var lastAt = num(s.lastAutoResetAt);
    if (lastAt > 0) {
      var reason = s.lastAutoResetReason === "recovered_after_quota"
        ? "بازیابی پس از خطای سهمیه" : "افزایش موجودی";
      lines.push("آخرین صفر شدن خودکار: " + sinceText(lastAt) + " (" + reason + ")");
    }
    return lines.join("\n");
  }

  function fillPopover(keepScroll) {
    if (!pop) return;
    var s = snapshot();
    var body = pop.querySelector(".usage-pop-body");
    if (!body) return;
    body.textContent = "";
    row(body, "کانتکست این گفتگو",
      compact(s.ctx) + " از " + compact(s.limit) + "  (" + fa(s.pct) + "٪)",
      "حجم متن این گفتگو؛ با گفتگوی تازه از نو شمرده می‌شود.");
    row(body, "مصرف تجمعی " + (s.provName || "سرویس‌دهنده"),
      compact(s.provider) + " توکن",
      "ذخیره‌شده روی دیسک؛ با تشخیص شارژ سرویس خودکار صفر می‌شود.");
    if (s.model) {
      row(body, "مصرف مدل فعال (" + s.model + ")", compact(s.modelTokens) + " توکن", "");
    }
    // ---------- وضعیت پایش خودکار شارژ ----------
    var mon = document.createElement("div");
    mon.className = "usage-row usage-monitor";
    var mt = document.createElement("div");
    mt.className = "usage-row-label";
    mt.textContent = "پایش شارژ خودکار";
    mon.appendChild(mt);
    var mv = document.createElement("div");
    mv.className = "usage-monitor-status";
    mv.textContent = monitorText(s);
    mon.appendChild(mv);
    body.appendChild(mon);

    var since = sinceText(s.since);
    if (since) {
      var meta = document.createElement("div");
      meta.className = "usage-pop-meta";
      meta.textContent = "شمارنده از " + since + " (آخرین تشخیص شارژ)";
      body.appendChild(meta);
    }
    if (s.streaming) {
      var live = document.createElement("div");
      live.className = "usage-pop-meta";
      live.textContent = "در حال پاسخ‌دهی؛ عدد کانتکست پس از پایان نوبت دقیق می‌شود.";
      body.appendChild(live);
    }
    if (!keepScroll) body.scrollTop = 0;
  }

  function buildPopover() {
    pop = document.createElement("div");
    pop.className = "usage-pop";
    pop.setAttribute("dir", "rtl");
    pop.hidden = true;

    var head = document.createElement("div");
    head.className = "usage-pop-head";
    var title = document.createElement("span");
    title.className = "usage-pop-title";
    title.textContent = "مصرف توکن";
    var close = document.createElement("button");
    close.type = "button";
    close.className = "usage-pop-close";
    close.setAttribute("aria-label", "بستن");
    close.textContent = "×";
    close.addEventListener("click", function () { togglePopover(false); });
    head.appendChild(title);
    head.appendChild(close);

    var body = document.createElement("div");
    body.className = "usage-pop-body";

    var foot = document.createElement("div");
    foot.className = "usage-pop-foot";
    var note = document.createElement("span");
    note.className = "usage-pop-footnote";
    note.textContent = "صفر شدن شمارنده خودکار است؛ این دکمه فقط موجودی را دوباره می‌خواند.";
    var probe = document.createElement("button");
    probe.type = "button";
    probe.className = "btn btn-ghost btn-sm usage-probe";
    probe.textContent = "بررسی شارژ سرویس‌دهنده";
    probe.addEventListener("click", function (ev) {
      ev.stopPropagation();
      runProbe(probe);
    });
    foot.appendChild(note);
    foot.appendChild(probe);

    pop.appendChild(head);
    pop.appendChild(body);
    pop.appendChild(foot);
    document.body.appendChild(pop);

    document.addEventListener("click", function (ev) {
      if (!popOpen) return;
      if (pop.contains(ev.target) || (widget && widget.contains(ev.target))) return;
      togglePopover(false);
    });
    document.addEventListener("keydown", function (ev) {
      if (popOpen && ev.key === "Escape") togglePopover(false);
    });
  }

  // runProbe «بررسی شارژ»: موجودی Provider فعال را همین حالا می‌خواند.
  // این فراخوانی هرگز مستقیم شمارنده را صفر نمی‌کند؛ اگر با این خواندن شارژ
  // تشخیص داده شود، بک‌اند رویداد usage:auto-reset می‌فرستد و همان‌جا
  // صفر شدن اعلام می‌شود. btn اختیاری است (API KodYarUsageRing.probe بدون پاپ‌اور).
  function runProbe(btn) {
    if (probeBusy) return;
    var be = window.go && window.go.main && window.go.main.App;
    if (!be || typeof be.ProbeProviderBalance !== "function") {
      toast("در حالت پیش‌نمایش مرورگر، بررسی موجودی ممکن نیست", "info");
      return;
    }
    probeBusy = true;
    var oldText = btn ? btn.textContent : "";
    if (btn) btn.textContent = "در حال بررسی…";
    Promise.resolve(be.ProbeProviderBalance()).then(function (res) {
      probeBusy = false;
      if (btn) btn.textContent = oldText;
      res = res || {};
      if (res.error) {
        toast("بررسی موجودی ناموفق بود: " + res.error, "err");
      } else if (res.supported && res.ok) {
        toast("موجودی سرویس‌دهنده: " + faMoney(res.total, res.currency), "ok");
      } else {
        toast("این سرویس‌دهنده گزارش موجودی ندارد؛ شارژ از روی رفتار درخواست‌ها تشخیص داده می‌شود.", "info");
      }
      fillPopover(true);
      render();
    }).catch(function (err) {
      probeBusy = false;
      if (btn) btn.textContent = oldText;
      toast("بررسی موجودی ناموفق بود: " + (err && err.message ? err.message : err), "err");
    });
  }

  function position() {
    if (!pop || !widget) return;
    var r = widget.getBoundingClientRect();
    pop.style.top = Math.round(r.bottom + 8) + "px";
    // RTL: تراز راست با لبهٔ راست ویجت، ولی داخل صفحه بمان
    var width = pop.offsetWidth || 320;
    var right = Math.max(8, Math.min(window.innerWidth - width - 8, r.right - width));
    pop.style.left = Math.round(right) + "px";
  }

  function togglePopover(force) {
    if (!pop) buildPopover();
    var next = typeof force === "boolean" ? force : !popOpen;
    popOpen = next;
    if (!next) {
      pop.hidden = true;
      if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
      widget.setAttribute("aria-expanded", "false");
      return;
    }
    fillPopover(false);
    pop.hidden = false;
    position();
    widget.setAttribute("aria-expanded", "true");
    if (pollTimer) clearInterval(pollTimer);
    pollTimer = setInterval(function () { if (popOpen) fillPopover(true); }, 2500);
  }

  // ------------------------------------------------ کمکی/اتصال

  function toast(msg, kind) {
    if (typeof window.toast === "function") window.toast(msg, kind || "info");
  }

  // onAutoReset تشخیص شارژ از سمت بک‌اند: شمارنده‌ها آن‌جا صفر شده‌اند؛
  // این‌جا فقط خبر می‌دهیم و UI را تازه می‌کنیم.
  function onAutoReset(data) {
    data = data || {};
    var name = data.providerName || "سرویس‌دهنده";
    var reason = data.reason === "recovered_after_quota"
      ? "بازیابی پس از خطای سهمیه" : "افزایش موجودی";
    toast(name + " شارژ شد (" + reason + ")؛ شمارندهٔ مصرف خودکار صفر شد.", "ok");
    if (typeof window.refreshSettings === "function") {
      try { window.refreshSettings(); } catch (e) { /* اختیاری */ }
    }
    render();
    if (popOpen) fillPopover(true);
  }

  function connect() {
    if (!build()) return;
    render();
    var rt = window.runtime;
    if (rt && typeof rt.EventsOn === "function") {
      ["chat:usage", "chat:done", "chat:start", "chat:error", "chat:notice"].forEach(function (name) {
        try { rt.EventsOn(name, render); } catch (e) { /* بدون runtime */ }
      });
      try { rt.EventsOn("usage:auto-reset", onAutoReset); } catch (e) { /* بدون runtime */ }
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", connect);
  } else {
    connect();
  }

  // API برای تست/دیباگ
  window.KodYarUsageRing = {
    snapshot: snapshot,
    render: render,
    open: function () { togglePopover(true); },
    close: function () { togglePopover(false); },
    probe: function () { runProbe(null); }
  };
})();
