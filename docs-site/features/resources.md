# Kubernetes resources and YAML

Kubron provides a resource browser for common Kubernetes objects and dynamically discovered custom resources. It combines list views, contextual details and controlled editing in one workflow.

## Built-in resource coverage

Resource views cover the main Kubernetes areas:

- **Workloads:** Pods, Deployments, StatefulSets, DaemonSets, ReplicaSets, Jobs and CronJobs.
- **Networking:** Services, Endpoints, EndpointSlices, Ingresses, IngressClasses and NetworkPolicies.
- **Configuration:** ConfigMaps, Secrets and Events.
- **Storage:** PersistentVolumes, PersistentVolumeClaims and StorageClasses.
- **Access control:** ServiceAccounts, Roles, RoleBindings, ClusterRoles and ClusterRoleBindings.
- **Scheduling and policy:** HPAs, PodDisruptionBudgets, ResourceQuotas, LimitRanges, PriorityClasses, RuntimeClasses and Leases.
- **Admission:** MutatingWebhookConfigurations and ValidatingWebhookConfigurations.

The exact list shown depends on the Kubernetes API resources and permissions available to the connected identity.

## Custom resources

Kubron discovers CustomResourceDefinitions from the active cluster. For supported CRDs it reads the preferred served version, scope, short names and `additionalPrinterColumns`, so custom resources can be displayed in a table similar to `kubectl get`.

## Inspect an object

Detail panels bring together conditions, events, owner references, containers, volumes, affinity, tolerations, labels and annotations where relevant to the resource type.

## YAML editor

The Monaco editor supports viewing and editing resource YAML. Before applying changes, review the generated diff. You can also create a new object from a manifest. Deleting resources and applying YAML require the appropriate write permission.

## Bulk actions

Some resource views support selecting multiple objects and applying the same action to the selection. Results are reported per object: one failure does not silently hide the outcome of the other operations.

## Secrets are separately protected

Listing a Secret object does not automatically grant access to its values. Reading Secret data requires the separate Kubron permission `secrets.read` and the corresponding Kubernetes permission. The built-in `user` role does not have this permission.

[Review roles and permissions](/features/security) · [Explore day-to-day operations](/features/operations)