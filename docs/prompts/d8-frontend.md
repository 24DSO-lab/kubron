# D8 — Интерфейс: виды цели, пересчёт корреляции, встраивание карты

Перед началом прочитай `00-common.md`, `README.md` и `contracts.md` (§5–§7). Ветка: `intel2/d8-frontend` от `refactor`. Ты **ни от кого не зависишь** и никого не ждёшь: все точки расширения уже в `refactor`, чужие результаты берёшь из `contracts.md` и подменяешь фейками.

## Цель

Интерфейс принимает новые виды цели и новые данные: форма открытия для всех видов, кнопка пересчёта корреляции, отображение новых видов кандидата, подключение карты D7. Всё — против контрактов, без ожидания бэкенда.

## Твои файлы (создаёшь и правишь только их)

- `frontend/src/types/incident.ts` — `TargetKind` (алиас `WorkloadKind` сохранить), типы RPC пересчёта.
- `frontend/src/components/incidents/OpenIncidentForm.vue` — выбор вида из `TargetKind`, `Node` без namespace; `frontend/src/components/incidents/*` кроме `map/**` — отображение новых видов кандидата корреляции, кнопки «Пересчитать корреляцию», встраивание `DependencyMap` в `IncidentView` одной строкой.
- `frontend/src/i18n/locales/{en,ru,cn}/incidents.json`, `incidentsIntel.json`, `incidentsInsights.json` — подписи видов (`incidents.kinds.*`), кнопки, подсказки.
- Точки входа «Расследовать» из карточек ресурсов (Service, PVC, Node, Ingress, HPA, StatefulSet, Job, CronJob), где такие уже есть, — только добавление кнопки через существующий компонент.
- Тесты: `frontend/src/components/incidents/*.test.ts`, `incidents.golden.test.ts` (обновляй только при необходимости, он рендерит все `report.json` цепочек), фикстуры `__fixtures__/*` вне `map/`.

## Не трогай

- Всё из `components/incidents/map/**`, `composables/dependencyMap*`, `incidentsMap.json` (D7); `incidentsChain*.json`, `incidentsMetrics.json`, `incidentsCorrelation.json` (D1–D6).
- `frontend/src/composables/incidentModel.ts` — `CODE_NAMESPACES` уже содержит новые пространства; нужна правка — вопрос в PR.
- Бэкенд и Go-код.

## Задачи

1. **Типы и форма.** `TargetKind` по `contracts.md` §6; `OpenIncidentForm` — все виды, подписи `incidents.kinds.<Kind>`; у `Node` поле namespace скрыто, у остальных обязательно; валидация как в первой итерации.
2. **Пересчёт.** Кнопка «Пересчитать корреляцию» в `CorrelationBlock`: `callApp('IncidentCorrelationRecompute', clusterID, id)`, состояние загрузки, ошибки (`forbidden`, закрытое расследование — кнопка недоступна), результат заменяет расследование в состоянии. Для бэкенда, который ещё не слит (метод не найден) — показывается безопасная ошибка «недоступно», без падения.
3. **Виды кандидата.** `shared_dependency`, `shared_root`, `shared_symptom` отображаются штатным компонентом: убедись, что неизвестный вид даёт безопасный текст; для `shared_symptom` — явная пометка «низкая уверенность, без структурной связи» (ключ в `incidentsIntel.json`).
4. **Карта.** Встрой `DependencyMap` (D7) в `IncidentView` одной строкой за `v-if="report.graph"`; пока D7 не слит — не импортируй чужой файл напрямую: добавляй встраивание **в отдельном коммите после слияния D7** либо через динамический импорт `defineAsyncComponent(() => import('./map/DependencyMap.vue'))` с обработкой отсутствия модуля (предпочтительно, чтобы PR не ждал).
5. **Метрики B.** Проверь, что `FindingsPanel`/факты показывают `metric.*` новых сигналов (коды в переводах D5; безопасный текст пока перевода нет) и подсказки «с мониторингом точнее».
6. **Golden.** `incidents.golden.test.ts` рендерит все `report.json` цепочек: убедись, что рендер проходит для случаев всех видов (D1–D4 добавляют свои) — тест должен быть устойчив к неизвестным кодам и новым видам ресурсов.
7. **Доступность и i18n.** Все новые тексты — en/ru/cn, `npm run i18n:check`.

## Тесты и фикстуры

Vitest: форма (все виды, Node без namespace), кнопка пересчёта (успех/ошибка/закрытое), рендер нового вида кандидата, async-встраивание карты без модуля. `npm test` и `npm run build` (`vue-tsc`).

## Не входит

Реализация карты (D7), бэкенд RPC (D6), переводы кодов видов (D1–D6), документация (D9).

## Готово, когда

- Все пункты «Задач» выполнены и покрыты тестами;`cd frontend && npm test && npm run build` зелёные.
- `git diff --stat origin/refactor` содержит только файлы из раздела «Твои файлы».
- Переводы en/ru/cn с паритетом ключей; нет сырых значений Kubernetes в переводах.
- Описание PR по `00-common.md` (в том числе строка для CHANGELOG).
