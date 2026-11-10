# nobara: tack serve on demand

DESIGN §8.2. nobara serves the Mac's model on demand. The preferred way is systemd socket activation, with the user
units here; the fallback is `tack serve -detach` over ssh. Either way, `tack serve` does the following:

- It refuses to start goinfer-serve while goinfer's timing lock (`~/.goinfer-timing.lock`) is held, and answers
  "busy" instead.
- It runs goinfer-serve as a supervised child on loopback, with tack's admission (the G2 fit check and the
  decode-path check).
- It unloads the model, and exits, after `[serve] idle_unload` without a request (15 minutes by default).

Nothing here is installed by tack, and nothing here runs on nobara's GPU until the units are installed and the Mac
connects.

## config.toml on nobara

`100.64.0.1` here and in `tack-serve.socket` is a placeholder, not a real machine. Substitute the serving machine's own
tailnet address (`tailscale ip -4` on it) in both places before installing.

```toml
[servers.local]
kind = "goinfer-child"
binary = "~/tack-runs/bin/goinfer-serve-v0.24.1"   # the release asset tack pins (models.GoinferVersion)
model_path = "~/models/qwen2.5-7b-instruct-q4_k_m.gguf"
ctx = 16384
# v0.24.1 also serves the model as "<model>-fast" (thinking off), which compaction's summarizer asks for; it is on by
# default, and "-fast-alias=false" here turns it off. A goinfer-serve older than v0.24.0 has no alias.
args = ["-backend", "cuda"]

[serve]
server = "local"
listen = "100.64.0.1:8091"            # used only without socket activation (tack serve -detach)
key_file = "~/.config/tack/serve-api-key"
# idle_unload = "15m"
# timing_lock = "~/.goinfer-timing.lock"
```

## Install (socket activation)

1. Edit `ListenStream=` in `tack-serve.socket` to nobara's tailnet address.
2. Copy both units to `~/.config/systemd/user/`.
3. Run `systemctl --user daemon-reload && systemctl --user enable --now tack-serve.socket`.
4. Check them with `systemd-analyze --user verify`.

## The night queue (G6)

`tack serve` checks goinfer's timing lock on every start, every request and every idle tick. While a timed run holds
it, `tack serve` answers busy, starts nothing, and stops a model it has loaded, child and all. So a timed run started
by hand does not run beside a loaded model. But night.py holds the lock per job, not for the whole queue, so a
connection that arrives between two jobs could still start goinfer-serve.

The queue's `before_queue` hook should therefore stop the socket as well as the service:

`systemctl --user stop tack-serve.socket tack-serve.service`

An `after_queue` hook should start the socket again. Today the hook runs `~/tack-runs/serve/stop`. Changing it is a
change on nobara and is for goinfer's process to make.
