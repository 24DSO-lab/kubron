# Incidents

The **Incidents** section diagnoses a workload without changing the cluster and without AI. It answers "why is this Pod or Deployment not working" and shows what the answer is based on.

## Open an investigation

Choose a kind (Pod, Deployment or ReplicaSet), a namespace and a name. Kubron reads the object, its owners (Pod → ReplicaSet → Deployment), its Pods, Events, Namespace and Nodes — read-only. **Secrets are never read.**

The report separates four kinds of entries, and the interface shows them differently:

- **Facts** — what was observed, with its source (object field, event, container log) and the time it was received.
- **Hypotheses** — a guess about the cause with a confidence (low, medium, high), the supporting facts and the contradicting facts. A hypothesis is never presented as an established cause: only you can confirm it, when you mark the result.
- **Recommendations** — suggested changes. Kubron does not apply them.
- **Diagnostic steps** — what else to check.

If data is missing (a source is forbidden by RBAC, the Events list was truncated, a container never started), the report says so under **Missing context** instead of guessing.

Covered: Pending Pods (resources, node selector or affinity, taints, volume claims), CrashLoopBackOff, OOMKilled, container start errors (image pull, missing ConfigMap reference, runtime errors) and rollout problems of Deployments and ReplicaSets (progress deadline, quota, admission denial, paused rollout, unhealthy new Pods).

## Events and log evidence

Events are captured when the investigation opens and are refreshed with **Update Events** while it is open; the snapshot is stored because cluster Events expire.

A finding can refer to a **log fragment**: a window of lines around an anchor line (the last line that looks like an error) of the current or previous container instance. Fragments the findings refer to are saved automatically; others you can save with **Save as evidence**. Limits are fixed: 200 lines and 32 KiB per fragment, lines cut at 2 KiB, up to 10 fragments and 256 KiB per investigation.

Before saving, the text is redacted (secrets, tokens, passwords). If redaction fails for any reason, the log is not text, or a limit is exceeded, the fragment is **not saved** and the finding says "evidence not saved" with the reason. Fragments are encrypted at rest on the server, are never sent to an external AI, and are not part of an export unless you choose to include them.

## Access

Everyone with `read` can open investigations, see the report and metadata of fragments (container, time, number of lines), and mark the result. **Reading the content of saved fragments** needs the `diagnostics.logs` permission (administrators and operators by default); the server checks it on every read. Without it the interface shows "content hidden". Deleting fragments or investigations also needs `diagnostics.logs`; changing the storage settings needs `users.manage`. Reading fragments and exports are written to the audit log without their content. Live log viewing still needs only `read`.

## Storage settings

Fragments are kept at most **15 days** (the default and the upper limit; you can only shorten it to 1–15 days). Investigations are kept **90 days** by default, up to **365**. An administrator can turn log fragment saving off for the whole organization (the application on desktop) and exclude namespaces. You can also turn saving off for a single investigation and delete the fragments already saved. Deleting an investigation deletes its fragments.

On the server, defaults for organizations without saved settings come from `KUBRON_INCIDENT_LOG_EVIDENCE` (`on`/`off`), `KUBRON_INCIDENT_FRAGMENT_RETENTION_DAYS` (1–15), `KUBRON_INCIDENT_RETENTION_DAYS` (1–365), `KUBRON_INCIDENT_EXCLUDED_NAMESPACES` (comma-separated) and `KUBRON_INCIDENT_PURGE_INTERVAL` (default `1h`).
