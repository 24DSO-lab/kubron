# Быстрый запуск Web локально

Для локального знакомства без базы данных сервер использует файловое хранилище в домашнем каталоге и слушает только loopback-интерфейс:

```bash
mkdir -p frontend/dist && touch frontend/dist/.keep
wails generate module                         # на Linux добавьте -tags webkit2_41
cd frontend && npm ci && npm run build:web && cd ..
go run ./cmd/kubron-server --listen 127.0.0.1:8080
```

Откройте [http://127.0.0.1:8080](http://127.0.0.1:8080).

Для production-подобного запуска используйте PostgreSQL и инструкции по Docker/Helm. См. [развёртывание](/ru/server/deploy).
