!function(){"use strict";const{$:e,$$:t,esc:n}=window.MyAIUtils,o=()=>window.MyAIState,s=()=>window.go.main.App;function i(e,t){const n=t.getBoundingClientRect();e.style.visibility="hidden",e.classList.add("open");const o=e.offsetWidth,s=e.offsetHeight;let i=n.bottom+8,d=n.right-o;d<8&&(d=8),d+o>window.innerWidth-8&&(d=window.innerWidth-8-o),i+s>window.innerHeight-8&&(i=Math.max(8,n.top-s-8)),e.style.top=i+"px",e.style.left=d+"px",e.style.visibility="visible"}function d(){t(".popover").forEach(e=>e.classList.remove("open")),t(".pill-select").forEach(e=>e.classList.remove("open"))}function c(){const t=o().view,i=e("#model-popover");if(!t)return;let c="";(t.providers||[]).length?(t.providers||[]).forEach(e=>{c+=`<div class="group-label"><span class="prov-dot" style="background:${e.color||"#7c6cf0"}">${n((e.name||"?")[0].toUpperCase())}</span><span>${n(e.name)}</span></div>`,(e.models||[]).forEach(o=>{const s=e.id===t.provider&&o===t.model;c+=`<button class="opt${s?" selected":""}" data-prov="${e.id}" data-model="${n(o)}"><span>${n(o)}</span><span class="check">${Icons.get("check",15)}</span></button>`})}):c=`<div class="pop-empty">${Icons.get("globe",26)}<div class="t1">سرویس‌دهنده‌ای تعریف نشده</div><div class="t2">Provider خود را با Base URL و کلید API اضافه کنید؛ هر سرویس سازگار با API نوع OpenAI کار می‌کند.</div><button class="btn btn-primary btn-sm" id="pop-add-prov">${Icons.get("plus",14)}<span>افزودن Provider</span></button></div>`,c+=`<button class="foot-link" id="open-prov-settings">${Icons.get("gear",15)}<span>مدیریت Providerها…</span></button>`,i.innerHTML=c;const a=e("#pop-add-prov",i);a&&a.addEventListener("click",()=>{d(),window.MyAIModals.openSettings("providers")}),i.querySelectorAll(".opt").forEach(e=>{e.addEventListener("click",()=>{const n=e.dataset.prov,o=e.dataset.model;s().SaveConnection(n,o,t.environment||"","").then(()=>{const e=(t.providers||[]).find(e=>e.id===n);t.provider=n,t.model=o,e&&e.contextWindow&&(t.tokenLimit=e.contextWindow),window.MyAIState.view=t,window.renderAll(),d(),window.toast("مدل روی «"+o+"» تنظیم شد")}).catch(e=>window.toast(String(e.message||e),"err"))})});const r=e("#open-prov-settings",i);r&&r.addEventListener("click",()=>{d(),window.MyAIModals.openSettings("providers")})}window.MyAIPopovers={bind:function(){const t=e("#model-select"),a=e("#env-select"),r=e("#model-popover"),p=e("#env-popover");t.addEventListener("click",e=>{e.stopPropagation();const n=!r.classList.contains("open");d(),n&&(c(),i(r,t),t.classList.add("open"))}),a.addEventListener("click",t=>{t.stopPropagation();const c=!p.classList.contains("open");d(),c&&(function(){const t=o().view,i=e("#env-popover");if(!t)return;const c=o().envPresets||[];i.innerHTML=c.map(e=>`<button class="opt${e.id===t.environment?" selected":""}" data-env="${e.id}"><div class="env-title">${n(e.title)}</div><div class="env-desc">${n(e.desc||"")}</div><span class="check">${Icons.get("check",15)}</span></button>`).join(""),i.querySelectorAll(".opt").forEach(e=>{e.addEventListener("click",()=>{const n=e.dataset.env;s().SaveConnection(t.provider,t.model,n,"").then(()=>{t.environment=n,window.MyAIState.view=t,window.renderAll(),d();const e=(o().envPresets.find(e=>e.id===n)||{}).title||n;window.toast("محیط روی «"+e+"» تنظیم شد")}).catch(e=>window.toast(String(e.message||e),"err"))})})}(),i(p,a),a.classList.add("open"))}),e("#mini-model").addEventListener("click",t=>{t.stopPropagation();const n=!r.classList.contains("open");d(),n&&(c(),i(r,e("#mini-model")))})},closePopovers:d}}();
/* MyAI 1.10.0 — Mission 6 (issue 3) — additive hook; minified body above untouched.
 * جستجوی مدل: وقتی مدل‌های لیست‌شده ≥ ۸ تا باشند، یک جعبه‌ی جستجوی چسبان
 * بالای پاپ‌آور مدل‌ها اضافه می‌شود. Enter = انتخاب اولین نتیجه، Esc = بستن.
 * فقط display آپشن‌ها عوض می‌شود (بدون بازسازی DOM) — سبک برای سیستم‌های ضعیف. */
;(function () {
  "use strict";
  var pop = document.getElementById("model-popover");
  if (!pop || !window.MyAIPopovers) return;
  function filterModelList(q) {
    q = (q || "").trim().toLowerCase();
    var labels = pop.querySelectorAll(".group-label");
    for (var i = 0; i < labels.length; i++) {
      var lab = labels[i], any = false, el = lab.nextElementSibling;
      while (el && !el.classList.contains("group-label") && !el.classList.contains("foot-link")) {
        if (el.classList.contains("opt")) {
          var hit = !q || (el.getAttribute("data-model") || "").toLowerCase().indexOf(q) !== -1 || el.textContent.toLowerCase().indexOf(q) !== -1;
          el.style.display = hit ? "" : "none";
          if (hit) any = true;
        }
        el = el.nextElementSibling;
      }
      lab.style.display = any ? "" : "none";
    }
    var empty = pop.querySelector(".pop-empty");
    if (empty) empty.style.display = q ? "none" : "";
  }
  var observer = new MutationObserver(function () {
    if (!pop.classList.contains("open")) return;
    var opts = pop.querySelectorAll(".opt");
    var old = pop.querySelector(".pop-search");
    if (opts.length < 8) { if (old) old.remove(); return; }
    var bar = old;
    if (!bar) {
      bar = document.createElement("div");
      bar.className = "pop-search";
      var inp = document.createElement("input");
      inp.id = "model-search";
      inp.type = "text";
      inp.placeholder = "جستجوی مدل…";
      inp.setAttribute("autocomplete", "off");
      inp.setAttribute("spellcheck", "false");
      inp.addEventListener("input", function () { filterModelList(inp.value); });
      inp.addEventListener("keydown", function (ev) {
        if (ev.key === "Escape") { ev.stopPropagation(); window.MyAIPopovers.closePopovers(); }
        else if (ev.key === "Enter") {
          var first = null;
          pop.querySelectorAll(".opt").forEach(function (o) { if (!first && o.style.display !== "none") first = o; });
          if (first) { ev.preventDefault(); first.click(); }
        }
      });
      bar.appendChild(inp);
    }
    pop.insertBefore(bar, pop.firstChild);
    var input = bar.querySelector("input");
    input.value = "";
    filterModelList("");
    setTimeout(function () { try { input.focus(); } catch (e) { /* noop */ } }, 30);
  });
  observer.observe(pop, { attributes: true, attributeFilter: ["class"] });
})();
