// The mock API in the browser, for `npm run dev:mock`. Never part of the production build.
import { setupWorker } from "msw/browser";
import { applyScenarios, handlers } from "./handlers";
import { readScenarios } from "./scenarios";

applyScenarios(readScenarios());

export const worker = setupWorker(...handlers);
