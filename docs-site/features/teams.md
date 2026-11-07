# Teams and shared access

Team management is available in the Web edition when Kubron is configured with PostgreSQL. It is not part of the Desktop edition or the Web single-user file-storage mode.

## Organization → team → cluster

Kubron uses a simple ownership model:

- an organization contains teams;
- users are members of one or more teams;
- each cluster belongs to exactly one team;
- a user's role is assigned separately in each team.

A team member can see and operate only the clusters owned by teams they belong to. An organization administrator can manage users and teams, but does not automatically gain access to cluster data without team membership.

## Manage teams

Organization administrators (those with the `users.manage` permission) can create, rename and delete teams, manage organization users, and assign team membership. A team administrator can also rename the team. Team administrators can manage their team's members and clusters.

A team administrator can:

- add a cluster to the team, including the cluster Kubron itself runs in (using the server's ServiceAccount);
- update its connection and display settings;
- manage membership and team roles, subject to the organization-level rules.

A team can be deleted only when it is empty: move or delete its clusters first. Moving a cluster to another team is an explicit operation that requires administrator rights in both the current and the new team.

## Roles within a team

Kubron has three built-in roles:

| Role | Access |
|---|---|
| `admin` | Full access within the team, including team membership, cluster management and privileged operations |
| `operator` | Read access, Secret values and safe workload operations such as scale, restart and rollback |
| `user` | Read-only access; no Secret values or privileged operations |

The exact permissions are enforced by the backend, not just by hiding controls in the interface. Kubernetes RBAC still applies: a Kubron permission does not grant a Kubernetes API permission that the cluster credentials do not have.

## Isolation

The application checks team membership for each cluster-scoped operation. A cluster belonging to another team is not exposed in lists or by guessing its identifier. Helm repositories, OCI credentials and Helm operation history are also stored per team in PostgreSQL.

See [Security and access control](/features/security) and [Web deployment](/server/deploy).