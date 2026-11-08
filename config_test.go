package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTomlFlat(t *testing.T) {
	root, err := parseToml(`
# printer config
host = "192.168.1.185"
port = 9100
width = 48
font = "a"
follow = false
feed = 3
indent = "  "  # trailing comment
`)
	if err != nil {
		t.Fatal(err)
	}
	if root["host"] != "192.168.1.185" {
		t.Errorf("host = %v", root["host"])
	}
	if root["port"] != 9100 {
		t.Errorf("port = %v", root["port"])
	}
	if root["width"] != 48 {
		t.Errorf("width = %v", root["width"])
	}
	if root["follow"] != false {
		t.Errorf("follow = %v", root["follow"])
	}
	if root["indent"] != "  " {
		t.Errorf("indent = %q", root["indent"])
	}
}

func TestParseTomlTable(t *testing.T) {
	root, err := parseToml(`
[printer]
host = "10.0.0.5"
port = 9100
cut  = true
`)
	if err != nil {
		t.Fatal(err)
	}
	printer, ok := root["printer"].(map[string]interface{})
	if !ok {
		t.Fatalf("printer = %#v", root["printer"])
	}
	if printer["host"] != "10.0.0.5" {
		t.Errorf("host = %v", printer["host"])
	}
	if printer["cut"] != true {
		t.Errorf("cut = %v", printer["cut"])
	}
}

func TestParseTomlBoolCase(t *testing.T) {
	root, err := parseToml("cut = true\nno_header = false\n")
	if err != nil {
		t.Fatal(err)
	}
	if root["cut"] != true {
		t.Errorf("cut = %v", root["cut"])
	}
	if root["no_header"] != false {
		t.Errorf("no_header = %v", root["no_header"])
	}
}

func TestParseTomlStringEscapes(t *testing.T) {
	root, err := parseToml(`indent = ">\t<"
`)
	if err != nil {
		t.Fatal(err)
	}
	if root["indent"] != ">\t<" {
		t.Errorf("indent = %q", root["indent"])
	}
}

func TestParseTomlLiteralString(t *testing.T) {
	root, err := parseToml(`input_encoding = 'cp1252'
`)
	if err != nil {
		t.Fatal(err)
	}
	if root["input_encoding"] != "cp1252" {
		t.Errorf("input_encoding = %q", root["input_encoding"])
	}
}

func TestParseTomlInvalid(t *testing.T) {
	cases := []string{
		"foo",            // no '='
		"width =",        // empty value
		"width = \"quux", // unterminated string
	}
	for _, in := range cases {
		if _, err := parseToml(in); err == nil {
			t.Errorf("parseToml(%q) expected an error", in)
		}
	}
}

func TestLoadConfigFileAndPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "printer.toml")
	content := `
host = "10.0.0.7"
port = 9100
width = 64
font = "b"
follow = true
from_start = true
timestamps = true
feed = 5
cut = false
codepage = "cp850"
input_encoding = "latin1"
max_lines = 100
file = "/tmp/some.log"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	c := defaultConfig()
	if err := loadConfigFile(&c, path); err != nil {
		t.Fatal(err)
	}
	if c.host != "10.0.0.7" {
		t.Errorf("host = %q", c.host)
	}
	if c.width != 64 || c.font != "b" {
		t.Errorf("width/font = %d/%q", c.width, c.font)
	}
	if !c.follow || !c.fromStart || !c.timestamps {
		t.Errorf("booleans: follow=%v from_start=%v timestamps=%v", c.follow, c.fromStart, c.timestamps)
	}
	if c.feed != 5 {
		t.Errorf("feed = %d", c.feed)
	}
	if c.codepage != "cp850" || c.inputEncoding != "latin1" {
		t.Errorf("codepage/input = %q/%q", c.codepage, c.inputEncoding)
	}
	if c.maxLines != 100 {
		t.Errorf("max_lines = %d", c.maxLines)
	}
	if c.file != "/tmp/some.log" {
		t.Errorf("file = %q", c.file)
	}
	// Unset keys keep their defaults.
	if c.cut || c.noHeader {
		t.Errorf("cut/noHeader should default to false: %v/%v", c.cut, c.noHeader)
	}
	if c.indent != "  " || c.port != 9100 {
		t.Errorf("indent/port unexpected: %q/%d", c.indent, c.port)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	c := defaultConfig()
	if err := loadConfigFile(&c, filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestLoadConfigWrongType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(path, []byte("host = 42.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := defaultConfig()
	if err := loadConfigFile(&c, path); err == nil {
		t.Fatal("expected a type error for host")
	}
}

func TestLoadConfigFeedOnlyKey(t *testing.T) {
	c := defaultConfig()
	if c.feedOnly {
		t.Fatal("feedOnly should default to false")
	}
	root, err := parseToml("feed_only = true\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := applyToml(&c, root); err != nil {
		t.Fatal(err)
	}
	if !c.feedOnly {
		t.Fatal("feedOnly should be true after applying feed_only key")
	}
}
