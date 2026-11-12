# Acknowledged terminal paste (version 1)

The shared terminal core 0.5.0 captures external clipboard events before xterm's
bubbling handler. It preserves xterm's LF/CRLF-to-CR conversion and sends one
bracketed-paste pair for the complete operation. Clipboard contents are never
stored in browser storage, logged, or replayed after a connection change.

## Negotiation and framing

GoTTY advertises `"paste-protocol": 1` in terminal preferences only when its PTY
backend supports cancellable writes. Its Hub connector also advertises the
`paste` capability. The current Hub gateway permits prefix `5` only for a
writable stream on a device with this capability. Operators may paste;
observers cannot. Both standalone shares and Hub streams recheck write access.

Client input prefix `5` carries a JSON chunk:

```json
{"id":"random-per-paste-id","seq":0,"size":5,"bracketed":true,"data":"aGVsbG8=","final":true}
```

- `size` is normalized UTF-8 payload bytes, excluding bracket markers/base64.
- `seq` starts at zero and increases by one. IDs are bounded to 64 characters.
- Decoded chunks are at most 4096 bytes; total input is at most 64 MiB.
- `final` must agree exactly with the declared total. There is no retry/resume.
- At most eight chunks are outstanding from the client. The server also has a
  bounded per-paste queue, independent of the Hub connector's reader loop.
- `{"id":"...","cancel":true}` stops the remaining task. Already written bytes
  cannot be rolled back. Cancellation never injects Enter or Ctrl+C.

Server output prefix `e` returns receipts only to the originating client:

```json
{"id":"random-per-paste-id","seq":0,"received":5,"status":"done"}
```

Statuses are `writing`, `done`, `cancelled`, or `error`; errors carry an `error`
string. `received` counts bytes accepted by PTY writes, including partial writes
on failure/cancellation. A `done` receipt is emitted only after the last payload
and closing bracket marker have been written. This does **not** mean the shell
has executed anything, an application has finished processing input, or Vim has
saved a file. Upload receipts continue using prefix `d` unchanged.

## Ordering and lifetime

Only one paste owns a shared PTY. Other clients' ordinary input/pastes receive a
busy response, and API execution/input is excluded until cleanup finishes.
Recognized terminal protocol replies still flow, including replies arriving
while the closing bracket is written. Browser keystrokes typed during paste
wait behind it; stopping, failing or disconnecting discards those queued keys.
A second paste on the same browser terminal is rejected until the first ends.

A worker owns each paste; no session mutex is held during PTY writes. The local
PTY uses Go's poller with serialized writes and 100 ms cancellation checks.
Resize uses `SyscallConn` so obtaining a raw file descriptor cannot accidentally
restore blocking I/O. There is a 30-second idle/write deadline. Bracket cleanup
has its own two-second bound; failure is reported instead of claiming success.
Disconnect, terminal replacement, slow-consumer removal, and shutdown cancel
the task against its original PTY. Reconnect never resubmits any payload.

For servers without negotiation, the browser uses paced legacy `1` input frames
and socket buffering limits. Progress explicitly says **unconfirmed sending**.
Stopping drops unsent text and sends the closing marker when possible; text
already queued on the connection may still arrive. There is no remote receipt
or stronger delivery/cancellation guarantee for this fallback.

## Progress presentation

Both frontends use a nonmodal page-centered progress card, visible above 8 KiB or
when an operation lasts at least 150 ms. It shows the originating terminal,
percentage, UTF-8 bytes, and Stop. Completion hides after one second; stopped or
failed tasks remain dismissible. The overlay preserves terminal focus/geometry
and follows the mobile visual viewport. Hub uses one page-level portal stack,
so split panes and hidden tabs keep the correct task identity.

Hub and GoTTY each require their matching frontend/backend build for the new
protocol. New Hub with older GoTTY keeps the unconfirmed fallback. The new
`terminal-core` tarball is versioned; older published packages are unchanged.
