import { FilePlus2 } from "lucide-react";
import { useId, useRef, useState } from "react";
import { buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export type FilePickerProps = {
  /** What to do here: "Choose your files". Read out in both sizes, shown in the full one. */
  title: string;
  /** The heading level that fits the page's outline. */
  as?: "h2" | "h3";
  /** How: "Pick several files, or one zip, or drop them here." */
  hint: string;
  /** The button. It keeps this name in both sizes: "Choose files". */
  chooseLabel: string;
  /** What is accepted, already as a sentence: "Accepted: .mkv .mp4 .zip". */
  accepted?: string;
  /** The `accept` of the file input: extensions separated by commas. */
  accept: string;
  onFiles: (files: File[]) => void;
  disabled?: boolean;
  /**
   * `full` is the page with nothing chosen yet: the place where the files will be.
   * `compact` is one line for adding more, once there are files under it.
   */
  size?: "full" | "compact";
  /**
   * Whether choosing files is the main action of the page right now. It is while nothing is
   * chosen and while an interrupted upload waits for its files; it is not once "Start
   * upload" is on the page.
   */
  primary?: boolean;
  className?: string;
};

/**
 * The file picker: a dashed outline where the files go, with the system's file dialog
 * behind one button; files can be dropped anywhere on it. The dashed outline is the same
 * one an empty state has: it stands where content will be.
 */
export function FilePicker({
  title,
  as: Heading = "h2",
  hint,
  chooseLabel,
  accepted,
  accept,
  onFiles,
  disabled = false,
  size = "full",
  primary = true,
  className,
}: FilePickerProps) {
  const inputId = useId();
  const input = useRef<HTMLInputElement>(null);
  const [over, setOver] = useState(false);
  const compact = size === "compact";
  const variant = primary ? "default" : "outline";

  const take = (list: FileList | null) => {
    if (list && list.length > 0) onFiles([...list]);
    if (input.current) input.current.value = "";
  };

  return (
    <section
      aria-labelledby={`${inputId}-title`}
      data-size={size}
      onDragOver={(e) => {
        if (disabled) return;
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={(e) => {
        // Moving onto a child also fires this; only leaving the outline ends the drop.
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setOver(false);
      }}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        if (!disabled) take(e.dataTransfer.files);
      }}
      className={cn(
        "rounded-xl border border-dashed border-input transition-colors duration-150",
        compact
          ? "flex flex-wrap items-center gap-x-4 gap-y-3 p-4"
          : "grid justify-items-start gap-5 px-5 py-8 sm:px-8",
        over && "border-primary bg-primary-soft",
        className,
      )}
    >
      {compact ? (
        <Heading id={`${inputId}-title`} className="sr-only">
          {title}
        </Heading>
      ) : (
        <div className="grid gap-2">
          <Heading id={`${inputId}-title`} className="text-lg leading-tight tracking-[-0.02em]">
            {title}
          </Heading>
          <p className="max-w-[52ch] text-ink-2">{hint}</p>
        </div>
      )}
      <label
        htmlFor={inputId}
        data-slot="button"
        data-variant={variant}
        data-size="lg"
        className={cn(
          buttonVariants({ variant, size: "lg" }),
          "cursor-pointer has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-ring has-[:focus-visible]:outline-solid",
          disabled && "pointer-events-none opacity-50",
        )}
      >
        <FilePlus2 aria-hidden="true" />
        {chooseLabel}
        <input
          ref={input}
          id={inputId}
          name="files"
          type="file"
          multiple
          accept={accept}
          disabled={disabled}
          className="sr-only"
          onChange={(e) => take(e.target.files)}
        />
      </label>
      {(compact || accepted) && (
        <p className="min-w-0 flex-1 basis-56 text-sm text-muted-foreground">
          {compact && <span className="text-ink-2">{hint} </span>}
          {accepted}
        </p>
      )}
    </section>
  );
}
