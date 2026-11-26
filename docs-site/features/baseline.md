# Configuration baseline

A baseline records the current configuration of selected resources. Later you run a check and see what has drifted from it. Kubron only **reads** the cluster: it never applies a baseline, fixes drift or checks on a schedule.

[[toc]]

## What is supported

Deployments, StatefulSets, DaemonSets, Services, Ingresses, NetworkPolicies and ConfigMaps. **Secrets are not supported and are never read**, even if you list one explicitly.

Values that may be sensitive (ConfigMap data, environment variable values and any text that looks like a credential) are stored only as keyed hashes. A check tells you that such a value changed and which key it belongs to, never the value itself.

## Create a baseline

You need the `baseline.manage` permission.

1. Open **Configuration baseline** and choose **New baseline**.
2. Enter a name, the namespaces, an optional label selector and the resource types.
3. Choose **Preview**. Kubron reads the cluster and shows what would be recorded and anything it had to skip.
4. Enter a **comment**, tick the confirmation that the current state is recorded as is, **including deviations that already exist**, and choose **Record baseline**.

If the cluster changes after the preview, nothing is recorded. Preview again and confirm what you see. Nothing is ever created silently.

On a multi-replica server any replica can confirm a preview issued by another one. After the master key (`KUBRON_SECRET_KEY`) is rotated, previews issued before the change are rejected as stale: preview again.

## Run a check

Anyone who can read the cluster can run a check. Each resource gets one status:

| Status | Meaning |
|---|---|
| Match | Same as the active version |
| Changed | Differs; expand the row to see the fields |
| Missing in cluster | The resource no longer exists |
| Not checked | Could not be compared (no permission, API error, unsupported type, other rules version). It is **never** shown as a match |

Changes carry a priority. A source (“according to managedFields”, or “applied from Kubron”) appears only when there is evidence for it.

For workloads managed by a HorizontalPodAutoscaler the replica count is ignored. If the autoscaler could not be read, the result says so.

## Versions

**New version** records the current state again; it becomes active and keeps the exceptions of the previous version. Older versions stay unchanged, and you can make any of them active. A version created with older comparison rules cannot be compared: record a new one.

Kubron keeps at least the 20 latest versions of every baseline and never removes the active one. Check results are kept for 90 days.

## Exceptions

An exception excludes a field from comparison for one resource or for every resource of a type. It needs a comment and can expire. Exceptions are listed in every result, so a hidden difference stays visible. Expired exceptions are not applied.
