import { Check, Copy } from "lucide-react";
import { type ReactNode, useState } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export type CopyFieldProps = {
  /** What the value is, in a few words: "Folder in Radarr". */
  label: string;
  /** The exact text to carry to another app: a path, an address, an identifier. */
  value: string;
  /** The button: "Copy". */
  copyLabel: string;
  /** The button for a screen reader, naming what it copies: "Copy the Radarr folder". */
  copyName?: string;
  /** Said once it is on the clipboard: "Copied". */
  copiedText: string;
  /** Said when the browser refused: what to do instead (select the text and copy it). */
  failedText: string;
  /** More actions for the same value, after the button: a link to where it is pasted. */
  children?: ReactNode;
  className?: string;
};

/**
 * The copy field: a value the user carries to another app by hand, such as the folder to
 * choose in Radarr. It is set in the figure face on raised paper, whole and selectable,
 * because one wrong character makes it useless; it wraps anywhere and is never cut off.
 * The result of copying is said in words next to the button, in a place that is kept free
 * for it, so nothing moves.
 */
export function CopyField({
  label,
  value,
  copyLabel,
  copyName,
  copiedText,
  failedText,
  children,
  className,
}: CopyFieldProps) {
  const [result, setResult] = useState<"idle" | "copied" | "failed">("idle");

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setResult("copied");
    } catch {
      setResult("failed");
    }
  }

  return (
    <div className={cn("grid gap-2", className)}>
      <p className="text-sm text-muted-foreground">{label}</p>
      <p
        translate="no"
        className="figure rounded-md border bg-card px-3 py-2.5 text-sm whitespace-normal [overflow-wrap:anywhere] select-all"
      >
        {value}
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" variant="outline" aria-label={copyName} onClick={() => void copy()}>
          {result === "copied" ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
          {copyLabel}
        </Button>
        {children}
        <span role="status" className="min-h-[1lh] text-sm text-muted-foreground">
          {result === "copied" && copiedText}
          {result === "failed" && failedText}
        </span>
      </div>
    </div>
  );
}
