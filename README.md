# Yargı Asistan

Yargı Asistan is a Turkish legal-research workspace with a FastAPI and SQLite backend, a React frontend, access to Yargı MCP sources, and Anthropic-powered chat.

## Setup

Run each block from the repository root. Install the backend first:

```sh
cd server
uv sync
```

Then install and build the frontend:

```sh
cd web
npm install
npm run build
```

Copy `server/.env.example` to `server/.env` and set the values needed for your environment. `ANTHROPIC_API_KEY` must be set in `server/.env` for chat to work. Optionally set `ANTHROPIC_BASE_URL` to point the Anthropic client at a custom or proxy endpoint.

### Anthropic-compatible endpoints

The app can talk to any Anthropic-compatible endpoint by setting `ANTHROPIC_BASE_URL` and `ANTHROPIC_API_KEY`. Model IDs are configurable through `CHAT_MODEL`, `TITLE_MODEL`, `MODEL_SONNET5`, `MODEL_OPUS`, and `MODEL_HAIKU`, so the UI choices can use GPT models exposed by a proxy such as cliproxyapi.

## Run

```sh
cd server
uv run uvicorn app.main:app --port 8600
```

Open [http://127.0.0.1:8600](http://127.0.0.1:8600).

For frontend development, run `npm run dev` from `web/` while the backend runs on port 8600. The Vite development server proxies `/api` requests to `http://127.0.0.1:8600`.

## E-imza development

The e-imza work is an isolated, disabled-by-default Go service under [`signing-service/`](signing-service/). It is not yet connected to FastAPI or React and does not currently access PKCS#11, accept PINs, or make production/legal-validity claims.

```sh
go -C signing-service test ./...
go -C signing-service test -race ./...
go -C signing-service vet ./...
```

Architecture, phase gates, provenance, and non-claims are recorded in:

- [`docs/EIMZA_INTEGRATION_PLAN.md`](docs/EIMZA_INTEGRATION_PLAN.md)
- [`docs/tasks/EIMZA_BUILD_LEDGER.md`](docs/tasks/EIMZA_BUILD_LEDGER.md)
- [`signing-service/third_party/eimza-go/UPSTREAM.md`](signing-service/third_party/eimza-go/UPSTREAM.md)
