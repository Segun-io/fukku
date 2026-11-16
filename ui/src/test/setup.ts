import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterAll, afterEach, beforeAll } from "vitest";
import { resetMocks } from "@/mocks/handlers";
import { server } from "@/mocks/server";

// A request without a handler is a bug in the test: fail it instead of letting it out.
beforeAll(() => server.listen({ onUnhandledFrame: "error" }));
afterEach(() => {
  cleanup();
  server.resetHandlers();
  resetMocks();
  localStorage.clear();
});
afterAll(() => server.close());
