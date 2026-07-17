import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const css = readFileSync(new URL("../../static/style.css", import.meta.url), "utf8");
const baseTemplate = readFileSync(new URL("../../internal/components/base.templ", import.meta.url), "utf8");
const sidebarTemplate = readFileSync(new URL("../../internal/components/sidebar.templ", import.meta.url), "utf8");

test("desktop sidebar collapse becomes a compact rail", () => {
  assert.match(css, /--sidebar-panel-width:\s*clamp\(220px,\s*20vw,\s*320px\);/);
  assert.match(css, /--sidebar-compact-width:\s*72px;/);
  assert.match(css, /--sidebar-width:\s*var\(--sidebar-panel-width\);/);
  assert.match(css, /\.sidebar\s*\{[\s\S]*?width:\s*var\(--sidebar-panel-width\);/);
  assert.match(
    css,
    /html\.sidebar-collapsed\s*\{[\s\S]*?--sidebar-width:\s*var\(--sidebar-compact-width\);[\s\S]*?html\.sidebar-collapsed \.sidebar\s*\{[\s\S]*?width:\s*var\(--sidebar-compact-width\);/,
  );
  assert.doesNotMatch(css, /html\.sidebar-collapsed \.sidebar\s*\{[^}]*translateX\(-100%\)/);
});

test("compact mode has its own page and utility controls", () => {
  assert.match(sidebarTemplate, /class="sidebar-panel-expand-icon"/);
  assert.match(sidebarTemplate, /data-sidebar-compact-add/);
  assert.match(sidebarTemplate, /data-sidebar-compact-download/);
  assert.match(sidebarTemplate, /data-sidebar-compact-logs/);
  assert.match(css, /html\.sidebar-collapsed \.sidebar-compact-actions\s*\{[\s\S]*?display:\s*flex;/);
  assert.doesNotMatch(baseTemplate, /sidebar-expand-icon/);
});

test("the fully hidden mode remains the small-screen drawer", () => {
  assert.match(css, /@media screen and \(max-width:\s*768px\)[\s\S]*?\.sidebar\s*\{[\s\S]*?transform:\s*translateX\(-100%\);/);
  assert.match(css, /\.sidebar-open \.sidebar\s*\{[\s\S]*?transform:\s*translateX\(0\);/);
});
