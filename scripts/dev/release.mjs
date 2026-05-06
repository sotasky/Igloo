#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { appendFileSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

export function bumpSemver(version, bump) {
  const match = /^(\d+)\.(\d+)\.(\d+)$/.exec(version);
  if (!match) throw new Error(`invalid semver: ${version}`);

  const major = Number.parseInt(match[1], 10);
  const minor = Number.parseInt(match[2], 10);
  const patch = Number.parseInt(match[3], 10);

  switch (bump) {
    case "patch":
      return `${major}.${minor}.${patch + 1}`;
    case "minor":
      return `${major}.${minor + 1}.0`;
    default:
      throw new Error(`unsupported bump: ${bump}`);
  }
}

export function planAutomaticRelease(commits, threshold = 10) {
  const commitCount = commits.length;
  const shouldRelease = commitCount >= threshold;
  const hasMinorMarker = commits.some((commit) => {
    const message = `${commit.subject || ""}\n${commit.body || ""}`;
    return /^release:\s*minor\s*$/im.test(message);
  });

  return {
    shouldRelease,
    bump: hasMinorMarker ? "minor" : "patch",
    commitCount,
  };
}

export function updatePackageJsonText(text, version) {
  const pkg = JSON.parse(text);
  pkg.version = version;
  return `${JSON.stringify(pkg, null, 2)}\n`;
}

export function updatePackageLockText(text, version) {
  const lock = JSON.parse(text);
  lock.version = version;
  if (lock.packages && lock.packages[""]) {
    lock.packages[""].version = version;
  }
  return `${JSON.stringify(lock, null, 2)}\n`;
}

export function updateAndroidBuildGradle(text, versionName, versionCode) {
  let updated = text.replace(
    /versionCode\s*=\s*\d+/,
    `versionCode = ${versionCode}`,
  );
  updated = updated.replace(
    /versionName\s*=\s*"[^"]+"/,
    `versionName = "${versionName}"`,
  );

  if (updated === text) {
    throw new Error("android version fields were not updated");
  }
  if (!updated.includes(`versionCode = ${versionCode}`)) {
    throw new Error("android versionCode was not updated");
  }
  if (!updated.includes(`versionName = "${versionName}"`)) {
    throw new Error("android versionName was not updated");
  }
  return updated;
}

function commitRef(sha, repository) {
  const short = sha.slice(0, 7);
  if (!repository) return `\`${short}\``;
  return `[${short}](https://github.com/${repository}/commit/${sha})`;
}

export function renderReleaseNotes({ newTag, repository, commits }) {
  const lines = [
    `## Release ${newTag}`,
    "",
    "changes:",
    "",
  ];

  for (const commit of commits) {
    lines.push(`- ${commit.subject} (${commitRef(commit.sha, repository)})`);
  }

  lines.push("");
  return lines.join("\n");
}

function git(args, opts = {}) {
  return execFileSync("git", args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", opts.stderr || "pipe"],
  }).trim();
}

function gitMaybe(args) {
  try {
    return git(args);
  } catch (_) {
    return "";
  }
}

function parseAndroidVersion(text) {
  const codeMatch = /versionCode\s*=\s*(\d+)/.exec(text);
  const nameMatch = /versionName\s*=\s*"([^"]+)"/.exec(text);
  if (!codeMatch || !nameMatch) {
    throw new Error("could not read android version fields");
  }
  return {
    versionCode: Number.parseInt(codeMatch[1], 10),
    versionName: nameMatch[1],
  };
}

function commitsSince(previousTag) {
  const range = previousTag ? [`${previousTag}..HEAD`] : [];
  const raw = git([
    "log",
    "--reverse",
    "--format=%x1e%H%x00%s%x00%b",
    ...range,
  ]);
  if (!raw) return [];
  return raw.split("\x1e").filter(Boolean).map((record) => {
    const clean = record.replace(/^\n+/, "").replace(/\n+$/, "");
    const [sha, subject, body = ""] = clean.split("\0");
    return { sha, subject, body };
  });
}

function parseArgs(argv) {
  const out = {
    command: argv[0],
    bump: argv[1],
    notesPath: "release-notes.md",
    threshold: 10,
  };

  const optionStart = out.command === "prepare" ? 2 : 1;
  for (let i = optionStart; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "--notes") {
      out.notesPath = argv[++i];
      continue;
    }
    if (arg === "--threshold") {
      out.threshold = Number.parseInt(argv[++i], 10);
      if (!Number.isFinite(out.threshold) || out.threshold < 1) {
        throw new Error("threshold must be a positive integer");
      }
      continue;
    }
    throw new Error(`unknown argument: ${arg}`);
  }
  return out;
}

function writeGithubOutput(values) {
  if (!process.env.GITHUB_OUTPUT) return;
  const lines = [];
  for (const [key, value] of Object.entries(values)) {
    lines.push(`${key}=${value}`);
  }
  appendFileSync(process.env.GITHUB_OUTPUT, `${lines.join("\n")}\n`);
}

function prepareRelease(argv) {
  const args = parseArgs(argv);
  if (args.command !== "prepare" && args.command !== "prepare-auto") {
    throw new Error(
      "usage: release.mjs prepare <patch|minor> [--notes path] | prepare-auto [--threshold n] [--notes path]",
    );
  }

  const packagePath = resolve("package.json");
  const packageLockPath = resolve("package-lock.json");
  const androidPath = resolve("android/app/build.gradle.kts");

  const packageText = readFileSync(packagePath, "utf8");
  const packageLockText = readFileSync(packageLockPath, "utf8");
  const androidText = readFileSync(androidPath, "utf8");

  const currentVersion = JSON.parse(packageText).version;
  const androidVersion = parseAndroidVersion(androidText);
  if (androidVersion.versionName !== currentVersion) {
    throw new Error(
      `package.json version ${currentVersion} does not match Android version ${androidVersion.versionName}`,
    );
  }

  const previousTag = gitMaybe([
    "describe",
    "--tags",
    "--abbrev=0",
    "--match",
    "v[0-9]*",
  ]);
  const commits = commitsSince(previousTag);
  if (commits.length === 0) {
    throw new Error("no commits found since previous release tag");
  }

  let bump = args.bump;
  let shouldRelease = true;
  if (args.command === "prepare-auto") {
    const plan = planAutomaticRelease(commits, args.threshold);
    bump = plan.bump;
    shouldRelease = plan.shouldRelease;
    if (!shouldRelease) {
      const outputs = {
        should_release: "false",
        commit_count: String(plan.commitCount),
        bump,
        previous_tag: previousTag,
      };
      writeGithubOutput(outputs);
      for (const [key, value] of Object.entries(outputs)) {
        console.log(`${key}=${value}`);
      }
      return;
    }
  }

  const nextVersion = bumpSemver(currentVersion, bump);
  const nextTag = `v${nextVersion}`;
  if (gitMaybe(["rev-parse", "-q", "--verify", `refs/tags/${nextTag}`])) {
    throw new Error(`tag already exists: ${nextTag}`);
  }

  writeFileSync(packagePath, updatePackageJsonText(packageText, nextVersion));
  writeFileSync(
    packageLockPath,
    updatePackageLockText(packageLockText, nextVersion),
  );
  writeFileSync(
    androidPath,
    updateAndroidBuildGradle(
      androidText,
      nextVersion,
      androidVersion.versionCode + 1,
    ),
  );
  writeFileSync(
    resolve(args.notesPath),
    renderReleaseNotes({
      newTag: nextTag,
      previousTag,
      repository: process.env.GITHUB_REPOSITORY || "",
      commits,
    }),
  );

  const outputs = {
    should_release: String(shouldRelease),
    version: nextVersion,
    tag: nextTag,
    previous_tag: previousTag,
    commit_count: String(commits.length),
    bump,
  };
  writeGithubOutput(outputs);
  for (const [key, value] of Object.entries(outputs)) {
    console.log(`${key}=${value}`);
  }
}

const invokedPath = process.argv[1] ? pathToFileURL(process.argv[1]).href : "";
if (import.meta.url === invokedPath) {
  try {
    prepareRelease(process.argv.slice(2));
  } catch (err) {
    console.error(err instanceof Error ? err.message : String(err));
    process.exit(1);
  }
}
