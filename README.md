# MultiNet Downloader (`mndl`)

Download **one file over several networks at the same time** (Wi-Fi, iPhone/Android tethering, Ethernet, USB LAN).
Each network opens its own pool of connections (like `aria2c -x`), and faster networks automatically take a bigger share of the file.
Works on **macOS, Windows and Linux**.

- **The CLI (`mndl`) is the core.** The desktop app is just a UI that runs the CLI and reads its JSON output. Both share the same settings file.
- **Resumable.** Stop it, quit it, lose power — run the same command again (or press ▶ in the app) and it continues where it left off.
- **Survives a dead network.** If one network drops, the others finish the job.
- **Polite to servers.** If a server limits connections (HTTP 429/503), extra connections back off automatically.
- **Works without Range support** too (falls back to a single stream).
- **Never overwrites.** An existing file is kept and the new one is saved as `name (1).ext`.

> **You need more than one real network interface.** A laptop's built-in Wi-Fi chip can join only one Wi-Fi network at a time, and adding a second "Wi-Fi service" in System Settings does not change that.
> Add another interface: iPhone/Android USB tethering, a USB Ethernet adapter, a travel router in client mode, etc.
> Several adapters on the *same* router share the same internet line, so they rarely help.

---

## Install

Grab the files from the [latest release](https://github.com/jeamxn/multinet-dl/releases/latest).

| OS | Desktop app | CLI only |
|---|---|---|
| macOS (Apple Silicon + Intel) | `MultiNet-Downloader-macos.dmg` | `mndl-darwin-universal` |
| Windows x64 / ARM64 | `MultiNet-Downloader-windows-<arch>.zip` | `mndl-windows-<arch>.exe` |
| Linux x64 / ARM64 | `MultiNet-Downloader-linux-<arch>.tar.gz` | `mndl-linux-<arch>` |

### macOS

1. Open `MultiNet-Downloader-macos.dmg` and drag **MultiNet Downloader** into **Applications**.
2. The app is not notarized, so the first launch is blocked. Either right-click the app → **Open** → **Open**, or run:
   ```bash
   xattr -dr com.apple.quarantine "/Applications/MultiNet Downloader.app"
   ```
3. (Optional) Use the CLI from Terminal — it ships inside the app:
   ```bash
   sudo ln -sf "/Applications/MultiNet Downloader.app/Contents/MacOS/mndl" /usr/local/bin/mndl
   mndl nets
   ```
   CLI only, without the app:
   ```bash
   curl -L -o mndl https://github.com/jeamxn/multinet-dl/releases/latest/download/mndl-darwin-universal
   chmod +x mndl && xattr -d com.apple.quarantine mndl 2>/dev/null; sudo mv mndl /usr/local/bin/
   ```

### Windows

1. Unzip `MultiNet-Downloader-windows-amd64.zip` (or `-arm64` on ARM PCs) to any folder, e.g. `C:\Tools\MultiNet`.
   **Keep `MultiNet Downloader.exe` and `mndl.exe` in the same folder** — the app runs `mndl.exe`.
2. Run `MultiNet Downloader.exe`. If SmartScreen appears (the exe is unsigned): **More info** → **Run anyway**.
   WebView2 is required; it is built into Windows 10/11 already.
3. (Optional) To use the CLI anywhere, add that folder to your `PATH`:
   ```powershell
   [Environment]::SetEnvironmentVariable("Path", $env:Path + ";C:\Tools\MultiNet", "User")
   # open a new terminal
   mndl nets
   ```

### Linux

1. Install the runtime libraries for the desktop app (CLI alone needs nothing):
   ```bash
   # Debian / Ubuntu 22.04+
   sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0
   # Fedora
   sudo dnf install gtk3 webkit2gtk4.1
   ```
2. Extract and run:
   ```bash
   mkdir -p ~/.local/opt/multinet && tar -xzf MultiNet-Downloader-linux-amd64.tar.gz -C ~/.local/opt/multinet
   ~/.local/opt/multinet/multinet-downloader
   ```
   Put the CLI on your `PATH`:
   ```bash
   ln -sf ~/.local/opt/multinet/mndl ~/.local/bin/mndl
   ```
3. **Recommended:** allow strict interface binding (`SO_BINDTODEVICE` needs `CAP_NET_RAW`):
   ```bash
   sudo setcap cap_net_raw+ep ~/.local/opt/multinet/mndl
   ```
   Without it, `mndl` only pins the source address, and depending on your routing table traffic may still leave through the default network. `mndl nets` prints a warning when this applies.

### Build from source

Needs Go 1.23+ and [Wails v2](https://wails.io) (Linux also needs `libgtk-3-dev libwebkit2gtk-4.1-dev`).

```bash
git clone https://github.com/jeamxn/multinet-dl && cd multinet-dl
go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0
go test ./internal/...

go build -o mndl ./cmd/mndl   # CLI only
scripts/build.sh              # app + CLI for this OS -> dist/
scripts/build.sh windows      # cross-build Windows from macOS/Linux
scripts/build.sh cli          # CLI for every platform
```

On Linux with WebKit2GTK 4.1 run `WAILS_TAGS=webkit2_41 scripts/build.sh`. Pushing a `v*` tag makes GitHub Actions build all three OSes and publish a release.

---

## Desktop app

1. **Networks** (left): tick the networks to use. **Check connection** verifies each one really reaches the internet and shows its public IP. Tick "show VPN / virtual adapters" to see the rest.
2. **Connections per network**: 1–64. 8–16 is usually plenty; above 16 some servers start refusing (the app backs off automatically).
3. **Save to**: pick the download folder.
4. Paste one or more URLs (one per line), or drag a link from your browser onto the window, then **Download** (`Enter` / `⌘↵` / `Ctrl+↵`). Optional: file name, extra request headers (e.g. `Cookie: ...`).

The progress bar is colored per network, so you can see who downloaded how much. Each item can show the exact CLI command it ran.
Closing the app pauses running downloads; open it again and press ▶ to resume.

## CLI

```bash
mndl nets                 # usable networks (--all: include VPN/virtual, --json)
mndl test                 # does each network reach the internet? + public IP
mndl get URL              # download using the configured networks
mndl get -n en0,en7 -c 16 -d ~/Downloads URL
mndl get -o name.iso -H "Cookie: a=b" URL
mndl get URL1 URL2 URL3   # several files, one after another

mndl config               # show defaults
mndl config dir ~/Downloads
mndl config networks en0,en7
mndl config conns 16
mndl discard NAME         # delete leftover resume files for NAME
```

| Option | Meaning |
|---|---|
| `-n, --net` | Networks to use, comma-separated. Interface ID (`en0`, `Wi-Fi 2`, `wlan0`) or label. Default: the configured list, otherwise every physical network that is up. |
| `-c, --conns` | Connections per network, 1–64 (`aria2c -x` stops at 16). |
| `-d, --dir` | Download folder. |
| `-o, --out` | File name (single URL only). Default: from the server. |
| `-H, --header` | Extra request header, repeatable. |
| `--json` | One JSON event per line on stdout; accepts `pause` / `cancel` on stdin. This is what the app uses. |

- `Ctrl+C` pauses and keeps the partial file; run the same command again to resume.
- Exit codes: `0` done · `1` failed · `130` paused/canceled.
- Settings file: macOS `~/Library/Application Support/multinet-dl/config.json`, Windows `%AppData%\multinet-dl\config.json`, Linux `~/.config/multinet-dl/config.json`. Override with `MNDL_CONFIG`.

## How it works

1. A `Range: bytes=0-0` request finds the size, Range support and ETag.
2. The file is split into `networks × connections` pieces. Every connection is pinned to one interface so it really goes out through that network's gateway:
   macOS `IP_BOUND_IF`, Windows `IP_UNICAST_IF`, Linux `SO_BINDTODEVICE`. HTTP/2 is disabled on purpose — it would multiplex all ranges onto one TCP connection.
3. A connection that finishes takes the back half of the largest unfinished piece (work stealing), so faster networks end up carrying more.
4. Remaining ranges are saved to `<name>.mndl.json` every second next to `<name>.mndl.part`. On resume the ETag / Last-Modified must match, otherwise it starts over.

## License

[GPL-3.0-or-later](LICENSE)
