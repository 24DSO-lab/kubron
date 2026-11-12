# Upgrade readiness

**Upgrade readiness** (in the **Configuration** group of the sidebar) assesses whether a cluster is ready for a Kubernetes upgrade. Kubron only **reads** the cluster: it never changes it and never runs the upgrade.

The report is an assessment, not a guarantee. Anything that could not be checked is shown as **not checked** and is never turned into "compatible".

## Running a check

1. Choose a cluster and, optionally, a target version (the three latest supported minor versions). Without a target, only APIs that are already deprecated are reported.
2. Run the check. It is synchronous and needs the `read` permission on the cluster.
3. Read **Coverage** first: it lists what was checked and what was not.

## What you get

- **Deprecated and removed APIs** found in live objects (through `managedFields` and `last-applied-configuration`), with the replacement and a link to the Kubernetes deprecation guide.
- **Components** (kubelet and common system workloads) with one of four statuses: *compatibility confirmed by the source*, *known limitation*, *not enough data*, *not recognized*. Each recognized component shows its documentation link and the date it was checked.
- **Upgrade path** checks: more than one minor version at a time, or a current version outside the upstream support window.
- **Blockers, warnings and information**, ordered by priority, plus preparation steps and checks after the upgrade.
- **What changed** since the previous check of this cluster.

## Coverage and limits

Generic Kubernetes mode only: provider-specific checks (EKS, GKE, AKS and others) are not performed. GitOps manifests (Argo CD, Flux, Git) and local files are not analysed and are listed as *not checked*. Clients that only read an API leave no trace in objects and cannot be detected. Kinds that Kubron may not read are listed as not covered.

## History and export

Every check is saved. By default Kubron keeps 50 reports per cluster for 180 days (`KUBRON_READINESS_KEEP_PER_CLUSTER`, `KUBRON_READINESS_MAX_AGE`; `0` means no limit). Export a report as **JSON** or **Markdown**; both contain the coverage section. On the web the file is downloaded; on the desktop it is copied to the clipboard.
