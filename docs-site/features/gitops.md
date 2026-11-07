# GitOps with Argo CD

Kubron includes an Argo CD integration in its Kubernetes workspace. It discovers Argo CD resources through the Kubernetes API; you do not need to install a separate Kubron UI component in the cluster. Argo CD itself must already be installed and its CRDs available. Flux CD is supported too: see [Flux CD](#flux-cd) below.

## What you can inspect

- Applications, ApplicationSets and Projects.
- Sync and health status, deployed revision, destination and last sync time.
- Application resource graph.
- Live-versus-desired diff for an Application.
- Project roles and access information exposed by Argo CD.

## Available actions

- Sync one or more Applications, with options such as revision, prune, force and dry-run where applicable.
- Refresh or hard-refresh application state.
- Terminate a running operation.
- Roll back to a previous revision.
- Suspend or resume automated sync.
- Delete an Application with or without cascading deletion.

Bulk actions report their result per Application, so a failure on one item does not hide the result for the rest.

## Access and permissions

Syncing, refreshing, terminating an operation and rolling back need the Kubron `operate` permission. Suspending or resuming auto-sync and deleting an Application need `write`. All of them are also subject to Kubernetes RBAC; see the [permission reference](/features/security#permission-reference). Changes made through Kubron are sent to the Argo CD resources in the cluster. Kubron does not replace Argo CD's own reconciliation engine or project policies.

An optional published Argo CD URL can be configured in cluster settings. When present, Kubron can open that address directly; otherwise, the UI can use the configured port-forward path where available.

User guides: [English PDF](https://github.com/24DSO-lab/kubron/blob/master/docs/DOCS/EN/Kubron-ArgoCD-User-Guide-EN.pdf) · [Русский PDF](https://github.com/24DSO-lab/kubron/blob/master/docs/DOCS/RU/Kubron-ArgoCD-User-Guide.pdf)


## Flux CD

Kubron also supports Flux CD. Like Argo CD, it is discovered through the Kubernetes API: nothing is installed in the cluster for Kubron, and Flux itself must already be running. The Flux section appears in a cluster when the Kustomize or Helm controller is present. Tabs for controllers that are not installed (source, notification, image automation) are hidden, because Flux can be installed partially.

### What you can inspect

- Kustomizations and HelmReleases: a computed state (Ready, Reconciling, Failed, Stalled or Suspended), interval, source and dependencies, applied and attempted revision, last reconcile time, conditions and the controller message.
- The resources a Kustomization or HelmRelease manages (its inventory) and the chain from source to Kustomization or HelmRelease to resources.
- HelmRelease chart, release name and release history.
- Sources: GitRepository, OCIRepository, HelmRepository, HelmChart and Bucket.
- Notifications (Alert, Provider, Receiver) and image automation (ImageRepository, ImagePolicy, ImageUpdateAutomation), read-only.
- A dashboard with the number of objects per state, problem objects and a breakdown by namespace and source. Objects that Flux manages are marked as such in resource lists, and Flux changes appear in recent changes on the Overview.

The view updates by itself when Flux objects change. Values that may contain secrets are never shown: HelmRelease `spec.values` and `valuesFrom`, Kustomization `postBuild` substitutions, `kubeConfig` and `decryption` contents, credentials inside URLs, and webhook paths of Receivers.

### Available actions

- Reconcile one or more objects, optionally together with their source.
- Suspend and resume.
- Delete a Kustomization or a HelmRelease, one object at a time.

Reconcile, suspend and resume work on Kustomizations, HelmReleases, sources, ImageRepositories and ImageUpdateAutomations. Notification objects and image policies are read-only. Bulk actions report their result per object.

Delete asks you to type the object name and shows the consequences first. A Kustomization with `prune: true` removes the resources it manages (the dialog shows how many); with `prune: false` they stay in the cluster without management. Deleting a HelmRelease uninstalls the Helm release and its resources. Kubron cannot undo a deletion; restore the object from Git.

### Access and permissions

Reconcile needs the Kubron `operate` permission; suspend, resume and delete need `write`. They are also subject to Kubernetes RBAC. Kubron's in-cluster ServiceAccount profiles contain no Flux rules, so grant them through `rbac.extraRules`; a ready-made fragment is in the [deployment guide](https://github.com/24DSO-lab/kubron/blob/master/docs/deploy.md). Without `patch` and `delete` rules the Flux section stays read-only in practice.

### Limitations

- There is no diff and no rollback. A rollback in Flux is a change in Git (for example `git revert`); Kubron does not imitate it.
- Flux 2.3 or newer is required (the first release with the stable `HelmRelease` v2 API). If only older API versions are served, Kubron explains how to upgrade or migrate and does not parse the objects.
- The resources tab of a HelmRelease needs Flux 2.8 or newer, which is the first version that records the inventory. On older versions the tab is unavailable.
- Resources managed through `spec.kubeConfig` live in another cluster and are shown with a "remote" mark instead of being resolved.
- Flux Operator resources (`FluxInstance`, `ResourceSet`) are not supported, and Kubron does not open a Flux web UI or run the `flux` CLI.
- Creating and editing Flux objects uses the general YAML editor. If `spec.suspend` is set in Git, the parent Kustomization restores it after you resume or suspend an object by hand.
