# Incidents

The **Incidents** section diagnoses a workload without changing the cluster and without AI. It answers "why is this Pod, Deployment or Service not working", follows the chain of dependencies to a likely cause and shows what the answer is based on.

Four things make the answer better, and **none of them is required**: the dependency chain, metrics, related investigations and the results you mark yourself. Without metrics, without permission to read some objects or without any history, the report is still complete and says what it could not read.

[[toc]]

## Open an investigation

There are two ways in. Open **Incidents** in the sidebar and fill in the form, or choose **Investigate** among the actions of a resource (a Pod, Deployment, ReplicaSet, StatefulSet, Job, CronJob, Service, Ingress, HorizontalPodAutoscaler, PersistentVolumeClaim or Node): the investigation of that object opens at once. Nothing is changed in the cluster either way.

Choose a kind (Pod, Deployment, ReplicaSet, StatefulSet, Job, CronJob, Ingress, HorizontalPodAutoscaler, Service, PersistentVolumeClaim or Node), a namespace and a name. A Node has no namespace. A ConfigMap is not a target: it appears as a link in the chain of a Pod that refers to it. Kubron reads the object, its owners (Pod → ReplicaSet → Deployment), its Pods, Events, Namespace and Nodes, and then the objects it depends on (see [Dependency chain](#dependency-chain)). Everything is read-only. **Secrets are never read.**

The report separates four kinds of entries, and the interface shows them differently:

- **Facts** — what was observed, with its source (object field, event, container log, metric) and the time it was received.
- **Hypotheses** — a guess about the cause with a confidence (low, medium, high), the supporting facts and the contradicting facts. A hypothesis is never presented as an established cause: only you can confirm it, when you mark the result.
- **Recommendations** — suggested changes. Kubron does not apply them.
- **Diagnostic steps** — what else to check. Where Kubron can check something itself (a PVC, a StorageClass, the capacity of a Node), it does, and the result is a fact instead of a step.

If data is missing (a source is forbidden by RBAC, the Events list was truncated, a container never started), the report says so under **Missing context** instead of guessing. "Not found" and "could not read" are never mixed up: see [Dependency chain](#dependency-chain).

Covered:

- Pending Pods: resources, node selector or affinity, taints and volume claims. A claim problem is found by following the chain, not by matching text.
- CrashLoopBackOff and OOMKilled.
- Container start errors: image pull, missing ConfigMap reference, runtime errors.
- Rollout problems of Deployments and ReplicaSets: progress deadline, quota, admission denial, paused rollout, unhealthy new Pods.
- Services without working endpoints.
- PersistentVolumeClaims that are Pending or Lost.
- Nodes that are not ready, under memory, disk or PID pressure, or unschedulable.

The same snapshot of a cluster always produces the same report, except for the observation times.

## Dependency chain

A symptom and its cause are often in different objects: a Pod waits for a claim, the claim waits for a StorageClass that does not exist. Kubron builds a **graph of dependencies** starting from the object you chose and walks it to find the cause.

What the graph connects:

- owners and their children (Deployment → ReplicaSet → Pod);
- a Service and the Pods its selector matches, and the Service's endpoints;
- a Pod and its PersistentVolumeClaims, a claim and its PersistentVolume and StorageClass;
- a Pod and the Node it runs on;
- an Ingress and the Services of its rules and default backend, and the IngressClass it uses;
- a HorizontalPodAutoscaler and the workload it scales (Deployment, StatefulSet or ReplicaSet);
- a Pod and the ConfigMaps it refers to (volumes, `envFrom`, `env.valueFrom`). Kubron checks **only that the ConfigMap exists**: its contents (`data`, `binaryData`) are dropped as soon as they are read and never reach the report, the history, the audit log or an export;
- a StatefulSet, its Pods and the claims made from its `volumeClaimTemplates`;
- a Job and its Pods, a CronJob and its Jobs.

The graph is a **snapshot at the moment of the investigation**, not a live model. Open the investigation again to rebuild it.

### Missing is not the same as unknown

Every object in the chain has a state: healthy, degraded, progressing, suspended, **missing** or **unknown**. The two last ones are different and the interface shows them differently:

| State | What it means |
|---|---|
| **Missing** | The cluster answered "not found". A claim refers to a StorageClass that does not exist, for example |
| **Unknown** | Kubron could not tell, and the reason is shown: `forbidden` (no RBAC right), `unavailable`, `too large`, `limit` (the graph reached its limit) or `policy` (a Secret, which Kubron never reads) |

A reference to something that does not exist is drawn as a missing object, and a Service whose selector matches no Pod is marked degraded. An empty result is not shown as "nothing is wrong".

### Candidate root cause

A hypothesis can **explain** other hypotheses of the same report. "The claim is not bound: the StorageClass was not found" explains "the Pod was not scheduled: its claim is not bound". A hypothesis that explains others and is not explained by any is marked as a **candidate root cause**. It is a candidate and not a conclusion: its confidence is still low, medium or high, and every link of the path to it is a fact with a source and a time. Only you can confirm a cause, when you mark the result.

### What is diagnosed along the chain

| Target or chain | What is diagnosed |
|---|---|
| Pod → PVC → PV and StorageClass | The class is not found, there is no default class, `WaitForFirstConsumer` without a consumer, provisioning errors from the claim's Events, a claim in `Lost`. The older text-based rule stays as a fallback with lower confidence |
| Service | The selector matches no Pod, the Pods are not Ready (so the endpoints are empty), the port or `targetPort` does not match the container ports |
| PersistentVolumeClaim | Pending and Lost; attach and mount errors in the Events of the Pods that use it |
| Node | Not ready, memory, disk and PID pressure, unschedulable, the Pods affected on the Node |
| Ingress | The backend Service does not exist, or it has no ready endpoints; the IngressClass is not found. A class that is not set and has no default is reported as a fact. A TLS Secret is not read: checking it stays a step for you |
| HorizontalPodAutoscaler | The target does not exist; metrics are unavailable (taken from the `ScalingActive` condition); the `maxReplicas` limit is reached |
| Pod and ConfigMap references | A referenced ConfigMap does not exist: the graph confirms what the kubelet reported as `CreateContainerConfigError`. A reference marked `optional: true` is not an error |
| StatefulSet | Ordered start is stuck on Pod N; a claim of the template is not bound; an update is stopped by `partition` |
| Job | `BackoffLimitExceeded`, `DeadlineExceeded`. Why a Pod of the Job failed is explained by the Pod rules |
| CronJob | The schedule is suspended; runs were missed (`startingDeadlineSeconds`); `concurrencyPolicy: Forbid` blocks the next run |

For a Node the Pods are listed across namespaces. If your RBAC is limited to some namespaces, that list is marked incomplete and the report says why.

### Limits and what is kept

The graph is built from the target and not for the whole cluster. The limits are fixed in the code: 200 objects, 600 links, 4 steps from the target and 40 additional read requests. If a limit is reached, the report says it is partial and keeps the part that was built; it does not fail.

Only identifiers of resources, state codes and reason codes are saved in the investigation. Field values, hosts and paths of Ingresses, labels and annotations are not. ConfigMap contents are never kept.

An investigation saved by an earlier version opens as before, without the chain.

### Dependency map

An investigation with a graph has a **map of dependencies**: the objects of the chain as nodes and the links between them. The color and marker of a node show its state, and **missing** and **unknown** nodes look different. Choose a node to see the facts about that object; you can move between nodes with the keyboard. A graph that is empty or was cut at a limit is shown with a note, without an error.

The map is a picture of the **same snapshot** as the chain. It is not live and does not refresh by itself: open the investigation again to rebuild it.

## Metrics as evidence

If the [metrics stack](/features/metrics) is installed and your account may use it, Kubron adds **facts from metrics** to the report. They are an extra source of evidence: the report is complete without them.

What metrics do and do not do:

- They add facts such as "memory peaked at 97% of the limit in the last hour". The facts contain only the value, the unit, the time window and the aggregate. Raw series and query text are not saved.
- They can change the confidence of an existing hypothesis by **at most one level**, up or down. **High** confidence is never reached by metrics alone. Each change is visible, because the metric fact is listed as supporting or contradicting.
- They never create a hypothesis on their own. A hypothesis based on metrics appears only when a symptom was already found, and its confidence is not above medium.
- "No data" is not "zero". A missing signal is reported as missing and never counts against a hypothesis.

The signals Kubron can read are a fixed list. Nothing you or the interface type is sent to Prometheus as a query.

| Signal | Window |
|---|---|
| Share of CPU periods that were throttled | 15 minutes |
| Peak CPU use against the limit | 15 minutes |
| Peak memory use against the limit | 1 hour |
| Container restarts | 6 hours |
| Container OOM events | 6 hours |
| Lowest available memory of a Node | 1 hour |
| Average CPU load of a Node | 15 minutes |
| Free space of a Node's root disk | now |
| Whether a Node was under memory, disk or PID pressure | 6 hours |
| Share of a PersistentVolumeClaim that is used | 1 hour |
| Whether a HorizontalPodAutoscaler is at its `maxReplicas` | 15 minutes |
| Unavailable replicas of a Deployment | 15 minutes |
| Share of 5xx responses of an Ingress | 15 minutes |
| 95th percentile of the response time of an Ingress | 15 minutes |

The Ingress signals are read **only when `ingress-nginx` metrics are found** in the stack; without them the report is complete and the hint appears only if the symptom needs them. A signal can strengthen an existing hypothesis (for example `hpa.max_replicas_reached` or `ingress.backend_no_endpoints`) by one level, and when there is no such hypothesis the metric rules can offer their own with the prefix `metrics.`, never above medium.

Signals are requested only for the objects of the investigation and for the Node, never for the whole cluster. Metrics are collected last and only if at least 5 seconds remain of the operation's time; otherwise they are skipped and the report is still complete.

### When there are no metrics

When the symptom is one where metrics would help, the report shows a short hint, **with monitoring enabled the diagnosis is more accurate and complete**, and the reason:

| Reason | What you see and what to do |
|---|---|
| Stack not installed | A link to [install the metrics stack](/features/metrics#install-it) |
| Access not granted | Metrics access is granted separately. A cluster administrator binds the role `prometheus-proxy-access` in `kubron-metrics` ([how](/features/metrics#who-can-see-the-charts)) |
| Unavailable | The stack exists but did not answer in time, or Prometheus returned an error |
| Metric family missing | The stack runs but does not provide the metrics needed, for example kube-state-metrics is not working |
| No series | Prometheus has no data for this object: a young Pod, or it is not scraped |
| History too short | The requested window is longer than the data Prometheus holds |

The hint appears **only for the signals that belong to the symptom that was found**. A report without a symptom, or a symptom for which metrics tell nothing, gets no hint.

A missing stack or missing access does not make the operation "partial". Only a stack that exists but fails (a timeout, an error) does.

::: warning Limits of the built-in stack
The stack keeps **2 days** of data on an `emptyDir`, so it is lost when the Prometheus Pod restarts. An investigation of something older, or opened after a restart, gets "history too short" and not a conclusion drawn from empty data. Only Kubron's own stack is read: an external Prometheus and your own kube-prometheus are not supported.
:::

## Related investigations and problems

When twelve Pods fail on one Node, you do not want twelve unrelated investigations. When you open an investigation, Kubron looks at the live cluster for a **possible shared source** and for the other workloads that depend on it. Nothing runs in the background: the check happens once, when you open the investigation, and its result is saved in the investigation.

### Possible shared source

| Kind | Shared source | Affected workloads |
|---|---|---|
| Shared Node | A Node | Workloads that have a Pod with a symptom (crash loop, OOM, not ready, evicted) on that Node |
| Shared storage | A StorageClass or its provisioner | Claims of that class that are Pending or Lost, and the workloads that use them |
| Shared dependency | An object that several investigations refer to and that is missing (for example a ConfigMap) or a Service without ready endpoints | At least two workloads whose graphs contain it |
| Shared root | A workload that owns the objects of the root-cause candidates of investigations at different layers of one chain (Ingress → Service → Deployment → Pod) | At least two investigations |
| Shared symptom | One namespace | At least five workloads with the same symptom code and the same hypothesis code |

A Node is abnormal if it is not ready, under memory, disk or PID pressure, unschedulable, has a `NoExecute` taint or has warning Events. The failures must start no more than 30 minutes after the Node's condition changed. The start of a failure is taken from the timestamps of the objects, not from the time you opened the investigation.

The block in the investigation shows the source, the confidence, the start time, the **number and list of affected workloads** and the facts that support it. The **potential impact area**, the workloads that depend on the source but have not failed yet, is shown separately from those already affected.

| Confidence | Meaning |
|---|---|
| **Low** | The same Node or class and at least three workloads, but no abnormal condition and no matching time. It is a hint, and no problem is created |
| **Medium** | A shared source and either an abnormal condition or a matching time |
| **High** | A shared source, an abnormal condition, a matching time and at least three workloads |

The wording is "possible" and "likely", never "cause". The shared source is confirmed by you.

A **shared symptom** has no structural link between the workloads, so its confidence is **always low** and it never creates a problem on its own.

### Recalculate the correlation

An open investigation keeps the correlation snapshot taken when it was opened. **Recalculate correlation** builds it again from live data. It changes only the correlation block: the saved Events, the report and the mark stay as they are (use **Update Events** for Events). It needs the `operate` permission, is written to the audit log (`incident.correlation_recompute`) and is not available for a resolved or closed investigation.

If your RBAC allows reading only some namespaces, the list of affected workloads is incomplete, and the block says so. It never shows more than your session may read. The check reads at most 10 additional objects and is skipped if less than 5 seconds remain.

### Problems

A **problem** groups the investigations that have the same shared source. It stores only its key and its members, and it **never changes the investigations**: they stay separate, nothing is deleted, hidden or merged.

- **Automatic attach.** When the best candidate has medium confidence or higher, the investigation joins an open problem with the same source that was updated in the last 6 hours, or starts a new one. A second investigation of another Pod on the same Node joins the same problem.
- **Reversible.** Detaching an investigation is remembered: it will not return to that problem automatically.
- **Closing.** A problem closes when its last member is closed. A new failure after the 6-hour window starts a new problem.
- **Limits.** A problem has at most 100 members. When a problem is full, the investigation keeps its correlation block but is not attached.
- **No duplicates.** Finding an open problem with the same source and creating a new one is a single atomic step: on the server it is serialized across all replicas (a database lock per organization, team, cluster and key), on desktop within the process. Two investigations opened at the same moment for the same source join one problem.

The problem screen lists the members, the affected workloads aggregated from the members' snapshots and the manual actions:

| Action | Effect |
|---|---|
| Attach | Adds an investigation to a problem. Attaching to a closed problem reopens it |
| Detach | Removes an investigation and remembers it |
| Merge | Moves the members of one problem to another; the first is closed |
| Create from selected | Starts a new problem from the investigations you select |

Viewing problems needs `read`. Changing the members needs `operate`, because it changes the history your team sees. Every action is written to the audit log with identifiers and without report content.

If the problem storage is unavailable, the correlation block is still shown and the investigation is saved without a group. The operation is then marked partial.

## Mark the result and learn from it

Marking the result closes the investigation. Only the result (resolved, unresolved or inconclusive) is required. Everything else is optional and takes one click:

- the hypothesis you **confirmed** (one) and the hypotheses you **rejected**;
- for each recommendation: not marked (the default), **helped** or **did not help**;
- whether **another action** helped, which you describe in the comment;
- **when it was resolved**, for a resolved result: not before the investigation was opened and not after the mark;
- whether the shared source from [Related investigations](#related-investigations-and-problems) was **confirmed**, **rejected** or is unknown;
- a comment, which is redacted before it is saved.

What you leave unmarked is "unknown" and is never counted as "did not help". A closed investigation is not edited later. If a fix did not work, open a new investigation: that is recorded as a **recurrence**.

### Recurrence

When you open an investigation that looks like one resolved in the last 30 days, a banner shows what was done, which cause was confirmed and what helped, with a link to the earlier investigation. "Looks like" means the same owner workload, the same symptoms and the same hypotheses of at least medium confidence, compared as a fingerprint that contains no resource values. Only the last 100 closed investigations of the cluster are searched, and the interface says so. The earlier investigation is not changed.

### Rule statistics

Kubron counts what you and your colleagues marked, **for this cluster**, and uses it only to present the report:

- inside one confidence level, hypotheses are sorted by how often they were confirmed in this cluster;
- a hypothesis shows "confirmed in N of M rated cases";
- a recommendation shows "helped in N of M", or "did not help before" when at least 5 cases were rated and at least 70% of them did not help;
- the typical time to resolve (the median, so one forgotten investigation does not distort it) and recurrences are shown.

What statistics **never** do: change the stored report or its confidence, hide a recommendation, switch rules off or change their thresholds. Percentages appear only with at least 5 rated cases; below that you see only the number of cases.

The numbers cover the last 200 closed investigations of the cluster within the retention period and only the investigations that were marked, so they can look better than reality. The sample size and the number of unrated cases are always shown next to them. The statistics table of a cluster is available with `read`.

### Export for maintainers

To help improve the rules, an administrator can press **Export rule statistics**. The file is created for download and **nothing is sent automatically**. Sending it, for example by attaching it to an issue in the Kubron repository, is the administrator's decision. Kubron has no telemetry.

| In the file | Not in the file |
|---|---|
| Rule set version, rule identifier, rule epoch and code | Names of resources and namespaces |
| Counts: confirmed, rejected, helped, did not help, recurred | Identifiers of investigations, organizations, teams and clusters |
| Median times to resolve | Users and comments |
| Which sources were in the report (chain, metrics, correlation) | Fingerprints and exact times |
| Symptom coverage and accuracy of the shared-source checks by kind | |

Exporting needs the same permission as changing the storage settings (`users.manage`) and `read` on the cluster. The audit log records the export without the content of the file.

## Events and log evidence

Events are captured when the investigation opens and are refreshed with **Update Events** while it is open; the snapshot is stored because cluster Events expire.

A finding can refer to a **log fragment**: a window of lines around an anchor line (the last line that looks like an error) of the current or previous container instance. Fragments the findings refer to are saved automatically; others you can save with **Save as evidence**. Limits are fixed: 200 lines and 32 KiB per fragment, lines cut at 2 KiB, up to 10 fragments and 256 KiB per investigation.

Before saving, the text is redacted (secrets, tokens, passwords). If redaction fails for any reason, the log is not text, or a limit is exceeded, the fragment is **not saved** and the finding says "evidence not saved" with the reason. Fragments are encrypted at rest on the server, are never sent to an external AI, and are not part of an export unless you choose to include them.

## Access

Everyone with `read` can open investigations, see the report and metadata of fragments (container, time, number of lines), see problems and rule statistics, and mark the result. **Reading the content of saved fragments** needs the `diagnostics.logs` permission (administrators and operators by default); the server checks it on every read. Without it the interface shows "content hidden". Deleting fragments or investigations also needs `diagnostics.logs`; changing the storage settings and exporting rule statistics need `users.manage`. Changing the members of a problem needs `operate`. Reading fragments, problem changes and exports are written to the audit log without their content. Live log viewing still needs only `read`.

There is no separate permission for metrics: `read` is enough in Kubron, and the Kubernetes RBAC of your cluster decides whether the metrics are reachable.

### Kubernetes permissions

Kubron reads the cluster with the same credentials as the rest of the application. For the dependency chain, the account needs **`get` and `list` only** on:

- `services`, `endpoints` and `discovery.k8s.io` `endpointslices`;
- `persistentvolumeclaims`, `persistentvolumes`;
- `storage.k8s.io` `storageclasses`;
- `networking.k8s.io` `ingresses` and `ingressclasses`;
- `autoscaling` `horizontalpodautoscalers`;
- `configmaps` (to check that they exist; the contents are dropped on reading);
- `apps` `statefulsets` and `daemonsets`, and `batch` `jobs` and `cronjobs`;

in addition to what the investigations already read (Pods, ReplicaSets, Deployments, Events, Namespaces and Nodes). Kubron needs **no access to Secrets and no write access** for any of this.

If a right is missing, the investigation does not fail: the affected objects are marked **unknown** with the reason `forbidden`, and **Missing context** says which source could not be read. Metrics have their own access, described in [Who can see the charts](/features/metrics#who-can-see-the-charts).

## Storage settings

Fragments are kept at most **15 days** (the default and the upper limit; you can only shorten it to 1–15 days). Investigations are kept **90 days** by default, up to **365**. The dependency graph, the correlation result and the fields of the mark are part of the investigation and follow its retention; problems without living members are removed together with them. An administrator can turn log fragment saving off for the whole organization (the application on desktop) and exclude namespaces. You can also turn saving off for a single investigation and delete the fragments already saved. Deleting an investigation deletes its fragments.

On desktop, investigations and problems are stored in files in the data folder. On the server they are stored in PostgreSQL, and the update adds the table for problems and an index for finding an open problem through the usual migrations.

On the server, defaults for organizations without saved settings come from `KUBRON_INCIDENT_LOG_EVIDENCE` (`on`/`off`), `KUBRON_INCIDENT_FRAGMENT_RETENTION_DAYS` (1–15), `KUBRON_INCIDENT_RETENTION_DAYS` (1–365), `KUBRON_INCIDENT_EXCLUDED_NAMESPACES` (comma-separated) and `KUBRON_INCIDENT_PURGE_INTERVAL` (default `1h`).

## Limits at a glance

The limits are fixed in the code and are not settings.

| Area | Limit |
|---|---|
| Dependency graph | 200 objects, 600 links, 4 steps from the target, 40 additional reads. The new kinds do not raise the limits |
| Metrics | Windows up to 24 hours; one query up to 10 s, 1 MiB and 500 series; up to 12 signal queries and 5 capability checks per operation |
| Shared-source check | A 30-minute correlation window, up to 10 additional reads, up to 5 candidates, up to 50 workloads per candidate |
| Problems | A 6-hour window, up to 100 members |
| Statistics | The last 200 closed investigations, percentages from 5 rated cases |
| Recurrence | 30 days after the fix, among the last 100 closed investigations |

Metrics and the shared-source check run after the diagnosis and are skipped if less than 5 seconds remain.

## Not included yet

These are not part of the current version, and the pages above do not describe them as available:

- Charts of metrics inside the report, and a live dependency map: the map shows the snapshot taken when the investigation was opened.
- Diagnosis of DaemonSet as a target (it is read only as an owner of Pods), and the contents of a ConfigMap or a Secret: Kubron checks only that a ConfigMap exists and never reads a Secret.
- An external Prometheus or your own kube-prometheus as a source of metrics.
- Background detection of incidents. Everything happens when you open an investigation.
- Statistics for a whole organization, and any automatic sending of data.
