// Command logprint prints log files on an Epson TM-T20IV receipt printer
// (ESC/POS over TCP port 9100). It is a small, dependency-free tool.
//
// Features:
//   - wraps long lines to the paper width (default: 48 characters, font A)
//   - does NOT cut by default (cut only with -cut)
//   - prints a file once, reads from stdin, or tails a file like "tail -f"
//   - strips ANSI color codes and control characters
//   - uses CP858 (German umlauts + Euro); other code pages are available
//   - reads settings from a TOML file via -c so one instance per config
//     can be started with different printers/settings
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	esc = 0x1b
	gs  = 0x1d
)

var (
	ansiEscape = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\)|[@-_])`)

	// controlChars matches the characters that would reach the raw printer
	// buffer unescaped and must therefore be dropped.
	controlChars = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)

	// unicodeReplacer maps common Unicode symbols that have no equivalent in
	// an ESC/POS code page to plain ASCII look-alikes.
	unicodeReplacer = strings.NewReplacer(
		"\u2018", "'", "\u2019", "'", "\u201a", "'", "\u201b", "'",
		"\u201c", `"`, "\u201d", `"`, "\u201e", `"`,
		"\u2013", "-", "\u2014", "-", "\u2212", "-",
		"\u2026", "...", "\u00a0", " ", "\u00ad", "-",
		"\u2192", "->", "\u2190", "<-",
		"\u2713", "OK", "\u2717", "X",
		"\u2588", "#", "\u2591", ".", "\u2592", ":", "\u2593", "#",
	)
)

// config holds the resolved print settings. Values come from defaults, a
// -c TOML file, and command-line flags (flags win over the file).
type config struct {
	file          string
	follow        bool
	feedOnly      bool
	fromStart     bool
	host          string
	port          int
	width         int
	font          string
	indent        string
	codepage      string
	inputEncoding string
	timestamps    bool
	noHeader      bool
	feed          int
	cut           bool
	cutOnExit     bool
	maxLines      int
}

// encoder turns runes into the bytes of a specific ESC/POS code page.
type encoder struct {
	name   string
	escNum byte
	encode map[rune]byte
}

// newEncoder builds an encoder for the named code page and returns an error
// if name is not one of the supported code pages.
func newEncoder(name string) (*encoder, error) {
	num, ok := codepageNumbers[name]
	if !ok {
		return nil, fmt.Errorf("unknown code page %q", name)
	}
	tbl := codepageTables[name]
	m := make(map[rune]byte, 256)
	for b, r := range tbl {
		if r == 0 || r == 0xFFFD {
			continue
		}
		if _, exists := m[r]; !exists {
			m[r] = byte(b)
		}
	}
	return &encoder{name: name, escNum: num, encode: m}, nil
}

// encodeString maps a UTF-8 string to code-page bytes, using '?' for every
// rune the code page cannot represent.
func (e *encoder) encodeString(s string) []byte {
	out := make([]byte, 0, len(s)+1)
	for _, r := range s {
		if b, ok := e.encode[r]; ok {
			out = append(out, b)
		} else {
			out = append(out, '?')
		}
	}
	return out
}

// sanitize removes ANSI escape sequences, control characters, tabs and
// Unicode symbols, so log content cannot inject ESC/POS commands or produce
// orphaned output on the receipt.
func sanitize(line string) string {
	line = ansiEscape.ReplaceAllString(line, "")
	line = controlChars.ReplaceAllString(line, "")
	line = strings.ReplaceAll(line, "\t", "    ")
	line = unicodeReplacer.Replace(line)
	return strings.TrimRight(line, "\r\n")
}

// wrapLine breaks line at word boundaries so that every returned fragment
// fits into width; very long words are hard-split. Continuation lines are
// prefixed with indent.
func wrapLine(line string, width int, indent string) []string {
	if line == "" {
		return []string{""}
	}
	if len([]rune(line)) <= width {
		return []string{line}
	}

	rest := []rune(line)
	out := make([]string, 0, len(rest)/width+1)
	first := true
	indentRunes := []rune(indent)

	for len(rest) > width {
		limit := width
		if !first {
			limit = width - len(indentRunes)
			if limit < 1 {
				limit = 1
			}
		}
		cut := -1
		for i := limit; i >= 0; i-- {
			if i < len(rest) && rest[i] == ' ' {
				cut = i
				break
			}
		}
		if cut <= 0 {
			cut = limit
		}
		if cut > len(rest) {
			cut = len(rest)
		}
		part := strings.TrimRight(string(rest[:cut]), " ")
		if !first {
			part = indent + part
		}
		out = append(out, part)
		rest = rest[cut:]
		for len(rest) > 0 && rest[0] == ' ' {
			rest = rest[1:]
		}
		first = false
	}
	if len(rest) > 0 || len(out) == 0 {
		part := string(rest)
		if !first {
			part = indent + part
		}
		out = append(out, part)
	}
	return out
}

// decodeInput decodes a raw byte slice from the configured input encoding
// into UTF-8. Unknown encodings fall back to interpreting the bytes as UTF-8.
func decodeInput(b []byte, encoding string) string {
	switch encoding {
	case "utf-8", "utf8":
		return string(b)
	case "latin1", "iso-8859-1":
		var sb strings.Builder
		sb.Grow(len(b))
		for _, c := range b {
			sb.WriteRune(rune(c))
		}
		return sb.String()
	default:
		tbl, ok := codepageTables[encoding]
		if !ok {
			return string(b)
		}
		var sb strings.Builder
		sb.Grow(len(b))
		for _, c := range b {
			r := tbl[c]
			if r == 0 {
				r = '\uFFFD'
			}
			sb.WriteRune(r)
		}
		return sb.String()
	}
}

// printer manages the TCP connection to the printer and the init sequence
// that sets code page, font, alignment and line spacing on connect.
type printer struct {
	addr string
	enc  *encoder
	init []byte
	conn net.Conn
}

// newPrinter builds the init bytes and TCP address for the given config.
func newPrinter(c *config, enc *encoder) *printer {
	font := byte(0)
	if c.font == "b" {
		font = 1
	}
	init := []byte{
		esc, '@',
		esc, 't', enc.escNum,
		esc, 'M', font,
		esc, 'a', 0x00,
		esc, '2',
	}
	return &printer{
		addr: net.JoinHostPort(c.host, strconv.Itoa(c.port)),
		enc:  enc,
		init: init,
	}
}

// connect opens the TCP connection and sends the initialization bytes.
func (p *printer) connect() error {
	conn, err := net.DialTimeout("tcp", p.addr, 10*time.Second)
	if err != nil {
		return err
	}
	if _, err := conn.Write(p.init); err != nil {
		conn.Close()
		return err
	}
	p.conn = conn
	return nil
}

// send writes data to the printer, reconnecting with a short backoff when
// the connection is lost, and reports the last error if all attempts fail.
func (p *printer) send(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if p.conn == nil {
			if err := p.connect(); err != nil {
				lastErr = err
				time.Sleep(2 * time.Second)
				continue
			}
		}
		if _, err := p.conn.Write(data); err != nil {
			lastErr = err
			p.close()
			time.Sleep(2 * time.Second)
			continue
		}
		return nil
	}
	return lastErr
}

// close drops the printer connection if one is open.
func (p *printer) close() {
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
}

// encodeLine encodes one source line for the printer and appends a newline.
func encodeLine(enc *encoder, s string) []byte {
	b := enc.encodeString(s)
	return append(b, '\n')
}

// emitLine sanitizes, optionally timestamps and wraps line, then sends the
// resulting fragments to the printer.
func emitLine(p *printer, c *config, line string) error {
	line = sanitize(line)
	if c.timestamps {
		line = time.Now().Format("[15:04:05] ") + line
	}
	for _, part := range wrapLine(line, c.width, c.indent) {
		if err := p.send(encodeLine(p.enc, part)); err != nil {
			return err
		}
	}
	return nil
}

// printHeader sends a short header with the file name, start time and paper
// width that separates one print job from the next.
func printHeader(p *printer, c *config) error {
	lines := []string{
		"logprint  " + c.file,
		"Start: " + time.Now().Format("2006-01-02 15:04:05"),
		fmt.Sprintf("Width: %d chars (font %s)", c.width, strings.ToUpper(c.font)),
		strings.Repeat("-", c.width),
	}
	for _, line := range lines {
		if err := p.send(encodeLine(p.enc, line)); err != nil {
			return err
		}
	}
	return nil
}

// endSequence returns the bytes sent to the printer on exit: a paper feed
// and the optional cut. In follow mode only a cut-on-exit triggers it.
func endSequence(c *config) []byte {
	var out []byte
	if !c.follow || c.cutOnExit {
		out = append(out, esc, 'd', byte(c.feed))
	}
	if c.cutOnExit || (!c.follow && c.cut) {
		out = append(out, gs, 'V', 66, 0)
	}
	return out
}

// streamFile reads the log line by line, emitting each one, and returns the
// number of lines printed. In follow mode it keeps reading new data and
// reopens the file on rotation or truncation. stop aborts the run.
func streamFile(p *printer, c *config, stop <-chan struct{}) (int, error) {
	var f *os.File
	own := false
	if c.file == "-" {
		f = os.Stdin
	} else {
		var err error
		f, err = os.Open(c.file)
		if err != nil {
			return 0, err
		}
		own = true
	}
	defer func() {
		if own && f != nil {
			_ = f.Close()
		}
	}()

	if c.file != "-" && c.follow && !c.fromStart {
		if _, err := f.Seek(0, io.SeekEnd); err != nil {
			return 0, err
		}
	}

	var offset int64
	if c.file != "-" {
		if fi, err := f.Stat(); err == nil {
			offset = fi.Size()
			if !(c.follow && !c.fromStart) {
				offset = 0
			}
		}
	}
	var oldInfo os.FileInfo
	if c.file != "-" {
		oldInfo, _ = f.Stat()
	}

	buf := make([]byte, 65536)
	var pending []byte
	lines := 0

	for {
		select {
		case <-stop:
			return lines, nil
		default:
		}

		n, rerr := f.Read(buf)
		if n > 0 {
			offset += int64(n)
			pending = append(pending, buf[:n]...)
			for {
				idx := bytes.IndexByte(pending, '\n')
				if idx < 0 {
					break
				}
				lineBytes := pending[:idx]
				pending = pending[idx+1:]
				if err := emitLine(p, c, decodeInput(lineBytes, c.inputEncoding)); err != nil {
					return lines, err
				}
				lines++
				if c.maxLines > 0 && lines >= c.maxLines {
					return lines, nil
				}
			}
			continue
		}

		if rerr != nil && rerr != io.EOF {
			return lines, rerr
		}

		if !c.follow {
			if len(pending) > 0 {
				if err := emitLine(p, c, decodeInput(pending, c.inputEncoding)); err != nil {
					return lines, err
				}
				lines++
			}
			return lines, nil
		}

		select {
		case <-stop:
			return lines, nil
		case <-time.After(500 * time.Millisecond):
		}

		// Follow mode: detect rotation or truncation of the underlying file.
		if c.file != "-" {
			newInfo, err := os.Stat(c.file)
			if err != nil {
				continue
			}
			if oldInfo == nil || !os.SameFile(oldInfo, newInfo) || newInfo.Size() < offset {
				nf, err := os.Open(c.file)
				if err != nil {
					continue
				}
				_ = f.Close()
				f = nf
				oldInfo = newInfo
				offset = 0
				pending = nil
			}
		}
	}
}

// flags holds the raw, parsed command-line values. Only flags set explicitly
// are overlaid on a -c config file afterwards; the defaults matter only for
// the invocation and the built-in default logic.
type flags struct {
	config        string
	host          string
	feedOnly      bool
	port          int
	follow        bool
	followShort   bool
	fromStart     bool
	width         int
	font          string
	indent        string
	codepage      string
	inputEncoding string
	timestamps    bool
	noHeader      bool
	feed          int
	cut           bool
	cutOnExit     bool
	maxLines      int
}

// parseFlags reads the command line, applies the -c TOML file, overrides it
// with explicitly set flags and validates the result. A file to print is
// taken from the positional argument or the config file's "file" key.
func parseFlags() (*config, error) {
	var rf flags

	flag.StringVar(&rf.config, "c", "", "path to a TOML config file")
	flag.StringVar(&rf.host, "host", "192.168.1.185", "printer IP address or hostname")
	flag.IntVar(&rf.port, "port", 9100, "printer raw TCP port")
	flag.BoolVar(&rf.follow, "follow", false, "tail the file like tail -f")
	flag.BoolVar(&rf.followShort, "f", false, "tail the file like tail -f (short)")
	flag.BoolVar(&rf.fromStart, "from-start", false, "with -f print existing content first")
	flag.IntVar(&rf.width, "width", 0, "characters per line (default 48 for font A, 64 for font B)")
	flag.StringVar(&rf.font, "font", "a", "font: a (48 columns) or b (64 columns)")
	flag.StringVar(&rf.indent, "indent", "  ", "indent for wrapped continuation lines")
	flag.StringVar(&rf.codepage, "codepage", "cp858", "code page: cp437, cp850, cp852, cp858, cp866, cp1252")
	flag.StringVar(&rf.inputEncoding, "input-encoding", "utf-8", "log file encoding: utf-8, latin1, cp1252, ...")
	flag.BoolVar(&rf.timestamps, "timestamps", false, "prefix each line with [HH:MM:SS]")
	flag.BoolVar(&rf.noHeader, "no-header", false, "do not print the file-name/start-time header")
	flag.IntVar(&rf.feed, "feed", 3, "lines of paper feed at the end")
	flag.BoolVar(&rf.cut, "cut", false, "cut after a single run (default: no cut)")
	flag.BoolVar(&rf.cutOnExit, "cut-on-exit", false, "feed + cut on exit, also in follow mode (Ctrl-C/SIGTERM)")
	flag.IntVar(&rf.maxLines, "max-lines", 0, "stop after N lines (0 = unlimited)")
	flag.BoolVar(&rf.feedOnly, "feed-only", false, "only feed paper (and optionally cut), print nothing")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `logprint - print log files on an Epson TM-T20IV

Usage:
  logprint [options] FILE
  logprint [options] -
  logprint -c CONFIG.toml [FILE]

Examples:
  logprint /var/log/syslog
  logprint -f /var/log/myapp.log
  logprint -f --from-start /var/log/myapp.log
  logprint -c kitchen.toml       # everything from a TOML file
  logprint -feed-only -cut       # only feed + cut, print nothing
  tail -F /var/log/syslog | logprint -

Options (command-line flags win over a -c config file):
`)
		flag.PrintDefaults()
		fmt.Fprint(flag.CommandLine.Output(), `
Defaults without options:
  Target:          192.168.1.185:9100
  Mode:            single run (no follow)
  Start position:  beginning of file
  Line width:      48 characters (font A)
  Font:            a
  Indent:          2 spaces
  Code page:       cp858
  Input encoding:  utf-8
  Header:          on
  Timestamps:      off
  Trailing feed:   3 lines
  Cut:             off
  Cut on exit:     off
  Max lines:       unlimited
  Config file:     none (-c)
`)
	}

	flag.Parse()

	c := defaultConfig()

	// Precedence: defaults < -c TOML file < explicitly set CLI flags.
	if rf.config != "" {
		if err := loadConfigFile(&c, rf.config); err != nil {
			return nil, err
		}
	}

	visited := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { visited[f.Name] = true })

	if visited["host"] {
		c.host = rf.host
	}
	if visited["port"] {
		c.port = rf.port
	}
	if visited["follow"] || visited["f"] {
		c.follow = rf.follow || rf.followShort
	}
	if visited["from-start"] {
		c.fromStart = rf.fromStart
	}
	if visited["width"] {
		c.width = rf.width
	}
	if visited["font"] {
		c.font = rf.font
	}
	if visited["indent"] {
		c.indent = rf.indent
	}
	if visited["codepage"] {
		c.codepage = rf.codepage
	}
	if visited["input-encoding"] {
		c.inputEncoding = rf.inputEncoding
	}
	if visited["timestamps"] {
		c.timestamps = rf.timestamps
	}
	if visited["no-header"] {
		c.noHeader = rf.noHeader
	}
	if visited["feed"] {
		c.feed = rf.feed
	}
	if visited["cut"] {
		c.cut = rf.cut
	}
	if visited["cut-on-exit"] {
		c.cutOnExit = rf.cutOnExit
	}
	if visited["max-lines"] {
		c.maxLines = rf.maxLines
	}
	if visited["feed-only"] {
		c.feedOnly = rf.feedOnly
	}

	// A positional FILE argument overrides "file" from the config file.
	if flag.NArg() >= 1 {
		c.file = flag.Arg(0)
	}
	if c.file == "" && !c.feedOnly {
		return nil, errors.New("pass a FILE argument, '-' or a 'file' key in the config file, or use -feed-only")
	}

	if c.font != "a" && c.font != "b" {
		return nil, errors.New("-font must be 'a' or 'b'")
	}
	if c.width == 0 {
		if c.font == "b" {
			c.width = 64
		} else {
			c.width = 48
		}
	}
	if c.width < 1 {
		return nil, errors.New("-width must be greater than 0")
	}
	if c.feed < 0 {
		c.feed = 0
	}
	if c.feed > 255 {
		c.feed = 255
	}
	if c.maxLines < 0 {
		c.maxLines = 0
	}
	if c.follow && c.file == "-" {
		return nil, errors.New("-follow only works with a file, not with stdin")
	}
	if _, ok := codepageNumbers[c.codepage]; !ok {
		return nil, fmt.Errorf("unknown code page %q", c.codepage)
	}
	switch c.inputEncoding {
	case "utf-8", "utf8", "latin1", "iso-8859-1":
	default:
		if _, ok := codepageTables[c.inputEncoding]; !ok {
			return nil, fmt.Errorf("unknown input encoding %q", c.inputEncoding)
		}
	}
	return &c, nil
}

// main parses the flags, opens the printer connection and streams the log
// file to it, honouring SIGINT/SIGTERM and the configured end sequence.
func main() {
	c, err := parseFlags()
	if err != nil {
		fmt.Fprintln(os.Stderr, "logprint:", err)
		os.Exit(2)
	}

	enc, err := newEncoder(c.codepage)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logprint:", err)
		os.Exit(2)
	}

	p := newPrinter(c, enc)

	// Feed-only mode: send the paper feed (and optional cut), print nothing.
	if c.feedOnly {
		c.follow = false
		if seq := endSequence(c); len(seq) > 0 {
			if err := p.send(seq); err != nil {
				fmt.Fprintln(os.Stderr, "logprint:", err)
				os.Exit(1)
			}
		}
		p.close()
		os.Exit(0)
	}

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		close(stop)
	}()

	if !c.noHeader {
		if err := printHeader(p, c); err != nil {
			fmt.Fprintln(os.Stderr, "logprint:", err)
		}
	}

	_, err = streamFile(p, c, stop)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logprint:", err)
	}

	if seq := endSequence(c); len(seq) > 0 {
		_ = p.send(seq)
	}
	p.close()

	if err != nil {
		os.Exit(1)
	}
}
