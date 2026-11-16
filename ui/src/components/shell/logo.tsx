import type * as React from "react";

/** The hook. It takes the colour of the text around it. */
export function Logo(props: React.ComponentProps<"svg">) {
  return (
    <svg viewBox="0 0 32 32" width="24" height="24" aria-hidden="true" focusable="false" {...props}>
      <path
        d="M20 4v14a8 8 0 1 1-16 0"
        fill="none"
        stroke="currentColor"
        strokeWidth="3"
        strokeLinecap="round"
      />
      <circle cx="20" cy="4" r="2.5" fill="currentColor" />
    </svg>
  );
}
