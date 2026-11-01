# Features overview

Kubron covers the day-to-day work of a Kubernetes operator in a single desktop (or web) window.

## Clusters & dashboard

- Multi-cluster management with names, colors, icons and environment tags
- Namespace scoping that works with restricted RBAC
- Live updates via shared informers
- Cluster overview: nodes, workloads, capacity, storage, health
- Problem detection (problem pods, blockers, hot pods, low headroom nodes)
- Recent changes and expiring certificates

## Resources

- Wide coverage of built-in types (workloads, networking, config, storage, RBAC, …)
- Automatic CRD discovery with `additionalPrinterColumns`
- Detail panels (conditions, events, owners, containers, …)
- Monaco-based YAML editor with diff before apply
- Bulk actions and keyboard shortcuts

## Operations

- Pod logs, exec, port-forward, eviction
- Deployment scale / restart / history / rollback
- Node cordon, drain, shell
- Optional one-click metrics stack (Prometheus, kube-state-metrics, node-exporter)

## Helm

- Releases, history, diff, rollback, uninstall
- Chart catalog (including OCI and basic-auth repositories)
- Install / upgrade with dry-run and manifest diff
- Update hints for newer chart versions

## GitOps

- Built-in Argo CD applications, ApplicationSets and projects
- Sync, refresh, terminate, rollback, suspend/resume
- Live-vs-desired diff and resource graph

## Security

- Local users with `admin` / `operator` / `user` roles
- Fine-grained permissions, PBKDF2-SHA256, lockout, auto-logout
- Audit log
- Public `pkg/ext` API for external modules (SSO/OIDC planned as commercial add-on)
- No telemetry

See the individual feature pages for more detail.
