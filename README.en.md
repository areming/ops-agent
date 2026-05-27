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

## Install

Install `ops` on **your own machine** (the one you use to manage remote servers).

### Linux (amd64 / arm64)

One command: downloads the latest release, verifies sha256, installs to `/usr/local/bin/ops`:

```sh
curl -fsSL https://raw.githubusercontent.com/areming/ops-agent/main/install.sh | sudo sh
```

Pin a specific version:

```sh
curl -fsSL https://raw.githubusercontent.com/areming/ops-agent/main/install.sh | sudo OPS_VERSION=v0.0.1 sh
```

After installation, run `ops`. On first run it guides you through choosing a model provider and entering your API key, then drops you into a conversation.

### Windows

Build from source first, then run the one-step installer (adds `ops` to PATH + enables ssh-agent):

```powershell
./build.ps1
./install.ps1
```

Open a new terminal, load your SSH private key, and you're ready:

```powershell
ssh-add $env:USERPROFILE\.ssh\id_ed25519
ops
```

## Build (from source)

Requires Go 1.25+. Cross-compiles to `./dist/ops-{linux-amd64,linux-arm64,windows-amd64}`:

```powershell
./build.ps1
```

## Deploy

### Prerequisite: SSH setup

ops drives the target through your local `ssh`/`scp`, so before deploying make sure:

1. **`ssh <host>` works without a password** (key auth). If your private key has a passphrase, load it into ssh-agent so you aren't prompted repeatedly:
   - Windows: `./install.ps1` (enables ssh-agent), then `ssh-add $env:USERPROFILE\.ssh\id_ed25519`.
   - macOS/Linux: `ssh-add ~/.ssh/id_ed25519`.
2. **Target behind a jump host** (a private box reachable only through a public one): use ProxyJump in `~/.ssh/config`, then use the alias for enroll/connect:
   ```sshconfig
   Host gw
       HostName <jump-host public IP>
       User <jump-host user>
   Host vps
       HostName <private IP>
       User <target user>
       ProxyJump gw
   ```
   Verify: `ssh vps "echo ok"` should return without a password.
3. **The target's SSH user can sudo without a password** (enroll uses `sudo -n`): if not, on the target run `sudo visudo` and add `<user> ALL=(ALL) NOPASSWD:ALL` (you can narrow it later).
4. **The target has outbound HTTPS** to the model API (the agent calls it at runtime).

### Deploy

**Easiest — guided wizard** (recommended for first use):

```bash
ops setup
```

It walks you through provider / model / target host (and optionally which services patrol should watch and auto-restart, and a diagnosis model), **checks SSH and passwordless sudo** (with fix hints if either fails), then deploys and verifies the service is up. You only answer questions — no flags to remember.

Or deploy manually in one command:

```bash
# the API key is read from stdin, so it never lands in shell history / the process list
echo "$DEEPSEEK_KEY" | ops enroll web1 --provider deepseek --model deepseek-chat
```

Optional flags: `--services nginx,sshd` (units patrol watches and auto-restarts), `--diag-model <model>` (diagnosis model, reusing the main provider/key), `--user`, `--base-url`, `--bin`.

Over SSH, `enroll` detects the target architecture → scp's the matching binary → runs an idempotent privileged bootstrap that:

- creates the system user `opsagent` (override with `--user`) and installs the binary to `/usr/local/bin/ops` (with an `opsagent` symlink for the old name);
- writes the sudoers allowlist (validated with `visudo`, NOPASSWD for systemctl/journalctl only);
- writes the systemd unit (the API key is **not** in the unit — only in the encrypted keystore);
- pipes the base64'd key into the keystore (never written to the remote disk);
- adds your login user to the `opsagent` group (for `connect`) and starts the service with `enable --now`.

Then `ops connect web1` works (if the first connect is denied, re-login so the new group membership applies).

Main `enroll` flags: `--provider` (default `deepseek`), `--model`, `--base-url`, `--user` (default `opsagent`), `--bin` (default `dist/ops-linux-<arch>`).

## Usage

```bash
ops                                         # local conversation (onboards if unconfigured); /help for commands
ops connect <host>                          # open a conversation from your laptop (SSH)
ops connect --local /run/opsagent/agent.sock # on the server itself (no SSH; user must be in the opsagent group)
ops run -c "<instruction>" <host>... [--yes] # fan-out: one instruction across hosts
ops logs [-n N]                             # audit trail (with source: chat/patrol)
ops todos                                   # patrol / self-heal todos
ops key set <name>                          # store a secret (value read from stdin)
ops key list
```

Inside a conversation, slash commands (matching common CLIs): `/models [name]` view/switch the model of the machine this session talks to, `/logs [N]` view the audit trail, `/clear` reset the current conversation, `/help`, `/quit`.

Details of patrol and fan-out (boundaries, safe defaults, verification) are in [ONBOARDING.md](ONBOARDING.md) §6/§7.

## Configuration

Precedence is **environment variable > `config.json` (under StateDir) > built-in default**. Model selection (provider/model/base_url + the three diagnosis fields) is persisted to `config.json` during onboarding or a `/models` switch; the API key is **not** stored in config — it lives encrypted in the keystore (always under the entry name `api_key`). Common environment variables:

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

### Rotating the key / switching providers

ops has a **single active provider** at a time (the diagnosis model reuses its key by default), and the key is stored in the keystore under the fixed name `api_key`.

**① The key stopped working (expired/quota/revoked), same provider**

Local (`ops` on your own machine):

```bash
echo "$NEW_KEY" | ops key set api_key    # or: ops key set api_key, then paste + Ctrl-D
```

Effective on the next `ops` run (each local run is a fresh process).

Remote (an enrolled host running under systemd) — the running daemon read the key at startup, so a restart is needed to reload. Easiest is to re-run enroll from your machine (idempotent; rotates the key and restarts):

```bash
echo "$NEW_KEY" | ops enroll <host> --provider <same provider> --model <same model>
```

Or on the host manually:

```bash
sudo runuser -u opsagent -- env OPSAGENT_STATE_DIR=/var/lib/opsagent ops key set api_key
sudo systemctl restart opsagent
```

**② Switch / add a new provider**

In-session `/models <name>` only switches the model **within the current provider** — it cannot change the provider. Switching providers means changing provider + base_url + key together:

Local: delete `config.json` to re-trigger onboarding on the next `ops`, or edit `config.json`'s `provider`/`model`/`base_url` and run `ops key set api_key` with the new key. Local config dir: Windows `%AppData%\opsagent\`, macOS/Linux `~/.config/opsagent/` (holds `config.json` + `keystore.json`).

Remote: re-run enroll with the new provider; it rewrites the unit's environment, rotates the key, and restarts:

```bash
echo "$ANTHROPIC_KEY" | ops enroll <host> --provider anthropic --model claude-... [--base-url ...]
```

> **Note (enrolled hosts)**: enroll writes `OPSAGENT_PROVIDER` (and `OPSAGENT_MODEL` when `--model` is given) into the systemd unit's `Environment=`, which by the precedence above **overrides `config.json`**. So switching providers remotely by editing `config.json` won't work — re-run enroll (or edit the unit and `daemon-reload`). Likewise, if the unit pins `OPSAGENT_MODEL`, an in-session `/models` switch may be reverted after a restart.

## Development

```bash
go test ./...        # all tests
go vet ./...
gofmt -l internal/ cmd/
```

Workflow and decision log are in `TASK.md` (the single source of execution truth); design docs are `REQUIREMENTS.md` / `TECH_STACK.md` / `ARCHITECTURE.md` / `ROADMAP.md`; onboarding and internals are in [ONBOARDING.md](ONBOARDING.md).
