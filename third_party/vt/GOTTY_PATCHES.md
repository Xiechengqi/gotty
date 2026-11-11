# GoTTY terminal state adapter

Upstream: github.com/charmbracelet/x/vt v0.1.0 (MIT, LICENSE retained).
This source-owned copy adds a side-effect-free ANSI checkpoint API because the
upstream API does not expose the inactive screen, saved cursors or parser state.

GoTTY changes:

- snapshot.go generates safe state checkpoints and disables alternate history.
- emulator.go records incomplete sequences; reflow.go and screen.go retain soft
  wrapping, distinguish null cells from typed spaces, and resize normal history
  using xterm's default cursor-line policy.
- scrollback.go bounds lines and conservative cell memory (128 MiB) and tracks
  eviction; normal history is separate from the alternate buffer. History and
  reflow keep wide-character continuation cells; checkpoints do not turn the
  unused cell before a wide-character wrap into a typed space.
- utf8.go aligns the default code-point cell model with xterm, handles split
  combining marks and wide-character wrap, and honors insert mode.
- csi_mode.go supports DEC 47 switching; esc.go resets history/buffers; handlers.go
  aligns erase-display behavior with xterm's defaults.

Upstream terminal parsing and rendering remain the basis of the emulator. Update this directory
from the pinned upstream release and rerun the remote terminal recovery tests
when upgrading. Responses from the shadow terminal are discarded, never sent to
an actual PTY. Checkpoints are memory-only and bounded; they do not record files
or claim to archive an application's own conversation history.
