// A page's tab groups (cmd/nomi-docgen/tabs.go writes them; tabGroup in
// tour-client.mjs makes them tabs). An `os` group opens on the reader's
// system, a choice is kept per key and every group with the key follows it,
// a kept choice wins over the system, the strip answers the arrow keys, Home
// and End, and a page whose storage throws still switches tabs.
//
// It loads tour-client.mjs into headless Chrome (chrome.mjs) and skips
// without Chrome.
//
//   npm --prefix tour test
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { findChrome, openPage, serve } from "./chrome.mjs";

const group = (key, labels) =>
  `<div class="nomi-tabgroup" data-tabs="${key}">` +
  labels.map((l) => `<div class="nomi-tabpanel" data-tab="${l}"><p class="nomi-tablabel">${l}</p><p>${key} ${l}</p></div>`).join("") +
  "</div>";

// The page reports the platform its query names and no user agent, and with ?storage=throw
// has a localStorage whose every call throws.
const PAGE = `<!doctype html>
<html><head><meta charset="utf-8">
<script>
const q = new URLSearchParams(location.search);
Object.defineProperty(navigator, "userAgentData", { value: undefined, configurable: true });
Object.defineProperty(navigator, "platform", { value: q.get("platform") || "", configurable: true });
Object.defineProperty(navigator, "userAgent", { value: "", configurable: true });
if (q.get("storage") === "throw") {
  Object.defineProperty(window, "localStorage", { get() { throw new Error("no storage"); }, configurable: true });
}
</script>
<script type="module" src="/nomi/tour-client.mjs"></script>
</head><body>
${group("os", ["macOS", "Linux", "Windows"])}
${group("editor", ["Zed", "VS Code", "Neovim", "Helix"])}
${group("os", ["macOS", "Linux", "Windows"])}
</body></html>`;

// state answers each group's shown panel, its tabs' aria-selected and its
// panel's labelling tab, once the client has made the groups tabs.
const STATE = `(async () => {
  for (let i = 0; i < 100 && document.querySelectorAll(".nomi-tabgroup.is-ready").length < 3; i++) {
    await new Promise((r) => setTimeout(r, 50));
  }
  return [...document.querySelectorAll(".nomi-tabgroup")].map((g) => {
    const shown = [...g.querySelectorAll(".nomi-tabpanel")].filter((p) => !p.hidden);
    const tabs = [...g.querySelectorAll("[role=tab]")];
    return {
      shown: shown.map((p) => p.dataset.tab).join(","),
      selected: tabs.filter((t) => t.getAttribute("aria-selected") === "true").map((t) => t.textContent).join(","),
      labelled: shown[0] && document.getElementById(shown[0].getAttribute("aria-labelledby"))?.textContent,
      focusable: tabs.filter((t) => t.tabIndex === 0).length,
    };
  });
})()`;

const press = (key) => `document.activeElement.dispatchEvent(new KeyboardEvent("keydown", { key: "${key}", bubbles: true }))`;

async function withPage(t, profile, query, fn) {
  const chrome = findChrome();
  if (!chrome) return t.skip("no Chrome; NOMI_CHROME names its binary");
  const server = await serve(PAGE);
  let page = null;
  try {
    page = await openPage(chrome, `http://127.0.0.1:${server.address().port}/${query}`, profile);
    await fn(page);
  } finally {
    await page?.close();
    server.close();
  }
}

const shownOf = (state) => state.map((g) => g.shown);

test("an os group opens on the reader's system, and a choice is kept and followed", async (t) => {
  const profile = mkdtempSync(join(tmpdir(), "nomi-tour-tabs-"));
  try {
    for (const [platform, want] of [["Win32", "Windows"], ["Linux x86_64", "Linux"], ["MacIntel", "macOS"], ["", "macOS"]]) {
      await withPage(t, profile, `?platform=${encodeURIComponent(platform)}`, async (page) => {
        const state = await page.evaluate(STATE);
        assert.deepEqual(shownOf(state), [want, "Zed", want], `platform ${JSON.stringify(platform)}`);
        for (const g of state) {
          assert.equal(g.selected, g.shown, "the shown panel's tab is the selected one");
          assert.equal(g.labelled, g.shown, "the shown panel is labelled by its tab");
          assert.equal(g.focusable, 1, "one tab of a strip is in the tab order");
        }
        assert.equal(await page.evaluate(`localStorage.getItem("nomi-tab-os")`), null, "a detected system is not kept");
      });
    }
    await withPage(t, profile, "?platform=Win32", async (page) => {
      await page.evaluate(`document.querySelectorAll(".nomi-tabgroup")[2].querySelectorAll("[role=tab]")[1].click()`);
      assert.deepEqual(shownOf(await page.evaluate(STATE)), ["Linux", "Zed", "Linux"], "a click shows its label in every group with the key");
      await page.evaluate(`document.querySelectorAll(".nomi-tabgroup")[1].querySelector("[role=tab]").focus()`);
      await page.evaluate(press("End"));
      assert.equal(await page.evaluate(`document.activeElement.textContent`), "Helix");
      await page.evaluate(press("ArrowRight"));
      await page.evaluate(press("ArrowRight"));
      await page.evaluate(press("Home"));
      await page.evaluate(press("ArrowLeft"));
      assert.deepEqual(shownOf(await page.evaluate(STATE)), ["Linux", "Helix", "Linux"], "End, Right past the end, Home and Left wrap along the strip");
      await page.evaluate(`window.beforeReload = true; location.reload()`);
      for (let i = 0; i < 100; i++) {
        const fresh = await page.evaluate(`!window.beforeReload && document.readyState === "complete"`).catch(() => false);
        if (fresh) break;
        await new Promise((r) => setTimeout(r, 50));
      }
      assert.deepEqual(shownOf(await page.evaluate(STATE)), ["Linux", "Helix", "Linux"], "a kept choice wins over the system");
    });
  } finally {
    rmSync(profile, { recursive: true, force: true });
  }
});

test("a page whose storage throws still switches tabs", async (t) => {
  const profile = mkdtempSync(join(tmpdir(), "nomi-tour-tabs-"));
  try {
    await withPage(t, profile, "?platform=Linux&storage=throw", async (page) => {
      assert.deepEqual(shownOf(await page.evaluate(STATE)), ["Linux", "Zed", "Linux"]);
      await page.evaluate(`document.querySelector(".nomi-tabgroup [role=tab]").click()`);
      assert.deepEqual(shownOf(await page.evaluate(STATE)), ["macOS", "Zed", "macOS"]);
    });
  } finally {
    rmSync(profile, { recursive: true, force: true });
  }
});
