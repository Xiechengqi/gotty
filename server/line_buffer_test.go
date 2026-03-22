package server

import (
	"reflect"
	"testing"
)

func TestLineBufferGetLastNUsesLogicalLines(t *testing.T) {
	lb := NewLineBuffer(10)
	lb.Append([]byte("line1\nline"))
	lb.Append([]byte("2\nline3"))

	got := lb.GetLastN(3)
	want := []string{"line1", "line2", "line3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected lines\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestLineBufferRespectsMaxLines(t *testing.T) {
	lb := NewLineBuffer(2)
	lb.Append([]byte("line1\nline2\nline3\n"))

	got := lb.GetLastN(10)
	want := []string{"line2", "line3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected lines\nwant: %#v\ngot:  %#v", want, got)
	}
}
