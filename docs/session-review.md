# Session review operations (TSK-48)

## Run history unavailable

The dashboard must receive the optional `app.SessionHistoryRepository` capability
through `app.WithPermissions`, not only on the underlying PostgreSQL store.
Both the initially selected repository and repositories opened by namespace
switches pass through this permission wrapper. Losing the capability there
caused the run endpoints to return HTTP 501 even when durable history existed.
The regression tests cover both routes with the real permission wrapper and
with migrated PostgreSQL stores, including two scheduled runs sharing a session.

Before diagnosing a deployed instance, inspect the actual HTTP response to
`GET /api/agents/{name}/runs?namespace={namespace}`. A missing capability returns
501, a storage failure returns 500, and an empty successful history returns 200
with an empty `runs` array. These conditions must not be conflated. Verify that
the binary includes the permission forwarding fix and that the selected
namespace has migration `018_agent_session_runs.sql`. The base branch already
renumbered this migration from the conflicting 017 version. This task does not
apply migrations to a live service or change its configuration.

## Lazy session output

Task dialogs show escaped summary previews and state labels, not transcripts.
Clicking a valid run card opens a second dialog and only then requests the run
metadata and paginated output. Back or Escape returns to the task without
losing an unsent comment. Agent history also offers a session-output action for
scheduled runs without tasks. Output polling stops when the session dialog
closes and is scoped to its selected namespace and job ID.

Migration `019_session_run_output.sql` adds nullable event job attribution and a
partial paging index. Newly captured output is attributed to its exact job.
Pre-migration output cannot safely be assigned to a run by task, agent, or a
reused session ID, so it is intentionally not backfilled. Older summaries remain
reviewable and the output pane explains when correlated output is unavailable.
Apply embedded migrations through the normal operator-controlled upgrade process
before running the new binary. No live database is migrated by this change.

## Analyst screenshot verification memory

The agent is named **analyst**, not analyser. The coder role is intentionally
not authorized to modify another agent's memory. An attempt through the normal
CLI returned `permission denied: agents may modify only their own agent memories`.
Do not change roles or agent identities to bypass that check.

An authorized human operator can add the requested memory in the intended
namespace using their normal human-role CLI configuration:

```sh
looster --namespace YOUR_NAMESPACE agent memory add analyst <<'MEMORY'
Whenever a task includes a meaningful UI change, verify the affected user journeys in a browser, take screenshots of the changed UI, upload them with looster task file add, and reference the uploaded verification evidence in your task analysis comment. Report any inability to capture or upload evidence explicitly rather than claiming UI verification.
MEMORY
looster --namespace YOUR_NAMESPACE agent memory list analyst
```

This is an operational follow-up, not a reason to weaken the application
permission boundary. No analyst memory is changed by this source patch.
