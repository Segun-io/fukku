// The mock API in the browser, for `npm run dev:mock`. Never part of the production build.
import { setupWorker } from "msw/browser";
import { handlers } from "./handlers";

export const worker = setupWorker(...handlers);
