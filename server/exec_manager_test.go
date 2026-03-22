package server

import "testing"

func TestExtractFramedOutputReturnsTranscriptBetweenMarkers(t *testing.T) {
	start := []byte("<<<GOTTY_START:exec_abcd>>>")
	exit := []byte("<<<GOTTY_EXIT:exec_abcd:")
	input := []byte(`printf '\n<<<GOTTY_START:exec_abcd>>>\n'
<<<GOTTY_START:exec_abcd>>>
user@host:~$ echo hello
hello
printf '\n<<<GOTTY_EXIT:exec_abcd:%s>>>\n' "$?"
<<<GOTTY_EXIT:exec_abcd:0>>>
`)

	got := string(extractFramedOutput(input, start, exit, true))
	want := "user@host:~$ echo hello\nhello\n"
	if got != want {
		t.Fatalf("unexpected output\nwant: %q\ngot:  %q", want, got)
	}
}

func TestExtractFramedOutputAllowsPartialTranscript(t *testing.T) {
	start := []byte("<<<GOTTY_START:exec_abcd>>>")
	exit := []byte("<<<GOTTY_EXIT:exec_abcd:")
	input := []byte(`<<<GOTTY_START:exec_abcd>>>
user@host:~$ echo hello
hello`)

	got := string(extractFramedOutput(input, start, exit, true))
	want := "user@host:~$ echo hello\nhello"
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
