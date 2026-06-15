# Raspberry Pi 5 Homelab Dashboard

A fast, self-hosted web dashboard for managing a Raspberry Pi 5 (or any Debian-based
Linux box). A single Go binary serves a Svelte UI — live metrics, a web terminal, a
file manager, Docker management, systemd service control, package updates, LAN device
discovery, and a customizable service hub.

It is intentionally lightweight: one static binary (~10 MB), no database, low idle
memory, and a prebuilt frontend so the Pi needs no Node toolchain.

> ### ⚠️ This is a LAN-only admin tool — do not expose it to the internet
> The dashboard gives a browser a **root-capable terminal, full filesystem access, and
> host control**. It is designed to run on your trusted home network only. The installer
> does **not** open any ports on your router and you should not forward any. If you need
> remote access, reach it through a VPN (e.g. WireGuard) into your LAN — never a port
> forward. Always enable the built-in login (the installer sets it up) and serve over
> HTTPS (the installer generates a certificate).

---

## Features

- **Overview** — live CPU / memory / temperature / load gauges and a sortable process list, streamed over WebSockets.
- **Terminal** — a real PTY in the browser (xterm.js).
- **Files** — browse, edit, upload, download, rename, and delete within a configurable root.
- **Media** — image browser with on-demand, cached thumbnails.
- **Filesystem** — read/edit anywhere on the host (gated behind a password re-prompt).
- **Docker** — list/create/start/stop/remove containers, view logs, attach a container shell, manage images and volumes.
- **Services** — start/stop/restart/enable systemd units and tail host logs.
- **System** — host info, APT update check & upgrade, reboot/shutdown.
- **Hub** — a customizable grid of service links with live health checks.
- **Network** — LAN device discovery (via `arp-scan`) with optional per-device stats pulled over SSH using a key the dashboard generates.
- **Themes & scaling** — multiple themes (including Tokyo Night and Catppuccin Mocha) and adaptive UI scaling; responsive layout for phones.
- **Security** — optional bcrypt login with session cookies, and optional HTTPS/TLS.

---

## Requirements

- A Raspberry Pi 5 running **Raspberry Pi OS Lite (64-bit)** — or any `arm64`/`amd64` Debian/Ubuntu host.
- A normal user account **with `sudo`** (the dashboard escalates via `sudo` for service, package, and filesystem features).
- Outbound internet access during install (to fetch dependencies and, if you don't have Go, the prebuilt binary).

---

## Install

On the Pi:

```bash
git clone https://github.com/EternalCoder454/rpi5-homelab-dashboard.git
cd rpi5-homelab-dashboard
sudo ./install.sh
```

The installer is interactive — it asks for an admin username and password and whether to
enable HTTPS. Everything else uses sensible defaults.

### What the installer does

1. Installs runtime dependencies via APT (`arp-scan`, `openssl`, `curl`, `ca-certificates`).
2. Obtains the binary: builds from source if Go is installed, otherwise downloads the
   prebuilt release binary for your architecture.
3. Installs to `/opt/homelab-dashboard` (override with `INSTALL_DIR=`).
4. Generates `config.yaml` with your bcrypt-hashed password.
5. Generates a self-signed TLS certificate (if you choose HTTPS).
6. Adds a scoped `sudoers` rule so the privileged features work without password prompts.
7. Installs and starts a `systemd` service running as your user.

### Accessing it

When the service is up, open:

```
https://<your-pi-hostname>.local:8080
```

for example `https://raspberrypi.local:8080`. A self-signed certificate triggers a
one-time browser warning; accept it, or see [TLS](#tls) below to avoid it.

---

## Configuration

Settings live in `config.yaml` in the install directory. Restart the service after editing:
`sudo systemctl restart homelab-dashboard`.

| Key | Description |
|---|---|
| `port` | Port to listen on (default `8080`). |
| `upload_dir` | Root for the file manager / uploads / media. Relative to the install dir. |
| `exec_allowlist` | IPs/CIDRs allowed to use the "run command" feature. |
| `shell` | Shell for the web terminal (default `/bin/bash`). |
| `auth_user` | Login username. |
| `auth_password_hash` | bcrypt hash of the password. Generate with `./homelab-dashboard -hashpw 'pw'`. |
| `tls_cert` / `tls_key` | Paths to a TLS cert and key. When both are set, the dashboard serves HTTPS. |

If `auth_user`/`auth_password_hash` are empty the dashboard runs **with no login** and logs
a warning — don't do that on a shared network.

### TLS

The installer creates a self-signed certificate, which works but shows a browser warning.
To get a trusted certificate on your LAN, use [mkcert](https://github.com/FiloSottile/mkcert):

```bash
mkcert -install
mkcert <your-pi-hostname>.local
```

Then point `tls_cert`/`tls_key` at the generated files and restart the service.

---

## Privileges

Several features shell out to `sudo -n` (`systemctl`, `apt-get`, `arp-scan`, `reboot`,
and reading/writing root-owned files). The installer adds `/etc/sudoers.d/homelab-dashboard`
granting your user passwordless access to exactly those commands. This is broad by
necessity — a dashboard that can edit any file as root is effectively root. If you'd rather
not grant it, run the installer with `SKIP_SUDOERS=1`; the Overview, Terminal, Files,
Docker, and Hub features still work, but Services/System/Network/Filesystem will be limited.

---

## Managing the service

```bash
sudo systemctl status homelab-dashboard
sudo systemctl restart homelab-dashboard
sudo systemctl stop homelab-dashboard
journalctl -u homelab-dashboard -f
```

## Updating

```bash
cd rpi5-homelab-dashboard
git pull
sudo ./install.sh
```

The installer preserves your existing `config.yaml` and certificate.

## Uninstalling

```bash
sudo ./uninstall.sh
```

Stops and removes the service, the sudoers rule, and (with confirmation) the install
directory.

---

## Building from source / development

The repository ships a prebuilt `static/` so the Pi needs no Node. To rebuild it or to
develop locally you need **Go ≥ 1.25** and **Node ≥ 18**.

```bash
make frontend      # build the Svelte UI into static/
make build         # build the Go binary for the host
make run           # run locally (serves on :8080)
make cross-arm64   # cross-compile a linux/arm64 binary
```

For a live frontend dev server with hot reload, run the Go backend with `make run` and, in
another terminal, `cd frontend && npm install && npm run dev`.

### Project layout

```
main.go              entry point, routing, HTTP/HTTPS server
httpx.go             gzip middleware + static file serving
config/              config.yaml loading
handlers/            one file per feature (metrics, terminal, files, docker, ...)
metrics/             /proc + vcgencmd sampling
frontend/            Svelte 4 + Vite source (builds to ../static)
static/              prebuilt frontend (committed)
install.sh           installer
homelab-dashboard.service   systemd unit template
```

---

## Security model

- **Local network only.** No ports are forwarded; the dashboard binds to your LAN.
- **Authenticated.** bcrypt login with HttpOnly session cookies; the filesystem editor is gated behind a second password prompt.
- **Encrypted.** Optional (recommended) HTTPS so credentials and terminal traffic aren't sent in the clear.
- For host-level hardening of the Pi itself (key-only SSH, an `nftables` firewall, fail2ban, automatic security updates), see the Raspberry Pi documentation — those are independent of this dashboard.

## License

[MIT](LICENSE) © EternalCoder454
