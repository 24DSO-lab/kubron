# GitOps with Argo CD

Kubron includes an Argo CD integration in its Kubernetes workspace. It discovers Argo CD resources through the Kubernetes API; you do not need to install a separate Kubron UI component in the cluster. Argo CD itself must already be installed and its CRDs available.

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

Argo CD actions are subject to Kubron permissions and Kubernetes RBAC. Changes made through Kubron are sent to the Argo CD resources in the cluster. Kubron does not replace Argo CD's own reconciliation engine or project policies.

An optional published Argo CD URL can be configured in cluster settings. When present, Kubron can open that address directly; otherwise, the UI can use the configured port-forward path where available.

User guides: [English PDF](https://github.com/24DSO-labs/kubron/blob/master/docs/DOCS/EN/Kubron-ArgoCD-User-Guide-EN.pdf) · [Русский PDF](https://github.com/24DSO-labs/kubron/blob/master/docs/DOCS/RU/Kubron-ArgoCD-User-Guide.pdf)