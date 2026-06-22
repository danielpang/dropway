/**
 * Next.js instrumentation: the server-side global error net.
 *
 * `onRequestError` is invoked by Next for every uncaught server error — in
 * Server Components, Route Handlers, Server Actions, and middleware — so this is
 * the one place that catches server exceptions we don't handle explicitly. It
 * forwards them to the vendor-neutral `captureServerException` sink (PostHog by
 * default), mirroring the Go services' errtrack coverage.
 *
 * The hook is async and awaits the capture so the event is sent before a Vercel
 * function can freeze. posthog-node is a Node SDK, so we only capture on the
 * Node.js runtime (skip the edge runtime, where it isn't supported); the
 * dashboard runs its server logic on Node.
 */
export async function onRequestError(
  error: unknown,
  request: { path: string; method: string; headers: { [key: string]: string } },
  context: {
    routerKind: "Pages Router" | "App Router";
    routePath: string;
    routeType: "render" | "route" | "action" | "middleware";
    renderSource?: string;
    revalidateReason?: "on-demand" | "stale" | undefined;
    renderType?: "dynamic" | "dynamic-resume";
  },
): Promise<void> {
  if (process.env["NEXT_RUNTIME"] !== "nodejs") return;
  // Dynamic import keeps posthog-node (and "server-only") out of any edge bundle.
  const { captureServerException } = await import("@/lib/analytics-server");
  await captureServerException({
    error,
    properties: {
      path: request.path,
      method: request.method,
      router: context.routerKind,
      route: context.routePath,
      route_type: context.routeType,
      render_source: context.renderSource,
    },
  });
}
