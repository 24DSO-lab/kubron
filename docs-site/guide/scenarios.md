# Typical scenarios

Four everyday tasks and how Kubron helps with each. Every step links to the page that explains it in detail. Which actions you can take depends on your role: see the [permission reference](/features/security#permission-reference).

[[toc]]

## A Pod is failing

1. Open the cluster **Overview**. **Needs attention** lists problem Pods with the reason, the node, the restart count and how long the problem has lasted ([Cluster dashboard](/features/dashboard#problem-pods-and-warnings)).
2. Want a structured second opinion? Start an **Incident** for the Pod: it reads the Pod, its owners, Events and Nodes without changing anything, and reports facts with their sources, hypotheses with a confidence, recommendations and diagnostic steps ([Incidents](/features/intelligence)).
3. Open the Pod. The **Events** tab shows what Kubernetes says about it, and the logs show what the application says ([Working with resources](/features/working-with-resources#the-detail-panel), [Operations](/features/operations)).
4. If you need a shell: open a **terminal** in the container. If the image has no shell or tools, start a **debug container** with `busybox` or `netshoot` ([Debug containers](/features/debug-containers)).
5. If you suspect resources, compare the Pod with its limits. The dashboard lists the Pods closest to their limits, and with the [metrics stack](/features/metrics) you can open CPU and memory charts.
6. Fix it: **restart** or **scale** the workload, or **roll it back** ([Operations](/features/operations)).

## A Deployment got worse after a release

1. On the **Overview**, open **What changed** and choose 1, 6 or 24 hours. Rollouts, Helm updates, Argo CD syncs and Flux reconciles are listed in time order, so you can see what happened just before the problem.
2. Open the Deployment and its revision history, and **roll back** to the previous revision (needs the `operate` permission).
3. Check **Managed by** first. If Argo CD or Flux manages the Deployment, the controller can change it back at the next sync. Roll back through the owner instead: Argo CD has its own rollback, and for Flux you revert the commit in Git ([Who manages an object](/features/resources#managed-by), [GitOps](/features/gitops)).

## Upgrade a Helm release safely

1. Open **Helm**, choose the release and start an **upgrade**. You can keep the chart version or pick another one from the catalog ([Helm](/features/helm)).
2. Edit the values. Remember that the values you submit **replace** the previous ones as a whole: a key you delete returns to the chart default.
3. Review the **manifest diff** and the dry-run result before you confirm.
4. Follow the operation's progress, and check the Helm operation journal afterwards. If something is wrong, **roll back** the release.
5. If a Flux `HelmRelease` manages the release, Kubron warns you: Flux may overwrite a manual change at its next reconcile.

## My change keeps being reverted

1. Look at the **Managed by** column of the object: `Application/<name>` (Argo CD), `Kustomization/<name>` or `HelmRelease/<name>` (Flux), or `Helm/<release>` ([Who manages an object](/features/resources#managed-by)).
2. For Argo CD, open the application and compare **live and desired state**; **sync** after you have fixed the source ([GitOps](/features/gitops)).
3. For Flux, check the state and the revision of the Kustomization or HelmRelease. **Suspend** it while you investigate, then **resume** it. Kubron has no diff or rollback for Flux.
4. Make the real change in **Git**. Both tools bring the cluster back to what Git says, so a change made only in Kubron does not last.
