# Стек метрик и графики

Kubron рисует графики CPU, памяти, сети и диска для Pod, узлов, томов, ingress и нагрузок, а те же данные использует для «горячих точек» на дашборде. Данные берутся из небольшого стека Prometheus, который Kubron устанавливает в кластер **по вашей просьбе**. Всё остальное в Kubron работает без него.

[[toc]]

## Что вы получаете

| Где | Графики |
|---|---|
| Pod, на вкладке контейнеров | CPU, память, сеть, диск и дисковый I/O |
| Узел | CPU, память, сеть и диск, с линиями ёмкости и выделяемого объёма |
| PersistentVolumeClaim | Использование и ёмкость тома |
| Ingress | Трафик запросов и ответов, включая отправленные байты со статусами 2xx и 5xx |
| Deployment, ReplicaSet, StatefulSet, DaemonSet, Job | Использование всех их Pod вместе |
| Namespace и приложение Argo CD | Использование всех Pod в namespace или приложении |
| Дашборд кластера | Три Pod, ближе всего подошедшие к лимитам CPU и памяти, и три самых заполненных тома ([подробнее](/ru/features/dashboard#metrics-and-hotspots)) |

На графиках доступны периоды 1 час, 6 часов, 24 часа и 7 дней. Стек хранит данные **2 дня** (см. [Ограничения](#retention-and-limits)), поэтому период 7 дней показывает не больше того, что сохранено. График без данных пишет **No data available**.

## Как это работает

Стек работает в собственном namespace `kubron-metrics`. Kubron обращается к Prometheus через API-сервер Kubernetes (путь `services/proxy`) с теми же учётными данными, которыми вы работаете с кластером. Наружу ничего не публикуется: все три Service имеют тип ClusterIP, Ingress и NodePort нет.

Kubron читает только **собственный** стек (`prometheus` в `kubron-metrics`). К уже работающему у вас Prometheus он не подключается.

## Установка

1. Откройте **Settings** и найдите **Metrics stack**.
2. Нажмите **Install metrics stack**. Установка доступна администраторам.
3. Подождите несколько минут. Статус меняется с **Installing** на **Active** со счётчиком вида `3/3 ready`.

Kubron применяет манифест с вашими учётными данными, поэтому им нужны права Kubernetes на создание namespace, ClusterRole и ClusterRoleBinding. На практике это администратор кластера.

### Что устанавливается

| Компонент | Тип | Назначение | Ресурсы |
|---|---|---|---|
| `prometheus` v2.55.1 | StatefulSet, 1 реплика | Собирает и хранит метрики | Requests 100m CPU и 512 Mi; limits нет |
| `node-exporter` v1.7.0 | DaemonSet, по одному Pod на каждый Linux-узел | Метрики оборудования и ОС | Requests 10m и 24 Mi; limits 200m и 100 Mi |
| `kube-state-metrics` v2.11.0 | Deployment, 1 реплика | Метрики объектов Kubernetes | Requests 10m и 32 Mi; limits 200m и 150 Mi |

Prometheus опрашивает каждые 15 секунд: API-сервер, kubelet и cAdvisor, node-exporter, kube-state-metrics, контроллеры ingress-nginx (с меткой `app.kubernetes.io/name=ingress-nginx`), etcd там, где он доступен, и любые Pod или endpoint Service с аннотацией `prometheus.io/scrape: "true"`. Также создаются нужные ServiceAccount, ClusterRole и привязки, два ConfigMap (конфигурация и правила) и короткоживущий init-контейнер (`alpine`), который исправляет права каталога данных.

### Требования

- **Реестры образов.** Образы берутся из `quay.io` (Prometheus, node-exporter), `registry.k8s.io` (kube-state-metrics) и `docker.io` (`alpine`). В изолированном кластере сначала сделайте зеркала.
- **Linux-узлы.** Все три компонента планируются только на узлы `kubernetes.io/os=linux`.
- **Доступ к узлам.** `node-exporter` работает на каждом узле, в том числе с taint (он допускает `NoSchedule`), и монтирует `/proc`, `/sys` и `/` хоста только для чтения. Так устроен node-exporter, но это важно, если ваши политики ограничивают `hostPath`.

## Кто увидит графики

Метрики раскрывают имена Pod и namespace всего кластера, поэтому доступ выдаётся **не** всем. Стек создаёт Role `prometheus-proxy-access` в `kubron-metrics`; администраторы кластера уже могут ею пользоваться, остальным нужен RoleBinding. Например, для группы:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kubron-metrics-viewers
  namespace: kubron-metrics
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: prometheus-proxy-access
subjects:
  - kind: Group
    name: dev-team
    apiGroup: rbac.authorization.k8s.io
```

В Web-версии этот доступ нужен учётной записи, под которой Kubron работает с кластером (учётная запись команды или ServiceAccount in-cluster); дальше то, кто вправе открывать графики, решают роли самого Kubron.

## Ограничения хранения {#retention-and-limits}

- **2 дня или 5 ГиБ**, смотря что наступит раньше.
- **Хранилище — `emptyDir`.** Если Pod Prometheus перезапущен или перенесён, история теряется и графики начинаются заново. Используйте стек для недавней диагностики, а не как долговременный мониторинг.
- Период 7 дней не может показать больше сохранённых 2 дней.
- Опрос API-сервера и kubelet внутри кластера не проверяет их TLS-сертификаты. Это обычная настройка Prometheus внутри кластера, но учтите это, если ваша политика требует проверки.

## Состояние и удаление

| Статус | Что означает |
|---|---|
| **Not installed** | Namespace не существует |
| **Installing** | Стек создан менее пяти минут назад и ещё запускается |
| **Active** | Все компоненты готовы; карточка перечисляет их как `available/total` |
| **Degraded** | Компонент отсутствует или не готов после периода запуска. Кнопка **Reinstall** применяет манифест заново |

**Remove metrics stack** удаляет namespace `kubron-metrics` **вместе со всеми данными** после повторного подтверждения.

## Если что-то не работает

| Симптом | Причина и решение |
|---|---|
| Графики пишут **No data available** сразу после установки | Prometheus нужно несколько интервалов опроса (по 15 с). Подождите минуту и откройте график снова |
| Графики пусты у всех, хотя стек **Active** | У пользователя нет доступа к `services/proxy`. Добавьте RoleBinding выше |
| Графики Ingress пусты | Контроллер ingress должен опрашиваться этим Prometheus (метрики ingress-nginx). Другие контроллеры не охвачены |
| История пропала | Pod Prometheus перезапущен или перенесён. Данные лежат в `emptyDir` |
| Статус остаётся **Installing** или становится **Degraded** | Проверьте Pod в `kubron-metrics`: ошибки загрузки образов (доступ к реестрам), планирование (Linux-узлы, ресурсы) или init-контейнер |
| Установка завершается `forbidden` | У учётных данных нет права создавать ClusterRole или namespace. Используйте учётные данные администратора |

См. также [Дашборд кластера](/ru/features/dashboard) и [Конфигурацию](/ru/server/configuration) для Web-версии.
