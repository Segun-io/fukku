import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { server } from "@/mocks/server";
import { ApiError, apiFetch } from "./client";
import { getHealth } from "./health";
import { cancelJob, deleteJob, getJob, listJobs, useJobs } from "./jobs";
import { useSession } from "./session";

async function failureOf(p: Promise<unknown>): Promise<ApiError> {
  try {
    await p;
  } catch (err) {
    expect(err).toBeInstanceOf(ApiError);
    return err as ApiError;
  }
  throw new Error("expected the call to fail");
}

describe("apiFetch", () => {
  it("returns the JSON of a 2xx answer", async () => {
    expect(await getHealth()).toEqual({ status: "ok", version: "0.1.0" });
  });

  it("returns undefined for a 204", async () => {
    await expect(deleteJob("00000000000000000000000000000004")).resolves.toBeUndefined();
  });

  it("turns the error shape of #5 into an ApiError with the code", async () => {
    const err = await failureOf(getJob("ffffffffffffffffffffffffffffffff"));
    expect(err.status).toBe(404);
    expect(err.code).toBe("not_found");
    expect(err.message).toBe("No such job.");

    const conflict = await failureOf(cancelJob("00000000000000000000000000000004"));
    expect(conflict.status).toBe(409);
    expect(conflict.code).toBe("wrong_state");
  });

  it("uses `internal` when the body is not the error shape", async () => {
    server.use(http.get("/api/session", () => new HttpResponse("oops", { status: 502 })));
    const err = await failureOf(apiFetch("/api/session"));
    expect(err.status).toBe(502);
    expect(err.code).toBe("internal");
  });

  it("maps a 401 to signed_out", async () => {
    server.use(http.get("/api/session", () => new HttpResponse(null, { status: 401 })));
    expect((await failureOf(apiFetch("/api/session"))).code).toBe("signed_out");
  });

  it("maps a redirect to another origin to signed_out, without following it", async () => {
    let followed = false;
    server.use(
      http.get("/api/session", () => {
        return new HttpResponse(null, {
          status: 302,
          headers: { Location: "https://login.example.com/sign-in" },
        });
      }),
      http.get("https://login.example.com/sign-in", () => {
        followed = true;
        return HttpResponse.html("<html></html>");
      }),
    );
    const err = await failureOf(apiFetch("/api/session"));
    expect(err.code).toBe("signed_out");
    expect(followed).toBe(false);
  });

  it("maps an unreachable server to network", async () => {
    server.use(http.get("/api/session", () => HttpResponse.error()));
    const err = await failureOf(apiFetch("/api/session"));
    expect(err.code).toBe("network");
    expect(err.status).toBe(0);
  });

  it("maps an answer that is not JSON to bad_response", async () => {
    server.use(http.get("/api/session", () => HttpResponse.html("<html></html>")));
    expect((await failureOf(apiFetch("/api/session"))).code).toBe("bad_response");
  });

  it("refuses a path that is not on this origin", async () => {
    await expect(apiFetch("https://example.com/api/session")).rejects.toThrow(/this origin/);
    await expect(apiFetch("//example.com/api/session")).rejects.toThrow(/this origin/);
  });
});

describe("jobs endpoints", () => {
  it("passes the filter as query parameters", async () => {
    const { jobs } = await listJobs({ states: ["failed", "done"], limit: 10 });
    expect(jobs.map((j) => j.state).sort()).toEqual(["done", "failed"]);
  });
});

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function Probe() {
  const session = useSession();
  const jobs = useJobs({ states: ["probed"] });
  return (
    <div>
      <p>user: {session.data?.user.name}</p>
      <p>jobs: {jobs.data?.jobs.length}</p>
      <p>jobs error: {jobs.error instanceof ApiError ? jobs.error.code : "none"}</p>
    </div>
  );
}

describe("query hooks", () => {
  it("useSession and useJobs read the mock API", async () => {
    render(<Probe />, { wrapper });
    expect(await screen.findByText("user: user1")).toBeInTheDocument();
    expect(await screen.findByText("jobs: 1")).toBeInTheDocument();
  });

  it("an error answer reaches the hook as an ApiError with its code", async () => {
    server.use(
      http.get("/api/jobs", () =>
        HttpResponse.json(
          { error: { code: "forbidden", message: "Not allowed." } },
          { status: 403 },
        ),
      ),
    );
    render(<Probe />, { wrapper });
    await waitFor(() => expect(screen.getByText("jobs error: forbidden")).toBeInTheDocument());
  });
});
