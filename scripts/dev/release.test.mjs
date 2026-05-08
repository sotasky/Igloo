import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import {
  bumpSemver,
  normalizeReleaseBump,
  planAutomaticRelease,
  renderReleaseNotes,
  updateAndroidBuildGradle,
  updatePackageJsonText,
  updatePackageLockText,
  updateReleaseBumpText,
} from "./release.mjs";

test("bumps patch and minor versions", () => {
  assert.equal(bumpSemver("1.0.0", "patch"), "1.0.1");
  assert.equal(bumpSemver("1.0.9", "minor"), "1.1.0");
});

test("rejects unsupported version bumps", () => {
  assert.throws(() => bumpSemver("1.0.0", "major"), /unsupported bump/);
  assert.throws(() => bumpSemver("1.0", "patch"), /invalid semver/);
});

test("normalizes tracked release bump state", () => {
  assert.equal(normalizeReleaseBump(" minor\n"), "minor");
  assert.equal(updateReleaseBumpText("patch"), "patch\n");
  assert.throws(() => normalizeReleaseBump("major"), /unsupported bump/);
});

test("plans automatic releases every 20 commits", () => {
  const commits = Array.from({ length: 19 }, (_, index) => ({
    sha: `${index}`.repeat(40).slice(0, 40),
    subject: `change ${index}`,
    body: "",
  }));

  assert.deepEqual(planAutomaticRelease(commits, 20), {
    shouldRelease: false,
    bump: "patch",
    commitCount: 19,
  });

  commits.push({
    sha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    subject: "change 19",
    body: "",
  });

  assert.deepEqual(planAutomaticRelease(commits, 20), {
    shouldRelease: true,
    bump: "patch",
    commitCount: 20,
  });
});

test("release workflow uses the 20 commit automatic threshold", () => {
  const workflow = readFileSync(
    new URL("../../.github/workflows/release.yml", import.meta.url),
    "utf8",
  );

  assert.match(workflow, /prepare-auto --threshold 20\b/);
  assert.doesNotMatch(workflow, /prepare-auto --threshold 10\b/);
});

test("release workflow dispatches CodeQL for release tags", () => {
  const workflow = readFileSync(
    new URL("../../.github/workflows/release.yml", import.meta.url),
    "utf8",
  );

  assert.match(
    workflow,
    /gh workflow run codeql\.yml --ref "\$\{\{ steps\.release\.outputs\.tag \}\}"/,
  );
});

test("CodeQL runs on published releases instead of a weekly schedule", () => {
  const workflow = readFileSync(
    new URL("../../.github/workflows/codeql.yml", import.meta.url),
    "utf8",
  );

  assert.match(
    workflow,
    /\n  release:\n    types:\n      - published\n/,
  );
  assert.doesNotMatch(workflow, /\n  schedule:\n/);
  assert.doesNotMatch(workflow, /cron:/);
});

test("plans automatic minor releases from explicit bump state", () => {
  const commits = Array.from({ length: 10 }, (_, index) => ({
    sha: `${index}`.repeat(40).slice(0, 40),
    subject: `change ${index}`,
    body: "",
  }));

  assert.deepEqual(planAutomaticRelease(commits, 10, "minor"), {
    shouldRelease: true,
    bump: "minor",
    commitCount: 10,
  });
});

test("does not use commit messages as release bump markers", () => {
  const commits = Array.from({ length: 10 }, (_, index) => ({
    sha: `${index}`.repeat(40).slice(0, 40),
    subject: index === 4 ? "release: minor" : `change ${index}`,
    body: index === 5 ? "release: minor" : "",
  }));

  assert.deepEqual(planAutomaticRelease(commits, 10, "patch"), {
    shouldRelease: true,
    bump: "patch",
    commitCount: 10,
  });
});

test("updates package metadata versions", () => {
  const packageJson = updatePackageJsonText(
    JSON.stringify({ name: "igloo", version: "1.0.0" }, null, 2) + "\n",
    "1.0.1",
  );
  assert.equal(JSON.parse(packageJson).version, "1.0.1");
  assert.match(packageJson, /\n$/);

  const lockJson = updatePackageLockText(
    JSON.stringify({
      name: "igloo",
      version: "1.0.0",
      packages: {
        "": { name: "igloo", version: "1.0.0" },
      },
    }),
    "1.0.1",
  );
  const parsed = JSON.parse(lockJson);
  assert.equal(parsed.version, "1.0.1");
  assert.equal(parsed.packages[""].version, "1.0.1");
});

test("updates android release version fields", () => {
  const source = `
android {
    defaultConfig {
        versionCode = 3
        versionName = "1.0.0"
    }
}
`;
  const updated = updateAndroidBuildGradle(source, "1.0.1", 4);
  assert.match(updated, /versionCode = 4/);
  assert.match(updated, /versionName = "1.0.1"/);
});

test("renders exact commit release notes", () => {
  const notes = renderReleaseNotes({
    newTag: "v1.0.1",
    previousTag: "v1.0.0",
    repository: "screwys/Igloo",
    commits: [
      {
        sha: "1111111111111111111111111111111111111111",
        subject: "fixed hover not being triggered in feed",
      },
      {
        sha: "2222222222222222222222222222222222222222",
        subject: "added a release helper",
      },
    ],
  });

  assert.match(notes, /^## Release v1\.0\.1/m);
  assert.match(notes, /^changes:$/m);
  assert.match(
    notes,
    /- fixed hover not being triggered in feed \(\[1111111\]\(https:\/\/github\.com\/screwys\/Igloo\/commit\/1111111111111111111111111111111111111111\)\)/,
  );
  assert.doesNotMatch(notes, /^## commits/m);
  assert.doesNotMatch(notes, /since `v1\.0\.0`/);
});
