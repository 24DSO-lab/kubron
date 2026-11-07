# Helm

Kubron includes Helm workflows for managing releases and chart sources without leaving the Kubernetes workspace.

## Releases

- List releases in the active cluster and inspect their status.
- View release values, rendered manifests and chart notes (subject to `secrets.read`).
- Review revision history and compare two revisions.
- Roll back to a previous revision or uninstall a release; optionally keep release history.

## Permissions

Installing, upgrading and uninstalling releases needs the Kubron `write` permission. Rolling back a release needs `operate`. Reading release values, manifests, revision diffs and upgrade previews needs `secrets.read`, because they can contain secrets. Adding or removing repositories and OCI registries needs `clusters.manage`. See the [permission reference](/features/security#permission-reference).

## Chart catalog and repositories

- Add, update and remove HTTP chart repositories.
- Use repositories with basic authentication.
- Browse chart versions and search for charts.
- Add OCI registries and select charts published in them.
- Configure repository TLS behavior where supported; skipping certificate verification should be reserved for trusted environments.

## Install and upgrade safely

- Preview an installation or upgrade with a dry-run.
- Review the rendered manifest diff before applying it.
- Start from the release's current user-supplied values, edit them, or change only the chart version. Values you submit replace the previous ones as a whole, so a key you remove in the editor returns to the chart default.
- Follow operation progress and inspect the recent Helm operation journal. An install or upgrade in progress can be cancelled; a rollback or an uninstall cannot.
- Check whether a newer chart version is available for an installed release.

## Desktop and Web storage

Desktop and single-user file-storage mode keep Helm configuration in the local Kubron data directory. In PostgreSQL-backed Web team mode, repositories, encrypted repository credentials and the Helm operation journal are stored per team and shared across server replicas. The cache remains reproducible local data.

Web applies outbound network policy to chart repositories and OCI registries to reduce SSRF risk. Private or internal repository addresses may require an explicit operator allowlist; see [server configuration](/server/configuration).

More detailed user guides: [English PDF](https://github.com/24DSO-labs/kubron/blob/master/docs/DOCS/EN/Kubron-Helm-User-Guide-EN.pdf) · [Русский PDF](https://github.com/24DSO-labs/kubron/blob/master/docs/DOCS/RU/Kubron-Helm-User-Guide.pdf)