import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

/**
 * cn merges Tailwind class lists, resolving conflicts (later wins) so the
 * shadcn-style components can accept overriding `className` props cleanly.
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
