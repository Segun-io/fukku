package flows

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/segun-io/fukku/internal/store"
)

// What the confirmation and the import add to the job target's Options, next to what the
// match stored there (options, match.go):
//
//	"subtitles":  {"<file id>": {"language": "es", "forced": false, "hearingImpaired": true}}
//	"monitoring": {"monitored": false, "reason": "dub_only"}
//
// A merge (#55, merge.go) keeps its choice and its progress there too ("merge").
//
// The subtitles step (#42) reads the first through SubtitleChoices; JobDetail's result
// shows the second.

// SubtitleChoice is the user's choice for one subtitle file of the job, made at the
// confirmation; it overrides what the file's name says.
type SubtitleChoice struct {
	FileID          int64  `json:"fileId"`
	Language        string `json:"language"` // an ISO 639-1 code, as the subtitle manager takes it
	Forced          bool   `json:"forced"`
	HearingImpaired bool   `json:"hearingImpaired"`
}

type storedChoice struct {
	Language        string `json:"language"`
	Forced          bool   `json:"forced"`
	HearingImpaired bool   `json:"hearingImpaired"`
}

// SubtitleChoices returns the choices stored on the target, by file id.
func SubtitleChoices(t store.Target) map[int64]SubtitleChoice {
	var opts struct {
		Subtitles map[string]storedChoice `json:"subtitles"`
	}
	if len(t.Options) > 0 {
		_ = json.Unmarshal(t.Options, &opts)
	}
	out := map[int64]SubtitleChoice{}
	for k, c := range opts.Subtitles {
		id, err := strconv.ParseInt(k, 10, 64)
		if err != nil {
			continue
		}
		out[id] = SubtitleChoice{FileID: id, Language: c.Language, Forced: c.Forced, HearingImpaired: c.HearingImpaired}
	}
	return out
}

// Unmonitor reasons (result.unmonitorReason).
const (
	UnmonitorDubOnly = "dub_only" // only a dub was imported, merge.unmonitorAfterDubOnlyImport (B6)
	UnmonitorChosen  = "chosen"   // the user chose at the match not to replace the film
	UnmonitorMerged  = "merged"   // a merge replaced the film's file, merge.unmonitorAfterMerge (B6, #55)
)

// monitoring is what the imported step left: whether the title (a show: every imported
// episode) is monitored, and why not when Fukku turned it off.
type monitoring struct {
	Monitored bool   `json:"monitored"`
	Reason    string `json:"reason,omitempty"`
}

func monitoringOf(t store.Target) (monitoring, bool) {
	var opts struct {
		Monitoring *monitoring `json:"monitoring"`
	}
	if len(t.Options) > 0 {
		_ = json.Unmarshal(t.Options, &opts)
	}
	if opts.Monitoring == nil {
		return monitoring{}, false
	}
	return *opts.Monitoring, true
}

// withOption returns raw (a JSON object, or empty) with key set to v; v nil removes key.
// The other keys are kept as they are.
func withOption(raw json.RawMessage, key string, v any) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, err
		}
	}
	if v == nil {
		delete(obj, key)
	} else {
		data, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		obj[key] = data
	}
	return json.Marshal(obj)
}

// KnownLanguage reports whether code is an ISO 639-1 language code (lower case): the codes
// the subtitle manager takes.
func KnownLanguage(code string) bool {
	return len(code) == 2 && strings.Contains(iso6391, " "+code+" ")
}

const iso6391 = " aa ab ae af ak am an ar as av ay az ba be bg bh bi bm bn bo br bs ca ce ch co cr cs cu cv cy" +
	" da de dv dz ee el en eo es et eu fa ff fi fj fo fr fy ga gd gl gn gu gv ha he hi ho hr ht hu hy hz" +
	" ia id ie ig ii ik io is it iu ja jv ka kg ki kj kk kl km kn ko kr ks ku kv kw ky la lb lg li ln lo" +
	" lt lu lv mg mh mi mk ml mn mr ms mt my na nb nd ne ng nl nn no nr nv ny oc oj om or os pa pi pl ps" +
	" pt qu rm rn ro ru rw sa sc sd se sg si sk sl sm sn so sq sr ss st su sv sw ta te tg th ti tk tl tn" +
	" to tr ts tt tw ty ug uk ur uz ve vi vo wa wo xh yi yo za zh zu "
