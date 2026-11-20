/*
 * sound.js — صدای پایان کار ایجنت.
 *
 * وقتی اجرای ایجنت تمام می‌شود (نه وقتی کاربر خودش «توقف» را زده)، یک زنگ
 * کوتاه دو نُتی پخش می‌شود تا کاربر که مشغول کار دیگری است بفهمد کار تمام شده.
 *
 * چرا Web Audio و نه فایل صوتی؟
 *  - هیچ asset صوتی اضافه نمی‌شود (بستهٔ برنامه سبک می‌ماند) و در WebView2 هم
 *    کار می‌کند؛ فقط چند سازندهٔ oscillator لازم است.
 *  - ولوم پایین و پاک است؛ روی بلندگوهای کوچک هم آزاردهنده نیست.
 *
 * سیاست‌های مهم:
 *  - فقط در حالت ایجنت صدا می‌دهد؛ چت معمولی ساکت است.
 *  - پاسخ متوقف‌شده (aborted) صدا ندارد — کار «تمام» نشده.
 *  - مرورگر/WebView تا اولین کلیک یا کلید کاربر اجازهٔ پخش نمی‌دهد؛ برای همین
 *    AudioContext در نخستین تعامل کاربر resume می‌شود و تا آن لحظه ساکت می‌ماند.
 */
(function () {
  "use strict";

  var ctx = null;

  function audioCtx() {
    if (ctx) return ctx;
    var C = window.AudioContext || window.webkitAudioContext;
    if (!C) return null;
    try {
      ctx = new C();
    } catch (e) {
      return null;
    }
    return ctx;
  }

  function unlock() {
    var c = audioCtx();
    if (c && c.state === "suspended" && typeof c.resume === "function") {
      c.resume().catch(function () {});
    }
  }

  // یک نُت با پوش‌دادن نرم (بدون کلیک/پاپ) — سینوس + هارمونیک ملایم.
  function note(c, freq, at, dur, peak) {
    var osc = c.createOscillator();
    var gain = c.createGain();
    osc.type = "sine";
    osc.frequency.setValueAtTime(freq, at);
    gain.gain.setValueAtTime(0.0001, at);
    gain.gain.exponentialRampToValueAtTime(peak, at + 0.02);
    gain.gain.exponentialRampToValueAtTime(0.0001, at + dur);
    osc.connect(gain);
    gain.connect(c.destination);
    osc.start(at);
    osc.stop(at + dur + 0.03);
    osc.onended = function () {
      try {
        gain.disconnect();
        osc.disconnect();
      } catch (e) {
        /* already gone */
      }
    };
  }

  // زنگ «کار تمام شد»: دو نُت صعودی کوتاه (A5 → D6).
  function play() {
    var c = audioCtx();
    if (!c) return;
    unlock();
    if (c.state !== "running") return; // تا اولین تعامل کاربر ساکتیم
    var t = c.currentTime + 0.01;
    note(c, 880.0, t, 0.16, 0.05);
    note(c, 1174.66, t + 0.13, 0.28, 0.045);
  }

  function onDone(ev) {
    if (!ev || ev.aborted) return;
    var view = window.KodYarState && window.KodYarState.view;
    if (!view || !view.agentMode) return;
    play();
  }

  function playForTest() {
    var c = audioCtx();
    if (!c) return false;
    if (c.state !== "running") return false;
    play();
    return true;
  }

  document.addEventListener("pointerdown", unlock, true);
  document.addEventListener("keydown", unlock, true);

  if (window.runtime && typeof window.runtime.EventsOn === "function") {
    window.runtime.EventsOn("chat:done", onDone);
  }

  window.KodYarSound = { play: playForTest, unlock: unlock };
})();
