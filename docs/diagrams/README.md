<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Dropway system diagrams

Diagrams-as-code (Mermaid). The `.mmd` files are the source of truth; the `.png`
files are pre-rendered for quick viewing. GitHub renders the fenced `mermaid`
blocks below natively.

## 1. Components & directional requests

How the runtime pieces talk to each other. The **components are identical across
self-host and the hosted SaaS**; only the infrastructure they run on differs, so
there are two versions of this diagram. In both, `mcp` is the OAuth-protected MCP
server: an LLM agent reads **public** content as a crawler (via `llms.txt` on the
edge) and **gated** content only through `mcp`, after a browser OAuth flow against
the dashboard (the authorization server), scoped to one org by the same RLS as the
rest of the platform. Org-shared **skills** and **chat logs** (the "How this was
made" panel served on a site) travel the same paths: the `api` is the system of
record and the object store holds skill files + compiled chat transcripts, which
`mcp` reads under RLS and the edge injects into served pages.

### 1a. Self-host (Docker Compose)

`serve` is the plain-Go content edge behind Caddy; Redis/Valkey holds the route
projection + revocation denylist (`api` writes, `serve` reads revocation, and
resolves the route — with its `chat_id` — from Postgres); MinIO is the object store.
No billing (self-host is unlimited).

![Components — self-host](./components-selfhost.png)

```mermaid
flowchart LR
  user(["User / Browser"])
  agent(["LLM agent<br/>Claude · Cursor · Codex"])

  subgraph edge["Content edge"]
    direction TB
    caddy["Caddy<br/>TLS · on-demand certs · cache"]
    serve["serve (Go)<br/>*.dropwaycontent.com<br/>content · access · llms.txt<br/>How-this-was-made chat panel"]
  end

  subgraph control["Control plane"]
    direction TB
    dash["dashboard (Next.js)<br/>Better Auth · /authz · OAuth AS<br/>sites · skills · chats · feed"]
    api["api (Go)<br/>system of record + authz<br/>sites · skills · chat logs"]
  end

  subgraph llm["LLM access"]
    direction TB
    mcp["mcp (Go)<br/>OAuth-protected MCP server<br/>site · skill · chat tools"]
  end

  subgraph datap["Data plane"]
    direction TB
    pg[("Postgres<br/>identity + app schemas · RLS<br/>skills · chat_logs")]
    redis[("Redis / Valkey<br/>route projection · revocation")]
    store[("Object store · MinIO<br/>blobs · manifests<br/>skill files · chat transcripts")]
  end

  smtp(["SMTP / Mailpit"])

  user -->|session cookie| dash
  user -->|content GET| caddy
  user -. presigned upload .-> store
  agent -->|public: llms.txt · crawl| caddy
  agent -->|OAuth 2.1 sign-in + consent| dash
  agent -->|Bearer JWT · MCP| mcp
  caddy -->|reverse proxy · tls-check| serve
  dash -->|Bearer EdDSA JWT| api
  dash -->|identity schema| pg
  dash -. verify / magic link .-> smtp
  api -->|app schema · dropway_app · RLS| pg
  api -->|manifests · skill files · chat transcripts| store
  api -->|route + chat_id · revocation| redis
  serve -->|resolve_host + chat_id| pg
  serve -->|blobs · manifests · chat transcripts| store
  serve -->|read revocation| redis
  serve -->|fetch edge JWKS| api
  mcp -->|verify token · fetch JWKS| dash
  mcp -->|app schema · dropway_app · RLS| pg
  mcp -->|skill files · chat transcripts · blobs| store

  classDef plane fill:#f8fafc,stroke:#cbd5e1,color:#0f172a;
  class edge,control,datap,llm plane;
```

### 1b. Hosted SaaS (cloud)

The hosted build swaps the edge for the Cloudflare serving Worker (route projection
+ revocation in Workers KV, blobs/manifests/transcripts in R2), runs the dashboard
on Vercel and `api`/`mcp` on Fly.io against Supabase Postgres, and adds the
`cloud/` billing module: `api` drives **Stripe** for checkout + metered AI usage and
plan quotas, with a signed webhook writing `plan_tier` back. Errors and events flow
to PostHog.

![Components — hosted SaaS](./components-cloud.png)

```mermaid
flowchart LR
  user(["User / Browser"])
  agent(["LLM agent<br/>Claude · Cursor · Codex"])

  subgraph edge["Content edge · Cloudflare"]
    direction TB
    worker["serving Worker<br/>*.dropwaycontent.com<br/>content · access · llms.txt<br/>How-this-was-made chat panel"]
  end

  subgraph control["Control plane"]
    direction TB
    dash["dashboard (Next.js · Vercel)<br/>Better Auth · /authz · OAuth AS<br/>sites · skills · chats · feed"]
    api["api (Go · Fly.io)<br/>system of record + authz<br/>sites · skills · chat logs"]
  end

  subgraph llm["LLM access"]
    direction TB
    mcp["mcp (Go · Fly.io)<br/>OAuth-protected MCP server<br/>site · skill · chat tools"]
  end

  subgraph datap["Data plane"]
    direction TB
    pg[("Supabase Postgres<br/>identity + app schemas · RLS<br/>skills · chat_logs")]
    kv[("Cloudflare Workers KV<br/>route projection + chat_id · revocation")]
    r2[("Cloudflare R2<br/>blobs · manifests<br/>skill files · chat transcripts")]
  end

  subgraph cloudmod["cloud/ (SaaS only)"]
    direction TB
    stripe(["Stripe<br/>metered AI + plan quotas"])
  end

  email(["Transactional email"])
  posthog(["PostHog<br/>analytics · error tracking"])

  user -->|session cookie| dash
  user -->|content GET| worker
  user -. presigned upload .-> r2
  agent -->|public: llms.txt · crawl| worker
  agent -->|OAuth 2.1 sign-in + consent| dash
  agent -->|Bearer JWT · MCP| mcp
  dash -->|Bearer EdDSA JWT| api
  dash -->|identity schema · pooler| pg
  dash -. verify / magic link .-> email
  api -->|app schema · dropway_app · RLS| pg
  api -->|manifests · skill files · chat transcripts| r2
  api -->|route + chat_id · revocation| kv
  api -->|checkout · meter usage| stripe
  stripe -. plan_tier webhook .-> api
  worker -->|route + chat_id · revocation| kv
  worker -->|blobs · manifests · chat transcripts| r2
  worker -->|fetch edge JWKS| api
  mcp -->|verify token · fetch JWKS| dash
  mcp -->|app schema · dropway_app · RLS| pg
  mcp -->|skill files · chat transcripts · blobs| r2
  api -. errors · events .-> posthog
  dash -. errors · events .-> posthog
  worker -. errors .-> posthog

  classDef plane fill:#f8fafc,stroke:#cbd5e1,color:#0f172a;
  classDef saas fill:#fef3c7,stroke:#f59e0b,color:#78350f;
  class edge,control,datap,llm plane;
  class cloudmod saas;
```

## 2. Sequence flows

(a) sign up, (b) sign in, (c) create and deploy a site, (d) another user opening a
site shared with them (the gated edge-token exchange), (e) an LLM agent reading
gated content through the MCP server (the OAuth 2.1 flow), and (f) sharing the chat
behind a build as a site's "How this was made" panel. Sharing a **skill** reuses the
(c) deploy contract (prepare → presigned upload → finalize), so it isn't drawn
separately.

![Sequence](./sequence.png)

```mermaid
sequenceDiagram
  actor U as User (owner)
  actor V as Viewer
  actor L as LLM agent
  participant D as dashboard
  participant A as api
  participant S as serve
  participant MCP as mcp
  participant PG as Postgres
  participant OS as Object store
  participant M as Email

  rect rgb(232,245,233)
    note over U,M: a) Sign up
    U->>D: POST /api/auth/sign-up/email
    D->>PG: create user + session (identity schema)
    D->>M: send verification email
    D-->>U: Set-Cookie session, redirect to /onboarding
    U->>D: create organization
    D->>PG: insert organization + member
    D-->>U: redirect to /dashboard
  end

  rect rgb(227,242,253)
    note over U,M: b) Sign in
    U->>D: POST /api/auth/sign-in (password / magic link / Google)
    D->>PG: verify credentials + create session
    D-->>U: Set-Cookie session
  end

  rect rgb(255,243,224)
    note over U,M: c) Create + deploy a site
    U->>D: new site (slug) + drag-and-drop folder
    D->>D: mint short-lived EdDSA JWT (org_id claim)
    D->>A: POST /v1/sites (Bearer JWT)
    A->>PG: verify JWT, SET LOCAL RLS, INSERT site
    U->>A: POST /v1/sites/{id}/deployments/prepare (manifest)
    A-->>U: missing blobs + presigned PUT URLs
    U->>OS: PUT blobs (direct, presigned)
    U->>A: POST .../deployments (finalize) then publish
    A->>OS: write deploy manifest
    A->>PG: insert version, flip current_version_id
    A->>A: write route projection (route:host)
  end

  rect rgb(243,229,245)
    note over V,M: d) Another user opens a site shared with them (gated)
    V->>S: GET https://slug.dropwaycontent.com/
    S->>PG: resolve_host -> access_mode = allowlist / org_only
    S-->>V: 302 to dashboard /authz?host&next (no edge cookie)
    V->>D: GET /authz (has Better Auth session)
    D->>A: POST /v1/authz/mint (Bearer viewer JWT)
    A->>PG: authorize: membership / allowlist + revocation
    A-->>D: host-scoped edge token (EdDSA)
    D-->>V: 302 to /__authz/callback?token=
    V->>S: GET /__authz/callback?token=
    S->>A: fetch edge JWKS
    S->>S: verify token (aud=host, site_id, mode, revocation)
    S-->>V: Set-Cookie __Host-edge, 302 to path
    V->>S: GET / (with __Host-edge cookie)
    S->>OS: read manifest + blob
    S-->>V: stream content (private, no-store)
  end

  rect rgb(225,245,254)
    note over L,OS: e) LLM agent reads gated content via the MCP server (OAuth 2.1)
    L->>MCP: MCP request (no token)
    MCP-->>L: 401 + WWW-Authenticate (resource metadata)
    L->>MCP: GET /.well-known/oauth-protected-resource
    MCP-->>L: authorization_server = dashboard
    L->>D: register client (DCR) + authorize (PKCE, resource=mcp)
    D->>U: sign in + approve "Authorize MCP access"
    D-->>L: redirect with auth code
    L->>D: exchange code for JWT access token (aud=mcp, org_id)
    L->>MCP: MCP request (Bearer JWT)
    MCP->>D: fetch JWKS, verify token (iss/aud)
    MCP->>PG: check org_meta.mcp_enabled, SET LOCAL RLS
    MCP->>OS: read manifest + blob (org-scoped)
    MCP-->>L: list_sites / read_file results
  end

  rect rgb(255,249,196)
    note over U,OS: f) Share the chat behind a build ("How this was made")
    U->>A: share transcript (dropway chat share / mcp share_chat / dashboard)
    A->>PG: insert chat_log + messages (app schema, RLS)
    A->>OS: write compiled transcript JSON
    A->>A: attach to site -> route projection carries chat_id
    V->>S: GET https://slug.dropwaycontent.com/
    S->>OS: read content + transcript (route has chat_id)
    S-->>V: content + injected "How this was made" panel (site's access mode)
  end
```

## Regenerating the PNGs

The `.mmd` files are the source. Render them with the Mermaid CLI:

```sh
npx -y @mermaid-js/mermaid-cli -i components-selfhost.mmd -o components-selfhost.png -s 2 -b white
npx -y @mermaid-js/mermaid-cli -i components-cloud.mmd    -o components-cloud.png    -s 2 -b white
npx -y @mermaid-js/mermaid-cli -i sequence.mmd            -o sequence.png            -s 2 -b white
```

Edit the `.mmd` (and keep the fenced blocks above in sync), then re-render.
