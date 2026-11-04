# Cluster dashboard

The dashboard is a starting point for understanding the active cluster: its health, capacity and the changes that may explain a recent incident.

## Cluster health and capacity

- Node readiness and workload status.
- Counts and readiness of nodes and workloads.
- Capacity and storage summaries.
- Warnings and blockers that need attention.

## Troubleshooting signals

- Problem Pods and their readiness issues.
- Pods approaching their CPU or memory limits.
- Nodes with low remaining headroom.
- Pending or lost PersistentVolumeClaims.
- Recent Deployment rollouts, Helm releases and Argo CD syncs.

The recent-changes view combines events that Kubron can observe from the Kubernetes API and installed integrations. It is a useful timeline for investigation, not a complete audit trail of every cluster change.

## Certificates

Kubron can inspect TLS Secrets and report certificates that are expired or approaching expiry. It returns certificate metadata, not private key material. This view requires the Kubron `secrets.read` permission and the corresponding Kubernetes permissions to list TLS Secrets.

## Metrics and trends

Resource usage charts and hotspot calculations use Prometheus data from the optional metrics stack (Prometheus, kube-state-metrics and node-exporter). Without that stack, the core dashboard remains available, but metric-dependent charts and hotspots are not populated.

[Learn about the optional metrics stack and operational actions](/features/operations).