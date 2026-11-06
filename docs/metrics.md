# Метрики Prometheus

Сервер отдаёт `/metrics` в текстовом формате Prometheus на **отдельном порту**. По умолчанию метрики
выключены.

## Включение

| Способ | Как |
| --- | --- |
| Переменная | `KUBRON_METRICS_LISTEN=0.0.0.0:9090` (флаг `--metrics-listen`) |
| Helm | `metrics.enabled=true` (порт `metrics.port`, по умолчанию 9090) |
| ServiceMonitor | `metrics.serviceMonitor.enabled=true` (нужен Prometheus Operator) |

Адрес обязан отличаться от `KUBRON_LISTEN`: сервер не стартует, если метрики пришлось бы отдавать на
публичном порту. Порт метрик обслуживает только `GET /metrics`, остальное — `404`.

## Безопасность

У эндпоинта нет аутентификации, поэтому защита сетевая:

- в чарте порт метрик не входит в основной Service и не доступен через Ingress; для него создаётся
  отдельный Service `<release>-metrics`;
- NetworkPolicy пускает на порт метрик только `networkPolicy.metrics.from` (по умолчанию namespace
  `monitoring`) и не пускает из namespace ingress;
- в метриках нет секретов, имён пользователей, команд, кластеров и пространств имён. Допустимые
  значения лейблов фиксированы: имя RPC-метода из allowlist (остальное — `unknown`), код HTTP-ответа,
  состояние проверки кластера и версия сборки. Тест `TestNoPrivateLabels` следит за этим.

Если Prometheus стоит вне кластера и порт нужно открыть наружу, ограничьте источники на уровне сети;
не публикуйте порт метрик через Ingress.

## Метрики

Значения «на реплику» (сессии, WebSocket, терминалы, потоки) суммируйте по всем репликам:
`sum(kubron_sessions_active)`.

| Метрика | Тип | Лейблы | Что показывает |
| --- | --- | --- | --- |
| `kubron_rpc_requests_total` | counter | `method`, `code` | RPC-вызовы и коды ответов; ошибки — `code=~"4..\|5.."` |
| `kubron_rpc_request_duration_seconds` | histogram | `method` | Задержка RPC |
| `kubron_http_rate_limited_total` | counter | | Ответы `429` (ограничение запросов без сессии) |
| `kubron_login_blocked_total` | counter | | Попытки входа, отклонённые блокировкой после неудач |
| `kubron_login_lockouts_total` | counter | | Сколько блокировок началось |
| `kubron_login_lockout_degraded_total` | counter | | Проверки и записи неудач входа без общего хранилища (PostgreSQL недоступна, работает локальная защита). Рост — повод для оповещения |
| `kubron_sessions_active` | gauge | | Активные веб-сессии реплики |
| `kubron_ws_connections` | gauge | | WebSocket-подключения событий |
| `kubron_terminals_active` | gauge | | Открытые терминалы |
| `kubron_log_streams_active` | gauge | | Запущенные потоки логов |
| `kubron_port_forwards_active` | gauge | | Активные port-forward |
| `kubron_clusters` | gauge | `state` | Кластеры по состоянию проверки: `ok`, `unknown`, `unreachable`, `unauthorized`, `misconfigured` |
| `kubron_cluster_health_leader` | gauge | | `1`, если эта реплика проверяет кластеры (иначе читает результаты лидера) |
| `kubron_draining` | gauge | | `1`, пока реплика останавливается (`/readyz` отвечает `503`) |
| `kubron_db_pool_*` | gauge, counter | | Пул соединений с БД (только с PostgreSQL): занято, свободно, всего, максимум, ожидание |
| `kubron_build_info` | gauge | `version`, `go_version` | Всегда `1` |
| `go_*`, `process_*` | | | Стандартные метрики среды выполнения Go и процесса |

Счётчики начинаются с нуля при каждом запуске реплики: используйте `rate()` и `increase()`.

## Примеры запросов и алертов

```promql
# доля ошибок RPC за 5 минут
sum(rate(kubron_rpc_requests_total{code=~"5.."}[5m])) / sum(rate(kubron_rpc_requests_total[5m]))

# p95 задержки по методам
histogram_quantile(0.95, sum by (le, method) (rate(kubron_rpc_request_duration_seconds_bucket[5m])))

# нет лидера проверок кластеров (должна быть ровно одна реплика с 1)
sum(kubron_cluster_health_leader) != 1

# пул БД исчерпан
kubron_db_pool_acquired_connections / kubron_db_pool_max_connections > 0.9

# кластеры, недоступные дольше 10 минут
kubron_clusters{state=~"unreachable|unauthorized"} > 0
```
