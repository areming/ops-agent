package transport

import (
	"io"
	"net"
	"testing"
)

// TestConnRoundTrip writes frames through one end of an in-memory pipe
// and reads them back on the other, covering the length-prefix framing
// and the string payload helpers.
func TestConnRoundTrip(t *testing.T) {
	c1, c2 := net.Pipe()
	a, b := NewConn(c1), NewConn(c2)

	want := []Frame{
		mustText(t, TypeUserInput, "hello, 世界"),
		mustText(t, TypeAssistantDelta, "h"),
		{Type: TypeDone},
	}

	go func() {
		for _, f := range want {
			if err := a.WriteFrame(f); err != nil {
				t.Errorf("WriteFrame: %v", err)
				return
			}
		}
		_ = a.Close()
	}()

	for i, w := range want {
		got, err := b.ReadFrame()
		if err != nil {
			t.Fatalf("ReadFrame[%d]: %v", i, err)
		}
		if got.Type != w.Type {
			t.Errorf("frame[%d] type = %q, want %q", i, got.Type, w.Type)
		}
		gotText, _ := got.Text()
		wantText, _ := w.Text()
		if gotText != wantText {
			t.Errorf("frame[%d] text = %q, want %q", i, gotText, wantText)
		}
	}

	if _, err := b.ReadFrame(); err != io.EOF {
		t.Errorf("after last frame: err = %v, want EOF", err)
	}
}

func mustText(t *testing.T, ft FrameType, s string) Frame {
	t.Helper()
	f, err := TextFrame(ft, s)
	if err != nil {
		t.Fatalf("TextFrame: %v", err)
	}
	return f
}
