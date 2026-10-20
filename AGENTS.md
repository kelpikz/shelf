# AGENTS.md

`shelf` is a personal host for static files. The Owner uploads a file or a folder with a CLI, and the server serves it at `files.ajitha.fyi/<path>`. Ajitha is the Owner. The project is for learning, for personal use, for a resume, and for an article. The repo will be public.

## Status

Building v1. The server has the GitHub login and the Admin page sessions. The Sites part, the CLI, and the deploy are not built yet. The planning map shows the open tickets.

## Stack

- **Server:** Go and SQLite, in `server/`. One process serves two hosts: `files.ajitha.fyi` (the Sites and the Index page) and the admin host, for example `files-admin.ajitha.fyi` (the Admin page and the API).
- **CLI:** Rust, in `cli/`. The command is `shelf`.
- **Deploy:** one Docker image with one data volume. It runs behind the nginx that is on the droplet (DigitalOcean, 1 vCPU, 1 GB RAM). The Owner does the deploy.

## Where things are

| Path | What it is | In git |
|---|---|---|
| `CONTEXT.md` | The glossary. Use its words: Site, Owner, Visitor, Visibility, Archived, Index page, Admin page, Upload check. | Yes |
| `docs/adr/` | Records of decisions that are hard to change. | Yes |
| `research/` | Research reports. `research/README.md` is the brief for research agents. | Yes |
| `.scratch/v1-spec/map.md` | The planning map: the scope of v1, the decisions so far, and what is out of scope. | No |
| `.scratch/v1-spec/issues/` | The tickets. Each ticket holds one decision and its reasons. | No |
| `.scratch/v1-spec/assets/modules-draft.md` | The draft of the modules, the API, and the CLI commands. | No |
| `.scratch/sessionNotes/` | Short notes from each session. | No |

`.scratch/` is in `.gitignore`. Do not commit it. Some search tools skip ignored files, so open these files by their path.

## Rules

- **Before you build a part, show the Owner the decisions and the defaults for that part. Then wait for a yes.**
- Choose the simplest solution that works.
- Do not start dev servers. Ask the Owner to start them.
- Do not start research agents. The Owner runs research tickets with their own Codex agents. Prepare the ticket, and give the Owner a one-line prompt.
- Ask the Owner about scope only. For a small detail, write a default and continue.
- Do not put secrets in the repo. All config comes from env variables, for example `OWNER_EMAIL`. Local values go in `.env`, which is in `.gitignore`.
- To continue the planning in Claude Code, use `/wayfinder` with `.scratch/v1-spec/map.md`.

## Writing

- Text for people (docs, notes, and replies) uses ASD-STE100 Simplified Technical English: short sentences, simple words, and active voice.
- Explain new terms in plain words. Tell the Owner about risks that they possibly do not know about.

## Session notes

At the end of each session, write a short note in `.scratch/sessionNotes/YYYY-MM-DD-<short-slug>.md`. Write what you did and what comes next.

## Commits

- Keep the message short. Start it with `feat:`, `fix:`, `chore:`, or `doc:`.
- Do not add a co-author line.
