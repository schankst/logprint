package main

// logprint - druckt Logdateien auf einem Epson TM-T20IV (ESC/POS ueber TCP 9100).
//
// Eigenschaften:
//   - bricht lange Zeilen auf die Papierbreite um (Standard: 48 Zeichen, Font A)
//   - schneidet standardmaessig NICHT (Cut nur mit -cut)
//   - druckt eine Datei einmalig, von stdin oder wie "tail -f"
//   - entfernt ANSI-Farbcodes und Steuerzeichen
//   - nutzt CP858 (deutsche Umlaute + Euro), weitere Codepages moeglich

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
	ansiEscape   = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\)|[@-_])`)
	controlChars = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)

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

type config struct {
	file          string
	follow        bool
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

type encoder struct {
	name   string
	escNum byte
	encode map[rune]byte
}

func newEncoder(name string) (*encoder, error) {
	num, ok := codepageNumbers[name]
	if !ok {
		return nil, fmt.Errorf("unbekannte Codepage %q", name)
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

func sanitize(line string) string {
	line = ansiEscape.ReplaceAllString(line, "")
	line = controlChars.ReplaceAllString(line, "")
	line = strings.ReplaceAll(line, "\t", "    ")
	line = unicodeReplacer.Replace(line)
	return strings.TrimRight(line, "\r\n")
}

// wrapLine bricht an Wortgrenzen um; sehr lange Woerter werden hart getrennt.
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

type printer struct {
	addr string
	enc  *encoder
	init []byte
	conn net.Conn
}

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
		if _, err := p.conn.Write(data); err == nil {
			return nil
		} else {
			lastErr = err
			p.close()
			time.Sleep(2 * time.Second)
		}
	}
	return lastErr
}

func (p *printer) close() {
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
}

func encodeLine(enc *encoder, s string) []byte {
	b := enc.encodeString(s)
	return append(b, '\n')
}

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

func printHeader(p *printer, c *config) error {
	lines := []string{
		"logprint  " + c.file,
		"Start: " + time.Now().Format("2006-01-02 15:04:05"),
		fmt.Sprintf("Breite: %d Zeichen (Font %s)", c.width, strings.ToUpper(c.font)),
		strings.Repeat("-", c.width),
	}
	for _, line := range lines {
		if err := p.send(encodeLine(p.enc, line)); err != nil {
			return err
		}
	}
	return nil
}

// endSequence liefert die Bytes, die beim Beenden noch an den Drucker gehen.
// Normalbetrieb: Vorschub und optional Cut.
// Follow-Modus: nur mit -cut-on-exit (Vorschub + Cut).
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

		// Rotation oder Truncation erkennen.
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

func parseFlags() (*config, error) {
	var c config
	var followLong, followShort bool

	flag.StringVar(&c.host, "host", "192.168.1.185", "IP-Adresse des Druckers")
	flag.IntVar(&c.port, "port", 9100, "TCP-Port des Druckers")
	flag.BoolVar(&followLong, "follow", false, "wie tail -f folgen")
	flag.BoolVar(&followShort, "f", false, "wie tail -f folgen (Kurzform)")
	flag.BoolVar(&c.fromStart, "from-start", false, "mit -f zuerst vorhandenen Inhalt drucken")
	flag.IntVar(&c.width, "width", 0, "Zeichen pro Zeile (Default: 48 bei Font A, 64 bei Font B)")
	flag.StringVar(&c.font, "font", "a", "Font: a (48 Spalten) oder b (64 Spalten)")
	flag.StringVar(&c.indent, "indent", "  ", "Einrueckung fuer umgebrochene Folgezeilen")
	flag.StringVar(&c.codepage, "codepage", "cp858", "Zeichensatz: cp437, cp850, cp852, cp858, cp866, cp1252")
	flag.StringVar(&c.inputEncoding, "input-encoding", "utf-8", "Encoding der Logdatei: utf-8, latin1, cp1252, ...")
	flag.BoolVar(&c.timestamps, "timestamps", false, "jede Zeile mit [HH:MM:SS] prefixen")
	flag.BoolVar(&c.noHeader, "no-header", false, "keinen Kopf mit Dateiname/Startzeit drucken")
	flag.IntVar(&c.feed, "feed", 3, "Zeilen Vorschub am Ende")
	flag.BoolVar(&c.cut, "cut", false, "am Ende eines einmaligen Drucks schneiden (Default: nicht schneiden)")
	flag.BoolVar(&c.cutOnExit, "cut-on-exit", false, "beim Beenden Vorschub + Cut senden, auch im Follow-Modus (Ctrl-C/SIGTERM)")
	flag.IntVar(&c.maxLines, "max-lines", 0, "nach N Zeilen aufhoeren (0 = unbegrenzt)")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `logprint - Logdateien auf einem Epson TM-T20IV drucken

Aufruf:
  logprint [Optionen] DATEI
  logprint [Optionen] -

Beispiele:
  logprint /var/log/syslog
  logprint -f /var/log/myapp.log
  logprint -f --from-start /var/log/myapp.log
  tail -F /var/log/syslog | logprint -

Optionen:
`)
		flag.PrintDefaults()
		fmt.Fprint(flag.CommandLine.Output(), `
Standardwerte ohne Optionen:
  Ziel:            192.168.1.185:9100
  Modus:           einmalig (kein Follow)
  Startposition:   Dateianfang
  Zeilenbreite:    48 Zeichen (Font A)
  Font:            a
  Einrückung:      2 Leerzeichen
  Codepage:        cp858
  Input-Encoding:  utf-8
  Kopf:            an
  Zeitstempel:     aus
  Vorschub:        3 Zeilen
  Cut:             aus
  Cut on exit:     aus
  Max. Zeilen:     unbegrenzt
`)
	}

	flag.Parse()
	c.follow = followLong || followShort

	if flag.NArg() < 1 {
		return nil, errors.New("Logdatei oder '-' als Argument angeben")
	}
	c.file = flag.Arg(0)

	if c.font != "a" && c.font != "b" {
		return nil, errors.New("-font muss 'a' oder 'b' sein")
	}
	if c.width == 0 {
		if c.font == "b" {
			c.width = 64
		} else {
			c.width = 48
		}
	}
	if c.width < 1 {
		return nil, errors.New("-width muss groesser als 0 sein")
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
		return nil, errors.New("-follow funktioniert nur mit einer Datei, nicht mit stdin")
	}
	if _, ok := codepageNumbers[c.codepage]; !ok {
		return nil, fmt.Errorf("unbekannte Codepage %q", c.codepage)
	}
	switch c.inputEncoding {
	case "utf-8", "utf8", "latin1", "iso-8859-1":
	default:
		if _, ok := codepageTables[c.inputEncoding]; !ok {
			return nil, fmt.Errorf("unbekanntes Input-Encoding %q", c.inputEncoding)
		}
	}
	return &c, nil
}

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
