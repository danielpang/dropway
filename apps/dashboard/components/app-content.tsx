"use client";
import { usePathname } from "next/navigation";

/** Review canvases use the viewport; other app pages retain the standard shell. */
export function AppContent({
  children,
  footer,
}: {
  children: React.ReactNode;
  footer: React.ReactNode;
}) {
  const canvas = usePathname() === "/rooms";
  return (
    <>
      <main
        className={canvas ? "min-h-0 flex-1" : "container flex-1 py-6 sm:py-10"}
      >
        {children}
      </main>
      {canvas ? null : footer}
    </>
  );
}
