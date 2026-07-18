---
name: igloo-release
description: Use when preparing, committing, tagging, pushing, publishing, repairing, or checking an Igloo release, including Android version bumps, GitHub Releases, release notes, artifact workflows, or release tags.
---

# Igloo Release

Use the repository release scripts through the named `just` recipes. Do not assemble the release sequence by hand unless recovering from a partial release.

## Default Release Flow

1. Read the `Releases` section in `AGENTS.md` and keep its constraints active.
2. Require the requested bump and exact user-written summary. If either is missing, ask for it; do not choose a bump or invent public release text.
3. Finish and commit product changes first. Release creation requires a clean working tree.
4. Run the relevant proof for the touched area before release. If Android files changed, `just build-android` is required.
5. Create and publish the release with one command:

```bash
just release <patch|minor|major> "<user summary>"
```

This recipe delegates to the release script, which prepares release metadata, creates the signed release commit and tag, pushes `main` and the tag atomically, creates the GitHub Release from the generated notes file, and dispatches release artifact workflows.

Generated notes should link commit SHAs. If links are missing, check that `scripts/dev/release.mjs` can infer the GitHub repository from `GITHUB_REPOSITORY` or `git remote get-url origin`.

## Local-Only Release Tags

Use this only when the user explicitly wants a local signed tag without publishing:

```bash
just release-local <patch|minor|major> "<user summary>"
```

Do not then create the GitHub Release from `git show`, `git cat-file tag`, or `git for-each-ref --format='%(contents)'`. Signed annotated tag contents include the PGP signature block.

If publishing an already-created signed tag, build the notes file from the tag subject and body only:

```bash
tag=vX.Y.Z
notes_file="$(mktemp)"
{
  git for-each-ref "refs/tags/$tag" --format='%(contents:subject)'
  printf '\n\n'
  git for-each-ref "refs/tags/$tag" --format='%(contents:body)'
} > "$notes_file"
gh release create "$tag" --title "$tag" --notes-file "$notes_file" --verify-tag
rm -f "$notes_file"
```

## Recovery Checks

- If the GitHub Release body contains `-----BEGIN PGP SIGNATURE-----`, regenerate notes from `%(contents:subject)` and `%(contents:body)`, then run `gh release edit "$tag" --notes-file "$notes_file"`.
- Verify workflow dispatches with:

```bash
gh run list --workflow container-release.yml --limit 1
gh run list --workflow android-release.yml --limit 1
```

- End by checking `git status --short`, `git log --oneline -3 --decorate`, the release URL, and workflow URLs.
