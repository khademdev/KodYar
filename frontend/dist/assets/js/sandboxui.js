/* MyAI — سوییچ‌های سندباکس در تنظیمات → تب پوشه.
 *
 * سندباکس اجرای دستور در بک‌اند (sandbox_windows.go) با AppContainer ویندوز
 * پیاده شده است: فرایند داخل یک کانتینر سیستمی اجرا می‌شود، فقط در پوشه‌ی
 * کاری می‌نویسد و به فایل‌های شخصی دسترسی ندارد. این فایل فقط وضعیت را در
 * UI نشان می‌دهد و تغییر را با SetSandbox ذخیره می‌کند.
 *
 * - تغییر فوری ذخیره می‌شود (مثل سوییچ «تأیید تغییرات فایل»).
 * - اگر ذخیره شکست بخورد، سوییچ به حالت قبلی برمی‌گردد.
 * - روی پلتفرم بدون پشتیبانی، سوییچ غیرفعال و علتش در tooltip گفته می‌شود.
 */
(function () {
  "use strict";

  function el(id) {
    return document.getElementById(id);
  }
  function backend() {
    return (window.go && window.go.main && window.go.main.App) || null;
  }
  function view() {
    return (window.MyAIState && window.MyAIState.view) || null;
  }
  function toast(text, kind) {
    if (typeof window.toast === "function") window.toast(text, kind);
  }
  function available() {
    var v = view();
    return !v || v.sandboxAvailable === undefined ? true : !!v.sandboxAvailable;
  }
  function enabled() {
    var v = view();
    return !v || v.sandbox === undefined ? true : !!v.sandbox;
  }
  function networkAllowed() {
    var v = view();
    return !v || v.sandboxNetwork === undefined ? true : !!v.sandboxNetwork;
  }

  // --- مجوز دستگاه NUL (sandbox_nul_windows.go) -------------------------
  // AppContainer فقط با ACE صریح برای Package SID به NUL دسترسی دارد و آن
  // ACE بدون Administrator نوشتنی نیست؛ پس راه‌حل یک‌بار تأیید UAC است که
  // یک Task سیستمی ONSTART می‌سازد. این‌جا فقط وضعیت سنجیده‌شده را نشان
  // می‌دهیم (پروب واقعی داخل AppContainer) و دکمه را وصل می‌کنیم.
  var nul = { loading: false, data: null, at: 0 };

  function refreshNul(force) {
    var be = backend();
    if (!be || typeof be.SandboxNulStatus !== "function") return;
    if (nul.loading) return;
    if (!force && Date.now() - nul.at < 15000) return;
    nul.loading = true;
    var finish = function (d) {
      nul.loading = false;
      nul.at = Date.now();
      nul.data = d || null;
      paintNul();
    };
    be.SandboxNulStatus().then(finish, function () {
      finish(null);
    });
  }

  function paintNul() {
    var row = el("sandbox-nul-note");
    if (!row) return;
    var d = nul.data;
    var show = !!(d && d.available && d.granted === false && enabled() && available());
    row.hidden = !show;
    if (!show) return;
    var txt = el("sandbox-nul-text");
    if (!txt) return;
    var text =
      "مجوز دستگاه NUL برای سندباکس اعمال نشده است؛ go/git داخل سندباکس با «Access is denied» متوقف می‌شوند.";
    if (d.lastError) text += " آخرین تلاش ناموفق: " + String(d.lastError).slice(0, 160);
    else if (d.detail) text += " (" + d.detail + ")";
    txt.textContent = text;
  }

  function sync() {
    var root = el("sandbox-row");
    if (!root) return;
    var on = enabled();
    var ok = available();
    var sb = el("sandbox-switch");
    var net = el("sandbox-net-switch");
    if (sb) {
      sb.classList.toggle("on", on && ok);
      sb.classList.toggle("disabled", !ok);
      sb.setAttribute("role", "switch");
      sb.setAttribute("aria-checked", on && ok ? "true" : "false");
      sb.setAttribute("aria-disabled", ok ? "false" : "true");
      sb.title = ok
        ? "اجرای دستورها داخل AppContainer ویندوز: نوشتن فقط در پوشه‌ی کاری، بدون دسترسی به فایل‌های شخصی و شبکه (اختیاری)؛ دستورهای git به‌خاطر ناسازگاری MSYS خارج از کانتینر اجرا می‌شوند"
        : "سندباکس فقط در ویندوز در دسترس است";
      var row = el("sandbox-row");
      if (row) row.title = sb.title;
    }
    var note = el("sandbox-git-note");
    if (note) {
      // استثنای git فقط وقتی معنا دارد که سندباکس روشن و در دسترس باشد.
      note.hidden = !(on && ok);
    }
    if (on && ok) refreshNul(false);
    else nul.at = 0;
    paintNul();
    if (net) {
      var netOn = on && ok && networkAllowed();
      net.classList.toggle("on", netOn);
      if (!on || !ok) net.classList.add("disabled");
      else net.classList.remove("disabled");
      net.setAttribute("role", "switch");
      net.setAttribute("aria-checked", netOn ? "true" : "false");
      net.setAttribute("aria-disabled", !on || !ok ? "true" : "false");
      net.title = !ok
        ? "سندباکس فقط در ویندوز در دسترس است"
        : !on
        ? "ابتدا سندباکس را روشن کنید"
        : networkAllowed()
        ? "دستور داخل سندباکس به شبکه دسترسی دارد (برای npm/go/pip). کلیک: قطع شبکه"
        : "شبکه در سندباکس قطع است؛ نصب بسته‌ها و دانلود ممکن نیست. کلیک: اجازه‌ی شبکه";
    }
  }

  function ensureView() {
    if (view()) return true;
    if (backend() && backend().SettingsView) {
      // اگر تنظیمات هنوز بارگذاری نشده، هیچ کاری نکن؛ renderAll بعداً sync می‌کند.
      return false;
    }
    return false;
  }

  function save(nextEnabled, nextNetwork) {
    var be = backend();
    var v = view();
    if (!be || !v) return;
    var prev = { on: enabled(), net: networkAllowed() };
    v.sandbox = nextEnabled;
    v.sandboxNetwork = nextNetwork;
    sync();
    be.SetSandbox(nextEnabled, nextNetwork)
      .then(function () {
        toast(
          nextEnabled
            ? "سندباکس روشن شد — دستورها داخل محیط ایزوله اجرا می‌شوند" +
              (nextNetwork ? " (با شبکه)" : " (بدون شبکه)")
            : "سندباکس خاموش شد — دستورها با دسترسی کامل کاربر اجرا می‌شوند",
          nextEnabled ? "ok" : "info"
        );
        sync();
      })
      .catch(function (err) {
        v.sandbox = prev.on;
        v.sandboxNetwork = prev.net;
        sync();
        toast(String(err && err.message ? err.message : err), "err");
      });
  }

  function bind() {
    var sb = el("sandbox-switch");
    if (sb && !sb.dataset.bound) {
      sb.dataset.bound = "1";
      sb.addEventListener("click", function () {
        if (!available()) {
          toast("سندباکس فقط در ویندوز در دسترس است", "info");
          return;
        }
        save(!enabled(), networkAllowed());
      });
    }
    var net = el("sandbox-net-switch");
    if (net && !net.dataset.bound) {
      net.dataset.bound = "1";
      net.addEventListener("click", function () {
        if (!available()) {
          toast("سندباکس فقط در ویندوز در دسترس است", "info");
          return;
        }
        if (!enabled()) {
          toast("ابتدا سندباکس را روشن کنید", "info");
          return;
        }
        save(true, !networkAllowed());
      });
    }
    var grant = el("sandbox-nul-enable");
    if (grant && !grant.dataset.bound) {
      grant.dataset.bound = "1";
      grant.addEventListener("click", function () {
        var be = backend();
        if (!be || typeof be.SandboxNulEnable !== "function") return;
        grant.disabled = true;
        be.SandboxNulEnable()
          .then(function (msg) {
            toast(msg || "پنجرهٔ تأیید ویندوز باز شد", "info");
            var tries = 0;
            var tick = setInterval(function () {
              tries += 1;
              nul.at = 0;
              refreshNul(true);
              var done = tries >= 10 || (nul.data && nul.data.granted);
              if (!done) return;
              clearInterval(tick);
              grant.disabled = false;
              if (nul.data && nul.data.granted) {
                toast("دسترسی NUL فعال شد — go/git داخل سندباکس کار می‌کنند", "ok");
              }
            }, 2000);
          })
          .catch(function (err) {
            grant.disabled = false;
            toast(String(err && err.message ? err.message : err), "err");
          });
      });
    }
    sync();
  }

  // هم‌گام‌سازی با تنظیمات بارگذاری‌شده: هر بار رندر کامل یا باز شدن مودال.
  function wrap(name, obj) {
    if (!obj || typeof obj[name] !== "function") return;
    var orig = obj[name];
    obj[name] = function () {
      var r = orig.apply(this, arguments);
      try {
        sync();
      } catch (e) {
        /* noop */
      }
      return r;
    };
  }

  document.addEventListener("DOMContentLoaded", function () {
    bind();
    wrap("renderAll", window);
    wrap("openSettings", window.MyAIModals);
    setTimeout(sync, 300);
  });
})();
