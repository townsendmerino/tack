# Coding harness comparison, 2026-10-22

tack against Claude Code (the coding agent in the Claude app) and seven other widely used coding agents. Other products are as documented on 2026-10-22, from their own docs and READMEs (sources at the end); they change monthly, so re-check before relying on a cell. tack's column says what is built at main 4f8ae06 and what is planned.

Key: **Yes**, **Partial** (opt-in, partial or planned), **No**, **?** (not documented).

| Feature | tack | Claude Code | OpenAI Codex | Gemini CLI | Cursor | GitHub Copilot | OpenCode | Aider | Cline |
|---|---|---|---|---|---|---|---|---|---|
| Interface | Partial: command line and daemon today; a TUI and a native macOS app are planned | Terminal, IDE extensions, desktop app, web and mobile, CI | CLI, IDE extension, desktop app, cloud | Terminal CLI, VS Code companion, GitHub Action | Its own IDE, a CLI, cloud agents | VS Code agent, CLI, cloud agent that opens PRs | Terminal UI, desktop app (beta), web, server | Terminal CLI | VS Code, JetBrains, CLI, desktop |
| Open source | Partial: private now; MIT after phase P2 | No (its sandbox runtime is open) | Yes: Apache-2.0 | Yes: Apache-2.0 | No | Partial: extension code MIT; the service is proprietary | Yes: MIT | Yes: Apache-2.0 | Yes: Apache-2.0 |
| Local models | Yes: local only, by design (goinfer today; Ollama, LM Studio and llama.cpp planned) | No: Claude models only | Yes: official (`--oss` with Ollama or LM Studio) | No: not officially (an experimental local Gemma router) | ? | Yes: official (Ollama, Foundry Local, custom endpoints) | Yes: official (Ollama, LM Studio, llama.cpp, any OpenAI-compatible server) | Yes: official (Ollama, OpenAI-compatible servers) | Yes: official (Ollama, LM Studio, OpenAI-compatible servers) |
| Built for small windows | Yes: 16k design window, tool output fitted to the room left, instruction files capped at 15% | No: built for 200K to 1M windows; defers MCP tool schemas | ? | No: built for a 1M window | ? | ? | No | Partial: token-budgeted repo map (1K tokens by default) | Partial: a "compact prompt" option for local models |
| Fixed prompt (system and tools) | About 1,000 tokens (measured) | About 4,200 tokens (docs, marked illustrative) | ? | ? | ? | ? | About 11,000 tokens (measured in tack's A0 baseline) | ? | ? |
| Sandbox for commands | Yes: Seatbelt, deny by default, always on in auto mode (macOS); file-editing tools not yet inside it (code review S1) | Partial: Seatbelt or bubblewrap; off by default | Yes: Seatbelt, bubblewrap or Windows sandbox; on by default | Partial: opt-in, Seatbelt or containers | Yes: Seatbelt or Landlock; on in the default mode | Partial: VS Code: Seatbelt or bubblewrap, off by default; the cloud agent is firewalled | No: none documented | No | No: none locally |
| Network for commands | Yes: denied (loopback for tests held: it can't be limited safely) | Yes: proxy with domain allow and deny lists | Yes: off by default; opt-in, with an optional domain allowlist | Partial: only through proxied Seatbelt profiles | Yes: allowlist file; admin limits for cloud agents | Partial: local sandbox allows network by default; the cloud agent's firewall is on | No: none documented | No | No: none documented |
| Approval modes | Ask, plan, edits, auto (auto requires the sandbox) | Manual, accept edits, plan, auto (classifier), bypass | Ask, auto-review, full access, custom | Default, auto-edit, plan, yolo; a policy engine | Auto-review (default), allowlist, run everything | Manual, assisted (experimental), allow all, autopilot | Per-tool allow, ask or deny; a read-only plan agent | Confirmations or `--yes-always`; architect and ask modes | Per-category auto-approve, YOLO; plan and act |
| Code search | Yes: grep, glob and read, plus ken's hybrid search (keywords and embeddings), in-process | grep, glob and read; LSP through plugins | ? (shell tools) | An investigator subagent | Yes: grep plus a semantic index with its own embeddings | Yes: a semantic workspace index | LSP diagnostics (off by default) | Yes: repo map (tree-sitter, ranked) | No index, by design; follows imports |
| Context management | Partial: append-only; no compaction yet (the run stops when the window is full) | Yes: automatic compaction | Yes: automatic compaction | Yes: compression at half the window | Yes: automatic summary | Yes: automatic summary | Yes: automatic compaction; prunes old output | Yes: chat history summarized | Yes: automatic compaction |
| Instruction files | AGENTS.md, CLAUDE.md or CONTEXT.md (first section and outline only) | CLAUDE.md, rules, memory | AGENTS.md | GEMINI.md (name configurable) | .cursor/rules, AGENTS.md | copilot-instructions.md, AGENTS.md, CLAUDE.md | AGENTS.md, falling back to CLAUDE.md | CONVENTIONS.md, when passed in | .clinerules, AGENTS.md and others |
| MCP | No: not yet | Yes | Yes | Yes | Yes | Yes | Yes | No | Yes |
| Subagents | No | Yes, including background agents | Yes, on by default | Yes | Yes, plus parallel cloud agents | Yes (nesting off by default) | Yes | No (an architect and editor model pair) | Yes, in parallel |
| Checkpoints and undo | Partial: an append-only session log; no undo yet | Yes: checkpoints and rewind (file edits only) | Partial: fork and apply; undo not documented | Partial: shadow-git checkpoints, off by default | Yes: local checkpoints | Yes: checkpoints; the cloud agent commits to a branch | Yes: snapshots, undo and redo | Yes: commits each edit, with undo | Yes: shadow-git checkpoints after each step |
| Hooks and plugins | No | Yes: hooks, plugins, skills | Yes: hooks, skills, plugins | Yes: hooks, extensions | Yes: hooks, plugins | Yes: hooks, extensions | Yes: JavaScript and TypeScript plugins | No | Yes: hooks, SDK plugins |
| Scripting and CI | `tack run`; a pre-registered eval harness | `claude -p`, the Agent SDK, CI actions | `codex exec`, a GitHub Action, an SDK | `gemini -p`, a GitHub Action | CLI `-p`, a cloud-agent API | CLI `-p`, the cloud agent | `opencode run`, a server mode | `--message`, Python scripting | CLI with JSON output |
| Price | Free; runs on your own hardware | A Claude subscription, or pay-per-use API | ChatGPT plans, or an API key | Free tier with a Google login; API key | Free tier; paid plans | Free tier; Pro from $10 a month | Free with your own keys; optional paid models | Free with your own keys | Your own keys; optional usage billing |

## What tack does differently

- It never calls a hosted model. Only Codex, Copilot, OpenCode, Aider and Cline even support local models officially, and all of them are built around hosted ones.
- It is designed end to end for a 16k window: a fixed prompt of about 1,000 tokens, tool output cut to the room left, instruction files capped at 15% of the window.
- The command sandbox is on in auto mode, with the network denied. Among the others, only Codex (and Cursor's default mode) sandbox by default.
- Every loop change is measured on pre-registered, interleaved runs before it is adopted.

## Where tack is behind

- A command line only, for now. Everyone else has an IDE, desktop or cloud surface.
- No MCP, subagents, compaction, checkpoints or hooks yet.
- The 2026-10-22 code review found that its file-editing tools run outside the sandbox (S1). That needs fixing before "sandboxed" holds for the whole tool.
- Results: with Qwen3-8B on an 8 GB card, about 7% of the hard calibration tasks pass. The limit now is the model, more than the window.

## Closest relatives

- **Codex CLI**: official local models (`--oss`), sandbox on by default, open source. The nearest match on safety.
- **OpenCode**: open source and local-friendly, but no sandbox, and an 11,000-token fixed prompt (measured in tack's A0 baseline).
- **Aider** and **Cline**: local models and some budgeting for small windows (Aider's repo map, Cline's compact prompt), but no sandbox.

## Sources

- tack: this repo at 4f8ae06 (DESIGN.md, DECISIONS.md, the C6 records) and the 2026-10-22 code review.
- Claude Code: [overview](https://code.claude.com/docs/en/overview.md), [sandboxing](https://code.claude.com/docs/en/sandboxing.md), [permission modes](https://code.claude.com/docs/en/permission-modes.md), [context window](https://code.claude.com/docs/en/context-window.md), [subagents](https://code.claude.com/docs/en/sub-agents.md), [checkpointing](https://code.claude.com/docs/en/checkpointing.md), [LLM gateway](https://code.claude.com/docs/en/llm-gateway.md), [license](https://github.com/anthropics/claude-code/blob/main/LICENSE.md).
- OpenAI Codex: [GitHub](https://github.com/openai/codex), [CLI](https://learn.chatgpt.com/docs/codex/cli), [sandboxing](https://learn.chatgpt.com/codex/sandboxing), [permission modes](https://learn.chatgpt.com/codex/permission-modes), [subagents](https://learn.chatgpt.com/codex/agent-configuration/subagents), [pricing](https://learn.chatgpt.com/codex/pricing).
- Gemini CLI: [GitHub](https://github.com/google-gemini/gemini-cli), [sandbox](https://geminicli.com/docs/cli/sandbox/), [configuration](https://geminicli.com/docs/reference/configuration/), [subagents](https://geminicli.com/docs/core/subagents/), [checkpointing](https://geminicli.com/docs/cli/checkpointing/).
- Cursor: [agent](https://cursor.com/docs/agent/overview.md), [run modes](https://cursor.com/docs/agent/security/run-modes.md), [cloud agent](https://cursor.com/docs/cloud-agent.md), [hooks](https://cursor.com/docs/hooks.md), [API keys](https://cursor.com/help/models-and-usage/api-keys.md), [semantic search](https://cursor.com/en-US/blog/semsearch).
- GitHub Copilot: [agent mode](https://code.visualstudio.com/docs/copilot/chat/chat-agent-mode), [sandboxing](https://code.visualstudio.com/docs/agents/run/agent-sandboxing), [approvals](https://code.visualstudio.com/docs/agents/run/approvals), [language models](https://code.visualstudio.com/docs/copilot/language-models), [cloud agent](https://docs.github.com/en/copilot/concepts/agents/coding-agent/about-coding-agent), [plans](https://docs.github.com/en/copilot/get-started/plans).
- OpenCode: [GitHub](https://github.com/anomalyco/opencode), [providers](https://opencode.ai/docs/providers/), [permissions](https://opencode.ai/docs/permissions/), [LSP](https://opencode.ai/docs/lsp/), [plugins](https://opencode.ai/docs/plugins/).
- Aider: [docs](https://aider.chat/docs/), [repo map](https://aider.chat/docs/repomap.html), [git](https://aider.chat/docs/git.html), [options](https://aider.chat/docs/config/options.html), [GitHub](https://github.com/Aider-AI/aider).
- Cline: [GitHub](https://github.com/cline/cline), [local models](https://docs.cline.bot/running-models-locally/overview.md), [auto-approve](https://docs.cline.bot/features/auto-approve.md), [checkpoints](https://docs.cline.bot/core-workflows/checkpoints.md), [why no index](https://cline.bot/blog/why-cline-doesnt-index-your-codebase-and-why-thats-a-good-thing).
