// KodYar — تست دودِ مرورگری (Playwright) — بند D.2.2 مأموریت ۳
//
// هدف: هر مودال/کارتِ کل باید واقعاً «قابل‌مشاهده» و «قابل‌کلیک» شود و
// دکمه‌هایش به بریج (Mock) سیم‌کشی باشند — نگهبانِ همان کلاس باگِ
// «مودالِ نامرئی» نسخه‌ی 1.7.1. اجرا فقط برای توسعه است و هیچ وابستگی‌ای
// به ساخت برنامه ندارد؛ تست قراردادِ معادلِ همین چک‌ها در Go همیشه اجرا
// می‌شود (uicontract_test.go). دستور اجرا: frontend/tests/README.md
"use strict";

const { test, expect } = require("@playwright/test");

const MOCK_KEY = "kodyar-mock-v1";

// تنظیم حالت ایجنت با Provider کامل، پیش از بارگذاری صفحه (mock از
// localStorage می‌خواند؛ بدون Provider واقعی SendMessage رد می‌شود).
async function primeMockState(page, { agentMode = true } = {}) {
  await page.goto("/");
  await page.evaluate((key) => localStorage.removeItem(key), MOCK_KEY);
  await page.reload();
  await page.evaluate(({ key, agentMode }) => {
    const state = {
      defaultsRemoved: true,
      settings: {
        provider: "pw", model: "m1", environment: "general",
        workspace: "C:\\Projects\\MyWorkspace",
        allowCommands: true, agentMode: agentMode,
        confirmFileWrites: true, editMode: agentMode ? "review_each" : "auto",
        tokensUsed: 0, tokenLimit: 200000, recentUsage: [], hasKeys: { pw: true },
        temperature: 0.7, theme: "dark", fontSize: 14, density: "normal",
      },
      providers: [{ id: "pw", name: "PW", baseUrl: "http://localhost:9/v1", models: ["m1"], color: "#7c6cf0" }],
      conversations: [],
    };
    localStorage.setItem(key, JSON.stringify(state));
  }, { key: MOCK_KEY, agentMode });
  await page.reload();
}

// شمارنده‌ی فراخوانی‌های یک متد بریج — بدون بازنویسی رفتار mock.
async function spyBridge(page, method) {
  return page.evaluate((m) => {
    const app = window.go.main.App;
    app["__calls_" + m] = 0;
    const orig = app[m];
    app[m] = async function (...args) {
      app["__calls_" + m]++;
      return orig.apply(this, args);
    };
    return null;
  }, method);
}
async function bridgeCalls(page, method) {
  return page.evaluate((m) => window.go.main.App["__calls_" + m] || 0, method);
}

test("diff modal: agent:confirm opens #diff-overlay, it is visible and accept wires to the mock bridge", async ({ page }) => {
  await primeMockState(page, { agentMode: false });
  await spyBridge(page, "RespondFileConfirm");
  await page.evaluate(() => {
    window.runtime.EventsEmit("agent:confirm", { change: {
      id: "chg_test", kind: "modify", path: "src/agent.go",
      added: 2, removed: 1, at: Date.now() / 1e3,
      diff: "--- a/src/agent.go\n+++ b/src/agent.go\n@@ -1,2 +1,3 @@\n-old\n+new\n context",
    } });
  });
  const overlay = page.locator("#diff-overlay.open");
  await expect(overlay).toBeVisible();                    // واقعاً نمایش داده می‌شود
  await expect(overlay.locator("#diff-file-path")).toHaveText("src/agent.go");
  await expect(overlay.locator("#diff-accept")).toBeEnabled();
  await overlay.locator("#diff-accept").click();          // قابل‌کلیک + سیم‌کشی‌شده
  await expect(overlay).not.toBeVisible();
  expect(await bridgeCalls(page, "RespondFileConfirm")).toBe(1);
});

test("permission modal: agent:permission_ask becomes visible, allow responds through the mock bridge", async ({ page }) => {
  await primeMockState(page, { agentMode: false });
  await spyBridge(page, "RespondPermissionConfirm");
  await page.evaluate(() => {
    window.runtime.EventsEmit("agent:permission_ask", { ask: {
      id: "perm_test", kind: "command", command: "go test ./...",
      pattern: "go test*", noAlways: false,
      message: "اجازه‌ی اجرای این دستور در پوشه‌ی کاری؟", at: 1,
    } });
  });
  const overlay = page.locator(".modal-overlay.perm-ask.open");
  await expect(overlay).toBeVisible();
  await expect(overlay.locator(".modal-title")).toHaveText("تأیید دسترسی");
  const allow = overlay.getByRole("button", { name: "اجازه یک‌بار" });
  await expect(allow).toBeVisible();
  await allow.click();
  await expect(overlay).not.toBeVisible();
  expect(await bridgeCalls(page, "RespondPermissionConfirm")).toBe(1);
});

test("turn-summary card: after an agent turn it is visible with files, protection chip and the turn restore button", async ({ page }) => {
  await primeMockState(page, { agentMode: true });
  await page.fill("#msg-input", "یک تغییر آزمایشی اعمال کن");
  await page.click("#btn-send");
  // مودال Diff در جریان موک (agentMode + confirmFileWrites) باز می‌شود؛
  // تأییدش جریان را کامل می‌کند و در پایان کارت خلاصه‌ی نوبت می‌آید.
  const diff = page.locator("#diff-overlay.open");
  await expect(diff).toBeVisible({ timeout: 15000 });
  await diff.locator("#diff-accept").click();
  await expect(diff).not.toBeVisible();
  const card = page.locator(".ts-card");
  await expect(card).toBeVisible({ timeout: 25000 });
  await expect(card).toContainText("خلاصه‌ی واقعی این نوبت");
  await expect(card).toContainText("src/agent.go");
  await expect(card).toContainText("package.json");        // فایل محافظت‌شده در فهرست
  await expect(card).toContainText("go build ./...");      // راستی‌آزمایی ثبت‌شده
  await expect(card.getByRole("button", { name: "بازگردانی این نوبت" })).toBeVisible();

  // مسیر بازگردانی: پیش‌نمایش (PreviewCheckpointRestore) → تأیید →
  // بازگردانی (RestoreCheckpoint)، هر دو روی بریج Mock.
  await spyBridge(page, "PreviewCheckpointRestore");
  await spyBridge(page, "RestoreCheckpoint");
  await card.getByRole("button", { name: "بازگردانی این نوبت" }).click();
  const restore = page.locator(".modal-overlay .modal-restore");
  await expect(restore).toBeVisible();
  await expect(restore).toContainText("بازگردانی نوبت ۷ — پیش‌نمایش");
  await expect(restore).toContainText("src/agent.go");
  await expect(restore).toContainText("package.json");  // بازنویسی
  await expect(restore).toContainText("dist/build.out"); // انتقال به سبد زباله
  await restore.getByRole("button", { name: "بازگردانی", exact: true }).click();
  await expect(restore).toHaveCount(0);
  await expect(card).toContainText("این نوبت بازگردانده شد.");
  expect(await bridgeCalls(page, "PreviewCheckpointRestore")).toBe(1);
  expect(await bridgeCalls(page, "RestoreCheckpoint")).toBe(1);
});


test("settings: thinking-effort select is built, defaults to low and saves through SetThinkingEffort", async ({ page }) => {
  await primeMockState(page, { agentMode: true });
  await spyBridge(page, "SetThinkingEffort");
  await page.click("#btn-settings");
  await page.click('.modal-tab[data-tab="model"]');

  const sel = page.locator("#gen-effort");
  await expect(sel).toBeVisible();
  await expect(sel).toHaveValue("low");                       // پیش‌فرض کم‌مصرف
  await expect(sel.locator("option")).toHaveCount(4);         // کم/متوسط/زیاد/خودکار

  await sel.selectOption("high");
  await page.click("#gen-save");
  expect(await bridgeCalls(page, "SetThinkingEffort")).toBe(1);
  const persisted = await page.evaluate(() => {
    const s = JSON.parse(localStorage.getItem("kodyar-mock-v1") || "{}");
    return s.settings && s.settings.thinkingEffort;
  });
  expect(persisted).toBe("high");
});
