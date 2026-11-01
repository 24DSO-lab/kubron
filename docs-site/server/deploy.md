# Docker & Kubernetes

## Build the image

```bash
docker build -t kubron:dev .
```

## Helm chart

The chart lives in the repository at [`deploy/helm/kubron`](https://github.com/24DSO-labs/kubron/tree/master/deploy/helm/kubron).

Detailed deployment notes (key management, backups, current limitations) are in the upstream document:

[docs/deploy.md](https://github.com/24DSO-labs/kubron/blob/master/docs/deploy.md) (currently in Russian).

Publishing the image and chart to a registry is on the [roadmap](https://github.com/24DSO-labs/kubron#roadmap).
