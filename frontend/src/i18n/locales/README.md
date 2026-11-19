# Локализация: схема ключей Kubron Intelligence

Locale-файлы лежат в `locales/{en,ru,cn}/`, подхватываются автоматически (`import.meta.glob`), все файлы одного языка сливаются в одно дерево сообщений. Паритет ключей между языками проверяется тестом и `npm run i18n:check`.

## Коды диагностики

Код с точками превращается в плоский ключ заменой `.` на `_` (`codeKey()` в `composables/incidentModel.ts`):

| Раздел | Ключ | Пример |
|---|---|---|
| симптом | `incidents.symptoms.<код>` | `pod.pending_unbound_pvc` → `incidents.symptoms.pod_pending_unbound_pvc` |
| факт | `incidents.facts.<код>` | `metric.memory_usage_vs_limit` → `incidents.facts.metric_memory_usage_vs_limit` |
| гипотеза | `incidents.hypotheses.<код>` | `pending.unbound_pvc` → `incidents.hypotheses.pending_unbound_pvc` |
| рекомендация | `incidents.recommendations.<код>` | |
| шаг | `incidents.steps.<код>` | |
| недостающий контекст | `incidents.missing.<код>` | `metrics.unavailable` → `incidents.missing.metrics_unavailable` |

Подстановки — именованные (`{container}`, `{percent}`); значения берутся из `params` кода.

## Файлы и владельцы

| Файл | Владелец | Содержимое |
|---|---|---|
| `incidents.json` | D8 | заголовки, кнопки, пометки, подсказки статистики, формы |
| `incidentsMetrics.json` | D1 | `facts.metric_*`, `hypotheses.metrics_*`, `missing.metrics_*` |
| `incidentsChain.json` | D3 | коды цепочечных правил, причины `Missing`/`Unknown` вершин графа |
| `incidentsCorrelation.json` | D4 | `facts.correlation_*`, `missing.correlation_*`, названия видов кандидатов |

Файлы владельцев пишут только в корне `incidents.{facts,hypotheses,recommendations,steps,missing,symptoms}`. Собственные ключи интерфейса (`incidents.chain.*`, `incidents.problem.*` и т. п.) — только в `incidents.json`. Конфликт ключей решает D8.

Неизвестный код в интерфейсе не показывается «сырым»: `te()` не нашёл ключ — выводится безопасный текст по умолчанию.
