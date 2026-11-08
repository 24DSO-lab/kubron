# Журналы, аудит и метрики

Здесь описано, что Web-сервер пишет в журналы, как скопировать журнал аудита во внешний сборщик и как мониторить сервер через Prometheus.

[[toc]]

## Журнал процесса

Сервер пишет структурированный журнал (`log/slog`) в **stderr**, по одной JSON-записи на строку:

```json
{"time":"2026-11-01T10:00:00.123Z","level":"ERROR","msg":"rpc call failed","method":"GetPods","error":"...","request_id":"3f9c1a7e5b2d4c80"}
```

| Переменная | Значения | По умолчанию |
|---|---|---|
| `KUBRON_LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
| `KUBRON_LOG_FORMAT` | `json` или `text` (читаемый вывод для разработки) | `json` |

В Helm-чарте: `kubron.logLevel` и `kubron.logFormat`. Неверное значение — сервер не запускается. Старый код, который ещё пишет через стандартный `log`, тоже выходит JSON-ом, без поля `request_id`.

### Идентификатор запроса

Каждый HTTP-запрос получает идентификатор; записи журнала, сделанные при его обработке, несут поле `request_id`. Он же возвращается в заголовке ответа `X-Request-Id`, поэтому пользователь может назвать его при обращении в поддержку, а администратор — найти все записи запроса.

Если запрос уже пришёл с `X-Request-Id` (ingress-nginx, Envoy), он принимается, только если состоит из 8–64 символов `A–Z a–z 0–9 . _ -`. Иначе создаётся новый: так заголовком нельзя подделать запись журнала.

### Секреты в журнале

Журнал не должен содержать секретов, и код их туда не передаёт. Дополнительно каждая запись (сообщение и все атрибуты, включая тексты ошибок) проходит через редактор, который заменяет на `[REDACTED]`:

- значения атрибутов с именами, содержащими `password`, `secret`, `token`, `authorization`, `cookie`, `kubeconfig`, `apikey`, `databaseurl`, `dsn`, `credential` или `csrf`;
- заголовки `Authorization: Bearer …` и `Basic …`, а также пары вида `password=…`, `token: …`, `client_secret=…`;
- пароль в адресах `scheme://user:password@host` (пользователь и хост остаются для диагностики);
- JWT и закрытые ключи PEM;
- значения, известные серверу: `KUBRON_SECRET_KEY` (и прежние ключи) и пароли из `KUBRON_DATABASE_URL` и `KUBRON_DB_LISTEN_URL`.

Редактор — страховка на случай, когда секрет попал в чужое сообщение об ошибке (драйвер базы, клиент кластера, OIDC-провайдер). Он может скрыть и безобидный текст вида `token: expired`; это сознательный выбор в пользу безопасности.

## Аудит в stdout

Журнал аудита хранится в PostgreSQL и остаётся основным. Если внешнему сборщику (Loki, Elastic, Splunk) нельзя доверить доступ к базе или нужна независимая копия, задайте `KUBRON_AUDIT_STDOUT=1` (чарт: `kubron.auditStdout=true`). Тогда каждая запись аудита дополнительно пишется JSON-строкой в **stdout**:

```json
{"type":"audit","level":"INFO","msg":"audit","time":"2026-11-01T10:00:00Z","user":"alice","role":"admin","action":"DeletePod","result":"ok","detail":"namespace=default name=nginx","source":"203.0.113.9","teamId":"t1","clusterId":"c1"}
```

- В stdout идут **только** строки аудита, а журнал процесса — в stderr, поэтому сборщик может разделить потоки. Строки аудита отличает и поле `"type":"audit"`.
- Запись попадает в поток, даже если хранилище аудита отказало (ошибка записи в базу видна в интерфейсе как `writeError`). Отказ самого потока на действие не влияет.
- Поле `detail` проходит тот же редактор секретов.
- Для административных команд (`kubron-server rekey` и др.) зеркало не включается: их stdout — результат команды.
- Хранение в сборщике — на вашей стороне. Журнал в базе имеет собственный срок хранения: см. `KUBRON_AUDIT_RETENTION` в [справочнике по конфигурации](/ru/server/configuration).

## Метрики Prometheus

Сервер отдаёт `/metrics` в текстовом формате Prometheus на **отдельном порту**. По умолчанию метрики выключены.

### Включение

| Способ | Как |
|---|---|
| Переменная | `KUBRON_METRICS_LISTEN=0.0.0.0:9090` (флаг `--metrics-listen`) |
| Helm | `metrics.enabled=true` (порт `metrics.port`, по умолчанию 9090) |
| ServiceMonitor | `metrics.serviceMonitor.enabled=true` (нужен Prometheus Operator) |

Адрес обязан отличаться от `KUBRON_LISTEN`: сервер не запустится, если метрики пришлось бы отдавать на публичном порту. Порт метрик обслуживает только `GET /metrics`, остальное — `404`.

### Безопасность

У эндпоинта нет аутентификации, поэтому защита сетевая:

- В чарте порт метрик не входит в основной Service и недоступен через Ingress. Для него создаётся отдельный Service `<release>-metrics`.
- NetworkPolicy пускает на порт метрик только `networkPolicy.metrics.from` (по умолчанию namespace `monitoring`) и не пускает из namespace ingress.
- В метриках нет секретов, имён пользователей, команд, кластеров и пространств имён. Допустимые значения лейблов фиксированы: имя RPC-метода из allowlist (остальное — `unknown`), код HTTP-ответа, состояние проверки кластера и версия сборки.

Если Prometheus стоит вне кластера и порт нужно открыть наружу, ограничьте источники на уровне сети и никогда не публикуйте порт метрик через Ingress.

### Метрики

Значения «на реплику» (сессии, WebSocket, терминалы, потоки) суммируйте по всем репликам, например `sum(kubron_sessions_active)`.

| Метрика | Тип | Лейблы | Что показывает |
|---|---|---|---|
| `kubron_rpc_requests_total` | counter | `method`, `code` | RPC-вызовы и коды ответов; ошибки — `code=~"4..\|5.."` |
| `kubron_rpc_request_duration_seconds` | histogram | `method` | Задержка RPC |
| `kubron_http_rate_limited_total` | counter | | Ответы `429` (ограничение запросов без сессии) |
| `kubron_login_blocked_total` | counter | | Попытки входа, отклонённые блокировкой после неудач |
| `kubron_login_lockouts_total` | counter | | Сколько блокировок началось |
| `kubron_login_lockout_degraded_total` | counter | | Проверки и записи неудач входа без общего хранилища (PostgreSQL недоступна, работает только локальная защита). Рост — повод для оповещения |
| `kubron_sessions_active` | gauge | | Активные веб-сессии реплики |
| `kubron_ws_connections` | gauge | | WebSocket-подключения событий |
| `kubron_terminals_active` | gauge | | Открытые терминалы |
| `kubron_log_streams_active` | gauge | | Запущенные потоки логов |
| `kubron_port_forwards_active` | gauge | | Активные port-forward |
| `kubron_clusters` | gauge | `state` | Кластеры по состоянию проверки: `ok`, `unknown`, `unreachable`, `unauthorized`, `misconfigured` |
| `kubron_cluster_health_leader` | gauge | | `1`, если эта реплика проверяет кластеры (иначе читает результаты лидера) |
| `kubron_draining` | gauge | | `1`, пока реплика останавливается (`/readyz` отвечает `503`) |
| `kubron_db_pool_*` | gauge, counter | | Пул соединений с базой: занято, свободно, всего, максимум, ожидание (только с PostgreSQL) |
| `kubron_build_info` | gauge | `version`, `go_version` | Всегда `1` |
| `go_*`, `process_*` | | | Стандартные метрики среды выполнения Go и процесса |

Счётчики начинаются с нуля при каждом запуске реплики: используйте `rate()` и `increase()`.

### Примеры запросов и оповещений

```text
# доля ошибок RPC за 5 минут
sum(rate(kubron_rpc_requests_total{code=~"5.."}[5m])) / sum(rate(kubron_rpc_requests_total[5m]))

# p95 задержки по методам
histogram_quantile(0.95, sum by (le, method) (rate(kubron_rpc_request_duration_seconds_bucket[5m])))

# нет лидера проверок кластеров (должна быть ровно одна реплика с 1)
sum(kubron_cluster_health_leader) != 1

# пул базы почти исчерпан
kubron_db_pool_acquired_connections / kubron_db_pool_max_connections > 0.9

# кластеры, недоступные или без авторизации
kubron_clusters{state=~"unreachable|unauthorized"} > 0

# блокировка входа работает без общего хранилища
increase(kubron_login_lockout_degraded_total[10m]) > 0
```

### Проверки состояния

- `GET /healthz` возвращает `200`, пока процесс работает (liveness).
- `GET /readyz` проверяет базу и признак draining. Пока реплика останавливается, он возвращает `503`.

Обе проверки освобождены от контроля допустимых хостов, потому что оркестратор ходит по адресу пода.
