# Dashboard

[Back to README](../readme.md) · [Usage](usage.md)

Run `looster --config config.toml web` and open `http://127.0.0.1:8080`.
> [!CAUTION]
> **LOCAL MACHINE ONLY. NO AUTHENTICATION OR SECURITY BOUNDARY. NEVER EXPOSE PUBLICLY.**
> Run the UI locally against your working database and keep its default loopback
> binding. Do not publish or port-forward it. Namespace selection is not access
> control, and the UI can read and change data using its database credentials.

## Attachments

Task details show files below the description and below the comment they belong
to. Choose files and **Upload files** in either location. To attach to a new
comment, post the comment first, then use its file picker. Uploads run one file
at a time, so a failed file does not roll back earlier successful uploads.

The CLI uses the same attachment records and private objects:

```sh
looster task file add TSK-1 ./screenshot.png
looster task file add TSK-1 ./screenshot.png --comment EVENT_ID
looster task file list TSK-1
looster task file get TSK-1 FILE_ID ./download.png
```

Configure the existing `LOOSTER_S3_*` settings on the dashboard server, not in
the browser. Keep the bucket private. No public bucket policy or browser S3
credentials are needed. Without an S3 bucket the rest of the dashboard works,
but attachments report that storage is unavailable. Invalid configured storage
credentials fail dashboard startup rather than silently disabling attachments.

Dashboard uploads are limited to 10 MiB per file. PNG, JPEG and GIF content can
be previewed only after server-side decoding, with an 8192-pixel side limit and
16-megapixel total limit. Previews are re-encoded as PNG. SVG, HTML and other
files remain download-only, including older CLI attachments. Images that exceed
preview limits can still be downloaded within the dashboard's file-size limit.
Namespace and task selection scope file access, but are not user authentication.
The trusted-local-only warning above still applies.

## Navigation

The dashboard opens on **Graph**, the leftmost navigation tab, without a hint
ribbon. **List** (formerly Board) remains beside it. The old `#board` bookmark
still opens List. Navigation links show their shortcuts:

| Keys | View |
| --- | --- |
| `gg` | Graph |
| `gl` | List |
| `gs` | Servers |
| `gp` | Performance |
| `gb` | Behaviours |

Press both keys within 700 ms, outside text fields or dialogs. Former H/L tab
navigation is removed. `G` still jumps to the last task or page bottom. Dialogs
retain `gg`/`G` for top/bottom scrolling. The namespace switcher next to the logo
changes the namespace and clears the prior view.

## Performance

Open **Performance** with `gp` or the `#performance` bookmark. Seven line charts
show 30 daily UTC buckets, including today's partial bucket. Counters cover the
exact trailing seven days (168 hours). Both resolutions are fixed.

Task metrics are validated count, closed count, and validated divided by closed
(shown as a percentage, or N/A when closed is zero). Each task counts once using
its current status and latest activity timestamp, not each status transition.
Later comments or task-associated session logs can move that task to a newer day,
so these charts are current-state snapshots rather than historical completion
throughput. Planning and todo tasks do not contribute to these outcome counts.

Blocker and question reports count immutable session-log observations by report
time. Task-associated reports appear under **Tasks**. Reports without a task
appear separately under **Autonomous agents**, and are not included in task
report counters. Other log kinds are excluded. Data is scoped to the selected
namespace, refreshes with the dashboard, and can be retried with **Refresh** or
`r`. Expand **Daily data (UTC)** for exact accessible chart values.

## Reading the list

Each task-stage and agent column has its own server-backed page of **15 tasks**.
Previous and Next controls at the bottom show the visible range, total matching
tasks and current page. Controls are keyboard-accessible, report loading and
errors, and offer Retry without discarding the current cards. Agent columns
contain only tasks with currently running sessions. **Other sessions** continues
to show active taskless runs rather than task pages.

Changing one column's page does not change the other columns. Polling, manual
refresh, stage visibility and offline-agent toggles retain page selections.
Search changes or namespace switches reset pages to the first page. If tasks
are removed or move between columns, the server clamps an unavailable last page
to the new last page, including an empty first page.

List search runs against the entire selected namespace, not just loaded cards.
List uses case-insensitive substring matching against task name, description,
status, `TSK-ID`, tags and associated agents. The global command palette uses a
dedicated `/api/palette/tasks` read port: PostgreSQL ranks tasks across all stages
before returning at most **50 task candidates**. It mirrors the client fuzzy
scorer's greedy ordered-character, adjacency, word-start and length scoring,
with task-label ordering for ties. The client merges those candidates with other
palette item kinds and selects the final 50 results. Palette task search fields
are exactly `TSK-ID`, name, description, status and sorted tags, not associated
agents or session logs. Regex-shaped input remains literal, not executable regex.
On UTF-8 PostgreSQL with ICU, the SQL scorer uses locale-independent Unicode
lowercasing, including dotted I and final sigma, and UTF-16 scoring positions.
Other installations upgrade normally and use portable C/ASCII lowercasing.
UTF-8 databases retain UTF-16 positions, while other encodings use native
positions. ASCII-only task/search strings retain exact client scoring, but
non-ASCII matches can rank differently without UTF-8 and ICU. No database
conversion or additional extension is required for this change.
Planning, todo and agent columns promote historical blocker logs before question
logs, then sort by latest activity with ascending task ID as a tie-breaker.
Validated and closed columns sort only by activity and ID. Details and CLI task
commands retain their existing behavior.

List uses `GET /api/board/list`, with `search`, optional `namespace` and `pages`
(a JSON object mapping column keys to one-based page numbers, for example
`{"todo":2,"agent:coder":3}`). The response includes column `tasks`, `total`,
`page` and fixed `page_size`, alongside agent/session placement metadata. The
repository counts and selects each page in a read-only repeatable-read snapshot,
then hydrates tags, counters and session-log bodies for only those selected task
IDs. It never calls the unbounded task-list repository method. Counts, ordering
and search still inspect matching database records, and placement metadata spans
the namespace, but task and log payload hydration is bounded per column.
Graph deliberately retains `GET /api/board` for its whole-dataset graph view.

## Reading the graph

Only unfinished tasks (`planning` and `todo`) and their actual session links
appear by default. Completed tasks (`validated` and `closed`) are excluded unless
selected in the status controls. Agents linked to visible tasks appear, alongside
agents with active taskless sessions as disconnected nodes. A mixed-use agent
keeps its legitimate task links. Taskless sessions remain visible when task search,
tag, or status filters exclude all tasks, and disappear when no longer active.
Links represent actual task sessions, not planned
behaviour rules. Animated links indicate current work; quiet links indicate prior
or idle sessions. Agent online status comes from live worker coverage, independent
of whether the agent is working on a particular task.

Online agents and tasks with running sessions have blue highlights. Inactive
stage rings and offline agents use theme-aware black/white highlights. Task nodes
show `comments/state changes` counters such as `3/10` and a three-segment status
ring: planning fills one segment, todo fills two. State-change counts include the
initial status, matching List. Click tasks or agents for details. Labels stay
compact; hover/focus and task details expose full titles.

## Filtering and controls

The graph has +/− zoom buttons and a heatmap toggle. The **Tags** dropdown accepts
multiple tags, matching any selected tag. With no selection it shows all
unfinished tasks, including untagged ones. Tag filters combine with search,
survive polling in the current client, and reset on reload or namespace switch.
Filtered tasks lose their links, and unrelated agents disappear too, except for
agents with active taskless sessions.

## Agent run history

Click an agent in Graph or the Behaviours workflow, or an **Other sessions**
card in List, to open the shared agent inspector. Native buttons also support
keyboard activation. The inspector retains the agent prompt and memories and
adds paginated run history with individual run summaries, outcomes, task and
session identities, start/finish times, and errors. History does not require a
task and is scoped to the selected namespace.

Every new agent execution, including taskless and resumed executions, is asked
to finish with a 4-5 sentence summary covering attempted work, outcomes,
verification, blockers, and unfinished work. The final structured harness
response is saved as the summary, not arbitrary intermediate output. Missing
summaries are shown explicitly rather than inferred as success. Failed and
terminally interrupted jobs retain their outcome and error even without a
summary.

History is append-only and keyed by queued job, so successive scheduled jobs
retain separate records even if they share a session identity. Records become
available when a job reaches a terminal outcome. Worker cancellation and archive
upload failures leave the same job resumable and do not prematurely finalize its
history. Existing jobs completed before this feature are not backfilled. Apply
database migration 017 before running upgraded workers.

The read-only endpoints are `GET /api/agents/{name}/runs` (with `limit` and
`before_job_id` pagination) and `GET /api/agents/{name}/runs/{jobID}`. They use the
same namespace selection and trusted-local-only security model as the dashboard.

## Other sessions in List

The far-right **Other sessions** column shows running sessions without a task
assignment, including scheduled and monitoring agents. Concurrent runs have
separate cards identified by their durable job IDs, even before a harness session
ID is assigned. These cards do not open task details or invent task references.
Standalone-only agents are omitted from ordinary agent columns, while mixed-use
agents retain their task-bound sessions. Completed, interrupted, and stale-worker
runs are not shown as active cards. Liveness requires both live worker coverage
and the run's advisory lease.

Heat means comment count plus state-change count, not errors or task health.
Heatmap activity does not invent relationships or change their link weights.
Search and the namespace switcher apply to Graph as well as other views.

The List hides task-stage columns and offline agent columns by default. Use
**Show task stages** (`s`) and **Show offline agents** to reveal them independently.
An agent is online when a live worker covers it, regardless of session status.
These visibility choices survive polling, navigation, and namespace switches in
the current client, but reset on reload. No list visibility preferences are persisted.

## Layout and activity aging

Locally vendored D3 force physics uses weighted task-agent attraction, repulsion,
and collision handling. The layout moves and settles in the browser. Dragging or
changed relationships reheats it. Active work attracts more strongly than past
sessions, so shared agents form clusters. Unchanged polling keeps positions
stable, and dragged nodes remain anchored when relationships change.

Positions and anchors exist only in the current web client's memory. They are
never saved to the database or sent to the server, and reset on reload or namespace
switch. The D3 bundle and its third-party license ship in the Go executable.
There is no Node.js, npm, bundler, package installation, JavaScript build step,
or runtime CDN requirement for the running dashboard.

Time since the last task activity reduces circle size and attraction, with
minimums to keep inactive work visible and connected. Tasks without event history
fall back to creation time. Missing or invalid dates count as fresh. Age is
computed in whole-day buckets rather than continuously rearranging the graph.
The diameter falls from 60 graph units when fresh to about 29 after 30 inactive
days, approaching a 24-unit minimum. Running tasks remain full-sized. New activity
revives size and attraction. Heatmap color remains independent of age.

One CSS scale ages all task-node contents together: circle, ring, counters and
labels. Collision envelopes use the same scale, while graph positions and edge
anchors remain unscaled. The additive migration adds an event-time lookup index,
not stored graph positions. Run `looster db migrate` when updating.
