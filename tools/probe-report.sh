#!/usr/bin/env bash
# Makes the result of the scheduled online probes (ci.yml, job probe-report) visible: opens one
# issue when a verdict changed or the gate failed, and keeps using that issue instead of opening
# another each week.
#
#   tools/probe-report.sh <committed-table> <new-table> <gate-result>
#
#   committed-table  docs/compatibility.md as committed: the verdicts the project last accepted
#   new-table        docs/compatibility.md as `make compat` wrote it after this run (may be missing)
#   gate-result      the probes job's result: success, failure, ...
#
# Environment: GH_TOKEN (GITHUB_TOKEN with issues: write), GITHUB_REPOSITORY, RUN_URL.
# DRY_RUN=1 prints what it would do and calls no write API (a manual run of ci.yml uses it).
#
# Dedupe: the open issue titled $TITLE carries a hidden marker with a fingerprint of the findings
# in its body and in each comment the workflow adds. Nothing is written when the findings equal
# the last fingerprint. A new fingerprint adds a comment to the open issue; with no open issue a
# new one is opened. When the run is clean again, one comment says so and the issue is left open
# for the maintainer to close.
set -euo pipefail

committed=${1:?usage: probe-report.sh <committed-table> <new-table> <gate-result>}
current=${2:?}
gate=${3:?}
title="Online probes: a verdict changed or the gate failed"
dry=${DRY_RUN:-0}
run_url=${RUN_URL:-}

# "<ID> <verdict>" per probe from the table's rows; the verdict is the last bold word.
verdicts() {
  [ -f "$1" ] || return 0
  sed -nE 's/^\| ([A-Za-z][A-Za-z0-9_-]*) \|.*\| \*\*([a-z]+)\*\*[^|]*\|$/\1 \2/p' "$1" | sort -u
}
# Probes that differ or errored and are not on the accepted list ("(accepted: ...)" in the cell).
failing() {
  [ -f "$1" ] || return 0
  sed -nE '/\| \*\*(differs|error)\*\*[^|]*\|$/{/\(accepted/d;s/^\| ([A-Za-z][A-Za-z0-9_-]*) \|.*\| \*\*([a-z]+)\*\*.*/\1 \2/p}' "$1" | sort -u
}

old=$(verdicts "$committed")
new=$(verdicts "$current")
changes=""
if [ -n "$new" ]; then
  changes=$(
    join -a1 -a2 -e '(none)' -o 0,1.2,2.2 <(printf '%s\n' "$old") <(printf '%s\n' "$new") |
      awk '$2 != $3 { printf "- `%s`: %s -> %s\n", $1, $2, $3 }'
  )
fi
bad=$(failing "$current" | awk '{ printf "- `%s`: %s\n", $1, $2 }')

problem=0
[ -n "$changes" ] && problem=1
[ "$gate" != success ] && problem=1
[ -z "$new" ] && problem=1 # no table: the run did not get as far as the results

if [ "$problem" = 1 ]; then
  fp=$(printf 'gate=%s\n%s\n%s\n' "$gate" "$changes" "$bad" | sha256sum | cut -c1-16)
else
  fp=ok
fi

body=$(
  echo "The scheduled online probes ran against the pinned versions."
  echo
  echo "- Gate (\`make probes-online\`): **$gate**"
  [ -n "$run_url" ] && echo "- Run: $run_url"
  if [ -z "$new" ]; then
    echo "- No results table came out of the run (the test environment or the probes did not finish); see the run's log."
  fi
  if [ -n "$changes" ]; then
    echo
    echo "Verdicts that differ from the committed \`docs/compatibility.md\` (committed -> this run):"
    echo
    echo "$changes"
  fi
  if [ -n "$bad" ]; then
    echo
    echo "Probes that differ or errored and are not in \`test/probes/accepted.json\`:"
    echo
    echo "$bad"
  fi
  if [ "$problem" = 1 ]; then
    echo
    echo "What to do: read the probe's entry in the run's \`probe-results\` artifact. If upstream changed behaviour Fukku relies on, fix Fukku or record the finding in \`test/probes/accepted.json\` with its issue; if it is the expected outcome, run \`make compat\` and commit \`docs/compatibility.md\`. This issue is reused: the next runs add a comment only when the findings change."
  fi
  echo
  echo "<!-- probe-report: $fp -->"
)

if [ "$dry" = 1 ]; then
  echo "DRY RUN: the issue \"$title\" would be created or updated. Fingerprint: $fp"
  echo "----"
  echo "$body"
  exit 0
fi

# The open issue with this exact title, and the last fingerprint the workflow wrote on it.
number=$(gh issue list --repo "$GITHUB_REPOSITORY" --state open --search "\"$title\" in:title" --json number,title,author \
  --jq "[.[] | select(.title == \"$title\" and (.author.login | test(\"^(app/)?github-actions\$\")))] | sort_by(.number) | .[0].number // empty")
last=""
if [ -n "$number" ]; then
  last=$(gh issue view "$number" --repo "$GITHUB_REPOSITORY" --json body,comments \
    --jq '[.body, (.comments[] | select(.author.login | test("^(app/)?github-actions$")) | .body)] | [.[] | scan("<!-- probe-report: ([0-9a-f]+|ok) -->") | .[0]] | last // empty')
fi

if [ -z "$number" ]; then
  if [ "$problem" = 1 ]; then
    gh issue create --repo "$GITHUB_REPOSITORY" --title "$title" --body "$body"
  else
    echo "Probes clean and no open issue: nothing to do."
  fi
elif [ "$fp" = "$last" ]; then
  echo "Issue #$number already records this state ($fp): nothing to do."
elif [ "$fp" = ok ] && [ -z "$last" ]; then
  echo "Probes clean: nothing to do."
else
  gh issue comment "$number" --repo "$GITHUB_REPOSITORY" --body "$body"
fi
