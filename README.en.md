# ops-agent

[中文](README.md) | English

A lightweight ops assistant: a single pure-Go static binary that runs resident on a server as its "brain". From your laptop you talk to it over SSH to manage the box — run commands, read/write files (behind a safety gate), plus background patrol/self-heal and fan-out to many hosts.

Built for "the servers are old Ubuntu boxes with possibly ancient CPUs": `CGO_ENABLED=0` pure-Go single static binary, no runtime dependencies, one-command deploy.

---

## Features

- **Conversational ops**: natural language → model proposes a command → safety gate classifies → confirm/allow → execute → feed the result back and continue.
- **Safety gate**: rule blacklist (`rm -rf`, `mkfs`, `dd of=`, `reboot`, `drop table`, …) + read-only command allowlist + the model's self-assessment, taking the more cautious of the two; the model can never downgrade a dangerous action.
- **Background patrol / self-heal**: periodic `disk` / `load` / `key_services` checks; a watched service that goes down is auto-restarted (through a narrow allowlist) with an audit trail; high-risk or irreversible findings are skipped and recorded as todos, never run unattended.
- **Multi-model diagnosis**: for findings with no safe auto-fix (disk/load), a diagnosis model investigates read-only, finds the likely root cause, and records its recommendation as a todo.
- **Fan-out**: run one instruction across many hosts concurrently, non-interactively (write actions needing confirmation are declined by default).
- **Encrypted keystore**: the API key is sealed with secretbox on disk — no plaintext in config, environment, or the process list.
- **Single-binary deploy**: `enroll <host>` creates the user, installs the service, configures the sudo allowlist, provisions the key, and starts systemd — in one command.
- **No network ports**: the agent listens on none; the CLI tunnels over SSH to a local unix socket, keeping the attack surface minimal.

## Architecture

```
local CLI ──SSH──> opsagent daemon on the server ──outbound HTTPS──> model API
                     (unix socket, no network ports)
```

The agent runs as a dedicated `opsagent` user; privilege escalation goes through an auto-generated sudo allowlist (`systemctl`/`journalctl` only). For the internal structure see [ONBOARDING.md](ONBOARDING.md).

## Requirements

- **Build**: Go 1.25+.
- **Runtime (target host)**: Linux amd64 or arm64; no other runtime dependencies (single static binary). systemd (for deployment).
- **Deploy prerequisite**: SSH access to the target, and the SSH user can run sudo non-interactively (NOPASSWD) or is root (`enroll` uses `sudo -n`, so a password requirement fails fast and clearly).
- **Model**: one model API key (DeepSeek / OpenAI-compatible / Anthropic).

## Build

```powershell
./build.ps1
```

Cross-compiles to `./dist/opsagent-{linux-amd64,linux-arm64,windows-amd64}`, each a single static binary (`file dist/opsagent-linux-amd64` shows `statically linked`).

## Deploy

```bash
# the API key is read from stdin, so it never lands in shell history / the process list
echo "$DEEPSEEK_KEY" | opsagent enroll web1 --provider deepseek --model deepseek-chat
```

Over SSH, `enroll` detects the target architecture → scp's the matching binary → runs an idempotent privileged bootstrap that:

- creates the system user `opsagent` (override with `--user`) and installs the binary to `/usr/local/bin/opsagent`;
- writes the sudoers allowlist (validated with `visudo`, NOPASSWD for systemctl/journalctl only);
- writes the systemd unit (the API key is **not** in the unit — only in the encrypted keystore);
- pipes the base64'd key into the keystore (never written to the remote disk);
- adds your login user to the `opsagent` group (for `connect`) and starts the service with `enable --now`.

Then `opsagent connect web1` works (if the first connect is denied, re-login so the new group membership applies).

Main `enroll` flags: `--provider` (default `deepseek`), `--model`, `--base-url`, `--user` (default `opsagent`), `--bin` (default `dist/opsagent-linux-<arch>`).

## Usage

```bash
opsagent connect <host>                          # open a conversation (SSH)
opsagent run -c "<instruction>" <host>... [--yes] # fan-out: one instruction across hosts
opsagent logs [-n N]                             # audit trail (with source: chat/patrol)
opsagent todos                                   # patrol / self-heal todos
opsagent key set <name>                          # store a secret (value read from stdin)
opsagent key list
```

Details of patrol and fan-out (boundaries, safe defaults, verification) are in [ONBOARDING.md](ONBOARDING.md) §6/§7.

## Configuration

Configuration is via environment variables for now (TOML config is deferred). Common ones:

| Variable | Default | Meaning |
|---|---|---|
| `OPSAGENT_PROVIDER` | `openai` | `openai` / `deepseek` / `anthropic` |
| `OPSAGENT_MODEL` | — | model name |
| `OPSAGENT_API_KEY` | — | plaintext override; empty reads from the encrypted keystore |
| `OPSAGENT_BASE_URL` | — | API base override |
| `OPSAGENT_DIAG_PROVIDER/_MODEL/_BASE_URL` | falls back to main model | dedicated diagnosis model |
| `OPSAGENT_PATROL` | `true` | enable background patrol |
| `OPSAGENT_PATROL_INTERVAL` | `5m` | patrol period |
| `OPSAGENT_PATROL_CHECKS` | `disk,load,key_services` | enabled checks |
| `OPSAGENT_PATROL_SERVICES` | (empty) | units patrol watches and may auto-restart; **empty means nothing is auto-restarted** |
| `OPSAGENT_PATROL_DISK_PCT` | `90` | disk-usage alert threshold |
| `OPSAGENT_PATROL_LOAD` | `2.0` | per-CPU 1-minute load alert threshold |

The full list (state/db/knowledge paths, etc.) is in [ONBOARDING.md](ONBOARDING.md) §5.

## Development

```bash
go test ./...        # all tests
go vet ./...
gofmt -l internal/ cmd/
```

Workflow and decision log are in `TASK.md` (the single source of execution truth); design docs are `REQUIREMENTS.md` / `TECH_STACK.md` / `ARCHITECTURE.md` / `ROADMAP.md`; onboarding and internals are in [ONBOARDING.md](ONBOARDING.md).
