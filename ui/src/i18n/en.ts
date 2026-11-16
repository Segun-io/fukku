// The source catalogue: a flat object of "area.key": "text". `es.ts` must have the same keys
// (it is typed against this object, so a missing key does not compile).
//
// - `{name}` is replaced by the parameter of that name.
// - A key can have plural forms: "area.key.one", "area.key.other" (the CLDR categories of the
//   language). `t("area.key", { count })` picks the form for `count`; `.other` is the fallback.
// - Add keys under the area of the page or component that uses them (shell, upload, jobs,
//   job, state, errors). #8 adds `shell.*` and `patterns.*`.
const en = {
  "shell.skip": "Skip to content",
  "shell.language": "Language",
  "shell.insecureDev": "Authentication is off. Use this only for development.",
  "nav.upload": "Upload",
  "nav.jobs": "Jobs",

  "upload.title": "Upload",
  "upload.placeholder": "Uploading arrives in a later release.",

  "jobs.title": "Jobs",
  "jobs.loading": "Loading jobs…",
  "jobs.empty": "No jobs yet.",
  "jobs.count.one": "{count} job",
  "jobs.count.other": "{count} jobs",
  "jobs.files.one": "{count} file",
  "jobs.files.other": "{count} files",
  "jobs.unnamed": "Untitled job",

  "job.title": "Job",
  "job.loading": "Loading the job…",
  "job.back": "Back to jobs",
  "job.state": "State",
  "job.updated": "Updated {date}",
  "job.size": "Size: {size}",
  "job.files": "Files",

  "state.uploading": "Uploading",
  "state.uploaded": "Uploaded",
  "state.probing": "Checking",
  "state.probed": "Checked",
  "state.matched": "Matched",
  "state.awaiting-confirmation": "Waiting for you",
  "state.importing": "Importing",
  "state.imported": "Imported",
  "state.post-processing": "Finishing",
  "state.done": "Done",
  "state.failed": "Failed",
  "state.stalled": "Stalled",
  "state.cancelled": "Cancelled",

  "errors.unknown": "Something went wrong.",
  "errors.signed_out": "Your session ended. Sign in again.",
  "errors.network": "The server cannot be reached. Check your connection.",
  "errors.bad_response": "The server sent an answer the page cannot read.",
  "errors.forbidden": "You are not allowed to do that.",
  "errors.origin_mismatch": "The request came from another site and was refused.",
  "errors.not_found": "Not found.",
  "errors.bad_request": "The request was not valid.",
  "errors.too_large": "The request is too large.",
  "errors.internal": "The server had a problem. Try again later.",
  "errors.uploads_unfinished": "Some uploads are not finished yet.",
  "errors.no_files": "Add at least one file first.",
  "errors.wrong_state": "That is not possible in the job's current state.",
  "errors.job_active": "The job is still running.",
  "errors.bad_name": "The file name is not valid.",
  "errors.job_not_uploading": "The job no longer accepts uploads.",
  "errors.duplicate_name": "A file with this name was already added.",
  "errors.file_too_large": "The file is larger than the server allows.",
  "errors.extension_not_allowed": "This type of file is not accepted.",
  "errors.too_many_uploads": "Too many uploads at once. Wait for one to finish.",
  "errors.in_flight_limit": "The server is receiving as much as it can. Try again later.",
  "errors.insufficient_space": "The server does not have enough free space.",
  "errors.storage_unavailable": "Storage is not available right now. Try again later.",
};

export default en;
export type Catalogue = typeof en;
