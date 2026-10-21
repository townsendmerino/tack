# E1 calibration C6, change 4.3: loopback in the sandbox, held, 2026-10-21

<!-- citations-at: 3e2960d -->

Change 4.3 (§4.3, ADR-005) was built on branch `c6-43-loopback` and held by the owner before its screen, because a
test server inside the sandbox was reachable from another machine. It has no screen and no verdict.

## What was built (branch `c6-43-loopback`)

- **Why per-port.** Seatbelt ignores a port's denial once `localhost:*` is allowed. Probes found no form that holds
  a port back:
  - a denial after the allowance or before it;
  - an exclusion inside it.
- **What the profile allows.** It names each port of the ephemeral range (49152–65535), leaving out the ports
  listening when the command starts (the kernel's TCP socket table, held to `netstat`). It allows binding and
  accepting on loopback.
- **The cost.** Compiling that profile takes 4.94 s, against 0.01 s, so only commands the shell parses as running
  `go test` got it.
- **Escape tests that passed:**
  - pre-existing listeners on 127.0.0.1, `[::1]` and the LAN address;
  - a SOCKS proxy;
  - nobara's ssh port from a `go test` command;
  - UDP;
  - no network for any other command.
- **What it fixed.** In the sandbox, ken's `internal/modelfetch` failed without loopback and passed with it.

## Why it is held

`TestLoopbackNotReachableFromAnotherMachine`: a sandboxed `go test` listens on all addresses and on each of the Mac's
own addresses, and nobara tries to connect. nobara reaches the Mac only over Tailscale; the Mac's LAN is another subnet.

| profile | all addresses | Mac's Tailscale address | Mac's LAN address | 127.0.0.1 |
|---|---|---|---|---|
| no sandbox | nobara connected | nobara connected | listening, no route from nobara | refused |
| per-port loopback (`go test`) | **nobara connected** | **nobara connected** | listening, no route from nobara | refused |
| no network (every other command) | listen refused | listen refused | listen refused | listen refused |

- **The profile cannot close it.**
  - A host in a network rule can only be `*` or `localhost`, and to Seatbelt `localhost` covers every address of the
    machine.
  - Inbound is checked only at `listen`, by local address. A rule on the remote address made every `listen` fail,
    127.0.0.1 included.
- **The risk.** While a `go test` command runs, a server it starts on all addresses or a fixed port can be reached from
  the LAN or the tailnet. The process behind it can read the workspace, so a peer that connects in is a way out for
  data, though outbound stays blocked.

## What stays

- **The sandbox keeps loopback denied,** as before 4.3.
- **The known cost:** tests that start a local server fail in the sandbox. ken's `internal/modelfetch` fails with
  `httptest: failed to listen on a port: listen tcp6 [::1]:0: bind: operation not permitted`. Captured screens show
  such failures in 1–8 runs of 72.
- **Open design question:** run `go test` outside Seatbelt, in a VM, or under a port-scoped firewall rule.
