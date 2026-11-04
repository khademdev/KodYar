// MyAI — تست دودِ مرورگری (Playwright) — بند D.2.2 مأموریت ۳
//
// هدف: هر مودال/کارتِ کل باید واقعاً «قابل‌مشاهده» و «قابل‌کلیک» شود و
// دکمه‌هایش به بریج (Mock) سیم‌کشی باشند — نگهبانِ همان کلاس باگِ
// «مودالِ نامرئی» نسخه‌ی 1.7.1. اجرا فقط برای توسعه است و هیچ وابستگی‌ای
// به ساخت برنامه ندارد؛ تست قراردادِ معادلِ همین چک‌ها در Go همیشه اجرا
// می‌شود (uicontract_test.go). دستور اجرا: frontend/tests/README.md
"use strict";

const { test, expect } = require("@playwright/test");

const MOCK_KEY = "myai-mock-v1";

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

// پنل «تغییرات فایل» با اولین badgeِ تغییرِ معلق خودکار باز می‌شود (رفتار
// برنامه: کشف اولیه‌ی پنل — اسکریپت inline در index.html) و چون بعد از
// diff-overlay در DOM است، روی مودال‌های بعدی سایه می‌اندازد. پس در
// شروع هر تست می‌بندیمش.
async function closeChangesPanelIfOpen(page) {
  const panel = page.locator("#changes-overlay.open");
  if (await panel.count()) {
    await panel.locator("#changes-close").click();
    await expect(panel).not.toBeVisible();
  }
}

test("diff modal: agent:confirm opens #diff-overlay, it is visible and accept wires to the mock bridge", async ({ page }) => {
  await primeMockState(page, { agentMode: false });
  await closeChangesPanelIfOpen(page);
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
  await closeChangesPanelIfOpen(page);
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

test("turn-summary card: after an agent turn it is visible with files, protection chip, verification and the revert button", async ({ page }) => {
  await primeMockState(page, { agentMode: true });
  await closeChangesPanelIfOpen(page);
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
  const revert = card.getByRole("button", { name: "برگرداندن همه‌ی تغییرات این نوبت" });
  await expect(revert).toBeVisible();
  await expect(revert).toBeEnabled();
  // و دکمه‌ی نقطه‌ی بازگشت پروژه (مرحله B) هم در کارت هست
  const cp = card.getByRole("button", { name: "بازگردانی کل پروژه به قبل از این نوبت" });
  await expect(cp).toBeVisible();
  await expect(cp).toBeEnabled();
});

test("checkpoint preview modal: opens from the turn-summary card, is visible, lists rewrite/trash, confirm wires RestoreCheckpoint", async ({ page }) => {
  await primeMockState(page, { agentMode: true });
  await closeChangesPanelIfOpen(page);
  await page.fill("#msg-input", "یک تغییر آزمایشی اعمال کن");
  await page.click("#btn-send");
  const diff = page.locator("#diff-overlay.open");
  await expect(diff).toBeVisible({ timeout: 15000 });
  await diff.locator("#diff-accept").click();
  const card = page.locator(".ts-card");
  await expect(card).toBeVisible({ timeout: 25000 });
  await spyBridge(page, "RestoreCheckpoint");
  await card.getByRole("button", { name: "بازگردانی کل پروژه به قبل از این نوبت" }).click();
  const modal = page.locator("#checkpoint-overlay.open");
  await expect(modal).toBeVisible();
  await expect(modal).toContainText("بازگردانی کل پروژه — قبل از نوبت");
  await expect(modal).toContainText("src/agent.go");       // rewrite
  await expect(modal).toContainText("dist/build.out");     // trash
  await expect(modal).toContainText("go.mod");             // unchanged
  await modal.getByRole("button", { name: "بازگردانی کن" }).click();
  await expect(modal).not.toBeVisible();
  expect(await bridgeCalls(page, "RestoreCheckpoint")).toBe(1);
});
