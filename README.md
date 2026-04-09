# fishnet

A fast, local GraphRAG + social simulation CLI — the Go successor to MiroFish.

**~15-20x faster** than Python-based alternatives. No Zep, no REST API, no Docker required.

## Features

- **Knowledge Graph** — Ontology-first entity extraction from any document collection, stored in local SQLite
- **Dual-Platform Simulation** — Twitter (Info Plaza) + Reddit (Topic Community) agent simulation across multiple rounds
- **ReAct Report Agent** — Multi-tool reasoning with InsightForge, PanoramaSearch, and QuickSearch
- **Interactive TUI** — Full 5-step workflow UI powered by Bubbletea; no commands needed
- **Session Management** — Save, fork, modify, and restore simulation configurations
- **Batch Interview** — Question all agents concurrently with up to 4 parallel LLM calls
- **Graph Feedback Loop** — Simulation actions written back to the knowledge graph
- **Branching Simulations** — Run multiple "what-if" scenario variants in parallel (auto or manual)
- **Copy Reaction Testing** — Inject marketing copy into a running simulation and measure agent sentiment
- **Graph Visualization** — ASCII summary in the terminal or interactive browser-based graph
- **CLIProxyAPI Support** — Route through a local proxy server for API credential pooling

## Installation

```bash
git clone https://github.com/yourname/fishnet
cd fishnet
go build -o fishnet .
```

Or install globally:

```bash
go install .
```

**Requirements:** Go 1.21+, an LLM API key (OpenAI, Anthropic, Ollama, or Codex)

## Quick Start

### Interactive TUI (Recommended)

```bash
export OPENAI_API_KEY=sk-...
./fishnet
```

The TUI launches automatically when no subcommand is given and guides you through the full 5-step workflow. Navigate with arrow keys, number keys, and Enter.

### CLI Workflow

```bash
# 1. Initialize a project
fishnet init myproject --dir ./docs

# 2. Set your API key (or use env var)
export FISHNET_API_KEY=sk-...

# 3. Analyze documents and build the graph
fishnet analyze

# 4. Check graph stats
fishnet graph stats

# 5. Run a simulation
fishnet sim platform --scenario "AI regulation debate" --rounds 10

# 6. Generate a report
fishnet report generate --scenario "AI regulation debate" --output report.md

# 7. Interview an agent
fishnet interview Alice
```

## Commands

### `fishnet init`

Initialize a project directory, create `.fishnet/config.json`, and set up the local database.

```bash
fishnet init <project-name> [flags]

# Examples
fishnet init myproject
fishnet init copylab --dir ./brand-docs --provider anthropic --model claude-sonnet-4-6
```

| Flag | Description |
|------|-------------|
| `--dir` | Source documents directory (default: `.`) |
| `--model` | LLM model name |
| `--provider` | LLM provider (openai\|anthropic\|ollama\|codex\|codex-cli\|clicliproxy) |
| `--api-key` | LLM API key |
| `--base-url` | LLM base URL |

---

### `fishnet analyze`

Read all `.txt`, `.md`, `.rst`, `.csv`, and `.json` files from the source directory, chunk them, extract entities via LLM, and store in the local graph.

```bash
fishnet analyze [flags]

# Examples
fishnet analyze
fishnet analyze --dir ./docs --concurrency 8
fishnet analyze --dir ./reports --chunk-size 800
fishnet analyze --community    # also run community detection after extraction
fishnet analyze --docs-only    # only load docs, skip graph extraction
```

| Flag | Description |
|------|-------------|
| `--dir` | Documents directory (default: from config) |
| `--concurrency` | Concurrent LLM calls (default: from config) |
| `--chunk-size` | Characters per chunk (default: from config) |
| `--chunk-overlap` | Overlap between chunks (default: from config) |
| `--docs-only` | Only load documents, skip graph extraction |
| `--community` | Run community detection after extraction |
| `--ontology` | Generate domain ontology before extraction (default: true) |

---

### `fishnet graph`

Graph inspection and visualization commands.

```bash
fishnet graph stats              # show node/edge/document/community counts
fishnet graph show               # print ASCII graph summary
fishnet graph web                # open interactive browser visualization
fishnet graph community          # run Louvain community detection
fishnet graph community --summarize --min-size 3   # with LLM summaries

# Search the graph
fishnet graph search quick    --query "AI policy" --limit 20
fishnet graph search panorama --query "social media" --limit 50
fishnet graph search insight  --query "what do experts say about regulation?"
```

**Search modes:**

| Command | Description |
|---------|-------------|
| `quick` | Keyword search on node names and summaries |
| `panorama` | Broad search — returns all matching nodes plus their edges |
| `insight` | LLM decomposes query into sub-questions, runs QuickSearch for each |

---

### `fishnet sim`

Social simulation commands.

#### `sim platform` — Multi-round Twitter/Reddit simulation

```bash
fishnet sim platform --scenario "AI regulation debate" --rounds 10
fishnet sim platform --scenario "Product launch" --rounds 5 --platforms twitter
fishnet sim platform --scenario "Climate policy" --agents 30 --output ./sim-out
```

| Flag | Description |
|------|-------------|
| `--scenario` | Simulation scenario/topic (required) |
| `--rounds` | Number of simulation rounds (default: 10) |
| `--agents` | Max agents — 0 means all nodes (default: 0) |
| `--platforms` | Comma-separated: `twitter`, `reddit`, or both (default: both) |
| `--output` | Directory to save `actions.jsonl` |
| `--concurrency` | Max concurrent LLM calls (default: 6) |
| `--quiet` | Suppress per-action feed output |
| `--branches` | Branch mode: `auto` to generate branches via LLM |
| `--branch-count` | Number of auto branches to generate (default: 2) |
| `--branch` | Explicit branch: `name:description` (repeatable) |

#### `sim run` — Copywriting simulation

```bash
fishnet sim run --scenario "We're launching an AI assistant for small businesses"
fishnet sim run --scenario "New policy restricting social media for teens" --agents 20
fishnet sim run --scenario "Product launch" --output result.json --quiet
```

#### `sim branch` — Parallel "what-if" branching

Run the base scenario plus one or more variants concurrently. Each branch is a full independent simulation from round 0.

```bash
fishnet sim branch --scenario "AI regulation" --branches auto --rounds 10
fishnet sim branch --scenario "Product launch" \
  --branch "govt:government bans it" \
  --branch "viral:goes viral overnight"
```

| Flag | Description |
|------|-------------|
| `--branches` | Branch mode: `auto` or use `--branch` flags |
| `--branch-count` | Number of auto branches (default: 2) |
| `--branch` | Explicit branch definition: `name:description` (repeatable) |
| `--max-branches` | Max simultaneous branches (default: 3) |

#### `sim copy-react` — Copy/content reaction testing

Inject marketing copy as a Brand post at a specified round and measure agent reactions.

```bash
fishnet sim copy-react --copy "Our new AI assistant..." --round 2 --platform twitter
fishnet sim copy-react --copy "Introducing MiroFish" --title "Big announcement" --agents 20
```

| Flag | Description |
|------|-------------|
| `--copy` | Copy text to test (required) |
| `--title` | Optional headline for the copy |
| `--platform` | Platform context: `twitter` or `reddit` (default: twitter) |
| `--round` | Round at which to inject the copy (default: 1) |
| `--rounds` | Total simulation rounds (default: 5) |
| `--agents` | Max agents (default: 0 = all) |

#### `sim oasis` — OASIS Python wrapper

Launch the Python OASIS simulator with a config auto-generated from the knowledge graph. Requires the `oasis` binary in PATH or `OASIS_PATH` env var. For a built-in 15-20x faster alternative, use `sim platform`.

```bash
fishnet sim oasis --scenario "AI regulation debate" --rounds 10
fishnet sim oasis --scenario "Product launch" --config ./oasis_config.json
```

#### `sim stop` / `sim status` / `sim list`

```bash
fishnet sim stop          # send SIGTERM to a running simulation
fishnet sim status        # check whether a simulation is running
fishnet sim list          # list past simulations (last 50)
```

---

### `fishnet report`

#### `report generate`

Run the ReAct report agent to produce a full Markdown analysis of the knowledge graph.

```bash
fishnet report generate --scenario "AI regulation debate"
fishnet report generate --scenario "Product launch" --output report.md
```

| Flag | Description |
|------|-------------|
| `--scenario` | Scenario description for the report (required) |
| `--output` | Save report as Markdown to this file |

---

### `fishnet interview`

Interview a graph entity as an in-character persona.

```bash
# Single question (non-interactive)
fishnet interview Alice --question "What do you think about AI regulation?"

# Interactive REPL (type 'exit' to quit)
fishnet interview "Elon Musk"

# Batch interview — all agents, concurrently
fishnet interview --all --question "What is your stance on {scenario}?"

# Batch interview — specific agents
fishnet interview --batch "Alice,Bob,Carol" --question "What's your position?"
```

| Flag | Description |
|------|-------------|
| `--question` | Single question (non-interactive mode) |
| `--all` | Interview all agents in the graph concurrently |
| `--batch` | Comma-separated agent names to interview concurrently |

---

### `fishnet session`

Save, restore, and fork simulation configurations.

```bash
# List all sessions
fishnet session list

# Save a session
fishnet session save \
  --scenario "AI regulation debate" \
  --name "baseline" \
  --rounds 10 \
  --platforms twitter,reddit \
  --tags "experiment,policy"

# Show session details
fishnet session show <id>

# Run a saved session
fishnet session run <id>
fishnet session run <id> --rounds 20 --scenario "revised scenario"

# Fork a session (copy with new ID)
fishnet session fork <id> --name "variant-a"

# Modify session fields
fishnet session modify <id> --rounds 15 --notes "extended run"

# Delete a session
fishnet session delete <id>
```

---

### `fishnet query`

Query simulation results stored in the database.

```bash
# List recent simulations for the current project
fishnet query sims
fishnet query sims --limit 20

# List posts from a simulation
fishnet query posts --sim <sim_id>
fishnet query posts --sim <sim_id> --platform twitter --limit 20

# List actions from a simulation
fishnet query actions --sim <sim_id>
fishnet query actions --sim <sim_id> --agent alice --type CREATE_POST --limit 50

# Show chronological timeline
fishnet query timeline --sim <sim_id>
fishnet query timeline --sim <sim_id> --limit 30

# Per-agent statistics
fishnet query stats --sim <sim_id>
```

---

### `fishnet config`

```bash
fishnet config show
fishnet config set model gpt-4o
fishnet config set provider anthropic
fishnet config set api-key sk-...
fishnet config set base-url http://localhost:8080/v1
```

## TUI Navigation

Launch the interactive TUI with `fishnet` (no arguments). It guides you through the full 5-step workflow with live feedback.

| Key | Action |
|-----|--------|
| `1`–`5` | Switch between workflow steps |
| `↑` / `↓` or `j` / `k` | Navigate lists |
| `←` / `→` or `h` / `l` | Switch panels |
| `Enter` | Confirm / run |
| `/` | Search |
| `s` | Session browser |
| `q` | Query history |
| `?` | Help overlay |
| `Esc` | Back to dashboard |
| `Ctrl+C` | Quit |

## Configuration

Config is stored at `.fishnet/config.json` (project-local) or `~/.fishnet/config.json` (global). Local config takes precedence.

```json
{
  "project": "myproject",
  "db_path": ".fishnet/fishnet.db",
  "llm": {
    "provider": "openai",
    "model": "gpt-4o-mini",
    "api_key": "",
    "base_url": "https://api.openai.com/v1",
    "rate_limit": 10,
    "max_concurrency": 5,
    "max_tokens": 4096,
    "use_codex_cli": false,
    "codex_bin": "",
    "proxy_port": 8080
  },
  "graph": {
    "chunk_size": 600,
    "chunk_overlap": 80,
    "community_min_size": 2
  },
  "sim": {
    "default_rounds": 3,
    "max_agents": 30
  }
}
```

API keys can also be set via environment variables (checked in this order):

```bash
FISHNET_API_KEY=...       # highest priority
OPENAI_API_KEY=...
ANTHROPIC_API_KEY=...
CODEX_API_KEY=...          # checked first for codex/codex-cli providers
```

Global flags (override config for a single command):

```bash
fishnet --model gpt-4o --provider openai --api-key sk-... <command>
```

## Providers

| Provider | Description | Default Model | API Key |
|----------|-------------|---------------|---------|
| `openai` | OpenAI API | `gpt-4o-mini` | `OPENAI_API_KEY` |
| `anthropic` | Anthropic API | `claude-sonnet-4-6` | `ANTHROPIC_API_KEY` |
| `ollama` | Local Ollama server | any Ollama model | none |
| `codex` | OpenAI API with Codex models | `o4-mini` | `OPENAI_API_KEY` |
| `codex-cli` | Local `codex` binary, falls back to OpenAI | `o4-mini` | `CODEX_API_KEY` |
| `clicliproxy` | Local CLIProxyAPI server for credential pooling | any | none |

```bash
# Use Anthropic Claude
fishnet init myproject --provider anthropic --model claude-opus-4-6

# Use local Ollama
fishnet init myproject --provider ollama --model llama3 --base-url http://localhost:11434/v1

# Use a local CLIProxyAPI server
fishnet config set provider clicliproxy
fishnet config set base-url http://localhost:8080/v1
```

## 5-Step Workflow

### Step 1: Graph Build

Ingest your document corpus and build a local knowledge graph.

1. **Ontology design** — LLM analyzes a sample of your docs to generate entity and edge type schemas tailored to your domain
2. **Entity extraction** — Each document chunk is processed concurrently; entities and relationships are extracted and deduplicated
3. **Community detection** — Louvain algorithm groups related entities; optional LLM-generated summaries per community

```bash
fishnet init myproject --dir ./docs
fishnet analyze --ontology --community --concurrency 8
fishnet graph stats
fishnet graph web    # interactive browser visualization
```

### Step 2: Agent Setup

Entities extracted from the graph become simulation agents. Each agent carries its name, type, and a generated persona summary that shapes how it posts and reacts.

- Personas are derived directly from entity summaries in the graph
- Stance assignment is determined by the agent's community membership and connections
- Agent count is configurable; defaults to all nodes up to `max_agents`

### Step 3: Simulation

Run a multi-round, dual-platform social simulation.

- **Twitter** — agents post, retweet, like, follow, and reply
- **Reddit** — agents submit posts, comment, upvote, and downvote
- Rounds progress sequentially; each agent takes one or more actions per round
- Runs in the foreground with a live feed, or pipe `--quiet` for background use
- PID file at `.fishnet/sim.pid` enables `fishnet sim stop` at any time

```bash
fishnet sim platform --scenario "AI regulation debate" --rounds 10
fishnet sim stop      # graceful shutdown via SIGTERM
fishnet sim status    # check if running
```

**Branching:** run multiple scenario variants in parallel to compare outcomes.

```bash
fishnet sim branch --scenario "Product launch" --branches auto --branch-count 3
```

### Step 4: Report

The ReAct report agent uses InsightForge, PanoramaSearch, and QuickSearch tools to iteratively retrieve context from the graph, then generates a structured Markdown report section by section.

```bash
fishnet report generate --scenario "AI regulation debate" --output report.md
```

The agent prints each section as it completes, then writes the full Markdown to `--output` if provided.

### Step 5: Interview

Speak directly to any graph entity in character. Each agent's persona is grounded in its extracted entity summary and graph neighborhood.

```bash
# Interactive REPL (multi-turn, maintains history)
fishnet interview Alice

# Single question
fishnet interview "Elon Musk" --question "What do you think about AI regulation?"

# Batch — all agents concurrently, up to 4 at once
fishnet interview --all --question "What is your stance on the new policy?"

# Batch — named subset
fishnet interview --batch "Alice,Bob,Carol" --question "How does this affect you?"
```

## Architecture

```
fishnet/
├── main.go                   # entry point
├── cmd/                      # Cobra CLI commands
│   ├── root.go               # root command, TUI launch, global flags
│   ├── init.go               # fishnet init, fishnet config
│   ├── analyze.go            # fishnet analyze
│   ├── graph.go              # fishnet graph (stats/show/web/community/search)
│   ├── sim.go                # fishnet sim (platform/run/branch/copy-react/oasis/stop/status/list)
│   ├── report.go             # fishnet report generate, fishnet interview
│   ├── session.go            # fishnet session (list/show/save/run/fork/modify/delete)
│   └── query.go              # fishnet query (sims/posts/actions/timeline/stats)
└── internal/
    ├── config/               # Config struct, Load/Save, provider URLs
    ├── db/                   # SQLite database layer (modernc.org/sqlite, no CGo)
    ├── doc/                  # Document reader (.txt/.md/.rst/.csv/.json) + chunker
    ├── graph/                # Ontology generation, entity extraction, community detection, search
    ├── llm/                  # LLM client (OpenAI-compatible, all providers)
    ├── platform/             # Agent state machine, Twitter/Reddit action logic
    ├── report/               # ReAct report agent, interview engine
    ├── session/              # Session persistence (JSON files in .fishnet/sessions/)
    ├── sim/                  # Simulation engine, branching, copy-reaction, feedback loop
    ├── tui/                  # Bubbletea TUI (5-step workflow UI)
    └── viz/                  # ASCII graph printer + HTTP browser visualization
```

**Key technology choices:**

| Concern | Choice | Reason |
|---------|--------|--------|
| CLI framework | Cobra | Standard Go CLI with subcommands |
| TUI | Bubbletea + Lipgloss | Reactive terminal UI, no web server |
| Database | SQLite via modernc.org/sqlite | Pure Go, no CGo, single file |
| LLM client | Custom (OpenAI-compatible) | Works with any provider |
| Concurrency | goroutines + semaphores | Native Go, no queuing middleware |

## Comparison with MiroFish

| Feature | MiroFish (Python) | fishnet (Go) |
|---------|-------------------|--------------|
| GraphRAG storage | Zep (cloud) | Local SQLite |
| Simulation speed | ~1x | ~15-20x faster |
| API server | FastAPI REST | No server (TUI + CLI) |
| Frontend | Vue.js web app | Bubbletea TUI |
| Setup | Docker + Zep + Python venv | Single binary |
| Search | Zep cloud graph | Local SQLite full-text |
| Branching simulations | No | Yes (parallel goroutines) |
| Copy reaction testing | No | Yes (`sim copy-react`) |
| Session management | No | Yes (`session` subcommand) |
| Offline support | No (Zep required) | Full (SQLite, local LLM via Ollama) |
| Graph visualization | Web app | Browser (local HTTP) + ASCII |
