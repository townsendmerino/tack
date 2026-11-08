// Package daemon is what makes the core a daemon (DESIGN §6.1, §9.1): the single-daemon lock, the Unix socket and the
// check of each connection's peer uid, the client token, and the loop that serves the core on every connection.
//
// Everything lives in tack's state directory, which is 0700 and the daemon's user's own: daemon.lock, held with flock
// for the daemon's lifetime; daemon.sock, 0600, removed and re-created only under the lock; and client.token, 0600,
// made fresh at every start under the lock. The sandbox denies all three to the commands a model runs (internal/sandbox,
// section tack), so only an unsandboxed client can read the token and open a privileged connection.
package daemon
