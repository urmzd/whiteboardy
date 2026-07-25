<div align="center">

# whiteboardy

**Timeboxed system design and coding practice, with a coach that watches you work.**

[![CI](https://github.com/urmzd/whiteboardy/actions/workflows/ci.yml/badge.svg)](https://github.com/urmzd/whiteboardy/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-beta-orange.svg)](#known-gaps)

<img src="showcase/demo.png" alt="A senior-level system design session: the brief on the left, the board in the middle, the coach on the right" width="80%">

</div>

> [!WARNING]
> **Beta, and still in testing.** The harness works end to end, but prompts, scoring
> calibration, and the session format are all still moving. Scores are directional, not
> authoritative. Expect rough edges and breaking changes to the saved-session format.

A native desktop app that generates a practice exercise, times you, interrupts when it is
worth interrupting, and scores what you actually produced against a rubric written before
you started. Runs fully local against [Ollama](https://ollama.com), or against a hosted
model if you would rather.

## Why

Practice tools tend to hand you a problem and a solution. The gap between "I read the
answer and it made sense" and "I can produce that under a clock" is the thing that
actually needs training, and nothing closes it for you.

whiteboardy is built around that gap:

- **A clock that matters.** Every exercise is scoped to the timebox, and pacing nudges
  fire on arithmetic, not vibes.
- **A coach that mostly stays quiet.** It speaks when you are about to build on a bad
  assumption, when a high-weight area is running out of time, or when a choice is worth
  questioning. Silence is the default, because interrupting someone mid-thought is worse
  than saying nothing.
- **A hidden rubric.** Written at generation time, revealed only in the debrief, so you
  cannot teach to the test.
- **Curveballs.** Partway through, a requirement changes, the way it does when an
  interviewer notices you look comfortable.
- **A profile that accumulates.** Rubric criteria map onto a fixed skill taxonomy, so
  scores compare across sessions. That is the part that tells you where you actually are.

## Two modes

| Mode | Surface | What it trains |
|------|---------|----------------|
| **System design** | A board of labeled components and annotated connections | Scoping, data modeling, scaling math, failure modes, tradeoff reasoning |
| **Coding** | An editor seeded with a signature and a worked example | Decomposition, correctness, edge cases, complexity, clarity |

The board is a node graph rather than a freehand canvas on purpose: the coach reads your
components, connections, and annotations as structure, which is what makes real critique
possible instead of pattern-matching on pixels.

## Install

Needs [Go 1.25+](https://go.dev), [Node 22+](https://nodejs.org), and
[Wails v2](https://wails.io). Prebuilt binaries land on the
[releases page](https://github.com/urmzd/whiteboardy/releases) once tagged.

```bash
git clone https://github.com/urmzd/whiteboardy
cd whiteboardy
make init
make build
```

The app lands in `build/bin/`. For a live-reloading dev loop, use `make dev`.

## Quick start

1. Install a model. Anything in the 7-9B range and up is usable; smaller models produce
   noticeably thinner rubrics.
   ```bash
   ollama pull qwen3.5:9b
   ```
2. Launch whiteboardy, pick a provider and model on the setup screen, and hit
   **Test connection**.
3. Choose a mode, a level, and a timebox. Start. Generation takes a moment on a local
   model: it writes the brief, then the hidden rubric.
4. Work. The coach reads your board or code on an interval and speaks only when it has
   something worth saying.
5. Hit **Finish & score** for the debrief, then check **Progress** once you have a few
   sessions behind you.

<div align="center">
  <img src="showcase/demo-setup.png" alt="The setup screen: mode, level, timebox, provider, and model" width="80%">
</div>

Sessions are written to `~/.whiteboardy/sessions` as plain JSON. Nothing leaves your
machine unless you point it at a hosted provider.

## The debrief

Every criterion is scored 0-4 with the evidence that earned it, quoted from your board
and your notes. The overall number is computed from those scores and their weights, so it
cannot drift from the reasoning behind it.

<div align="center">
  <img src="showcase/demo-review.png" alt="The debrief: an overall score, a verdict, and per-criterion scores with quoted evidence" width="80%">
</div>

After a few sessions, the per-criterion scores aggregate by skill area. Ranking is
withheld until an area has at least two samples, and trend needs three, because calling
something a weakness off one data point is noise dressed as insight.

<div align="center">
  <img src="showcase/demo-progress.png" alt="The progress screen: sessions, average score, and a bar per skill area" width="70%">
</div>

## Providers

| Provider | Model discovery | Notes |
|----------|-----------------|-------|
| Ollama | Automatic | Default. Fully local, no key. |
| Anthropic | Manual model id | Needs an API key |
| OpenAI | Manual model id | Needs an API key |
| Google | Manual model id | Needs an API key |

Provider plumbing comes from [saige](https://github.com/urmzd/saige)'s agent SDK.

## Architecture

The backend drives the UI over an [AG-UI](https://github.com/ag-ui-protocol/ag-ui)-shaped
event stream: a run is bracketed by `RUN_STARTED` / `RUN_FINISHED`, agent speech arrives
as `TEXT_MESSAGE_START` / `CONTENT` / `END`, and shared state moves as `STATE_SNAPSHOT`
plus `STATE_DELTA`. The frontend is a pure reduction of that stream and never polls, so
the agent decides when to respond rather than the UI asking. The transport is Wails'
event bus rather than AG-UI's SSE encoding, since this is a single process; a networked
mode would be a transport swap, not a redesign.

| Package | Role |
|---------|------|
| `internal/domain` | Data model and the fixed skill taxonomy that makes scores comparable |
| `internal/harness` | Problem generation, the coach, the reviewer |
| `internal/agui` | Event vocabulary and the typed emitter |
| `internal/session` | Clock, curveball timeline, coach loop, review transition |
| `internal/store` | Session persistence and profile aggregation |
| `internal/llm` | Provider construction and structured generation |

Two design notes worth knowing before hacking on it:

**Generation is two calls, not one.** A single schema covering the brief, the rubric, the
curveballs, and the outline is too much for a small local model to hold at once: push on
one field in the prompt and another silently collapses. Splitting it also lets the second
call read the brief that was actually produced, so criteria grade the exercise that
exists rather than the one that was requested.

**A coaching tick is also two calls.** A cheap schema-constrained decision (speak or stay
quiet, and about what), then, only if it speaks, a streamed message body. Silence costs
one small call. The streaming is what makes the coach read as someone watching rather
than a notification that pops in fully formed.

## Development

```bash
make check   # fmt, vet, lint, test, typecheck
make test    # Go tests only
make live    # exercise the harness against a real model (needs ollama)
make dev     # live-reloading app
```

`make live` is how prompt changes get validated. It asserts the things that actually
matter: that generated rubrics map onto the taxonomy, that curveballs exist, that the
coach streams rather than emitting one blob, and that near-empty work does not score
well. Override the model with `make live MODEL=qwen3.5:2b`.

After changing any exported method on `App`, run `make bindings` to regenerate the
TypeScript.

## Known gaps

This is where the beta label comes from.

- Scoring calibration is unvalidated across models. A 3B model and a 9B model do not
  grade the same work the same way, and neither has been checked against human judgment.
- Coach silence is a soft bias, not a guarantee. It sometimes speaks earlier than it
  should.
- The saved-session format is not stable yet, so old sessions may stop aggregating.
- No import, export, or replay of sessions.
- Coding mode has no execution and no test runner. Correctness is judged by reading.

## License

[Apache-2.0](LICENSE)
