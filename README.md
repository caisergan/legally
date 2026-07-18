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

Copy `server/.env.example` to `server/.env` and set the values needed for your environment. `ANTHROPIC_API_KEY` must be set in `server/.env` for chat to work.

## Run

```sh
cd server
uv run uvicorn app.main:app --port 8600
```

Open [http://127.0.0.1:8600](http://127.0.0.1:8600).

For frontend development, run `npm run dev` from `web/` while the backend runs on port 8600. The Vite development server proxies `/api` requests to `http://127.0.0.1:8600`.
