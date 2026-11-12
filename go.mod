module github.com/sorenisanerd/gotty

go 1.26.0

replace github.com/charmbracelet/x/vt => ./third_party/vt

require (
	github.com/NYTimes/gziphandler v1.1.1
	github.com/charmbracelet/x/ansi v0.11.7
	github.com/charmbracelet/x/vt v0.1.0
	github.com/creack/pty v1.1.24
	github.com/fatih/structs v1.1.0
	github.com/gorilla/websocket v1.5.3
	github.com/pkg/errors v0.9.1
	github.com/urfave/cli/v2 v2.27.7
	github.com/yudai/hcl v0.0.0-20151013225006-5fa2393b3552
	golang.org/x/crypto v0.45.0
	golang.org/x/sys v0.41.0
)

require (
	github.com/charmbracelet/colorprofile v0.4.2 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260303162955-0b88c25f3fff // indirect
	github.com/charmbracelet/x/exp/ordered v0.1.0 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.7 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/mattn/go-runewidth v0.0.23 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/xrash/smetrics v0.0.0-20250705151800-55b8f293f342 // indirect
	golang.org/x/sync v0.19.0 // indirect
)
