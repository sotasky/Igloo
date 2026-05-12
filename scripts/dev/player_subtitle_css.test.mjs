import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const css = readFileSync(new URL("../../static/style.css", import.meta.url), "utf8");
const playerJs = readFileSync(new URL("../../static/js/src/player/index.js", import.meta.url), "utf8");

function cssVar(name) {
  const match = css.match(new RegExp(`${name}:\\s*(-?[0-9]+)px;`));
  assert.ok(match, `missing ${name}`);
  return Number(match[1]);
}

test("player subtitles use separate offsets for normal and fullscreen states", () => {
  assert.equal(cssVar("--player-subtitles-offset-idle"), 36);
  assert.equal(cssVar("--player-subtitles-offset-controls"), 72);
  assert.equal(cssVar("--player-subtitles-offset-fullscreen-idle"), 52);
  assert.equal(cssVar("--player-subtitles-offset-fullscreen-controls"), 104);
  assert.match(css, /--player-subtitles-font-size:\s*clamp\(22px,\s*1\.6vw,\s*34px\);/);
  assert.match(css, /--player-subtitles-font-size-fullscreen:\s*clamp\(28px,\s*2vw,\s*52px\);/);
});

test("player subtitle overlay is defined for shared browser rendering", () => {
  assert.match(
    css,
    /\.player-subtitle-overlay\s*\{[\s\S]*?position:\s*absolute;[\s\S]*?width:\s*min\(92%,\s*980px\);/,
  );
  assert.match(
    css,
    /\.player-subtitle-cue\s*\{[\s\S]*?white-space:\s*pre-wrap;[\s\S]*?box-decoration-break:\s*clone;/,
  );
  assert.match(
    css,
    /\.player-layout:fullscreen \.player-wrapper \.player-subtitle-cue[\s\S]*?--player-subtitles-font-size-fullscreen/,
  );
});

test("control-visible subtitles use the app-owned controller state", () => {
  assert.ok(cssVar("--player-subtitles-offset-controls") > cssVar("--player-subtitles-offset-idle"));
  assert.ok(cssVar("--player-subtitles-offset-fullscreen-controls") > cssVar("--player-subtitles-offset-fullscreen-idle"));
});

test("player controls hide from app-owned visibility state, not lingering focus", () => {
  assert.match(
    css,
    /#main-media-controller\[data-player-controls-ready\]:not\(\[data-player-controls-visible="1"\]\) media-control-bar\.dashboard-media-control-bar[\s\S]*?opacity:\s*0;/,
  );
  assert.doesNotMatch(css, /#main-media-controller\[userinactive\]:not\(:focus-within\)/);
  assert.doesNotMatch(css, /\.player-wrapper:not\(:hover\):not\(:focus-within\) #main-media-controller\[mediapaused\]/);
});

test("player JavaScript renders subtitles in app-owned overlay", () => {
  assert.match(playerJs, /function subtitleOffsetPx\(\)/);
  assert.match(playerJs, /function setupPlayerControlsVisibility\(\)/);
  assert.match(playerJs, /data-player-controls-visible/);
  assert.match(playerJs, /function ensureSubtitleOverlay\(\)/);
  assert.match(playerJs, /function renderSubtitleOverlay\(\)/);
  assert.match(playerJs, /function parseVtt\(/);
  assert.match(playerJs, /fetch\(subtitleTrackUrl,\s*\{ credentials: 'same-origin' \}\)/);
  assert.match(playerJs, /video\.addEventListener\('timeupdate', renderSubtitleOverlay/);
  assert.match(playerJs, /player-subtitle-cue/);
  assert.match(playerJs, /--player-subtitles-offset-fullscreen-controls/);
  assert.match(playerJs, /overlay\.style\.bottom = subtitleOffsetPx\(\) \+ 'px'/);
  assert.doesNotMatch(playerJs, /cue\.line =/);
  assert.doesNotMatch(playerJs, /textTracks && video\.textTracks\[0\]/);
});
