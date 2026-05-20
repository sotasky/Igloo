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
    case "major":
      return `${major + 1}.0.0`;
    default:
      throw new Error(`unsupported bump: ${bump}`);
  }
}

export function normalizeReleaseBump(value) {
  const bump = String(value || "patch").trim().toLowerCase();
  if (bump !== "patch" && bump !== "minor" && bump !== "major") {
    throw new Error(`unsupported bump: ${value}`);
  }
  return bump;
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

export function renderReleaseNotes({ newTag, repository, commits, description = "" }) {
  const summary = String(description || "").trim() || `Release ${newTag}`;
  const lines = [
    summary,
    "",
    "Changelog",
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

export function parseAndroidVersion(text) {
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
    description: "",
  };

  const optionStart = out.command === "prepare" ? 2 : 1;
  for (let i = optionStart; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "--notes") {
      out.notesPath = argv[++i];
      continue;
    }
    if (arg === "--description") {
      out.description = argv[++i];
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
  if (args.command !== "prepare") {
    throw new Error(
      "usage: release.mjs prepare <patch|minor|major> [--notes path] [--description text]",
    );
  }

  const androidPath = resolve("android/app/build.gradle.kts");

  const androidText = readFileSync(androidPath, "utf8");
  const description = String(args.description || "").trim();

  const androidVersion = parseAndroidVersion(androidText);
  const currentVersion = androidVersion.versionName;

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

  const bump = normalizeReleaseBump(args.bump);

  const nextVersion = bumpSemver(currentVersion, bump);
  const nextTag = `v${nextVersion}`;
  if (gitMaybe(["rev-parse", "-q", "--verify", `refs/tags/${nextTag}`])) {
    throw new Error(`tag already exists: ${nextTag}`);
  }

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
      description,
    }),
  );

  const outputs = {
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
