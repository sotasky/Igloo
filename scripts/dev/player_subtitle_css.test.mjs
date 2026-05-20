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

function cssLength(name) {
  const match = css.match(new RegExp(`${name}:\\s*(-?[0-9]+(?:\\.[0-9]+)?)(px|%);`));
  assert.ok(match, `missing ${name}`);
  return { value: Number(match[1]), unit: match[2] };
}

test("player subtitles define idle offsets and control gaps for normal and fullscreen states", () => {
  assert.deepEqual(cssLength("--player-subtitles-offset-idle"), { value: 2, unit: "%" });
  assert.equal(cssVar("--player-subtitles-offset-controls"), 72);
  assert.deepEqual(cssLength("--player-subtitles-offset-fullscreen-idle"), { value: 2, unit: "%" });
  assert.equal(cssVar("--player-subtitles-offset-fullscreen-controls"), 104);
  assert.equal(cssVar("--player-subtitles-controls-gap"), 6);
  assert.equal(cssVar("--player-subtitles-controls-gap-fullscreen"), 12);
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

test("control-visible subtitles use the app-owned controller state and measured bar", () => {
  assert.match(playerJs, /function controlsSubtitleOffsetPx\(isFs\)/);
  assert.match(playerJs, /querySelector\('media-control-bar\.dashboard-media-control-bar, media-control-bar, \.dashboard-media-control-bar'\)/);
  assert.match(playerJs, /playerWrapper\.getBoundingClientRect\(\)/);
  assert.match(playerJs, /bar\.getBoundingClientRect\(\)/);
  assert.match(playerJs, /wrapperRect\.bottom - barRect\.top \+ gap/);
  assert.match(playerJs, /--player-subtitles-controls-gap-fullscreen/);
});

test("idle subtitles can use percentage offsets from the player edge", () => {
  assert.match(playerJs, /function readSubtitleOffset\(name, fallback\)/);
  assert.match(playerJs, /raw\.endsWith\('%'\)/);
  assert.match(playerJs, /rect\.height \* value \/ 100/);
  assert.match(playerJs, /readSubtitleOffset\('--player-subtitles-offset-fullscreen-idle', 52\)/);
  assert.match(playerJs, /readSubtitleOffset\('--player-subtitles-offset-idle', 36\)/);
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
  assert.match(playerJs, /function sanitizeVttCueText\(/);
  assert.match(playerJs, /function decodeVttEntities\(/);
  assert.match(playerJs, /String\(raw \|\| ''\)\.trim\(\)\.split\(\/\\s\+\/\)\[0\]/);
  assert.match(playerJs, /&nbsp;\|\&#160;\|\&#x0\*a0;/);
  assert.match(playerJs, /fetch\(subtitleTrackUrl,\s*\{ credentials: 'same-origin' \}\)/);
  assert.match(playerJs, /video\.addEventListener\('timeupdate', renderSubtitleOverlay/);
  assert.match(playerJs, /player-subtitle-cue/);
  assert.match(playerJs, /--player-subtitles-offset-fullscreen-controls/);
  assert.match(playerJs, /overlay\.style\.bottom = subtitleOffsetPx\(\) \+ 'px'/);
  assert.doesNotMatch(playerJs, /cue\.line =/);
  assert.doesNotMatch(playerJs, /textTracks && video\.textTracks\[0\]/);
});
