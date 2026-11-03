# ADR-0002: Интерфейс Platform вместо вызовов Wails из ядра

- Статус: принято
- Дата: 2026-10-25
- Связано: Задача 0.3

## Контекст

В ядре три точки связи с Wails: `a.ctx`/`EventsEmit` в `startup`, `OpenFileDialog`, `BrowserOpenURL`; плюс `GetFreePort`. У `Manager` уже есть `SetNotifier`.

## Решение

Интерфейс `Platform { Emit, PickFile, OpenURL, Capabilities }` с реализациями `wails` и `web`. Ядро не импортирует `wails/v2` (проверяется depguard).

## Рассмотренные альтернативы

Build tags на каждую функцию (трудно тестировать); оставить как есть (веб невозможен).

## Последствия

Платформа передаётся при создании `App`; `Emit` становится адресным (см. ADR-0009).
