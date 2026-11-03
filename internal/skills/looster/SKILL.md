---
name: looster
description: Use the Looster CLI to work on orchestrated agent tasks, share task comments and memories, and exchange task attachments. Use when assigned a Looster task or asked to interact with Looster.
---

# Looster

Looster coordinates tasks through PostgreSQL and stores files and completed Jcode
session histories in S3. Managed harnesses receive `LOOSTER_ROLE=agent` and their
`LOOSTER_AGENT` identity and `LOOSTER_NAMESPACE`; the CLI enforces the agent's permissions in Go. All
processes share one database connection configuration, but work is scoped to the
selected namespace. Never switch namespaces or override `LOOSTER_NAMESPACE` to
access unrelated tasks or systems.
Credentials belong exclusively in environment variables. TOML may contain only
non-secret runtime settings; never put database URLs or S3 keys in TOML,
comments, memories, attachments, or skill files.

## Find the task and context

In a managed harness, `LOOSTER_TASK_ID` identifies the task and `LOOSTER_AGENT`
identifies the agent. If those are absent, use the task and agent supplied by the
user; `looster task ls` and `looster agent ls` can help discover them. Do not choose
an unrelated task when the assignment is unclear.

```bash
looster task "$LOOSTER_TASK_ID"
looster agent "$LOOSTER_AGENT"
looster task follow "$LOOSTER_TASK_ID" --once
looster task file list "$LOOSTER_TASK_ID"
```

Task detail includes its tags. Read relevant tag memories with `looster tag NAME`
or `looster tag memory list NAME`. Agent detail includes that agent's memories.
The server also supplies task context, comments, and relevant memories in the
session prompt. Managed workdirs are named `AGENT-TSK-n`; use the assigned workdir.

Detail and list commands return JSON. Tasks use `TSK-n`, memories use `MEM-n`, and
tags are addressed by name, such as `design/frontend`. Use actual returned IDs in
place of the illustrative IDs below.

## Record work and outcomes

Task statuses are exactly `planning`, `todo`, `validated`, and `closed`.
Closing a task terminates active agent runs and prevents queued task jobs from
starting while it remains closed. Completed runs may still archive their history.
`running`, `idle`, and `failed` describe sessions, not task statuses. Preserve the
workflow's status conventions. Work awaiting review stays in `todo`; record the
handoff in a comment and let AFTER rules dispatch review. Mark `validated` only
when the assigned validation has actually been completed.

```bash
printf '%s\n' 'Implemented the change; checks passed; ready for review.' | looster task comment TSK-1
# Leave the task in todo until independent validation is complete.
```

Entering `todo` can dispatch agents through behaviour rules. Do not toggle a task
out of and back into `todo` merely to refresh it: this schedules additional work.
Comment on blockers and failed checks so the next agent can act on them. Avoid
putting transient progress in durable memories.

Descriptions and comments come from stdin. When task creation is part of the
assignment, use existing tags and an explicit status:

```bash
printf '%s\n' 'Implement keyboard navigation and report the checks performed.' | looster task create 'Keyboard navigation' -t design/frontend --status planning
```

## Share reusable context

Agents can read all agent and tag memories. They can modify tag memories and only
their own agent memories, as enforced by the application role. Agent memories
are not secret storage. Agents cannot create or remove agents or tags, or change
behaviour rules; have the operator make those changes when needed.

```bash
printf '%s\n' 'Shared convention: use semantic HTML for interactive controls.' | looster tag memory add design/frontend
printf '%s\n' 'Run the project test command before handing work to review.' | looster agent memory add "$LOOSTER_AGENT"
looster agent memory list "$LOOSTER_AGENT"
looster agent memory rm "$LOOSTER_AGENT" MEM-1
```

`memory wipe NAME` replaces all memories for that owner with stdin; empty stdin
clears them. Use it only when replacing or clearing the whole set is intended.

## Exchange files

```bash
looster task file add TSK-1 ./report.txt
looster task file list TSK-1
looster task file get TSK-1 1 ./downloaded-report.txt
```

`file add` returns attachment metadata including its ID. `file get` takes a numeric
attachment ID and refuses to overwrite an existing output file; output `-` writes
the file bytes to stdout. To attach a file to a comment, first create the comment,
then pass its returned `event_id` to `file add --comment EVENT_ID`. This is a
comment event ID, not a task or attachment ID.

## Sessions, monitoring and configuration

The server starts or resumes the single session for each agent/task pair and
archives its full history on completion. Do not invent or rebind session IDs as
part of ordinary task execution. When explicitly linking an existing Jcode session,
use its real ID:

```bash
looster task bind coder session_existing --task TSK-1
looster agent sessions coder
looster task ps --once
looster logs --task TSK-1 --since 1h --json
looster task follow TSK-1 --after 20 --once
```

`task follow` streams NDJSON indefinitely unless `--once` is supplied; `--after`
resumes after an event ID. `task ps` also refreshes indefinitely without `--once`.
`looster logs` shows recent system-wide events; filter with `--task`, `--agent`,
`--kind` or `--since`. It exits after a snapshot unless `-f` is provided. Use
`--json` for NDJSON. Standalone scheduled runs appear without a task ID, and
`--agent` includes changes made by that agent as well as its run events.
Session completion can ping a downstream agent on the same task; it does not
automatically change task status. Do not start a new server just to execute an
assigned task.

`AFTER` handoffs carry the triggering task's context and the latest reported
results of peer agents. Review that concrete task; the source agent's session ID
is context and should not be used to replace your own session binding.

Recurring `EVERY ... PING ... WITH "..."` rules start standalone sessions with
the agent's configured instructions and supplied scheduled prompt. No task is assigned.
Non-default namespaces use isolated scheduled workdirs; the default namespace
keeps its historical no-workdir behavior. Inspect or create tasks
only as the prompt requires. Scheduling belongs to the server; only humans can
change complete manifests, including `behaviour apply` and version activation.

Changes to an attached task are delivered to the other agent sessions; the agent
that made the change is excluded. Notifications resume a session after any active
run finishes. Act on new work or blockers, and avoid acknowledgement-only comments
that would create more notifications. A deletion notification means stop working
on that task. Notification delivery itself does not trigger AFTER COMPLETED rules.

`LOOSTER_DATABASE_URL` configures PostgreSQL. Without a connection string, the CLI
also accepts `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD` (or `PG_PASS`), `PGDATABASE`,
and `PGSSLMODE`, plus password-file and other supported PG settings. Explicit
connection-string fields override those environment defaults. Managed harnesses
inherit the shared database settings along with their agent role and identity.
File commands additionally use
`LOOSTER_S3_ENDPOINT`, `LOOSTER_S3_REGION`, `LOOSTER_S3_BUCKET`, and optionally
`LOOSTER_S3_ACCESS_KEY` with `LOOSTER_S3_SECRET_KEY`; without explicit keys, the
standard AWS credential chain applies. The server supplies the managed harness's
shared database and S3 settings. Keep the assigned agent role and identity when
calling the CLI. For manually configured agent calls, use `--role agent` and
`--agent-name NAME` or the equivalent environment variables.

For an existing TOML config, use `looster --config PATH COMMAND`. The default is
`~/.config/looster/config.toml` (or `$XDG_CONFIG_HOME/looster/config.toml`). Flags
override environment variables, which override TOML. Inspect `looster --help` or
`looster task --help` for additional commands and options.
