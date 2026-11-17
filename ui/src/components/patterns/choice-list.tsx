import type * as React from "react";
import { useId } from "react";
import { cn } from "@/lib/utils";

export type Choice = {
  value: string;
  /** The choice in the words of a button: "Merge the audio into the library file". */
  label: React.ReactNode;
  /** What choosing it does, in one or two sentences. */
  body?: React.ReactNode;
  /** Attributes for the radio itself (a `data-` hook a test or a script reads). */
  input?: React.ComponentProps<"input"> & { [hook: `data-${string}`]: string };
};

type ChoiceListProps = Omit<React.ComponentProps<"fieldset">, "onChange"> & {
  /** The question the choices answer: "How do you want to add it?" */
  legend: React.ReactNode;
  choices: Choice[];
  /** The chosen value; an empty string while nothing is chosen. */
  value: string;
  onChange: (value: string) => void;
};

/**
 * A choice among a few ways to go on, each with its consequence: one raised strip of rows,
 * a native radio in each, the chosen row mint. The whole row is the target. Use it where
 * the choices need a sentence each; for a handful of bare values use `SelectField`. What
 * belongs to the question (a missing-choice message) goes in as children, under the strip.
 */
export function ChoiceList({
  legend,
  choices,
  value,
  onChange,
  children,
  className,
  ...props
}: ChoiceListProps) {
  const name = useId();
  return (
    <fieldset className={cn("grid min-w-0 gap-3", className)} {...props}>
      <legend className="mb-3 font-medium">{legend}</legend>
      <div className="divide-y overflow-hidden rounded-xl border bg-card shadow-sm">
        {choices.map((choice) => {
          const chosen = choice.value === value;
          return (
            <label
              key={choice.value}
              className={cn(
                "flex cursor-pointer items-start gap-3 p-4 transition-colors duration-150 hover:bg-accent/60",
                "has-[:focus-visible]:outline-2 has-[:focus-visible]:-outline-offset-2 has-[:focus-visible]:outline-ring",
                chosen && "bg-primary-soft hover:bg-primary-soft",
              )}
            >
              <input
                type="radio"
                name={name}
                value={choice.value}
                className="mt-0.5 size-5 shrink-0 cursor-pointer accent-primary outline-none"
                checked={chosen}
                onChange={() => onChange(choice.value)}
                {...choice.input}
              />
              <span className="grid min-w-0 gap-1">
                <span className="font-medium">{choice.label}</span>
                {choice.body && (
                  <span className="max-w-[65ch] text-sm text-ink-2">{choice.body}</span>
                )}
              </span>
            </label>
          );
        })}
      </div>
      {children}
    </fieldset>
  );
}
