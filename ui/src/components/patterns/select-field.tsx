import { ChevronDown } from "lucide-react";
import type * as React from "react";
import { cn } from "@/lib/utils";

export type SelectFieldProps = Omit<React.ComponentProps<"select">, "className"> & {
  /** What is chosen here, in a word or two, above the control: "State". */
  label: string;
  className?: string;
};

/**
 * The select field: a caption over a native select. Native, so it works with a keyboard, a
 * screen reader and the phone's own picker; it looks like the language switch of the
 * header. Use it for a filter or a choice among a handful of fixed values; its `<option>`s
 * are its children.
 */
export function SelectField({ label, className, children, ...props }: SelectFieldProps) {
  return (
    <label className={cn("grid gap-1 text-sm text-muted-foreground", className)}>
      {label}
      <span className="relative inline-flex items-center">
        <select
          autoComplete="off"
          className="h-10 w-full cursor-pointer appearance-none rounded-md border border-input bg-card pr-9 pl-3 text-base text-foreground shadow-xs hover:bg-accent pointer-coarse:h-11"
          {...props}
        >
          {children}
        </select>
        <ChevronDown
          aria-hidden="true"
          className="pointer-events-none absolute right-3 size-4 text-muted-foreground"
        />
      </span>
    </label>
  );
}
