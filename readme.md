<div align="center">

# Agentree

<pre>
        /\        
       /**\       
      /****\      
     /******\     
    /********\    
        ||        
     ___||___     
    /   ||   \    
   agent tree    
</pre>

**A terminal workspace for growing coding agents into task branches, worktrees, and shipped pull requests.**

</div>

Agentree is a Go TUI that runs inside `tmux`, tracks projects and tasks in SQLite, creates Git worktrees for agent sessions, and uses an LLM "brain" to help triage ideas and plan work.

## Features

- Project and task dashboard in a keyboard-first terminal UI.
- Per-task Git worktrees under `~/.agentree/worktrees` by default.
- Embedded agent panes hosted in an isolated `tmux` socket.
- Local SQLite store at `~/.config/agentree/agentree.db` by default.
- Optional PR flow through the GitHub CLI.

## Requirements

- Go `1.25.0` or newer.
- `git` available on `PATH`.
- `tmux` available on `PATH` for the full pane-hosted experience.
- `gh` optional, only needed when opening pull requests from Agentree.
- An OpenAI API key for real model-backed planning. Without credentials, Agentree falls back to deterministic stub behavior.

## Install

```sh
git clone <repo-url>
cd agentree
go mod download
go build -o agentree .
```

Run it locally:

```sh
./agentree
```

Or install it into your Go bin path:

```sh
go install .
agentree
```

## Environment

Agentree reads secrets from your shell environment.

```sh
export OPENAI_API_KEY="sk-..."
```

Supported variables:

| Variable | Required | Notes |
| --- | --- | --- |
| `OPENAI_API_KEY` | Recommended | Used by the default `openai` brain provider. |
| `ANTHROPIC_API_KEY` | Optional | Used only if `brain_provider` is set to `anthropic`. |
| `AGENTREE_OWNS_TMUX` | Internal | Set by Agentree when it bootstraps its own tmux session. You do not need to set it. |

Agentree does not require a `.env` loader at runtime, so export variables in your shell, terminal profile, or process manager.

## Configuration

On first run, Agentree uses defaults. You can override them in:

```text
~/.config/agentree/config.toml
```

Example:

```toml
brain_provider = "openai"
brain_model = "gpt-4o"
worktree_root = "/Users/you/.agentree/worktrees"
db_path = "/Users/you/.config/agentree/agentree.db"
tmux_socket = "agentree"
```

Config keys:

| Key | Default | Purpose |
| --- | --- | --- |
| `brain_provider` | `openai` | LLM provider used for triage and planning. |
| `brain_model` | `gpt-4o` | Model name sent to the configured provider. |
| `worktree_root` | `~/.agentree/worktrees` | Parent directory for per-task worktrees. |
| `db_path` | `~/.config/agentree/agentree.db` | SQLite database path. |
| `tmux_socket` | `agentree` | Dedicated tmux socket name. |

## Usage

Start Agentree:

```sh
agentree
```

Start Agentree focused on a repository:

```sh
agentree /path/to/repo
```

Run a one-off embedded pane spike:

```sh
agentree spike -- codex
```

When Agentree creates a task session, it creates a branch named like `agentree/<task-slug>` and a matching Git worktree. If the source repo has env files, Agentree provisions them into the worktree.

## Development

```sh
go test ./...
go run .
```

Build artifacts are intentionally simple: this is a single Go module with internal packages for config, store, orchestration, brain logic, and the TUI.
