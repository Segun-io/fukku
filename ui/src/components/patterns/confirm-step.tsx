import { LoaderCircle, TriangleAlert } from "lucide-react";
import { type FormEvent, type ReactNode, useId, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";

export type ConfirmFact = {
  term: string;
  value: ReactNode;
  /** Set for sizes, durations and counts: they are shown in the figure face. */
  figure?: boolean;
};

export type ConfirmStepProps = {
  /** The decision as a question, naming the thing: "Add this film to the library?" */
  title: string;
  /** What is about to happen, as facts the user can check. */
  facts: ConfirmFact[];
  /**
   * What the user should know first, one plain sentence each: what will be replaced,
   * deleted or downgraded. With warnings, the user ticks a box before confirming.
   */
  warnings?: ReactNode[];
  /** The verb and the thing ("Add to the library"); the next screen uses the same words. */
  confirmLabel: string;
  onConfirm: () => void;
  /** The way back, named for where it goes ("Choose another film"). */
  cancelLabel?: string;
  onCancel?: () => void;
  /** True while the request runs: the button keeps its label and its width. */
  busy?: boolean;
  className?: string;
};

/**
 * The confirmation step: the one careful decision of a flow. The facts come first, then
 * the warnings, then one primary button. The button is never disabled at rest; pressing it
 * without the tick says what is missing and moves the focus to the box.
 */
export function ConfirmStep({
  title,
  facts,
  warnings = [],
  confirmLabel,
  onConfirm,
  cancelLabel,
  onCancel,
  busy = false,
  className,
}: ConfirmStepProps) {
  const { t } = useT();
  const id = useId();
  const box = useRef<HTMLInputElement>(null);
  const [acknowledged, setAcknowledged] = useState(false);
  const [missing, setMissing] = useState(false);
  const needsTick = warnings.length > 0;

  function submit(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    if (needsTick && !acknowledged) {
      setMissing(true);
      box.current?.focus();
      return;
    }
    onConfirm();
  }

  return (
    <form
      noValidate
      aria-labelledby={`${id}-title`}
      aria-busy={busy}
      onSubmit={submit}
      className={cn("grid gap-5", className)}
    >
      <h2 id={`${id}-title`}>{title}</h2>

      <dl className="grid grid-cols-1 gap-x-6 gap-y-3 border-y py-4 sm:grid-cols-[auto_minmax(0,1fr)] sm:gap-y-2">
        {facts.map((fact) => (
          <div key={fact.term} className="grid sm:col-span-2 sm:grid-cols-subgrid">
            <dt className="text-sm text-muted-foreground sm:text-base">{fact.term}</dt>
            <dd
              className={cn(
                "min-w-0 font-medium [overflow-wrap:anywhere]",
                fact.figure && "figure whitespace-normal font-normal",
              )}
            >
              {fact.value}
            </dd>
          </div>
        ))}
      </dl>

      {needsTick && (
        <div className="rounded-lg border border-caution-edge bg-caution p-4">
          <h3 className="flex items-center gap-2">
            <TriangleAlert aria-hidden="true" className="size-5 shrink-0 text-caution-foreground" />
            {t("patterns.confirm.warnings")}
          </h3>
          <ul className="mt-2 grid list-disc gap-1.5 pl-5 marker:text-caution-foreground">
            {warnings.map((warning, index) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: a fixed list for one render
              <li key={index} className="max-w-[65ch]">
                {warning}
              </li>
            ))}
          </ul>
          <label
            htmlFor={`${id}-ack`}
            className="mt-3 flex min-h-11 cursor-pointer items-center gap-3 font-medium"
          >
            <input
              ref={box}
              id={`${id}-ack`}
              name="acknowledged"
              type="checkbox"
              required
              checked={acknowledged}
              aria-invalid={missing && !acknowledged ? true : undefined}
              aria-describedby={`${id}-help`}
              onChange={(event) => {
                setAcknowledged(event.target.checked);
                if (event.target.checked) setMissing(false);
              }}
              className="size-5 shrink-0 cursor-pointer accent-primary"
            />
            {t("patterns.confirm.ack")}
          </label>
          <p id={`${id}-help`} aria-live="polite" className="min-h-[1lh] text-sm text-destructive">
            {missing && !acknowledged ? t("patterns.confirm.ackMissing") : ""}
          </p>
        </div>
      )}

      <div className="flex flex-col gap-2 sm:flex-row sm:flex-wrap">
        <Button type="submit" size="lg" aria-disabled={busy} aria-busy={busy}>
          {busy && <LoaderCircle aria-hidden="true" className="animate-spin" />}
          {confirmLabel}
        </Button>
        {cancelLabel && onCancel && (
          <Button type="button" size="lg" variant="ghost" onClick={onCancel}>
            {cancelLabel}
          </Button>
        )}
        <span aria-live="polite" className="self-center text-sm text-muted-foreground">
          {busy ? t("patterns.confirm.working") : ""}
        </span>
      </div>
    </form>
  );
}
