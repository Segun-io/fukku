import { Link } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import type * as React from "react";
import { type FlowStep, FlowSteps } from "./flow-steps";

/** The way back to the upload a step belongs to, named for where it goes. */
export function BackToJob({ id, children }: { id: string; children: React.ReactNode }) {
  return (
    <p>
      <Link
        to="/jobs/$id"
        params={{ id }}
        className="inline-flex min-h-11 items-center gap-1.5 text-sm underline-offset-4 hover:underline"
      >
        <ArrowLeft aria-hidden="true" className="size-4" />
        {children}
      </Link>
    </p>
  );
}

type FlowHeaderProps = {
  /** The step of the import this page is. */
  step: FlowStep;
  /** The page's `h1`: what the user does here. */
  title: string;
  /** What it is done to: the upload's name, or the chosen title and its app. */
  subject?: React.ReactNode;
  /** One or two sentences: what to look at, and what happens next. */
  children?: React.ReactNode;
};

/**
 * The head of a step of the import: where the user is, what they do here and to what. The
 * subject is set like the name on a file label, so the page always says which upload the
 * decision is about. Put the way back (`BackToJob`) above it.
 */
export function FlowHeader({ step, title, subject, children }: FlowHeaderProps) {
  return (
    <header className="grid gap-4">
      <FlowSteps current={step} />
      <div className="grid gap-2">
        <h1>{title}</h1>
        {subject && (
          <p translate="no" className="font-medium [overflow-wrap:anywhere]">
            {subject}
          </p>
        )}
      </div>
      {children && <div className="grid max-w-[65ch] gap-2 text-ink-2">{children}</div>}
    </header>
  );
}
