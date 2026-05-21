import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const escapeRegExp = (value) => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

test("runtime downloader tool versions come from shared requirements", () => {
  const requirements = readFileSync(
    new URL("../../requirements-runtime.txt", import.meta.url),
    "utf8",
  );
  const dockerfile = readFileSync(new URL("../../Dockerfile", import.meta.url), "utf8");
  const flake = readFileSync(new URL("../../flake.nix", import.meta.url), "utf8");
  const runtimeVersion = (packageName) => {
    const version = requirements.match(
      new RegExp(`^${escapeRegExp(packageName)}==([^\\s]+)$`, "m"),
    )?.[1];
    assert.ok(version, `${packageName} must be pinned in requirements-runtime.txt`);
    return version;
  };
  const runtimeToolMetadata = (packageName, pypiName) =>
    new RegExp(
      `"${escapeRegExp(packageName)}" = \\{\\s+pypiName = "${escapeRegExp(
        pypiName,
      )}";\\s+version = "${escapeRegExp(
        runtimeVersion(packageName),
      )}";\\s+sha256 = "[a-f0-9]{64}";`,
      "m",
    );

  assert.match(requirements, /^yt-dlp==[^\s]+$/m);
  assert.match(requirements, /^gallery-dl==[^\s]+$/m);
  assert.match(dockerfile, /COPY requirements-runtime\.txt \/tmp\/requirements-runtime\.txt/);
  assert.match(dockerfile, /pip install --no-cache-dir -r \/tmp\/requirements-runtime\.txt/);
  assert.doesNotMatch(dockerfile, /ARG YT_DLP_VERSION|ARG GALLERY_DL_VERSION/);
  assert.doesNotMatch(dockerfile, /yt-dlp==|gallery-dl==/);
  assert.match(flake, /runtimeToolVersion "yt-dlp"/);
  assert.match(flake, /runtimeToolVersion "gallery-dl"/);
  assert.match(flake, runtimeToolMetadata("yt-dlp", "yt_dlp"));
  assert.match(flake, runtimeToolMetadata("gallery-dl", "gallery_dl"));
  assert.doesNotMatch(flake, /pname = "yt-dlp";\n\s+version = "/);
  assert.doesNotMatch(flake, /pname = "gallery_dl";\n\s+version = "/);
});
