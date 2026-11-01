# Server quick start (local)

Without a database the server uses file storage in your home directory and listens on loopback only:

```bash
mkdir -p frontend/dist && touch frontend/dist/.keep   # placeholder for go:embed
wails generate module                                 # add -tags webkit2_41 on Linux
cd frontend && npm ci && npm run build:web && cd ..
go run ./cmd/kubron-server --listen 127.0.0.1:8080
```

Open http://127.0.0.1:8080.

For production-like runs use PostgreSQL and the Docker / Helm instructions.
