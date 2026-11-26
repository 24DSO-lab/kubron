# Документация Kubron

Карта документов репозитория. Пользовательский сайт документации (VitePress) живёт отдельно, в [`docs-site/`](../docs-site).

## Для пользователей

| Документ | О чём |
|---|---|
| [cluster-sign-in.md](cluster-sign-in.md) | Какие окна входа в кластер бывают и что в них делать |
| [intelligence.md](intelligence.md) | Расследования Kubron Intelligence: что диагностируется, как хранятся логи как доказательства, права и retention |
| [i18n.md](i18n.md) | Локализация интерфейса: как добавить язык и ключи |

## Для эксплуатации сервера

| Документ | О чём |
|---|---|
| [deploy.md](deploy.md) | Развёртывание `kubron-server`: образ, Helm-чарт, переменные, сетевая политика |
| [ha.md](ha.md), [ha-sessions.md](ha-sessions.md) | Высокая доступность: несколько реплик, сессии в PostgreSQL |
| [lifecycle-readiness.md](lifecycle-readiness.md) | Готовность к обновлению Kubernetes: что проверяется, coverage, матрица API и порядок её обновления |
| [baseline.md](baseline.md) | Baseline конфигурации и дрейф: правила, хранение, retention, переменные |
| [intelligence-operations.md](intelligence-operations.md) | Intelligence для администраторов: метрики, проблемы, статистика правил, права, аудит и поведение при отказах |
| [backup.md](backup.md) | Резервное копирование и восстановление, ключ шифрования |
| [logging.md](logging.md), [metrics.md](metrics.md) | Журнал процесса, аудит в stdout, метрики Prometheus |
| [http-security.md](http-security.md) | Заголовки безопасности, защита от перебора |

## Для разработчиков

| Документ | О чём |
|---|---|
| [adr/](adr/README.md) | Архитектурные решения (ADR-0001…0034): почему устроено так |
| [intelligence-rule-improvement.md](intelligence-rule-improvement.md) | Как мантейнеры улучшают правила Intelligence по выгруженной статистике (ADR-0034 §9) |
| [architecture-reviews/2026-11-26-intelligence-lifecycle-stabilization.md](architecture-reviews/2026-11-26-intelligence-lifecycle-stabilization.md) | Техническая фиксация исправлений LIFE-01 и INT-01 |
| [architecture-reviews/2026-11-26-stage-closure.md](architecture-reviews/2026-11-26-stage-closure.md) | Итог этапа: стабилизация Intelligence и Lifecycle, закрытие замечаний и очистка устаревших промптов |
| [architecture/execution-identity-durable-operations-design.md](architecture/execution-identity-durable-operations-design.md) | Проектирование Execution Identity, Operation/Run/Step, durable execution, Desktop/Web lifecycle, HA и recovery |
| [architecture/2026-11-26-schedule-ownership-decision.md](architecture/2026-11-26-schedule-ownership-decision.md) | Принятое решение: расписания принадлежат Organization/Team, creator — только provenance; правила Desktop/Web и управления |
| [architecture/2026-11-26-missed-schedule-occurrences-decision.md](architecture/2026-11-26-missed-schedule-occurrences-decision.md) | Принятая политика пропущенных запусков: Observe — Coalesce, Assisted/Autonomous — Skip, Catch-up — явный opt-in с ограничениями |
| [e2e.md](e2e.md) | Smoke-e2e |
| [authentication-lab.md](authentication-lab.md) | Ручной стенд входа в облачные кластеры (EKS, GKE, AKS) без создания кластеров |
| [releasing.md](releasing.md) | Как выпускается релиз |
| [../CONTRIBUTING.md](../CONTRIBUTING.md) | Правила вклада, DCO, заголовки лицензии |

## Что где хранить

- **ADR** хранятся всегда. Принятый ADR не переписывается и не удаляется: если решение изменилось, пишется новый ADR, а старому ставится статус «заменено». Номера ADR встречаются в коде, чарте и CI, поэтому имена файлов не меняются.
- Новые руководства и справка для людей, а не для команды, идут в `docs-site/`, а не сюда.
