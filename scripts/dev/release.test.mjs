import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import test from "node:test";

import {
  bumpSemver,
  normalizeReleaseBump,
  parseAndroidVersion,
  repositoryFromRemote,
  renderReleaseNotes,
  updateAndroidBuildGradle,
} from "./release.mjs";

const escapeRegExp = (value) => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
const shaPinnedAction = (action, tag) =>
  new RegExp(`${escapeRegExp(action)}@[0-9a-f]{40} # ${escapeRegExp(tag)}`);
const releaseFingerprint = "05DC1C810BD2BC8D1BBD1397AAAC9B802753EA1A";

test("bumps patch, minor, and major versions", () => {
  assert.equal(bumpSemver("1.0.0", "patch"), "1.0.1");
  assert.equal(bumpSemver("1.0.9", "minor"), "1.1.0");
  assert.equal(bumpSemver("1.9.9", "major"), "2.0.0");
});

test("rejects unsupported version bumps", () => {
  assert.throws(() => bumpSemver("1.0.0", "weird"), /unsupported bump/);
  assert.throws(() => bumpSemver("1.0", "patch"), /invalid semver/);
});

test("normalizes release bump inputs", () => {
  assert.equal(normalizeReleaseBump(" minor\n"), "minor");
  assert.equal(normalizeReleaseBump(" major\n"), "major");
  assert.throws(() => normalizeReleaseBump("weird"), /unsupported bump/);
});

test("release workflow is manually dispatched", () => {
  const workflow = readFileSync(
    new URL("../../.github/workflows/release.yml", import.meta.url),
    "utf8",
  );

  assert.match(workflow, /\n  workflow_dispatch:\n/);
  assert.doesNotMatch(workflow, /\n  push:\n/);
  assert.doesNotMatch(workflow, /prepare-auto/);
  assert.doesNotMatch(workflow, /should_release/);
  assert.doesNotMatch(workflow, /threshold 30/);
});

test("release workflow leaves CodeQL to the published release trigger", () => {
  const workflow = readFileSync(
    new URL("../../.github/workflows/release.yml", import.meta.url),
    "utf8",
  );

  assert.doesNotMatch(workflow, /gh workflow run codeql\.yml/);
});

test("GitHub Actions workflow dependencies are SHA-pinned", () => {
  const workflowsDir = new URL("../../.github/workflows/", import.meta.url);
  const workflowNames = readdirSync(workflowsDir).filter((name) => name.endsWith(".yml"));

  for (const workflowName of workflowNames) {
    const workflow = readFileSync(new URL(workflowName, workflowsDir), "utf8");
    const uses = workflow.matchAll(/^\s*uses:\s+[^#\s]+@([^#\s]+)/gm);
    for (const match of uses) {
      assert.match(
        match[1],
        /^[0-9a-f]{40}$/,
        `${workflowName} has mutable action reference: ${match[0].trim()}`,
      );
    }
  }
});

test("release workflow allows manual major releases", () => {
  const workflow = readFileSync(
    new URL("../../.github/workflows/release.yml", import.meta.url),
    "utf8",
  );

  assert.match(workflow, /\n          - major\n/);
  assert.match(workflow, /description: Release summary/);
  assert.match(workflow, /INPUT_SUMMARY: \$\{\{ inputs\.summary \}\}/);
  assert.match(workflow, /\.github\/scripts\/prepare-release\.sh "\$\{INPUT_BUMP:-patch\}" release-notes\.md "\$INPUT_SUMMARY"/);
  assert.doesNotMatch(workflow, /release-description\.md/);
});

test("release workflow signs release commits and tags", () => {
  const workflow = readFileSync(
    new URL("../../.github/workflows/release.yml", import.meta.url),
    "utf8",
  );

  assert.match(workflow, /RELEASE_GPG_PRIVATE_KEY/);
  assert.match(workflow, /RELEASE_GPG_PASSPHRASE/);
  assert.doesNotMatch(workflow, /secrets\.RELEASE_GIT_USER_NAME/);
  assert.doesNotMatch(workflow, /secrets\.RELEASE_GIT_USER_EMAIL/);
  assert.match(workflow, /vars\.RELEASE_GIT_USER_NAME/);
  assert.match(workflow, /vars\.RELEASE_GIT_USER_EMAIL/);
  assert.match(workflow, /git commit -S -m "release \$\{\{ steps\.release\.outputs\.version \}\}"/);
  assert.match(workflow, /git tag -s "\$\{\{ steps\.release\.outputs\.tag \}\}"/);
  assert.match(workflow, /git add android\/app\/build\.gradle\.kts/);
  assert.doesNotMatch(workflow, /git add android\/app\/build\.gradle\.kts \.github\/release-description\.md/);
  assert.doesNotMatch(workflow, /\.github\/release-bump/);
  assert.doesNotMatch(workflow, /package\.json/);
  assert.doesNotMatch(workflow, /package-lock\.json/);
  assert.doesNotMatch(workflow, /git tag -a "\$\{\{ steps\.release\.outputs\.tag \}\}"/);
});

test("release workflow gates signed releases on the full quality suite", () => {
  const workflow = readFileSync(
    new URL("../../.github/workflows/release.yml", import.meta.url),
    "utf8",
  );

  assert.match(workflow, shaPinnedAction("actions/setup-go", "v6"));
  assert.match(workflow, shaPinnedAction("actions/setup-java", "v5"));
  assert.match(workflow, /java-version: "26"/);
  assert.match(workflow, /name: Run full release gate/);
  assert.match(workflow, /run: scripts\/dev\/test-full\.sh/);
  assert.ok(
    workflow.indexOf("run: scripts/dev/test-full.sh") <
      workflow.indexOf("git commit -S -m"),
    "full release gate should run before signing the release commit",
  );
});

test("local release script publishes a GitHub release on push", () => {
  const script = readFileSync(
    new URL("../../.github/scripts/create-release-tag.sh", import.meta.url),
    "utf8",
  );

  assert.match(script, /git push --atomic origin HEAD:main "refs\/tags\/\$tag"/);
  assert.match(script, /gh release create "\$tag"/);
  assert.match(script, /--notes-file "\$notes_file"/);
  assert.match(script, /gh workflow run container-release\.yml --ref "\$tag"/);
  assert.match(script, /gh workflow run android-release\.yml --ref "\$tag"/);
});

test("container release publishes signed provenance attestation", () => {
  const workflow = readFileSync(
    new URL("../../.github/workflows/container-release.yml", import.meta.url),
    "utf8",
  );

  assert.match(workflow, /\non:\n  workflow_dispatch:\n/);
  assert.doesNotMatch(workflow, /\n  push:\n/);
  assert.match(workflow, /\n  id-token: write\n/);
  assert.match(workflow, /\n  attestations: write\n/);
  assert.match(workflow, /\n  contents: write\n/);
  assert.match(workflow, /\n    if: startsWith\(github\.ref, 'refs\/tags\/v'\)\n/);
  assert.doesNotMatch(workflow, /\n    environment: container-release\n/);
  assert.match(workflow, /fetch-depth: 0/);
  assert.match(workflow, /name: Verify release tag/);
  assert.match(workflow, /run: \.github\/scripts\/verify-signed-release-tag\.sh/);
  assert.doesNotMatch(workflow, /BEGIN PGP SIGNATURE/);
  assert.match(workflow, /\n        id: build\n/);
  assert.match(workflow, shaPinnedAction("DeterminateSystems/determinate-nix-action", "v3"));
  assert.match(workflow, shaPinnedAction("DeterminateSystems/magic-nix-cache-action", "main"));
  assert.match(workflow, /use-flakehub: false/);
  assert.match(workflow, shaPinnedAction("cachix/cachix-action", "v17"));
  assert.match(workflow, /name: Check Cachix auth token/);
  assert.match(workflow, /\n        id: cachix\n/);
  assert.match(workflow, /CACHIX_AUTH_TOKEN: \$\{\{ secrets\.CACHIX_AUTH_TOKEN \}\}/);
  assert.match(workflow, /echo "enabled=true" >> "\$GITHUB_OUTPUT"/);
  assert.match(workflow, /if: \$\{\{ vars\.CACHIX_CACHE_NAME != '' && steps\.cachix\.outputs\.enabled == 'true' \}\}/);
  assert.match(workflow, /name: \$\{\{ vars\.CACHIX_CACHE_NAME \}\}/);
  assert.match(workflow, /authToken: \$\{\{ secrets\.CACHIX_AUTH_TOKEN \}\}/);
  assert.match(workflow, /useDaemon: true/);
  assert.match(workflow, /pushFilter: "\(-source\$\|\\\\\.tar\\\\\.gz\$\)"/);
  assert.match(workflow, /nix build \.#container --print-build-logs/);
  assert.match(workflow, /docker load < result/);
  assert.match(workflow, /SOURCE_IMAGE: ghcr\.io\/screwys\/igloo:latest/);
  assert.match(workflow, /docker tag "\$SOURCE_IMAGE" "\$tag"/);
  assert.match(workflow, /docker push "\$tag"/);
  assert.match(workflow, shaPinnedAction("sigstore/cosign-installer", "v4.1.2"));
  assert.match(workflow, /cosign-release: v3\.0\.6/);
  assert.match(workflow, /cosign sign --yes "\$IMAGE_DIGEST"/);
  assert.doesNotMatch(workflow, /COSIGN_PRIVATE_KEY/);
  assert.doesNotMatch(workflow, /COSIGN_PASSWORD/);
  assert.doesNotMatch(workflow, /Sign container image with release key/);
  assert.match(workflow, /name: Prepare security artifact directory/);
  assert.match(workflow, /run: mkdir -p release-artifacts/);
  assert.match(workflow, shaPinnedAction("anchore/sbom-action", "v0.24.0"));
  assert.match(workflow, /image: ghcr\.io\/\$\{\{ github\.repository_owner \}\}\/igloo@\$\{\{ steps\.build\.outputs\.digest \}\}/);
  assert.match(workflow, /output-file: release-artifacts\/igloo-container-\$\{\{ github\.ref_name \}\}\.spdx\.json/);
  assert.match(workflow, shaPinnedAction("anchore/scan-action", "v7.4.0"));
  assert.match(workflow, /sbom: release-artifacts\/igloo-container-\$\{\{ github\.ref_name \}\}\.spdx\.json/);
  assert.match(workflow, /severity-cutoff: critical/);
  assert.match(workflow, /only-fixed: true/);
  assert.match(workflow, /output-file: release-artifacts\/igloo-container-\$\{\{ github\.ref_name \}\}-vulnerabilities\.json/);
  assert.match(workflow, shaPinnedAction("actions/attest", "v4"));
  assert.match(workflow, /subject-name: ghcr\.io\/\$\{\{ github\.repository_owner \}\}\/igloo/);
  assert.match(workflow, /subject-digest: \$\{\{ steps\.build\.outputs\.digest \}\}/);
  assert.match(workflow, /push-to-registry: true/);
  assert.doesNotMatch(workflow, /Attach container security artifacts to GitHub Release/);
  assert.doesNotMatch(workflow, shaPinnedAction("softprops/action-gh-release", "v3"));
  assert.doesNotMatch(workflow, /files: release-artifacts\/\*/);
  assert.doesNotMatch(workflow, /go test \.\/\.\.\./);
  assert.doesNotMatch(workflow, /scripts\/dev\/test-full\.sh/);
  assert.doesNotMatch(workflow, /actions\/setup-go/);
  assert.doesNotMatch(workflow, /actions\/setup-java/);
  assert.doesNotMatch(workflow, /docker\/build-push-action/);
  assert.doesNotMatch(workflow, /go install "github\.com\/sigstore\/cosign\/v3\/cmd\/cosign/);
  assert.ok(
    workflow.indexOf("name: Verify release tag") <
      workflow.indexOf("name: Build Nix container image"),
    "container release tag verification should run before building",
  );
  assert.ok(
    workflow.indexOf("name: Verify release tag") <
      workflow.indexOf("name: Push container image"),
    "container release tag verification should run before publishing",
  );
});

test("Nix container runtime stays on cacheable nixpkgs packages", () => {
  const flake = readFileSync(new URL("../../flake.nix", import.meta.url), "utf8");

  assert.match(flake, /goBinaryArchives/);
  assert.match(flake, /https:\/\/dl\.google\.com\/go\/go\$\{goVersion\}\.linux-\$\{goArchive\.arch\}\.tar\.gz/);
  assert.match(flake, /if \(upstreamGo\.version or ""\) == goVersion then/);
  assert.match(flake, /CGO_ENABLED = upstreamGo\.CGO_ENABLED or 1;/);
  assert.match(flake, /GOOS = upstreamGo\.GOOS or "linux";/);
  assert.match(flake, /GOARCH = upstreamGo\.GOARCH or goArchive\.arch;/);
  assert.match(flake, /pkgs\.ffmpeg-headless/);
  assert.match(flake, /pkgs\.cacert/);
  assert.doesNotMatch(flake, /patchedRuntimeOverlay|runtimePkgs/);
  assert.doesNotMatch(flake, /go\$\{[^}]+\}\.src\.tar\.gz/);
  assert.doesNotMatch(flake, /ffmpeg\.org\/releases\/ffmpeg-/);
  assert.doesNotMatch(flake, /openssl\/releases\/download\/openssl-/);
  assert.doesNotMatch(flake, /openssl_3_6\.overrideAttrs|ffmpeg_8.*override/);
});

test("CI Go analysis tools are pinned and Renovate-managed", () => {
  const workflow = readFileSync(
    new URL("../../.github/workflows/ci.yml", import.meta.url),
    "utf8",
  );
  const fullGate = readFileSync(new URL("./test-full.sh", import.meta.url), "utf8");
  const versions = readFileSync(new URL("./go-tool-versions.sh", import.meta.url), "utf8");
  const renovate = readFileSync(new URL("../../renovate.json", import.meta.url), "utf8");

  assert.doesNotMatch(workflow, /@latest/);
  assert.doesNotMatch(fullGate, /@latest/);
  assert.match(fullGate, /go run \.\/scripts\/dev\/staticcheck/);
  assert.match(workflow, /\. scripts\/dev\/go-tool-versions\.sh/);
  assert.match(fullGate, /\. scripts\/dev\/go-tool-versions\.sh/);
  assert.match(workflow, /github\.com\/rhysd\/actionlint\/cmd\/actionlint@\$\{ACTIONLINT_VERSION\}/);
  assert.match(fullGate, /github\.com\/rhysd\/actionlint\/cmd\/actionlint@\$\{ACTIONLINT_VERSION\}/);
  assert.match(versions, /packageName=github\.com\/kisielk\/errcheck/);
  assert.match(versions, /ERRCHECK_VERSION=v\d+\.\d+\.\d+/);
  assert.match(versions, /packageName=honnef\.co\/go\/tools/);
  assert.match(versions, /STATICCHECK_VERSION=v\d+\.\d+\.\d+/);
  assert.match(versions, /packageName=golang\.org\/x\/vuln/);
  assert.match(versions, /GOVULNCHECK_VERSION=v\d+\.\d+\.\d+/);
  assert.match(versions, /packageName=github\.com\/rhysd\/actionlint/);
  assert.match(versions, /ACTIONLINT_VERSION=v\d+\.\d+\.\d+/);
  assert.doesNotMatch(versions, /COSIGN_VERSION/);
  assert.match(renovate, /Update pinned Go analysis tool versions/);
  assert.match(renovate, /scripts\/dev\/go-tool-versions\\\\\.sh/);
});

test("keyless container signing does not keep a static cosign public key", () => {
  assert.equal(
    existsSync(new URL("../../.github/cosign.pub", import.meta.url)),
    false,
  );
});

test("release tag verifier pins the release public key", () => {
  const publicKey = readFileSync(
    new URL("../../.github/release-gpg.pub", import.meta.url),
    "utf8",
  );
  const verifier = readFileSync(
    new URL("../../.github/scripts/verify-signed-release-tag.sh", import.meta.url),
    "utf8",
  );

  assert.match(publicKey, /^-----BEGIN PGP PUBLIC KEY BLOCK-----/);
  assert.match(publicKey, /-----END PGP PUBLIC KEY BLOCK-----\n$/);
  assert.match(verifier, new RegExp(`expected_fingerprint="${releaseFingerprint}"`));
  assert.match(verifier, /gpg --batch --import "\$public_key_path"/);
  assert.match(verifier, /gpg --batch --with-colons --fingerprint "\$expected_fingerprint"/);
  assert.match(verifier, /gpg --batch --import-ownertrust/);
  assert.match(verifier, /gpgconf --kill all/);
  assert.ok(
    verifier.includes('[[ ! "$release_ref_name" =~ ^v[0-9]+\\.[0-9]+\\.[0-9]+$ ]]'),
  );
  assert.match(verifier, /git cat-file -t "refs\/tags\/\$\{release_ref_name\}"/);
  assert.match(verifier, /git tag -v "\$release_ref_name"/);
  assert.match(verifier, /git merge-base --is-ancestor "\$tag_target" origin\/main/);
  assert.doesNotMatch(verifier, /BEGIN PGP SIGNATURE/);
});

test("container images run as non-root by default", () => {
  const dockerfile = readFileSync(new URL("../../Dockerfile", import.meta.url), "utf8");
  const flake = readFileSync(new URL("../../flake.nix", import.meta.url), "utf8");
  const compose = readFileSync(new URL("../../compose.yaml", import.meta.url), "utf8");

  assert.match(dockerfile, /^USER 10001:10001$/m);
  assert.match(dockerfile, /HOME=\/tmp/);
  assert.match(dockerfile, /chown -R 10001:10001 \/igloo/);
  assert.match(flake, /User = "10001:10001";/);
  assert.match(flake, /"HOME=\/tmp"/);
  assert.match(flake, /chown -R 10001:10001 igloo/);
  assert.match(compose, /user: "\$\{IGLOO_UID:-1000\}:\$\{IGLOO_GID:-1000\}"/);
  assert.match(compose, /read_only: true/);
  assert.match(compose, /\/tmp:size=256m,mode=1777/);
  assert.match(compose, /cap_drop:\n\s+- ALL/);
  assert.match(compose, /security_opt:\n\s+- no-new-privileges:true/);
  assert.match(compose, /mem_limit: \$\{IGLOO_MEM_LIMIT:-2g\}/);
  assert.match(compose, /cpus: "\$\{IGLOO_CPUS:-2\.0\}"/);
});

test("Android release publishes only the APK asset with signed provenance attestation", () => {
  const workflow = readFileSync(
    new URL("../../.github/workflows/android-release.yml", import.meta.url),
    "utf8",
  );

  assert.match(workflow, /\non:\n  workflow_dispatch:\n/);
  assert.doesNotMatch(workflow, /\n  push:\n/);
  assert.match(workflow, /\n    if: startsWith\(github\.ref, 'refs\/tags\/v'\)\n/);
  assert.doesNotMatch(workflow, /\n    environment: android-release\n/);
  assert.match(workflow, /fetch-depth: 0/);
  assert.match(workflow, /name: Verify release tag/);
  assert.match(workflow, /run: \.github\/scripts\/verify-signed-release-tag\.sh/);
  assert.doesNotMatch(workflow, /BEGIN PGP SIGNATURE/);
  assert.match(workflow, shaPinnedAction("actions/setup-java", "v5"));
  assert.match(workflow, /run: \.\/gradlew :app:assembleRelease/);
  assert.match(workflow, /\n  id-token: write\n/);
  assert.match(workflow, /\n  attestations: write\n/);
  assert.match(workflow, /mkdir -p release-artifacts/);
  assert.match(workflow, shaPinnedAction("actions/attest", "v4"));
  assert.match(workflow, /subject-path: release-artifacts\/\*\.apk/);
  assert.match(workflow, shaPinnedAction("softprops/action-gh-release", "v3"));
  assert.match(workflow, /files: release-artifacts\/\*\.apk/);
  assert.doesNotMatch(workflow, /^          files: release-artifacts\/\*$/m);
  assert.doesNotMatch(workflow, /Generate APK SBOM/);
  assert.doesNotMatch(workflow, /Scan APK SBOM/);
  assert.doesNotMatch(workflow, /anchore\/sbom-action/);
  assert.doesNotMatch(workflow, /anchore\/scan-action/);
  assert.doesNotMatch(workflow, /:app:testDevtestUnitTest :app:assembleRelease/);
  assert.doesNotMatch(workflow, /scripts\/dev\/test-full\.sh/);
  assert.doesNotMatch(workflow, /actions\/setup-go/);
  assert.ok(
    workflow.indexOf("name: Verify release tag") <
      workflow.indexOf("name: Decode release keystore"),
    "Android release tag verification should run before keystore access",
  );
  assert.ok(
    workflow.indexOf("name: Verify release tag") <
      workflow.indexOf("name: Build release APK"),
    "Android release tag verification should run before signing",
  );
});

test("Android Gradle wrapper pins the distribution checksum", () => {
  const properties = readFileSync(
    new URL("../../android/gradle/wrapper/gradle-wrapper.properties", import.meta.url),
    "utf8",
  );

  assert.match(properties, /distributionUrl=https\\:\/\/services\.gradle\.org\/distributions\/gradle-9\.5\.1-bin\.zip/);
  assert.match(
    properties,
    /^distributionSha256Sum=bafc141b619ad6350fd975fc903156dd5c151998cc8b058e8c1044ab5f7b031f$/m,
  );
});

test("CodeQL runs on published releases and manual dispatch only", () => {
  const workflow = readFileSync(
    new URL("../../.github/workflows/codeql.yml", import.meta.url),
    "utf8",
  );

  assert.match(
    workflow,
    /\n  release:\n    types:\n      - published\n/,
  );
  assert.match(workflow, /\n  workflow_dispatch:\n/);
  assert.doesNotMatch(workflow, /\n  push:\n/);
  assert.doesNotMatch(workflow, /\n  pull_request:\n/);
  assert.doesNotMatch(workflow, /\n  schedule:\n/);
  assert.doesNotMatch(workflow, /cron:/);
});

test("CI covers pull requests and pushes to main with static, Go, runtime, and Android jobs", () => {
  const workflow = readFileSync(
    new URL("../../.github/workflows/ci.yml", import.meta.url),
    "utf8",
  );

  assert.match(
    workflow,
    /\n  push:\n    branches:\n      - main\n    paths-ignore:\n      - "\*\*\/\*\.md"\n/,
  );
  assert.match(
    workflow,
    /\n  pull_request:\n    paths-ignore:\n      - "\*\*\/\*\.md"\n/,
  );
  assert.match(workflow, /\n  static:\n/);
  assert.match(workflow, /\n  go:\n/);
  assert.match(workflow, /\n  runtime:\n/);
  assert.match(workflow, /\n  android:\n/);
  assert.match(workflow, /run: go test \.\/\.\.\./);
  assert.match(workflow, /run: scripts\/dev\/web-test\.sh/);
  assert.match(workflow, /DeterminateSystems\/determinate-nix-action@[0-9a-f]{40}/);
  assert.match(workflow, /DeterminateSystems\/magic-nix-cache-action@[0-9a-f]{40}/);
  assert.match(workflow, /run: nix build \.#yt-dlp \.#gallery-dl --no-link --print-build-logs/);
  assert.match(workflow, /run: \.\/test\.sh/);
  assert.doesNotMatch(workflow, /workflow-pin-check\.sh/);
  assert.match(workflow, /No Android-relevant changes/);
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

test("reads android release version fields", () => {
  const parsed = parseAndroidVersion(`
android {
    defaultConfig {
        versionCode = 3
        versionName = "1.0.0"
    }
}
`);
  assert.deepEqual(parsed, {
    versionCode: 3,
    versionName: "1.0.0",
  });
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

  assert.match(notes, /^Release v1\.0\.1/m);
  assert.match(notes, /^Changelog$/m);
  assert.match(
    notes,
    /- fixed hover not being triggered in feed \(\[1111111\]\(https:\/\/github\.com\/screwys\/Igloo\/commit\/1111111111111111111111111111111111111111\)\)/,
  );
  assert.doesNotMatch(notes, /^## commits/m);
  assert.doesNotMatch(notes, /^changes:$/m);
  assert.doesNotMatch(notes, /since `v1\.0\.0`/);
});

test("renders release summary before commit list", () => {
  const notes = renderReleaseNotes({
    newTag: "v2.0.0",
    repository: "screwys/Igloo",
    description: "Igloo no longer depends on RSSHub.",
    commits: [
      {
        sha: "1111111111111111111111111111111111111111",
        subject: "replace rsshub ingest",
      },
    ],
  });

  assert.match(notes, /^Igloo no longer depends on RSSHub\.\n\nChangelog/m);
});

test("infers GitHub repository from common origin remotes", () => {
  assert.equal(repositoryFromRemote("https://github.com/screwys/Igloo.git"), "screwys/Igloo");
  assert.equal(repositoryFromRemote("git@github.com:screwys/Igloo.git"), "screwys/Igloo");
  assert.equal(repositoryFromRemote("ssh://git@github.com/screwys/Igloo.git"), "screwys/Igloo");
  assert.equal(repositoryFromRemote("https://example.com/screwys/Igloo.git"), "");
});
