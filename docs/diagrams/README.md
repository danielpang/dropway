<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Dropway — system diagrams

Diagrams-as-code (Mermaid). The `.mmd` files are the source of truth; the `.png`
files are pre-rendered for quick viewing. GitHub renders the fenced `mermaid`
blocks below natively.

## 1. Components & directional requests

How the runtime pieces talk to each other. `serve` is the self-host content edge
(the plain-Go alternative to the Cloudflare serving Worker); Redis/Valkey holds the
route projection + revocation denylist (`api` writes, `serve` reads). `mcp` is the
OAuth-protected MCP server: an LLM agent reads **public** content as a crawler (via
`llms.txt` on the edge) and **gated** content only through `mcp`, after a browser
OAuth flow against the dashboard (the authorization server) — scoped to one org by
the same RLS as the rest of the platform.

![Components](./components.png)

```mermaid
flowchart LR
  user(["User / Browser"])
  agent(["LLM agent<br/>Claude · Cursor · Codex"])

  subgraph edge["Content edge"]
    direction TB
    caddy["Caddy<br/>TLS · on-demand certs · cache"]
    serve["serve (Go)<br/>*.dropwaycontent.com<br/>content + access + llms.txt"]
  end

  subgraph control["Control plane"]
    direction TB
    dash["dashboard (Next.js)<br/>Better Auth · /authz · OAuth AS<br/>app.dropway.dev"]
    api["api (Go)<br/>system of record + authz<br/>api.dropway.dev"]
  end

  subgraph llm["LLM access"]
    direction TB
    mcp["mcp (Go)<br/>OAuth-protected MCP server<br/>mcp.dropway.dev"]
  end

  subgraph datap["Data plane"]
    direction TB
    pg[("Postgres<br/>identity + app schemas · RLS")]
    redis[("Redis / Valkey<br/>route projection · revocation")]
    store[("Object store · MinIO / R2<br/>blobs + manifests")]
  end

  smtp(["SMTP / Mailpit"])

  user -->|session cookie| dash
  user -->|content GET| caddy
  user -. presigned blob upload .-> store
  agent -->|public: llms.txt · crawl| caddy
  agent -->|OAuth 2.1 sign-in + consent| dash
  agent -->|Bearer JWT · MCP| mcp
  caddy -->|reverse proxy · tls-check| serve
  dash -->|Bearer EdDSA JWT| api
  dash -->|identity schema| pg
  dash -. verify / magic link .-> smtp
  api -->|app schema · dropway_app · RLS| pg
  api -->|presign · write manifest| store
  api -->|write route + revocation| redis
  serve -->|resolve_host| pg
  serve -->|read blobs + manifests| store
  serve -->|read revocation| redis
  serve -->|fetch edge JWKS| api
  mcp -->|verify token · fetch JWKS| dash
  mcp -->|app schema · dropway_app · RLS| pg
  mcp -->|read blobs + manifests| store

  classDef plane fill:#f8fafc,stroke:#cbd5e1,color:#0f172a;
  class edge,control,datap,llm plane;
```

## 2. Sequence flows

(a) sign up · (b) sign in · (c) create + deploy a site · (d) another user opening a
site shared with them (the gated edge-token exchange) · (e) an LLM agent reading gated
content through the MCP server (the OAuth 2.1 flow).

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
```

## Regenerating the PNGs

The `.mmd` files are the source. Render them with the Mermaid CLI:

```sh
npx -y @mermaid-js/mermaid-cli -i components.mmd -o components.png -s 2 -b white
npx -y @mermaid-js/mermaid-cli -i sequence.mmd   -o sequence.png   -s 2 -b white
```

Edit the `.mmd` (and keep the fenced blocks above in sync), then re-render.
