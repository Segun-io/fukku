package flows

import (
	"slices"

	"github.com/segun-io/fukku/internal/config"
)

// Changes to the library are admin-only by default (#116, auth.libraryChangesRequireAdmin).
// While the setting is on, only a member of auth.adminGroup (every user with
// auth.allUsersAreAdmins) may confirm what replaces or changes a file the library already
// has:
//
//   - an override of a row's rejections or warnings ("Import anyway", the language warning
//     of #39 included, and a shared_file override);
//   - the season-dub policies replace (Sonarr replaces the episodes' files) and version (a
//     second version next to an episode's file, #58);
//   - a flow 2 merge (Radarr replaces the film's file, #55) and a film's second version (#56).
//
// Anyone who may see the job can still import new films and new episodes, and choose the
// season-dub policy subtitlesOnly: it imports no video, and the subtitle manager adds the
// job's subtitle files next to the episodes' files, as it does for any import.
//
// The confirmation enforces it (CodeAdminRequired); the proposal and JobDetail move what
// would be offered to notOffered with ReasonAdminRequired, so the UI can say that an admin
// must confirm. An admin can open and confirm any job. A retry repeats what was confirmed,
// so the check is made once, at the confirmation.

// CodeAdminRequired refuses (403) a confirmation that would change the library, by a user
// who is not an admin, while auth.libraryChangesRequireAdmin is on. Rows lists the rows
// concerned when the refusal is about overrides.
const CodeAdminRequired = "admin_required"

// ReasonAdminRequired is why an offer is not made to this user: only an admin may confirm
// it (auth.libraryChangesRequireAdmin). The offer would be made to an admin.
const ReasonAdminRequired = "admin_required"

// OfferOverride names the overrides of rows' rejections and warnings in the proposal's
// notOffered.
const OfferOverride = "override"

// LibraryChangesAllowed reports whether a user may confirm what replaces or changes a file
// the library has: an admin, or anyone while auth.libraryChangesRequireAdmin is off.
func LibraryChangesAllowed(cfg config.Config, admin bool) bool {
	return admin || !cfg.Auth.LibraryChangesRequireAdmin
}

// adminOnly reports whether a season-dub policy changes a file the library has.
func adminOnly(policy string) bool {
	return policy == PolicyReplace || policy == PolicyVersion
}

// ForUser returns the view as a user who may not change the library sees it: merge and
// version, when offered, move to notOffered with ReasonAdminRequired, and version (the
// offer's details) goes. v unchanged when allowed; nil stays nil.
func (v *Flow2View) ForUser(allowed bool) *Flow2View {
	if v == nil || allowed || len(v.Offers) == 0 {
		return v
	}
	out := *v
	out.NotOffered = slices.Clone(v.NotOffered)
	for _, o := range v.Offers {
		out.NotOffered = append(out.NotOffered, NotOffered{Offer: o, Reason: ReasonAdminRequired})
	}
	out.Offers = []string{}
	out.Version = nil
	return &out
}

func adminRequired(format string, args ...any) *Error {
	return refuse(CodeAdminRequired, format, args...)
}
