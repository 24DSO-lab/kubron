# Справочник команд

`kubron-server` — бинарник Web-версии. Контейнерный образ запускает его как точку входа, поэтому внутри образа путь — `/kubron-server`. В образе нет оболочки: запускайте команды напрямую, например `kubectl exec deploy/kubron -- /kubron-server rekey`, либо одноразовым Job с теми же переменными окружения.

[[toc]]

## Запуск сервера

Без подкоманды бинарник запускает сервер. Настройки берутся из переменных окружения и флагов; см. [справочник по конфигурации](/ru/server/configuration). Список флагов вашей версии покажет `kubron-server --help`.

```bash
kubron-server --listen 127.0.0.1:8080
```

### Флаги

У каждого перечисленного флага есть переменная окружения с тем же смыслом (`--listen` — `KUBRON_LISTEN` и т. д.). Секреты и токены, такие как `KUBRON_SECRET_KEY` и `KUBRON_SETUP_TOKEN`, задаются **только окружением** намеренно: аргументы командной строки видны в списке процессов.

| Флаг | Назначение |
|---|---|
| `--listen` | Адрес прослушивания HTTP |
| `--allow-remote` | Разрешить не-loopback адрес |
| `--data-dir` | Каталог данных для файлового хранилища (по умолчанию `~/.kubron`) |
| `--database-url` | Строка подключения PostgreSQL. Лучше использовать переменную окружения |
| `--trust-proxy`, `--trusted-proxy-hops`, `--trusted-proxies` | Доверять обратному прокси в вопросах адреса клиента и схемы |
| `--csp-report-only` | Отдавать Content-Security-Policy в режиме report-only |
| `--session-ttl` | Срок жизни веб-сессии |
| `--metrics-listen` | Адрес listener Prometheus |
| `--replica-id` | Уникальный идентификатор реплики |
| `--drain-delay` | Пауза между SIGTERM и закрытием соединений |
| `--audit-retention` | Как долго записи аудита хранятся в PostgreSQL |
| `--cluster-check-interval` | Как часто проверяется доступность кластеров |
| `--max-terminals`, `--max-log-streams`, `--max-port-forwards` | Ограничения на пользователя (`0` — без ограничения) |
| `--disable-exec`, `--disable-node-shell`, `--disable-port-forward` | Отключить интерактивные возможности для всех |
| `--terminal-max-duration`, `--terminal-idle-timeout` | Ограничения срока жизни терминала |

### Эндпоинты

| Путь | Назначение |
|---|---|
| `/healthz` | Liveness: `200`, пока процесс работает |
| `/readyz` | Readiness: проверяет базу и возвращает `503`, пока реплика останавливается |
| `/metrics` | Метрики Prometheus, только на отдельном порту метрик (см. [Журналы, аудит и метрики](/ru/server/observability)) |

## Административные команды

Три подкоманды работают напрямую с базой. Настройки они читают из окружения (`KUBRON_DATABASE_URL`, `KUBRON_SECRET_KEY`, `KUBRON_SECRET_KEY_PREVIOUS`) и завершаются с ошибкой, если `KUBRON_DATABASE_URL` не задан.

### `create-admin`

Создаёт первого администратора прямо в базе, минуя сеть и токен первичной настройки.

```bash
printf '%s\n' 'длинная-парольная-фраза' | kubron-server create-admin --username admin
```

- `--username` обязателен.
- Пароль читается из `KUBRON_ADMIN_PASSWORD` либо из первой строки стандартного ввода. Аргументом он **никогда** не принимается: аргументы видны в списке процессов и истории оболочки; лишний аргумент отклоняется.
- Имя и пароль проверяются так же, как на веб-экране настройки (имя 3–32 символа; пароль 8–256 байт).
- Команда отказывает, если пользователи уже есть.
- Создание записывается в журнал аудита как `Setup` с источником `cli`.

В Kubernetes:

```bash
kubectl -n kubron exec -i deploy/kubron -- \
  /kubron-server create-admin --username admin < password.txt
```

### `rekey`

Перешифровывает сохранённые секреты текущим `KUBRON_SECRET_KEY`. Применяется после [ротации ключа](/ru/server/backup#ротация-ключа-и-rekey).

```bash
kubron-server rekey
# re-encrypted with key "2026-11": 12 kubeconfigs, 3 cluster secrets, 2 Argo CD tokens, 4 Helm repository secrets, 5 sign-in credentials (0 still in legacy format)
```

Команду безопасно повторять: уже перешифрованные записи пропускаются.

### `import-files`

Импортирует файловое хранилище однопользовательской или десктопной установки в PostgreSQL, например при переходе от личной установки к общему серверу.

```bash
kubron-server import-files --from ~/.kubron
# imported from /home/alex/.kubron: 2 users, 5 clusters, 1 Argo CD tokens, 340 audit events
# source files are untouched and can serve as a backup
```

- `--from` — каталог данных с `users.json`, `clusters/` и `audit.log`. По умолчанию `~/.kubron`.
- Команда печатает сводку импортированного и замечания о записях, которые не удалось перенести.
- Исходные файлы не изменяются, поэтому остаются резервной копией.
- Импортированные kubeconfig хранятся зашифрованными вашим `KUBRON_SECRET_KEY`. Помните, что Web-версия предъявляет к учётным данным в kubeconfig более строгие требования, чем Desktop; см. [Подключение к Kubernetes](/ru/features/cluster-auth#web).

## Завершение и сигналы

`SIGTERM` запускает плавную остановку: см. [Высокая доступность](/ru/server/ha#остановка-реплики-sigterm). Административные команды печатают результат в стандартный вывод и при ошибке завершаются с ненулевым кодом.
