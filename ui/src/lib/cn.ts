// Re-export the standard shadcn-style class-name helper so component code
// reads identically to what existed in the old Next.js UI.
import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
