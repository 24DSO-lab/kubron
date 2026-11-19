# Локализация: схема ключей Kubron Intelligence

Locale-файлы лежат в `locales/{en,ru,cn}/` и подхватываются автоматически (`import.meta.glob`). **Имя файла — пространство имён**: `incidentsMetrics.json` доступен как `incidentsMetrics.*`, а не как `incidents.*`. Паритет ключей между языками проверяется тестом и `npm run i18n:check`.

## Коды диагностики

Код с точками превращается в плоский ключ заменой `.` на `_`. Интерфейс (`codeText()` в `composables/incidentModel.ts`) ищет ключ по очереди в пространствах `incidents`, `incidentsMetrics`, `incidentsChain`, `incidentsCorrelation`; для кодов с префиксами `metric(s).`, `graph.`, `correlation.` сначала смотрит пространство владельца. Не найден нигде — выводится безопасный текст `incidentsIntel.unknownEntry`, «сырой» код не показывается.

| Раздел | Ключ внутри пространства | Пример |
|---|---|---|
| симптом | `symptoms.<код>` | `pod.pending_unbound_pvc` → `symptoms.pod_pending_unbound_pvc` |
| факт | `facts.<код>` | `metric.memory_usage_vs_limit` → `incidentsMetrics.facts.metric_memory_usage_vs_limit` |
| гипотеза | `hypotheses.<код>` | `pending.unbound_pvc` → `hypotheses.pending_unbound_pvc` |
| рекомендация | `recommendations.<код>` | |
| шаг | `steps.<код>` | |
| недостающий контекст | `missing.<код>` | `metrics.unavailable` → `incidentsMetrics.missing.metrics_unavailable` |

Подстановки — именованные (`{container}`, `{percent}`) из `params` кода. Параметр `reason` подставляется словами: ищется `reasons.<значение>` в пространстве владельца (для `metrics.*` — `incidentsMetrics`, для `correlation.*` — `incidentsCorrelation`), затем в `incidents.reasons` и `incidentsIntel.chain.unknownReasons`; не найден — «причина не указана». Причины чужих владельцев не подставляются.

## Файлы и владельцы

| Файл (пространство) | Владелец | Содержимое |
|---|---|---|
| `incidents.json` | D8 | существующие заголовки, кнопки, пометки и формы расследований |
| `incidentsIntel.json` | D8 | новые тексты интерфейса Intelligence: `chain.*`, `unknownEntry`, далее проблемы, корреляция, статистика |
| `incidentsMetrics.json` | D1 | `facts.metric_*`, `hypotheses.metrics_*`, `missing.metrics_*`, `reasons.*`, `hints.*` |
| `incidentsChain.json` | D3 | коды цепочечных правил, `missing.graph_*` |
| `incidentsCorrelation.json` | D4 | `facts.correlation_*`, `missing.correlation_*`, `kinds.*`, `reasons.*` |

Владельцы пишут только в свои файлы. Собственные новые ключи интерфейса (`chain.*`, `problem.*` и т. п.) — только в `incidentsIntel.json`. Конфликт решает D8.
