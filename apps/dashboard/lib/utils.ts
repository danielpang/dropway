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
