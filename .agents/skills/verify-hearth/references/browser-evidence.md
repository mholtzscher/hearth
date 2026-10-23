# Browser setup and HTTP evidence

Use this reference for browser recipes. Launch creates `$EVIDENCE`; preserve
that path and source its `run.env` in later shell calls. Browser Doctor starts
one HAR capture for the run, and Cleanup saves it before closing the session.

## Recover a missing CLI guide

First use the installed agent-browser skill and `agent-browser skills get core`.
If the native executable reports a missing skills directory, inspect its
location and version. On a Mise host with an already installed npm package,
the following locates its guide without installing or upgrading anything:

```sh
agent-browser --version > "$EVIDENCE/browser-version.txt"
readlink -f "$(command -v agent-browser)" > "$EVIDENCE/browser-executable.txt"
package_root=$(mise where npm:agent-browser)
mapfile -t guides < <(find "$package_root" -type f -path '*/skill-data/core/SKILL.md')
test "${#guides[@]}" -eq 1
skill_data=$(dirname "$(dirname "${guides[0]}")")
jq -r .version "$skill_data/../package.json" > "$EVIDENCE/browser-guide-version.txt"
AGENT_BROWSER_SKILLS_DIR="$skill_data" agent-browser skills get core \
  > "$EVIDENCE/browser-guide.txt"
```

Read the saved guide before driving. A native executable and npm guide can
have different versions. Record both and disclose any mismatch; check the
installed CLI's help if a command differs, and stop if its supported behavior
cannot satisfy the recipe. A successful guide load is not proof of an exact
version match. If the npm package is absent or the lookup is ambiguous, report
the prerequisite rather than searching unrelated directories or changing
shared tooling.

## Inspect the HAR after cleanup

After all intended browser actions, run the skill's Cleanup. It stops HAR
recording once before browser closure, including after a failed drive. If HAR
export fails, cleanup still closes the browser and stops the stack, but HTTP
evidence remains incomplete. Preserve a failure instead of redoing a mutation.

With the named evidence directory, extract the recorded mutations:

```sh
source "$EVIDENCE/run.env"
jq '[.log.entries[]
  | select(.request.method == "POST" or .request.method == "PATCH" or .request.method == "DELETE")
  | {url: .request.url, method: .request.method, request: .request.postData.text,
     status: .response.status, response: .response.content.text}]
' "$EVIDENCE/dashboard.har" > "$EVIDENCE/browser-mutations.json"
```

Inspect request and response JSON, not only the HTTP status. Match the actual
browser response ID to the independently read Core record and UI snapshot.
For each single-submission journey below, keep its discovered IDs in the shell
or record them in `run.env` before cleanup. The count assertions assume that
journey was run once during this capture. If deliberately exercising several
submissions, define their expected count and receipts before driving.

For the healthy power Command, with `POWER` and `COMMAND` from its recipe:

```sh
jq -e --arg url "$WEB/v1/entities/$POWER/commands" --arg id "$COMMAND" '
  [.[] | select(.method == "POST" and .url == $url)] as $calls
  | ($calls | length) == 1 and $calls[0].status == 200 and
    (($calls[0].request | fromjson) == {operation:"set",parameters:{value:true}}) and
    (($calls[0].response | fromjson) | .command_id == $id and .status == "satisfied")
' "$EVIDENCE/browser-mutations.json"
```

For the accepted notification, with `DESTINATION`, `ATTEMPT`, and `MESSAGE`
from its recipe:

```sh
jq -e --arg url "$WEB/v1/notification-destinations/$DESTINATION/notifications" \
  --arg id "$ATTEMPT" --arg destination "$DESTINATION" --arg message "$MESSAGE" '
  [.[] | select(.method == "POST" and .url == $url)] as $calls
  | ($calls | length) == 1 and $calls[0].status == 201 and
    (($calls[0].request | fromjson) == {title:"Hearth verification",message:$message}) and
    (($calls[0].response | fromjson) | .id == $id and .destination_id == $destination
      and .status == "accepted" and .title == "Hearth verification" and .message == $message)
' "$EVIDENCE/browser-mutations.json"
```

Require recording to have started before the action and a successful export
after it. HAR counts cover only that browser capture, not every possible Core
caller or later external effect. Keep authoritative Core and simulator receipt
checks from the feature recipe. Capture only this isolated simulation run;
HARs can contain request/response data and are proof artifacts, not files to
commit or publish automatically.
