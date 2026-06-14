-- SPDX-License-Identifier: LicenseRef-Shipped-Proprietary
--
-- =============================================================================
-- CLOUD-ONLY / PROPRIETARY MIGRATION -- NOT part of the FSL open-core build.
--
-- This migration is applied ONLY by the hosted-cloud deployment. The core
-- (OSS) build NEVER references the `billing` schema: FK direction is strictly
-- cloud -> core (billing.subscriptions.org_id -> app.org_meta.id), so the core
-- compiles and runs without billing ever existing (ARCHITECTURE.md §5 / §9 / §14).
--
-- Run ordering: the `app` migrations (db/migrations/app/) must be applied first
-- so that app.org_meta exists as the FK target.
-- =============================================================================
--
-- 0001_billing.sql
--
-- Stripe subscription mirror + webhook idempotency ledger. Stripe is the source
-- of truth; this schema MIRRORS it, written ONLY by the signature-verified
-- webhook in cloud/billing (never by a browser redirect; §9).

-- +goose Up

-- +goose StatementBegin
CREATE SCHEMA IF NOT EXISTS billing;
-- +goose StatementEnd

-- +goose StatementBegin
-- subscriptions: one row per org (one Stripe Customer per org). plan_tier here is
-- the AUTHORITATIVE entitlement the synchronous hard-cap check reads (§9).
-- org_status reflects derived account state (active / over_limit / past_due) the
-- edge mirrors to KV.
CREATE TABLE billing.subscriptions (
    org_id                 uuid PRIMARY KEY REFERENCES app.org_meta (id) ON DELETE CASCADE,
    stripe_customer_id     text NOT NULL UNIQUE,
    stripe_subscription_id text UNIQUE, -- null until first paid subscription exists
    plan_tier              text NOT NULL DEFAULT 'free'
                               CHECK (plan_tier IN ('free', 'business', 'enterprise')),
    seats                  int NOT NULL DEFAULT 0,
    status                 text NOT NULL DEFAULT 'active'
                               CHECK (status IN ('active', 'trialing', 'past_due', 'canceled', 'incomplete')),
    cancel_at_period_end   boolean NOT NULL DEFAULT false,
    current_period_end     timestamptz,
    org_status             text NOT NULL DEFAULT 'active'
                               CHECK (org_status IN ('active', 'over_limit', 'past_due', 'suspended')),
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX subscriptions_stripe_customer_id_idx ON billing.subscriptions (stripe_customer_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- processed_stripe_events: webhook dedupe ledger. The webhook INSERTs the Stripe
-- event id; ON CONFLICT -> already processed -> return 200 idempotently (§9).
CREATE TABLE billing.processed_stripe_events (
    event_id     text PRIMARY KEY, -- Stripe event.id (evt_...)
    event_type   text NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TABLE IF EXISTS billing.processed_stripe_events;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS billing.subscriptions;
-- +goose StatementEnd
-- +goose StatementBegin
DROP SCHEMA IF EXISTS billing CASCADE;
-- +goose StatementEnd
