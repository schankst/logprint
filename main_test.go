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
		t.Fatalf("erwartet mehrere Zeilen, got %d", len(lines))
	}
	for i, line := range lines {
		if n := utf8.RuneCountInString(line); n > 48 {
			t.Errorf("Zeile %d ist %d Zeichen lang: %q", i, n, line)
		}
	}
	// Fortsetzungszeilen muessen eingerueckt sein.
	for i := 1; i < len(lines); i++ {
		if len(lines[i]) < 2 || lines[i][:2] != "  " {
			t.Errorf("Zeile %d nicht eingerueckt: %q", i, lines[i])
		}
	}
}

func TestWrapLongWord(t *testing.T) {
	lines := wrapLine("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 48, "  ")
	for i, line := range lines {
		if n := utf8.RuneCountInString(line); n > 48 {
			t.Errorf("Zeile %d zu lang (%d): %q", i, n, line)
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
