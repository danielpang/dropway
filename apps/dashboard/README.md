# @dropway/dashboard

The Dropway control-plane dashboard is a Next.js (App Router, TypeScript) app that
runs on `app.dropway.dev` (Vercel in cloud; a container for self-host). It hosts
**Better Auth** (`/api/auth/*`, the only TS↔Postgres path, for its own identity
tables) and renders the management UI. For **all business data** it calls the Go
API (`api.dropway.dev`) via a typed client carrying a short-lived Better Auth
EdDSA JWT. It never opens a Postgres connection for business data.

> License: FSL-1.1-Apache-2.0 (part of the OSS core). No `cloud/`/`ee/` imports.

## Stack

- **Next.js 15** App Router + React 19, TypeScript (extends the repo
  `tsconfig.base.json`).
- **Better Auth**: Google + email/password + magic link + organization + jwt
  (EdDSA + JWKS) plugins, Postgres via the built-in `pg`/Kysely adapter.
- **Tailwind CSS** with semantic CSS-variable tokens (`--background`,
  `--foreground`, `--border`, `--primary`, `--muted`, …) defined for `:root` and
  `.dark` in `app/globals.css`; `tailwind.config.ts` maps them to utilities.
- **next-themes**: `attribute="class"`, `defaultTheme="system"`, `enableSystem`
  (follows `prefers-color-scheme`), with a manual toggle.
- **Geist** font (`geist/font`), shadcn-style UI primitives over Radix.

## Layout

```
apps/dashboard/
├── app/
│   ├── globals.css                  # Tailwind + theme tokens (:root / .dark)
│   ├── layout.tsx                   # Geist font + next-themes ThemeProvider
│   ├── page.tsx                     # session-aware router (→ /dashboard or /sign-in)
│   ├── api/auth/[...all]/route.ts   # Better Auth handler (+ JWKS for the Go API)
│   ├── (auth)/                      # public, polished first-impression surfaces
│   │   ├── layout.tsx               # theme-aware backdrop + theme toggle
│   │   ├── sign-in/page.tsx
│   │   └── sign-up/page.tsx
│   └── (app)/                       # authenticated, server-guarded
│       ├── layout.tsx               # app shell (guard + header + sign out)
│       └── dashboard/page.tsx       # signed-in user + org (server component)
├── components/
│   ├── auth/auth-form.tsx           # Google primary + email/password + magic link
│   ├── icons.tsx                    # Google logo
│   ├── sign-out-button.tsx
│   ├── theme-provider.tsx
│   ├── theme-toggle.tsx
│   └── ui/                          # button, card, input, label (token-driven)
├── lib/
│   ├── api.ts                       # typed Go-API client stub (until OpenAPI codegen)
│   ├── auth.ts                      # Better Auth server config
│   ├── auth-client.ts               # Better Auth React client
│   ├── env.ts                       # validated env access (server/public split)
│   └── utils.ts                     # cn() class merge
├── .env.example
├── next.config.ts · postcss.config.mjs · tailwind.config.ts
├── tsconfig.json · .eslintrc.json · next-env.d.ts
└── package.json
```

## Develop

Dependencies are installed once at the workspace root (`pnpm install` from the
repo root; do not run it inside this package).

```bash
cp apps/dashboard/.env.example apps/dashboard/.env.local   # then fill in
pnpm --filter @dropway/dashboard dev                       # http://localhost:3000
pnpm --filter @dropway/dashboard typecheck
pnpm --filter @dropway/dashboard lint
pnpm --filter @dropway/dashboard build
```

Better Auth migrates its own `identity` schema. With `DATABASE_URL` set, generate /
apply its tables with the Better Auth CLI, e.g.:

```bash
pnpm --filter @dropway/dashboard exec better-auth migrate
```

## Environment

See `.env.example`: `DATABASE_URL`, `BETTER_AUTH_SECRET`, `BETTER_AUTH_URL`,
`GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET`, `NEXT_PUBLIC_API_URL`.

## Auth screens

`sign-in` / `sign-up` are bespoke first-impression surfaces: a centered card on a
quiet, theme-aware backdrop, **Google as the primary button**, email/password +
magic link secondary, inline validation, loading/error/notice states, and full
focus-ring accessibility, polished in both light and dark.

## Notes / phasing

- Email verification is required (Google is pre-verified). Magic-link/verify
  emails are logged to the console in development until the email provider is
  wired by the infra agent.
- `lib/api.ts` is a hand-written placeholder; it is replaced by a generated,
  fully-typed client once the Go service publishes its OpenAPI spec.
- Access modes beyond `public` (`password`/`allowlist`/`org_only`) and the
  cross-domain `/authz` exchange are Phase 2, not implemented here.
```
