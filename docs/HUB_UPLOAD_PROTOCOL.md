# Acknowledged Hub file uploads

Hub terminal preferences advertise `hub-config.upload_ack: true`. Clients that
opt into this feature add `uploadId` to the existing `7` file-upload frame.
Requests retain `name`, `size`, `chunk`, `totalChunks` and base64 `data`.

After each successful write, GoTTY sends the originating client a `d` data
frame containing JSON `{uploadId,name,chunk,received,done}`. `received` is the
total byte count so far. `done` becomes true only after the entire file has
been synced, closed and renamed to its destination. An `error` string reports
validation or filesystem failure. These receipts are neither broadcast nor
recorded in terminal history.

The receiver validates the 64 MiB limit, safe basename, exact size and sequential
chunks. Each connected client has its own upload state. Concurrent acknowledged
uploads to the same destination are rejected. Temporary files in the selected
working directory keep existing files intact until successful completion;
permissions of existing regular files are retained. Symlink and nonregular
destinations are rejected.

Cancellation uses `7` with `{"uploadId":"id","cancel":true}`. Cancellation,
client removal, errors, PTY restart and server shutdown discard incomplete
temporary files. A cancellation arriving after the file was committed does not
undo that completed write. Uploads do not resume across connection loss.

Messages without `uploadId` use the legacy upload handler unchanged, including
standalone GoTTY clients. They do not receive acknowledgments. Updated Hub
clients connected to older GoTTY versions keep the legacy wire format and
report an unverified send rather than confirmed filesystem success.

Run `go test ./...` and `go test -race ./server`. The companion gotty-hub
repository has `WORKSPACE_SUITE=upload` browser coverage for actual filesystem
bytes, progress, errors, cancel/retry, mobile layout and legacy compatibility.
