# Releasing

Releases are cut in two steps:

```bash
scripts/release.sh X.Y.Z --prepare   # on the working branch, before merging into dev
scripts/release.sh X.Y.Z             # from dev, once dev is green
```

 Full workflow, GitHub setup and troubleshooting are described in [docs/releasing.md](https://github.com/24DSO-labs/kubron/blob/master/docs/releasing.md).

User-visible changes should be added to `CHANGELOG.md` under `[Unreleased]`.
