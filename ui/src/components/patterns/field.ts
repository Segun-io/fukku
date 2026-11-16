// The look of a native text field and a native select, for the places where `SelectField`
// (a caption over a select) does not fit: a cell of a table, a field with its own label.
// Both are 16px (a phone does not zoom in on them), 40px high and 44px for a finger, and
// take the one focus ring of `base.css`.

export const inputClass =
  "h-10 w-full min-w-0 rounded-md border border-input bg-card px-3 text-base text-foreground shadow-xs focus-visible:border-ring disabled:cursor-not-allowed disabled:opacity-60 pointer-coarse:h-11";

export const selectClass =
  "h-10 w-full min-w-0 cursor-pointer rounded-md border border-input bg-card px-2 text-base text-foreground shadow-xs hover:bg-accent focus-visible:border-ring disabled:cursor-not-allowed disabled:opacity-60 pointer-coarse:h-11";
