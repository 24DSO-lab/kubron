---
layout: home

hero:
  name: Kubron
  text: Kubernetes под контролем.
  tagline: Быстрая и приватная desktop IDE для Kubernetes. Кластеры, ресурсы, Helm и GitOps — в одном рабочем пространстве.
  actions:
    - theme: brand
      text: Скачать Kubron
      link: https://github.com/24DSO-labs/kubron/releases
    - theme: alt
      text: Начать работу
      link: /ru/guide/getting-started
    - theme: alt
      text: GitHub
      link: https://github.com/24DSO-labs/kubron

features:
  - icon: ⎈
    title: Все кластеры в одном окне
    details: Переключайтесь между kubeconfig-контекстами, выбирайте namespace и следите за состоянием кластеров.
  - icon: ◈
    title: Сразу видно, что требует внимания
    details: Проблемные workloads, нехватка ресурсов, недавние изменения и истекающие сертификаты.
  - icon: ⌘
    title: Не только наблюдение
    details: Просмотр ресурсов, YAML с предварительным diff, логи, терминалы и port-forward.
  - icon: ⇧
    title: Helm встроен
    details: Репозитории и чарты, установка и обновление релизов, история ревизий и rollback.
  - icon: ⟲
    title: GitOps в том же интерфейсе
    details: "Приложения Argo CD, ApplicationSet и проекты: sync, diff и граф ресурсов."
  - icon: ◉
    title: Приватность по умолчанию
    details: В desktop-режиме нет телеметрии и обязательной учётной записи. Kubeconfig остаётся на вашей машине.
---

<div class="home-proof">
  <span>GO + WAILS + VUE</span>
  <span>WINDOWS · LINUX · macOS</span>
  <span>APACHE-2.0</span>
  <span>БЕЗ ТЕЛЕМЕТРИИ</span>
</div>

## Всё необходимое для ежедневной работы с Kubernetes

Kubron объединяет повседневные задачи оператора Kubernetes в одном desktop-приложении: помогает оценить состояние кластеров, понять, что изменилось, и выполнить нужные действия без постоянного переключения между инструментами.

<div class="screenshot-grid">
  <figure>
    <img src="https://raw.githubusercontent.com/24DSO-labs/kubron/master/docs/images/screenshots/overview.png" alt="Обзор Kubron: состояние кластера, workloads и ресурсы" loading="lazy">
    <figcaption><strong>Обзор кластера</strong><br>Состояние, ресурсы, проблемы и последние изменения.</figcaption>
  </figure>
  <figure>
    <img src="https://raw.githubusercontent.com/24DSO-labs/kubron/master/docs/images/screenshots/pods.png" alt="Таблица Pod в Kubron со статусом и потреблением ресурсов" loading="lazy">
    <figcaption><strong>Ресурсы</strong><br>Список Pod и workloads с переходом к деталям.</figcaption>
  </figure>
  <figure>
    <img src="https://raw.githubusercontent.com/24DSO-labs/kubron/master/docs/images/screenshots/yaml-editor.png" alt="YAML-редактор Kubron с просмотром diff перед сохранением" loading="lazy">
    <figcaption><strong>YAML-редактор</strong><br>Проверьте diff перед применением изменений.</figcaption>
  </figure>
  <figure>
    <img src="https://raw.githubusercontent.com/24DSO-labs/kubron/master/docs/images/screenshots/gitops-applications.png" alt="Список GitOps-приложений в Kubron" loading="lazy">
    <figcaption><strong>GitOps</strong><br>Состояние приложений и синхронизации в одном месте.</figcaption>
  </figure>
</div>

## Для реальных рабочих сценариев

- **Несколько кластеров** — имена, цвета, теги окружений и выбор одного или нескольких namespace. Поддерживается ограниченный RBAC; частичные ошибки доступа не скрываются.
- **Актуальные данные** — shared informers обновляют списки и дашборд без агрессивного опроса API-сервера.
- **Ресурсы Kubernetes** — встроенные типы и автоматически обнаруживаемые CRD.
- **Операционные действия** — логи, exec, port-forward, масштабирование и перезапуск workloads, cordon и drain узлов.
- **Жизненный цикл Helm** — репозитории (включая OCI), поиск чартов, dry-run, обновления, diff ревизий и rollback.
- **Argo CD** — приложения, ApplicationSet, проекты, управление sync, сравнение live и desired и граф ресурсов.
- **Метрики** — графики для Pod, workloads, узлов, PVC и Ingress при установленном metrics stack.

## Desktop в первую очередь. Web — когда нужно.

Desktop-приложение построено на Go, Wails и Vue. Экспериментальный серверный режим предоставляет тот же интерфейс по HTTP для команд: состояние хранится в PostgreSQL, учётные данные шифруются, поддерживается несколько реплик. Сервер рассчитан на работу за reverse proxy с TLS.

[Подробнее о серверном режиме →](/ru/server/)

## Приватность по умолчанию

Desktop-режим не требует аккаунта, не отправляет телеметрию и хранит kubeconfig на вашей машине. Серверный режим добавляет локальных пользователей, роли и детальные разрешения, управление сессиями и журнал аудита.

[Подробнее о безопасности →](/ru/features/security)

## Начало работы

- [Скачать последнюю версию](https://github.com/24DSO-labs/kubron/releases)
- [Руководство по быстрому старту](/ru/guide/getting-started)
- [Все возможности](/ru/features/)
- [Исходный код и участие в разработке](https://github.com/24DSO-labs/kubron)

<div class="home-license">
  Kubron — открытый проект под лицензией Apache-2.0.
</div>
