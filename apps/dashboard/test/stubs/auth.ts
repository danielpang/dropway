// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Test stub for `@/lib/auth`. The real module instantiates Better Auth with a
// live `pg` Pool (a side-effecting import we don't want in pure unit tests).
// lib/api.ts imports `auth` only to call `auth.api.getToken(...)` INSIDE its
// async request helpers — never at import time — so the pure exports under test
// (`ApiError`, the typed `api` object's existence) load fine against this stub.
export const auth = {
  api: {
    async getToken(): Promise<{ token: string } | null> {
      return null;
    },
  },
};
