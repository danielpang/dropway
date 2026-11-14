// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Shown when the authenticated collaboration capability is unavailable or disabled.

export function RoomsClosed() {
  return (
    <section className="space-y-2" aria-label="Project rooms">
      <h1 className="text-lg font-medium text-foreground">Project rooms are off</h1>
      <p className="max-w-xl text-sm text-muted-foreground">
        Project collaboration is not available for this account right now.
      </p>
    </section>
  );
}
