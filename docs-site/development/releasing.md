# Releasing

This page is for maintainers. A release is one command; GitHub Actions does the rest. The full workflow with all details is in [docs/releasing.md](https://github.com/24DSO-lab/kubron/blob/master/docs/releasing.md).

[[toc]]

## One version for everything

A single tag `vX.Y.Z` sets the version of everything: the desktop files, the container image `ghcr.io/24dso-lab/kubron:X.Y.Z`, the Helm chart `oci://ghcr.io/24dso-lab/charts/kubron` at version `X.Y.Z` (its `appVersion` too), and the SBOM file names.

- The source of truth is `wails.json` (`info.productVersion`) and `frontend/package.json`. `scripts/release.sh --prepare` updates them, and the `release.yml` and `publish.yml` workflows refuse to run if the tag does not match (for a release candidate, the base version without the suffix is compared).
- `deploy/helm/kubron/Chart.yaml` holds the placeholder `0.0.0-dev`. `publish.yml` sets the chart version and `appVersion` from the tag, so **do not edit `Chart.yaml` for a release**.
- A trial publish without a tag (`workflow_dispatch`) gets the version `0.0.0-dev.<sha>`, and such images do not get `latest`.

## Branches

| Branch | Purpose | Protection |
|---|---|---|
| `refactor` (or feature branches) | Working branch | None |
| `dev` | Ready for a release. `refactor` reaches it only through a pull request | Pull requests only, the same required checks as `master` including code scanning results (CodeQL, high and above), no deletion, no force-push |
| `master` | Released code. Tags `vX.Y.Z` are placed here | Pull requests only, 7 required checks, no force-push |

GitHub is the primary repository. GitLab is secondary: push to it without `--force`, and always pull from GitHub.

## Procedure

1. **Commit to `refactor`** and push it to GitHub. For external pull requests from forks, CI checks DCO separately.
2. **Changelog.** Add a line under `## [Unreleased]` in `CHANGELOG.md` for every user-visible change (Added, Changed, Fixed, Removed, Security). These lines become the release notes.
3. **Prepare the release in `refactor`**, with a clean working tree:

   ```bash
   scripts/release.sh 0.2.0 --prepare   # [Unreleased] -> [0.2.0], versions in files, one commit; pushes nothing
   git push github refactor
   ```

   Wait for `refactor` to turn green. Preparing **before** the merge into `dev` means `dev` already holds everything that goes into the tag, so the script does not need to add new commits to `dev` afterwards.
4. **Merge into `dev`.** Open a pull request `refactor` to `dev` and wait for green checks, **including CodeQL**: an alert of level high blocks the merge (this is the GitHub code scanning result, not a job from `codeql.yml`). Merge the pull request (merge commit). CI runs again on the push to `dev`: wait for green.
5. **Release** from `dev`, with a clean working tree:

   ```bash
   scripts/release.sh 0.2.0 --dry-run   # checks and a preview of the notes, changes nothing
   scripts/release.sh 0.2.0             # the release itself (--watch waits for the workflow)
   ```

   If step 3 was skipped, the script stops and tells you to run `--prepare`.
6. **Done.** About 10 to 20 minutes later the release is published. Afterwards `refactor` equals `master`: continue with `git checkout refactor && git pull`.

A release candidate is `scripts/release.sh 0.3.0-rc.1 --prepare`, then the same way through `dev`. The changelog is not rolled over, the notes come from `[Unreleased]`, the files carry version `0.3.0`, and the release is marked as a pre-release.

## Before you tag

The tag goes on `master`, but you verify `dev`: the pull request from `dev` to `master` and the tag then use the same commit. Make sure everything below is green for the latest `dev` commit. Some workflows run only when certain paths change, so start them by hand if the last push did not touch them (`gh workflow run <file> --ref dev`).

| Check | Workflow |
|---|---|
| `go`, `go-race`, `windows`, `lint`, `frontend`, `build-web`, `licenses`, `headers` | `ci.yml` |
| `e2e` (Playwright and kind), `e2e-pg` (PostgreSQL, teams), `e2e-ha` (two replicas, replica loss) | `ci.yml` |
| `migrations` (forward and backward on PostgreSQL 16), `restore-check` | `ci.yml` |
| `deps-audit` (`npm audit`, osv-scanner, expiry of scanner exceptions) | `ci.yml` |
| `vulncheck` (govulncheck): does not block, but fix findings that have a fix | `ci.yml` |
| `deploy` (`hadolint`, `chart`, `kind`) | `deploy.yml` (only when relevant paths change) |
| `e2e-firefox` | `e2e-firefox.yml` (only when relevant paths change) |
| CodeQL: the last run on `dev` is green and there are no open high alerts | `codeql.yml` |
| Trial server publish | `publish.yml` by hand: build, Trivy gate, SBOM, chart package without push |

Beyond the checks:

1. **Changelog.** Everything user-visible is under `[Unreleased]`. Security changes go under "Security". Incompatible database schema or chart value changes go under "Changed" with an **Upgrade** note saying what to do when updating.
2. **Scanner exceptions.** `python3 scripts/check-ignores.py` passes, and entries in `.trivyignore` and `osv-scanner.toml` that expire before the end of next week are reviewed.
3. **Migrations.** For any new file in `internal/pgstore/migrations/`: it has a `down`, the schema stays compatible with the previous release, and any incompatibility is recorded in the changelog.
4. **Dependabot.** Open security-update pull requests for production dependencies are merged or deliberately deferred.
5. **Open `security` issues** (created by `scheduled-scan.yml`) are triaged.
6. `scripts/release.sh X.Y.Z --dry-run` passes and shows the expected notes.

### Why some jobs are "skipped" in a green run

CI does not repeat work on identical content. A test result belongs to the **content** (the git tree hash), not the commit: a pull request merge commit, the commit after a merge, and a promotion `refactor` to `dev` to `master` have different SHAs but the same files. After a fully green run, a `Record verified tree` job stores a 14-day marker, and the next run with the same content skips the deterministic jobs. `deps-audit`, `vulncheck`, `headers`, DCO, the tag build and manual runs always run again.

## What `scripts/release.sh` does

**With `--prepare` (in `refactor`):** moves `[Unreleased]` into a `## [X.Y.Z] - date` section, updates the links at the bottom of the changelog and the versions in `wails.json` and `frontend/package*.json`, and makes the commit `chore(release): vX.Y.Z` (signed off). It pushes nothing. Running it again when everything is already prepared does nothing. It refuses to run in `dev` or `master`.

**Without `--prepare` (in `dev`):**

1. Checks that you are on `dev`, the tree is clean, `dev` matches GitHub, the tag does not exist, and `dev` already contains the `[X.Y.Z]` changelog section and version `X.Y.Z` in `wails.json` and `frontend/package.json` (otherwise it stops and asks for `--prepare`).
2. Commits and pushes nothing to `dev`: exactly the verified content goes to `master`. That is why a release no longer triggers repeated CI, Deploy and CodeQL runs.
3. Opens a pull request from `dev` to `master`, waits for green checks, and merges it (merge commit).
4. Places the annotated tag `vX.Y.Z` on `master` and pushes it.
5. Fast-forwards `dev` and the working branch `refactor` to `master`. Other branches: `RELEASE_SYNC_BRANCHES="refactor feature-x"`; disable with `RELEASE_SYNC_BRANCHES=" "`. A branch with its own commits is left alone and the script tells you to merge manually (`git merge github/master`), where a `CHANGELOG.md` conflict is possible: keep only your new lines in `[Unreleased]` and take what is already rolled into `[X.Y.Z]` from `master`.

If the script stops (a red check, a network error), fix the cause and run **the same command** again: finished steps are skipped.

- **Checks on the `dev` to `master` pull request are red.** Fix in `refactor`, merge through a pull request into `dev`, wait for green, and run the command again. If only CodeQL is red, open the alerts in Security, Code scanning: high blocks the merge. If an end-to-end job failed without code changes, re-run the failed jobs first; the same test red twice is not a flake.

## What the `Release` workflow does (on a `v*.*.*` tag)

1. `verify` runs the same set of checks as `ci.yml` on the tag itself.
2. After a green `verify`, in parallel:
   - **Desktop** builds Windows (x64), Linux (x64, plus `.deb`) and macOS (universal), after checking the tag matches the version files.
   - **Server** (`publish.yml`) builds the image, runs the Trivy gate (HIGH or CRITICAL findings with a fix stop the publish), creates the image SBOM, pushes to ghcr.io, signs with cosign (keyless) and attaches provenance and SBOM attestations, then packages, pushes, signs and attests the chart with its own SBOM. Only versions without a suffix (`-rc`, `-beta`) get `latest`. An already published version is never overwritten: the workflow fails.
3. `release` creates the desktop SBOM, `SHA256SUMS.txt` (files and all SBOMs), attestations, and the notes from the changelog with a downloads table.
4. It creates the release (pre-release for `v0.*` and release candidates) and publishes it. Releases are **immutable**: files and the tag do not change after publishing.

To keep a release as a draft for manual review, set the repository variable `RELEASE_AUTO_PUBLISH=false`. How to verify the signatures and attestations is in [Docker and Kubernetes deployment](/server/deploy#verify-the-image-and-chart) and in [Requirements, download and verification](/guide/installation#verify-your-download).
