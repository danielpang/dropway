import { redirect } from "next/navigation";
import { headers } from "next/headers";

import { auth } from "@/lib/auth";

/**
 * Root entry. Authenticated users land on the dashboard; everyone else is sent
 * to the sign-in surface. No UI is rendered here — it's a pure router.
 */
export default async function HomePage() {
  const session = await auth.api.getSession({ headers: await headers() });
  redirect(session ? "/dashboard" : "/sign-in");
}
