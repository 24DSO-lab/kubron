---
layout: home

hero:
  name: Kubron
  text: Kubernetes под контролем.

  tagline: "Kubernetes Operations Workspace в двух версиях: Desktop-приложение и самостоятельно разворачиваемая Web-версия. Кластеры, ресурсы, Helm и GitOps — в одном месте."

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


Kubron объединяет повседневные операции с Kubernetes в одном рабочем пространстве. Выберите desktop-приложение для индивидуальной работы или собственную Web-версию для команды.


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

## Две версии. Одно рабочее пространство для Kubernetes.

### Desktop

Запускайте Kubron локально на Windows, Linux или macOS. Не нужно разворачивать сервер или создавать обязательную учётную запись; kubeconfig остаётся на вашем компьютере.

[Начать работу с Desktop →](/ru/guide/desktop)

### Web

Разверните Kubron в собственной инфраструктуре и открывайте его в браузере. Web-версия добавляет совместную работу пользователей, роли, разрешения и общее состояние в PostgreSQL. Это self-hosted решение, а не готовый облачный SaaS-сервис.

[Начать работу с Web →](/ru/guide/web)

## Для реальных рабочих сценариев

- **Несколько кластеров** — имена, цвета, теги окружений и выбор одного или нескольких namespace. Поддерживается ограниченный RBAC; частичные ошибки доступа не скрываются.
- **Актуальные данные** — shared informers обновляют списки и дашборд без агрессивного опроса API-сервера.
- **Ресурсы Kubernetes** — встроенные типы и автоматически обнаруживаемые CRD.
- **Операционные действия** — логи, exec, port-forward, масштабирование и перезапуск workloads, cordon и drain узлов.
- **Жизненный цикл Helm** — репозитории (включая OCI), поиск чартов, dry-run, обновления, diff ревизий и rollback.
- **Argo CD** — приложения, ApplicationSet, проекты, управление sync, сравнение live и desired и граф ресурсов.
- **Метрики** — графики для Pod, workloads, узлов, PVC и Ingress при установленном metrics stack.

## Почему Kubron

- **Лёгкий и нативный.** Go и системный webview: без встроенного браузерного движка, быстрый старт, скромный расход памяти.
- **Приватный по замыслу.** Без телеметрии и обязательной учётной записи; в Desktop kubeconfig остаётся на вашей машине.
- **Безопасный в работе.** Теги окружений, обязательный diff перед сохранением YAML и массовые действия, которые сообщают об ошибках, а не останавливаются.
- **Работает с ограниченным доступом.** Ограниченный RBAC — штатный сценарий: cluster-wide `list` не требуется.
- **Входит в ваши кластеры.** OIDC, Azure AKS, AWS EKS и Google GKE обрабатываются нативно, а Web-сервер никогда не выполняет команды из загруженного kubeconfig.
- **Проверяемый.** Контрольные суммы и provenance релизов, подписанные образ и чарт сервера с SBOM.
- **Открытый и многоязычный.** Apache-2.0, публичное API расширений, интерфейс на English, Русский и 中文.

[Что такое Kubron? →](/ru/guide/what-is-kubron)

## Go, Wails и Vue


Обе версии используют общий интерфейс Kubron и основные сценарии работы с Kubernetes. Web-версия добавляет централизованную совместную работу на инфраструктуре, которую контролируете вы.


[Подробнее о Web-версии →](/ru/server/)

## Приватность и управление доступом

Desktop не требует аккаунта, не отправляет телеметрию и хранит kubeconfig на вашем компьютере. Web добавляет локальные учётные записи, роли и детальные разрешения, управление сессиями и журнал аудита.

[Подробнее о безопасности →](/ru/features/security)

## Начало работы

- [Скачать последнюю версию](https://github.com/24DSO-labs/kubron/releases)
- [Выбрать Desktop или Web](/ru/guide/getting-started)
- [Требования и проверка](/ru/guide/installation)
- [Все возможности](/ru/features/)
- [Исходный код и участие в разработке](https://github.com/24DSO-labs/kubron)

<div class="home-license">
  Kubron — открытый проект под лицензией Apache-2.0.
</div>
