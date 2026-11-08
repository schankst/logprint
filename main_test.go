package main

import (
	"bytes"
	"testing"
	"unicode/utf8"
)

func TestSanitize(t *testing.T) {
	got := sanitize("\x1b[31mrot\x1b[0m\tund \x1b[1mfett\x1b[0m")
	want := "rot    und fett"
	if got != want {
		t.Fatalf("sanitize = %q, want %q", got, want)
	}
}

func TestWrapLine(t *testing.T) {
	s := "2026-11-04 21:40:02 DEBUG Dies ist eine absichtlich sehr lange Logzeile, die deutlich mehr als achtundvierzig Zeichen enthaelt und deshalb sauber umgebrochen werden muss."
	lines := wrapLine(s, 48, "  ")
	if len(lines) < 2 {
		t.Fatalf("expected multiple lines, got %d", len(lines))
	}
	for i, line := range lines {
		if n := utf8.RuneCountInString(line); n > 48 {
			t.Errorf("line %d is %d chars long: %q", i, n, line)
		}
	}
	// Continuation lines must be indented.
	for i := 1; i < len(lines); i++ {
		if len(lines[i]) < 2 || lines[i][:2] != "  " {
			t.Errorf("line %d not indented: %q", i, lines[i])
		}
	}
}

func TestWrapLongWord(t *testing.T) {
	lines := wrapLine("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 48, "  ")
	for i, line := range lines {
		if n := utf8.RuneCountInString(line); n > 48 {
			t.Errorf("line %d too long (%d): %q", i, n, line)
		}
	}
}

func TestCP858(t *testing.T) {
	enc, err := newEncoder("cp858")
	if err != nil {
		t.Fatal(err)
	}
	got := enc.encodeString("äöü ÄÖÜ ß €")
	want := []byte{0x84, 0x94, 0x81, 0x20, 0x8E, 0x99, 0x9A, 0x20, 0xE1, 0x20, 0xD5}
	if !bytes.Equal(got, want) {
		t.Fatalf("cp858 = % x, want % x", got, want)
	}
}

func TestDecodeLatin1(t *testing.T) {
	got := decodeInput([]byte{0xE4, 0xF6, 0xFC}, "latin1")
	if got != "äöü" {
		t.Fatalf("latin1 = %q", got)
	}
}

func TestEndSequence(t *testing.T) {
	cases := []struct {
		name string
		c    config
		want []byte
	}{
		{
			name: "normal without cut: only feed",
			c:    config{feed: 3},
			want: []byte{esc, 'd', 3},
		},
		{
			name: "normal with cut",
			c:    config{feed: 2, cut: true},
			want: []byte{esc, 'd', 2, gs, 'V', 66, 0},
		},
		{
			name: "follow without cut-on-exit: nothing",
			c:    config{follow: true, feed: 3},
			want: nil,
		},
		{
			name: "follow with cut (ignored)",
			c:    config{follow: true, feed: 3, cut: true},
			want: nil,
		},
		{
			name: "follow with cut-on-exit",
			c:    config{follow: true, feed: 3, cutOnExit: true},
			want: []byte{esc, 'd', 3, gs, 'V', 66, 0},
		},
		{
			name: "normal with cut-on-exit",
			c:    config{feed: 1, cutOnExit: true},
			want: []byte{esc, 'd', 1, gs, 'V', 66, 0},
		},
		{
			name: "feed 0 with cut",
			c:    config{feed: 0, cut: true},
			want: []byte{esc, 'd', 0, gs, 'V', 66, 0},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := endSequence(&tc.c)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("endSequence = % x, want % x", got, tc.want)
			}
		})
	}
}
