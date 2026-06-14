// Package ee is the root of Shipped's Enterprise Edition.
//
// It is source-visible but use-restricted under the Shipped Enterprise Edition
// License (see ee/LICENSE) — NOT the repository's FSL-1.1-Apache-2.0. The
// open-source / self-host build does not depend on anything in this tree.
//
// Enterprise features land here behind a license-key gate
// (docs/ARCHITECTURE.md §11, §14.1): SSO/SAML (UUID-keyed, never email),
// audit-log export + SIEM, advanced RBAC, and Cloudflare-for-SaaS custom
// domains. This package is currently an intentionally empty placeholder so the
// license boundary and module path exist from commit one.
package ee
