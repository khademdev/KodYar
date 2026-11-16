!function(){"use strict";const{$:e,$$:t,esc:n}=window.MyAIUtils,o=()=>window.MyAIState,s=()=>window.go.main.App;function i(e,t){const n=t.getBoundingClientRect();e.style.visibility="hidden",e.classList.add("open");const o=e.offsetWidth,s=e.offsetHeight;let i=n.bottom+8,d=n.right-o;d<8&&(d=8),d+o>window.innerWidth-8&&(d=window.innerWidth-8-o),i+s>window.innerHeight-8&&(i=Math.max(8,n.top-s-8)),e.style.top=i+"px",e.style.left=d+"px",e.style.visibility="visible"}function d(){t(".popover").forEach(e=>e.classList.remove("open")),t(".pill-select").forEach(e=>e.classList.remove("open"))}function c(){const t=o().view,i=e("#model-popover");if(!t)return;let c="";(t.providers||[]).length?(t.providers||[]).forEach(e=>{c+=`<div class="group-label"><span class="prov-dot" style="background:${e.color||"#7c6cf0"}">${n(((e.displayName||e.name||"?")[0].toUpperCase()))}</span><span>${n(e.displayName||e.name)}</span></div>`,(e.models||[]).forEach(o=>{const s=e.id===t.provider&&o===t.model;c+=`<button class="opt${s?" selected":""}" data-prov="${e.id}" data-model="${n(o)}"><span>${n(o)}</span><span class="check">${Icons.get("check",15)}</span></button>`})}):c=`<div class="pop-empty">${Icons.get("globe",26)}<div class="t1">سرویس‌دهنده‌ای تعریف نشده</div><div class="t2">Provider خود را با Base URL و کلید API اضافه کنید؛ هر سرویس سازگار با API نوع OpenAI کار می‌کند.</div><button class="btn btn-primary btn-sm" id="pop-add-prov">${Icons.get("plus",14)}<span>افزودن Provider</span></button></div>`,c+=`<button class="foot-link" id="open-prov-settings">${Icons.get("gear",15)}<span>مدیریت Providerها…</span></button>`,i.innerHTML=c;const a=e("#pop-add-prov",i);a&&a.addEventListener("click",()=>{d(),window.MyAIModals.openSettings("providers")}),i.querySelectorAll(".opt").forEach(e=>{e.addEventListener("click",()=>{const n=e.dataset.prov,o=e.dataset.model;s().SaveConnection(n,o,t.environment||"","").then(()=>{const e=(t.providers||[]).find(e=>e.id===n);t.provider=n,t.model=o,e&&e.contextWindow&&(t.tokenLimit=e.contextWindow),window.MyAIState.view=t,window.renderAll(),d(),window.toast("مدل روی «"+o+"» تنظیم شد")}).catch(e=>window.toast(String(e.message||e),"err"))})});const r=e("#open-prov-settings",i);r&&r.addEventListener("click",()=>{d(),window.MyAIModals.openSettings("providers")})}window.MyAIPopovers={bind:function(){const r=e("#model-popover");e("#mini-model").addEventListener("click",t=>{t.stopPropagation();const n=!r.classList.contains("open");d(),n&&(c(),i(r,e("#mini-model")))})},closePopovers:d}}();
/* MyAI — جستجوی مدل در انتخاب‌گر مدلِ چت‌باکس (#mini-model → #model-popover)
 * جعبه‌ی جستجوی چسبان بالای فهرست مدل‌ها؛ از ۳ مدل به بالا نمایش داده می‌شود.
 * تایپ = فیلتر زنده‌ی نام مدل (گروه‌های بی‌نتیجه پنهان می‌شوند)،
 * Enter = اولین نتیجه، Esc = بستن پاپ‌آور.
 * فقط display آپشن‌ها عوض می‌شود (DOM بازسازی نمی‌شود) — سبک برای سیستم‌های ضعیف. */
;(function () {
  "use strict";
  var pop = document.getElementById("model-popover");
  if (!pop || !window.MyAIPopovers) return;
  var MIN_MODELS = 3;
  var bar = null;
  var note = null;

  function filterModelList(q) {
    q = (q || "").trim().toLowerCase();
    var labels = pop.querySelectorAll(".group-label");
    var anyHit = false;
    for (var i = 0; i < labels.length; i++) {
      var lab = labels[i];
      var any = false;
      var el = lab.nextElementSibling;
      while (el && !el.classList.contains("group-label") && !el.classList.contains("foot-link")) {
        if (el.classList.contains("opt")) {
          var model = (el.getAttribute("data-model") || "").toLowerCase();
          var hit = !q || model.indexOf(q) !== -1 || el.textContent.toLowerCase().indexOf(q) !== -1;
          el.style.display = hit ? "" : "none";
          if (hit) any = true;
        }
        el = el.nextElementSibling;
      }
      lab.style.display = any ? "" : "none";
      if (any) anyHit = true;
    }
    if (note) note.hidden = !(q && labels.length && !anyHit);
  }

  function build() {
    bar = document.createElement("div");
    bar.className = "pop-search";
    var inp = document.createElement("input");
    inp.id = "model-search";
    inp.type = "search";
    inp.placeholder = "جستجوی مدل…";
    inp.setAttribute("autocomplete", "off");
    inp.setAttribute("spellcheck", "false");
    inp.setAttribute("aria-label", "جستجوی مدل");
    inp.addEventListener("input", function () {
      filterModelList(inp.value);
    });
    inp.addEventListener("keydown", function (ev) {
      if (ev.key === "Escape") {
        ev.stopPropagation();
        window.MyAIPopovers.closePopovers();
        return;
      }
      if (ev.key === "Enter") {
        ev.preventDefault();
        var opts = pop.querySelectorAll(".opt");
        for (var i = 0; i < opts.length; i++) {
          if (opts[i].style.display !== "none") {
            opts[i].click();
            break;
          }
        }
      }
    });
    bar.appendChild(inp);
    note = document.createElement("div");
    note.className = "pop-noresult";
    note.textContent = "مدلی با این نام پیدا نشد";
    note.hidden = true;
  }

  function removeBar() {
    if (bar && bar.parentNode) bar.remove();
    if (note && note.parentNode) note.remove();
    bar = null;
    note = null;
  }

  function ensure() {
    if (!pop.classList.contains("open")) return;
    if (pop.querySelectorAll(".opt").length < MIN_MODELS) {
      removeBar();
      return;
    }
    if (!bar) build();
    if (pop.firstChild !== bar) pop.insertBefore(bar, pop.firstChild);
    var foot = pop.querySelector(".foot-link");
    if (note.parentNode !== pop) {
      if (foot) pop.insertBefore(note, foot);
      else pop.appendChild(note);
    }
    var input = bar.querySelector("input");
    input.value = "";
    filterModelList("");
    /* فوکوس با یک تلاش دوم — بعضی مسیرها (مثل فوکوس خودکار چت‌باکس) آن را می‌دزدند. */
    function grab() {
      if (!pop.classList.contains("open") || document.activeElement === input) return;
      try {
        input.focus();
      } catch (e) {
        /* noop */
      }
    }
    setTimeout(grab, 20);
    setTimeout(grab, 140);
  }

  /* پاپ‌آور مدل را همان هندلر کلیکِ #mini-model باز می‌کند؛ بعد از آن درج می‌کنیم. */
  document.addEventListener(
    "click",
    function (ev) {
      var t = ev.target;
      if (t && t.closest && t.closest("#mini-model")) setTimeout(ensure, 30);
    },
    true
  );
})();
