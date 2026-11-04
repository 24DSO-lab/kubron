# AKS credential provider: device code, service principal and ROPC

**Статус: ждёт ревью человеком**

## Проблема

Нужен нативный вход в AKS по kubelogin без обращения к credentials среды сервера, локальным файлам, Azure CLI, IMDS или managed identity. Credential contract v1 заморожен; секретное состояние должно оставаться внутри Broker.

## Варианты

1. Использовать Azure MSAL или официальный kubelogin как зависимость/исполняемый процесс.
2. Реализовать ограниченный OAuth 2.0 v2 набор непосредственно через переданный `Env.HTTP`.
3. Делегировать все режимы внешнему exec/manual token.

## Решение

Выбран вариант 2 для `devicecode`, `spn` (client secret) и `ropc`. Все сетевые вызовы используют только `Env.HTTP`; URL должны быть HTTPS. Device code и refresh token сохраняются только в непрозрачном `Pending`/`Save`, шифруемых Broker. Refresh token rotation сохраняется. Scope — `<server-id>/.default`; legacy scope — `sp:<server-id>/.default`. Для devicecode/ropc используется `offline_access`, чтобы получить refresh token. Tenant по умолчанию — `organizations`, если kubeconfig не задал tenant. Идентификаторы Azure kubelogin: server ID `6dae42f8-4368-4678-94ff-3960e28e3630`, public client ID `80faf920-1908-4b52-b5ef-a8e7bedfc67a`.

Режимы `msi`, `azurecli`, `azd`, `workloadidentity` не поддерживаются и возвращают `ErrUnsupported`; provider не читает token file или environment. Сертификатные credentials и пути к файлам также не поддерживаются. JWT claims используются только как audit label, не как доказательство личности. Interactive PKCE отложен.

## Последствия

Серверный credential chain не может быть случайно использован. Пользователь должен явно предоставить tenant/credentials либо завершить device flow. ROPC может быть заблокирован MFA/Conditional Access; в этом случае пользователь должен выбрать другой поддерживаемый способ. Для HTTP-тестов нужен TLS test server или проверяемый HTTPS transport, поэтому локальные HTTP httptest endpoints не разрешены production policy.

## Отмена

Удалить регистрацию AKS provider и пакет `internal/credential/providers/aks`, затем удалить эту запись из README. Не менять контракт v1.
