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
| `gb` | Behaviours |

Press both keys within 700 ms, outside text fields or dialogs. Former H/L tab
navigation is removed. `G` still jumps to the last task or page bottom. Dialogs
retain `gg`/`G` for top/bottom scrolling. The namespace switcher next to the logo
changes the namespace and clears the prior view.

## Reading the graph

Only unfinished tasks (`planning` and `todo`) and their actual session links
appear. Completed tasks (`validated` and `closed`) are excluded. Only agents
linked to visible tasks appear. Links represent actual task sessions, not planned
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
Filtered tasks lose their links, and unrelated agents disappear too.

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
