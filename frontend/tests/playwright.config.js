// Playwright config — only for the optional browser smoke test.
// The app build never runs this directory.
"use strict";
module.exports = {
  testDir: __dirname,
  timeout: 45000,
  use: {
    baseURL: "http://localhost:5179",
    viewport: { width: 1280, height: 900 },
  },
  webServer: {
    command: "node serve.js 5179",
    port: 5179,
    cwd: __dirname,
    reuseExistingServer: true,
  },
};
