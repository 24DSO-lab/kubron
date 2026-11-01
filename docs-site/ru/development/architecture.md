# Архитектура

Основные каталоги репозитория:

```
cmd/                  # точки входа: desktop и server
frontend/             # интерфейс на Vue 3
internal/             # внутренние Go-пакеты
pkg/ext/              # публичный API расширений
deploy/helm/kubron    # Helm chart серверного режима
docs/                 # ADR, инструкции по развёртыванию и PDF-гайды
scripts/              # release, license headers, DCO checks
```

В desktop-режиме frontend встраивается в приложение Wails и обращается к Kubernetes API с использованием kubeconfig пользователя.

В серверном режиме тот же интерфейс обслуживается по HTTP; пользователи, кластеры и аудит хранятся в PostgreSQL, чувствительные данные шифруются в хранилище.

Архитектурные решения описаны в ADR в каталоге `docs/adr` основного репозитория.
