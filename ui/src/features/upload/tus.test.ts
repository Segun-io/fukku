import { describe, expect, it } from "vitest";
import { classify } from "./queue";
import { failureOf } from "./tus";

const upload = `${window.location.origin}/api/uploads/0123456789abcdef0123456789abcdef`;

/** A tus-js-client error as its browser stack makes it: the request, and the XHR's answer. */
function tusError(status: number, body: string, responseURL: string) {
  return Object.assign(new Error("tus: unexpected response"), {
    originalRequest: { getURL: () => upload },
    originalResponse: {
      getStatus: () => status,
      getBody: () => body,
      getUnderlyingObject: () => ({ responseURL }),
    },
  });
}

describe("failureOf", () => {
  it("reads the API's error code", () => {
    const body = JSON.stringify({ error: { code: "offset_mismatch", message: "x" } });
    expect(failureOf(tusError(409, body, upload))).toEqual({
      status: 409,
      code: "offset_mismatch",
    });
  });

  it("is a network failure without an answer", () => {
    expect(failureOf(new Error("tus: failed to upload chunk"))).toEqual({
      status: 0,
      code: undefined,
    });
  });

  // The proxy redirected the request to a sign-in page on this origin and the browser followed
  // it: the sign-in page's 404 is not the upload's, and the upload must not start over.
  it("takes an answer from a sign-in page the request was redirected to as signed out", () => {
    const failure = failureOf(
      tusError(404, "404 page not found", `${window.location.origin}/signin?rd=%2F`),
    );
    expect(failure).toEqual({ status: 0, code: "signed_out" });
    expect(classify(failure.status, failure.code)).toBe("auth");
  });
});
