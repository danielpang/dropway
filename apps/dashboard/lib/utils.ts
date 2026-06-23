import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

/**
 * cn merges Tailwind class lists, resolving conflicts (later wins) so the
 * shadcn-style components can accept overriding `className` props cleanly.
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}

/**
 * Format a byte count as a short human string (e.g. "0 B", "4.0 KB", "12.3 MB").
 * Uses decimal units (1 KB = 1000 B) to match how cloud storage is usually quoted.
 * Returns "0 B" for 0, negatives, or non-finite input.
 */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(
    units.length - 1,
    Math.floor(Math.log(bytes) / Math.log(1000)),
  );
  const value = bytes / 1000 ** i;
  // Whole bytes have no decimals; larger units show one decimal place.
  const formatted = i === 0 ? String(value) : value.toFixed(1);
  return `${formatted} ${units[i]}`;
}

/**
 * Compact "time ago" for an ISO timestamp (e.g. "just now", "5m ago", "3h ago",
 * "2d ago"), falling back to an absolute date past a week. `fallback` is returned
 * for a missing or unparseable timestamp. Shared by the feed and comment threads.
 */
export function formatRelativeTime(
  iso: string | undefined,
  fallback = "just now",
): string {
  if (!iso) return fallback;
  const then = new Date(iso);
  const ms = Date.now() - then.getTime();
  if (Number.isNaN(ms)) return fallback;
  const sec = Math.floor(ms / 1000);
  if (sec < 60) return "just now";
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  if (day < 7) return `${day}d ago`;
  return then.toLocaleDateString();
}
