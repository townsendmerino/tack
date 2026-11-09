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

```toml
[servers.local]
kind = "goinfer-child"
binary = "~/tack-runs/bin/goinfer-serve-tack"
model_path = "~/models/qwen2.5-7b-instruct-q4_k_m.gguf"
ctx = 16384
args = ["-backend", "cuda"]

[serve]
server = "local"
listen = "100.125.33.107:8091"            # used only without socket activation (tack serve -detach)
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

While the timing lock is held, a connection starts `tack serve`, which then answers busy and starts nothing. But
night.py holds the lock per job, not for the whole queue, so a connection that arrives between two jobs could start
goinfer-serve.

The queue's `before_queue` hook should therefore stop the socket as well as the service:

`systemctl --user stop tack-serve.socket tack-serve.service`

An `after_queue` hook should start the socket again. Today the hook runs `~/tack-runs/serve/stop`. Changing it is a
change on nobara and is for goinfer's process to make.
