import { CircleAlert } from "lucide-react";
import type * as React from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { inputClass } from "./field";

export type NudgeGroup = {
  /** When to press these, in the words of what the user sees or hears. */
  caption: string;
  /** The steps, the small one first. `label` says what the press does: "50 ms earlier". */
  steps: { by: number; label: string }[];
};

type NudgeFieldProps = {
  id: string;
  /** The caption of the field. It stays the field's accessible name. */
  label: string;
  /** The field's text, as typed. The buttons write a whole number into it. */
  value: string;
  onChange: (value: string) => void;
  /** The number the text stands for, or null when it is not one within the bounds. */
  parse: (value: string) => number | null;
  min: number;
  max: number;
  /** Two groups of buttons, one for each direction. */
  groups: [NudgeGroup, NudgeGroup];
  /** The unit, shown after the field: "ms". */
  unit?: string;
  /** What the value means in a sentence ("The new audio starts 0.25 s later"); read out when it changes. */
  readout?: React.ReactNode;
  /** How to fill it in by hand, and its bounds. */
  hint?: React.ReactNode;
  /** What is wrong with the text; shown under the field, and the field is marked invalid. */
  error?: React.ReactNode;
  /** More attributes for the field: `name`, `pattern`, `inputMode`. */
  input?: Omit<React.ComponentProps<"input">, "id" | "value" | "onChange" | "className">;
  className?: string;
};

/**
 * A number the user adjusts by ear or by eye rather than knows: a field that can be typed
 * into, and buttons that move it by a fixed step in each direction. Each group of buttons
 * is captioned with what the user notices ("The sound comes after the picture"), so nobody
 * has to work out which sign is which. The buttons only write into the field: a press
 * starts from the number in it (from 0 when the text is not a number) and stops at the
 * bounds. Deciding what the new value is used for stays with the page.
 */
export function NudgeField({
  id,
  label,
  value,
  onChange,
  parse,
  min,
  max,
  groups,
  unit,
  readout,
  hint,
  error,
  input,
  className,
}: NudgeFieldProps) {
  const current = parse(value) ?? 0;
  const nudge = (by: number) => onChange(String(Math.min(max, Math.max(min, current + by))));
  const describedBy =
    [readout ? `${id}-readout` : "", hint ? `${id}-hint` : ""].filter(Boolean).join(" ") ||
    undefined;
  return (
    <div className={cn("grid gap-3", className)}>
      <div className="grid gap-1">
        <label htmlFor={id} className="text-sm text-muted-foreground">
          {label}
        </label>
        <div className="flex items-center gap-2">
          <input
            type="text"
            autoComplete="off"
            spellCheck={false}
            {...input}
            id={id}
            className={cn(inputClass, "figure max-w-40")}
            value={value}
            aria-invalid={error ? true : undefined}
            aria-describedby={describedBy}
            onChange={(e) => onChange(e.target.value)}
          />
          {unit && (
            <span aria-hidden="true" className="figure text-sm text-muted-foreground">
              {unit}
            </span>
          )}
        </div>
        {error && (
          <p role="alert" className="flex items-start gap-2 text-sm text-destructive">
            <CircleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
            {error}
          </p>
        )}
        {readout && (
          <p id={`${id}-readout`} aria-live="polite" className="min-h-[1lh] text-sm text-ink-2">
            {readout}
          </p>
        )}
      </div>

      <div className="grid gap-3">
        {groups.map((group) => (
          <fieldset key={group.caption} className="grid min-w-0 gap-1.5">
            <legend className="mb-1.5 text-sm">{group.caption}</legend>
            <div className="flex flex-wrap gap-2">
              {group.steps.map((step) => (
                <Button
                  key={step.by}
                  type="button"
                  variant="outline"
                  onClick={() => nudge(step.by)}
                >
                  {step.label}
                </Button>
              ))}
            </div>
          </fieldset>
        ))}
      </div>

      {hint && (
        <p id={`${id}-hint`} className="max-w-[65ch] text-sm text-muted-foreground">
          {hint}
        </p>
      )}
    </div>
  );
}
