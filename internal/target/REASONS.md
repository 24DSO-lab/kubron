# Reason codes пакета `internal/target`

Стабильные безопасные коды (ADR-0055). Значения — публичный контракт: не меняются и не переиспользуются.
Коды не содержат значений полей, имён целей и метаданных чужих tenant'ов.

| Код | Где | Смысл |
|---|---|---|
| `cluster_unavailable` | ResolutionReport | кластер недоступен либо источник вернул ошибку; fail closed |
| `cluster_not_visible` | ResolutionReport | кластер неизвестен, чужой, несовместим по owner scope или недоступен requester'у (неотличимо) |
| `selector_invalid` | Error.Reason | selector не прошёл каноническую валидацию |
| `selector_limit_exceeded` | Error.Reason, ResolutionReport | превышен серверный предел (размер, сложность, число целей, query budget) |
| `target_not_found` | ResolutionReport | явный member отсутствует |
| `target_identity_incomplete` | ResolutionReport | нет UID либо identity неоднозначна/не совпала с запросом; цель не включается |
| `ownership_unresolved` | ResolutionReport, Error.Reason | доверенный owner не определён или изменился во время resolution |
| `group_revision_changed` | Error.Reason | revision группы изменилась во время resolution либо перед публикацией snapshot |
| `resolution_timeout` | Error.Reason | исчерпан серверный timeout |
