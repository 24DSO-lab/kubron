# Промпт 6. Документация, развёртывание, финальные проверки

Сначала прочитай `docs/prompts/sso/README.md`. Промпты 1–4 (и 5, если делался) выполнены.

## Задача

- Документация `docs/sso.md` (и страница в `docs-site`, если там дублируются разделы развёртывания):
  - переменные `KUBRON_OIDC_*` с таблицей, значениями по умолчанию и примерами;
  - redirect URI: `<KUBRON_PUBLIC_URL>/auth/sso/callback`; требования к `KUBRON_PUBLIC_URL` (https, без пути);
  - пошаговая настройка Keycloak, Authentik, Microsoft Entra ID (Azure AD): тип клиента (confidential, Authorization Code),
    включение claim `groups` (в Entra — `groupMembershipClaims`, ограничение по числу групп и «overage»; в Keycloak —
    mapper «Group Membership» без полного пути, в Authentik — scope с groups), какой claim использовать как имя;
  - модель безопасности: PKCE, state/nonce, SSRF-политика, ключ cookie потока входа и многореплика, разбор
    `KUBRON_OIDC_LINK_EXISTING` (риск, когда IdP разрешает самостоятельный выбор имени), аварийный вход при
    `KUBRON_OIDC_DISABLE_LOCAL_LOGIN`, роль по группам и «IdP — источник истины»;
  - диагностика: типичные ошибки (`iss mismatch`, неверный redirect URI, нет claim, пустые группы), что смотреть в журнале.
- Helm-чарт и `deploy/`: значения `sso.*` (issuer, clientId, существующий Secret для `clientSecret`, группы, роль по
  умолчанию, флаги), проверка схемы значений, шаблон Secret/env; секрет — только через `secretKeyRef`. Пример для
  docker-compose/манифестов в `examples/`.
- `README.md`: короткий раздел «Single sign-on» со ссылкой на `docs/sso.md`.
- `CHANGELOG.md` → `[Unreleased]`: «Added: sign-in through OpenID Connect (Keycloak, Authentik, Entra ID, any standard IdP)».
- ADR-0059: статус поменять с «предложено» на «принято» только по решению владельца; иначе оставить как есть.
- Безопасность: добавить `KUBRON_OIDC_CLIENT_SECRET` в список секретов, которые редактируются в логах
  (`internal/logredact`) и проверяются тестом с контрольной строкой; обновить `SECURITY.md`, если там перечислены
  поверхности атаки.
- Финальный прогон:
  ```
  python3 scripts/license-headers.py --fix
  gofmt -l .
  go vet ./... && go test -race ./internal/...
  cd frontend && npm test && npm run i18n:check && npm run build:web
  ```
  Проверка лицензий зависимостей (`go-licenses`, `license-checker`): новых зависимостей быть не должно.

## Критерии готовности

Все проверки зелёные; коммит `docs(sso): guide, Helm values and changelog` с `Signed-off-by` запушен в `refactor`;
PR не открывать: владелец проверит ветку и откроет сам.
