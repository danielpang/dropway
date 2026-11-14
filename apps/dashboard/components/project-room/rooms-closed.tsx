// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Shown while NEXT_PUBLIC_COLLAB_REVIEW is not exactly "1". No room request is made.

export function RoomsClosed() {
  return (
    <section className="space-y-2" aria-label="Project rooms">
      <h1 className="text-lg font-medium text-foreground">Project rooms are off</h1>
      <p className="max-w-xl text-sm text-muted-foreground">
        The dashboard gate is unset, and the org setting defaults off. Nothing here lists rooms,
        posts a comment, approves a change, or collects a provider key.
      </p>
    </section>
  );
}
