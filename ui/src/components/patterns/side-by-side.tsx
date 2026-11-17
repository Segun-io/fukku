import type * as React from "react";
import { useId } from "react";
import { cn } from "@/lib/utils";

export type SideBySideRow = {
  /** What is compared in this line: "Length". */
  term: string;
  /** The two values, in the order of the sides. */
  values: [React.ReactNode, React.ReactNode];
  /** Set for sizes and durations: both values are shown in the figure face. */
  figure?: boolean;
  /** Set for names and other identifiers that must not be translated. */
  verbatim?: boolean;
};

type SideBySideProps = {
  /** The names of the two things, in a word or two: "In the library", "Your upload". */
  titles: [string, string];
  rows: SideBySideRow[];
  /** The level of the two titles. */
  as?: "h3" | "h4";
  className?: string;
};

/**
 * Two things compared term by term: two file labels on one raised surface. From 48rem they
 * stand next to each other and every term is on one line across both, so a difference is
 * seen by reading across; the terms are written once. Below that the second stands under
 * the first, with the same terms in the same order. Each side is a region named by its
 * title and carries its own terms, so it reads whole to a screen reader. Say what the
 * difference means in a `Notice` under it; this only shows the facts.
 */
export function SideBySide({ titles, rows, as: Title = "h3", className }: SideBySideProps) {
  const id = useId();
  const span = (n: number) => ({ gridRow: `span ${n}` });
  return (
    <div
      className={cn(
        "overflow-hidden rounded-xl border bg-card shadow-sm md:grid md:grid-cols-[auto_minmax(0,1fr)_minmax(0,1fr)]",
        className,
      )}
    >
      {titles.map((title, side) => {
        const second = side === 1;
        return (
          <section
            key={title}
            aria-labelledby={`${id}-${side}`}
            style={span(rows.length + 1)}
            className={cn(
              "grid grid-cols-[6.5rem_minmax(0,1fr)] md:grid-rows-subgrid",
              second
                ? "border-t md:col-start-3 md:grid-cols-1 md:border-t-0 md:border-l"
                : "md:col-span-2 md:grid-cols-subgrid",
            )}
          >
            <Title
              id={`${id}-${side}`}
              className={cn(
                "col-span-full px-4 pt-4 pb-3 font-display text-base leading-[1.3] font-bold tracking-[-0.01em]",
                !second && "md:col-span-1 md:col-start-2 md:pl-0",
              )}
            >
              {title}
            </Title>
            <dl
              style={span(rows.length)}
              className="col-span-full grid grid-cols-subgrid md:grid-rows-subgrid"
            >
              {rows.map((row) => (
                <div key={row.term} className="col-span-full grid grid-cols-subgrid border-t">
                  <dt
                    className={cn(
                      "py-2.5 pr-3 pl-4 text-sm leading-6 text-muted-foreground",
                      second && "md:sr-only",
                    )}
                  >
                    {row.term}
                  </dt>
                  <dd
                    translate={row.verbatim ? "no" : undefined}
                    className={cn(
                      "min-w-0 py-2.5 pr-4 [overflow-wrap:anywhere]",
                      second && "md:pl-4",
                      row.figure && "figure whitespace-normal",
                    )}
                  >
                    {row.values[side]}
                  </dd>
                </div>
              ))}
            </dl>
          </section>
        );
      })}
    </div>
  );
}
