-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- 0002_mcp.sql
--
-- Org-level switch for the Dropway MCP server (LLM access to a tenant's deployed
-- documents over an authenticated, OAuth-authorized Model Context Protocol endpoint).
--
-- Default ENABLED. An org admin/owner can turn it off, which cuts MCP access off for
-- the whole org immediately: the OAuth consent refuses, and the MCP resource server
-- re-reads this flag on every request and 403s when it is false. See README
-- "LLM-friendly access" and services/mcp.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE app.org_meta ADD COLUMN mcp_enabled boolean NOT NULL DEFAULT true;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.org_meta DROP COLUMN mcp_enabled;
-- +goose StatementEnd
