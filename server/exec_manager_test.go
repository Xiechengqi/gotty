package server

import "testing"

func TestExtractOutputIncludesTerminalTranscriptAndRemovesMarker(t *testing.T) {
	input := []byte(
		"user@host:~$ echo hello\r\n" +
			"hello\r\n" +
			"<<<GOTTY_EXIT:exec_abcd:0>>>\r\n" +
			"user@host:~$ ",
	)

	got := extractOutput(input)
	want := "user@host:~$ echo hello\r\nhello\r\nuser@host:~$ "
	if got != want {
		t.Fatalf("unexpected output\nwant: %q\ngot:  %q", want, got)
	}
}

func TestStripMarkerLines(t *testing.T) {
	input := []byte("line1\n<<<GOTTY_EXIT:x:0>>>\nline2\n")
	got := stripMarkerLines(input, []byte("<<<GOTTY_EXIT:"))
	want := "line1\nline2\n"
	if string(got) != want {
		t.Fatalf("unexpected stripped output\nwant: %q\ngot:  %q", want, string(got))
	}
}
