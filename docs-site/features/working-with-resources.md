# Working with resources

This page is a practical guide to the lists and detail panels you use every day. For the list of supported resource types and what each kind of access allows, see [Resources and YAML](/features/resources) and the [permission reference](/features/security#permission-reference).

[[toc]]

## Choosing namespaces {#choosing-namespaces}

The namespace selector sits at the bottom of the sidebar. It is a **multi-select**: tick as many namespaces as you need.

- Type in the search box to narrow the list. **Select shown** ticks every namespace that matches, and **Clear** removes the selection.
- With **nothing selected**, Kubron shows **all namespaces**.
- You can select **up to 50** namespaces.
- Kubron remembers your selection for each cluster. When you reconnect, it restores it, or the cluster's [default namespace](/features/settings#cluster-settings) if that namespace exists.
- Lists for several namespaces are read in parallel. A namespace you cannot read is marked **No access** instead of failing the whole list, so you still see everything you are allowed to see.

If you are not allowed to list namespaces at all, the selector may stay empty. Set a default namespace in the cluster settings, and see [Troubleshooting](/guide/troubleshooting#connecting-to-a-cluster).

## Lists

Choose a resource type in the sidebar to open its list.

| You want to | How |
|---|---|
| Find an object | Type in the **Search** box above the list |
| Sort | Click a column header. Click again to change the direction |
| Show, hide or reorder columns | Open **Columns**. **Reset columns** restores the default set. Kubron remembers your choice for each resource type |
| Resize a column | Drag its edge. Double-click the edge to reset the width |
| Open an object | Click its row to open the detail panel, or use the row's context menu: **Show details** and **View YAML**, plus actions that suit the kind |
| Delete several objects | Tick the rows (or **Select all**) and choose **Delete selected**. Kubron asks for confirmation, and a failure on one object does not stop the others. Offered only when your role allows deleting |
| Create an object | **Create** (for example, **Create Deployment**) opens the YAML editor, with a starter template for the kinds that have one. Offered only when your role allows creating |
| Find out who manages it | See the **Managed by** column ([details](/features/resources#managed-by)) |

Lists refresh on their own at the interval set in [Settings](/features/settings#application-settings). A refresh updates the table in place, so your scroll position and selection stay where they are.

## The detail panel

Opening an object shows a panel on the right side of the window.

- **Properties.** Name, namespace, age, labels, annotations and the fields that matter for the kind. **Copy name** puts the name on the clipboard.
- **Events.** Recent Kubernetes events for the object.
- **Kind-specific tabs and sections.** For example: containers, volumes and metrics for a Pod; revisions for a Deployment; Pods, capacity, allocatable resources and taints for a Node; history for a CronJob; rules for Roles; ports and endpoints for a Service; rules for an Ingress; quota usage for a ResourceQuota. Argo CD and Flux objects add their own tabs, see [GitOps](/features/gitops).
- **Actions.** Scale, restart, roll back, delete and the other actions that fit the kind. They appear only when your role allows them. See [Operations](/features/operations).
- **Resizing.** Drag the divider, or focus it and use the arrow keys. **Expand panel** gives the panel more room, and **Restore panel width** brings your width back. Kubron remembers the width.

### ConfigMaps and Secrets

ConfigMap data is edited key by key: each key has its own editor and its own **Save**.

A Secret's data section needs the `secrets.read` permission. Without it the section only says that you have no access. With it, each key is shown **encoded** (base64); the eye button switches that key to the **decoded** text, the copy button copies it, and you can change the value and save the key. Viewing Secret values is recorded in the [audit log](/features/administration#audit-log).

## YAML

Use **View YAML** from the context menu or the panel to read the full object. The editor is Monaco, so its usual shortcuts work, for example `Ctrl + F` to search.

Editing follows the same rules as everywhere else: a **diff** of your changes is shown before anything is saved, and the change is applied to the cluster only after you confirm. Applying YAML needs the `write` permission and is recorded in the audit log. See [Resources and YAML](/features/resources).

## When something is missing

| Symptom | Check |
|---|---|
| A resource type is missing from the sidebar | The cluster does not serve it, or your credentials cannot list it. Custom resources appear when their CRD exists and you may read it |
| A list is empty although objects exist | The namespace selection may exclude them. Clear the selection to see all namespaces |
| **No access** next to a namespace | Kubernetes refused the read for your credentials. Ask for RBAC rights in that namespace |
| An action is not offered | Your Kubron role lacks the permission. See the [permission reference](/features/security#permission-reference) |
| Secret data says you have no access | You need `secrets.read`, and in the in-cluster mode the ServiceAccount must be allowed to read Secrets. See [Access for the in-cluster ServiceAccount](/server/deploy#access-for-the-in-cluster-serviceaccount) |
