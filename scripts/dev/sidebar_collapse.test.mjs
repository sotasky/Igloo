import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const css = readFileSync(new URL("../../static/style.css", import.meta.url), "utf8");
const baseTemplate = readFileSync(new URL("../../internal/components/base.templ", import.meta.url), "utf8");
const sidebarTemplate = readFileSync(new URL("../../internal/components/sidebar.templ", import.meta.url), "utf8");
const modalsTemplate = readFileSync(new URL("../../internal/components/modals.templ", import.meta.url), "utf8");
const siteBase = readFileSync(new URL("../../static/js/site_base.js", import.meta.url), "utf8");

test("desktop sidebar can be resized down to the compact rail", () => {
  assert.match(css, /--sidebar-panel-width:\s*clamp\(220px,\s*20vw,\s*320px\);/);
  assert.match(css, /--sidebar-compact-width:\s*72px;/);
  assert.match(css, /--sidebar-width:\s*var\(--sidebar-panel-width\);/);
  assert.match(css, /\.sidebar\s*\{[\s\S]*?width:\s*var\(--sidebar-panel-width\);/);
  assert.match(sidebarTemplate, /id="sidebar-resize-handle"/);
  assert.match(sidebarTemplate, /role="separator"/);
  assert.match(css, /\.sidebar-resize-handle[\s\S]*?cursor:\s*col-resize;/);
  assert.match(
    css,
    /html\.sidebar-collapsed\s*\{[\s\S]*?--sidebar-width:\s*var\(--sidebar-compact-width\);[\s\S]*?html\.sidebar-collapsed \.sidebar\s*\{[\s\S]*?width:\s*var\(--sidebar-compact-width\);/,
  );
  assert.doesNotMatch(css, /html\.sidebar-collapsed \.sidebar\s*\{[^}]*translateX\(-100%\)/);
});

test("dragging owns compact snapping and persisted custom widths", () => {
  assert.match(baseTemplate, /igloo\.sidebar\.width\.v1/);
  assert.doesNotMatch(baseTemplate, /igloo\.sidebar\.collapsed\.v1/);
  assert.match(siteBase, /SIDEBAR_COMPACT_WIDTH\s*=\s*72/);
  assert.match(siteBase, /SIDEBAR_FULL_MIN_WIDTH\s*=\s*200/);
  assert.match(siteBase, /SIDEBAR_MAX_WIDTH\s*=\s*420/);
  assert.match(siteBase, /pointerdown/);
  assert.match(siteBase, /pointermove/);
  assert.match(siteBase, /pointerup/);
  assert.match(siteBase, /setPointerCapture/);
  assert.match(siteBase, /igloo\.sidebar\.width\.v1/);
  assert.match(siteBase, /igloo\.sidebar\.full-width\.v1/);
  assert.match(siteBase, /fullSidebarWidth/);
  assert.doesNotMatch(sidebarTemplate, /id="sidebar-collapse"/);
  assert.doesNotMatch(sidebarTemplate, /sidebar-panel-(?:expand|collapse)-icon/);
});

test("Z toggles compact mode without discarding the saved full width", () => {
  assert.match(siteBase, /'global\.sidebar':\s*'z'/);
  assert.match(siteBase, /cfShortcuts\.match\('global\.sidebar', event\.key\)/);
  assert.match(siteBase, /currentSidebarWidth === SIDEBAR_COMPACT_WIDTH \? fullSidebarWidth : SIDEBAR_COMPACT_WIDTH/);
  assert.match(siteBase, /setSidebarWidth\([\s\S]*?true,[\s\S]*?false/);
  assert.match(modalsTemplate, /data-sc="global\.sidebar"/);
});

test("automatic cinema sidebar modes remain user-overridable", () => {
  assert.match(siteBase, /defaultSidebarMode[\s\S]*?hasStoredSidebarWidth\(\)/);
  assert.match(siteBase, /stored !== null && Number\.isFinite\(Number\(stored\)\)/);
  assert.match(siteBase, /setSidebarWidth\(SIDEBAR_COMPACT_WIDTH, false, false\)/);
  assert.match(css, /html\.sidebar-hidden[\s\S]*?--sidebar-width:\s*0px;/);
  assert.match(css, /html\.sidebar-hidden \.sidebar-toggle[\s\S]*?display:\s*flex;/);
  assert.match(siteBase, /sidebar-hidden[\s\S]*?setSidebarHidden\(false\)/);
});

test("compact mode keeps its page and utility controls", () => {
  assert.match(sidebarTemplate, /data-sidebar-compact-add/);
  assert.match(sidebarTemplate, /data-sidebar-compact-download/);
  assert.match(sidebarTemplate, /data-sidebar-compact-logs/);
  assert.match(css, /html\.sidebar-collapsed \.sidebar-compact-actions\s*\{[\s\S]*?display:\s*flex;/);
  assert.match(css, /html\.sidebar-collapsed \.sidebar-header \.logo > span\s*\{[\s\S]*?display:\s*none;/);
  assert.doesNotMatch(css, /html\.sidebar-collapsed \.sidebar-header \.logo\s*\{[^}]*display:\s*none;/);
});

test("compact download opens its own popup without expanding the sidebar", () => {
  assert.match(sidebarTemplate, /id="quick-download-modal"/);
  assert.match(sidebarTemplate, /@sidebarQuickDownloadForm\(p, "compact-quick-dl-input", "compact-quick-dl-status"\)/);
  assert.match(siteBase, /sidebarCompactDownload[\s\S]*?openModal\(quickDownloadModal\)/);
  assert.doesNotMatch(siteBase, /sidebarCompactDownload[\s\S]{0,180}?setSidebarWidth\(SIDEBAR_FULL_MIN_WIDTH/);
});

test("modal popups retain the document's scrollbar gutter", () => {
  assert.match(css, /html\s*\{[\s\S]*?scrollbar-gutter:\s*stable;/);
});

test("the fully hidden mode remains the small-screen drawer", () => {
  assert.match(css, /@media screen and \(max-width:\s*768px\)[\s\S]*?\.sidebar\s*\{[\s\S]*?transform:\s*translateX\(-100%\);/);
  assert.match(css, /\.sidebar-open \.sidebar\s*\{[\s\S]*?transform:\s*translateX\(0\);/);
  assert.match(css, /@media screen and \(max-width:\s*768px\)[\s\S]*?\.sidebar-resize-handle\s*\{[\s\S]*?display:\s*none;/);
  assert.match(siteBase, /#app-sidebar a\[href\]/);
});

test("the hamburger shares the floating header's top alignment", () => {
  assert.match(css, /\.floating-header\s*\{[\s\S]*?top:\s*0\.1rem;/);
  assert.match(css, /\.sidebar-toggle\s*\{[\s\S]*?top:\s*0\.1rem;/);
});
