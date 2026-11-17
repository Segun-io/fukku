// Package errorcodes holds a test and no code. The test reads the Go source of the API, the
// job runner and the flows, collects every code the server can send to the UI (error answers,
// job error codes, refusals, warnings, notices) and writes them to
// ui/src/api/error-codes.generated.json. The UI's tests read that file and fail when a code
// has no sentence in a language.
package errorcodes
