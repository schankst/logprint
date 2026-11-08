# logprint (Go)

[![CI](https://github.com/schankst/logprint/actions/workflows/ci.yml/badge.svg)](https://github.com/schankst/logprint/actions/workflows/ci.yml)

Prints log files on an Epson TM-T20IV (ESC/POS over TCP 9100).

- wraps long lines to the paper width (default: 48 characters, font A, 80 mm)
- does **not** cut by default
- prints a file once, from stdin or tails it like `tail -f`
- strips ANSI color codes and control characters
- CP858 (German umlauts + Euro), other code pages available
- no external dependencies, statically linkable

## Building

```bash
cd logprint-go
go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o logprint .
```

The result is a statically linked binary (`./logprint`, approx. 2.6 MB).

Cross compiling (without CGO):

```bash
CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -o logprint-linux-arm64 .
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o logprint-darwin-arm64 .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o logprint.exe .
```

## Usage

```bash
# print a whole file once
./logprint /var/log/syslog

# tail the file (only new lines)
./logprint -f /var/log/myapp.log

# print existing content first, then tail
./logprint -f --from-start /var/log/myapp.log

# print from a pipe
tail -F /var/log/syslog | ./logprint -

# cut when -f exits (Ctrl-C/SIGTERM)
./logprint -f -cut-on-exit /var/log/myapp.log

# print once and cut at the end
./logprint -cut /var/log/syslog

# only feed paper (and optionally cut), print nothing
./logprint -feed-only
./logprint -feed-only -feed 5 -cut
```

## Defaults

When no options are given, the following applies:

| Setting | Default |
|---|---|
| Target | `192.168.1.185:9100` |
| Mode | single run, print the whole file (no follow) |
| Start position | beginning of the file |
| Line width | 48 characters (font A, 80 mm) |
| Font | `a` |
| Indentation | 2 spaces |
| Code page | `cp858` (umlauts + Euro) |
| Input encoding | `utf-8` |
| Header with file name/start time | on |
| Timestamp per line | off |
| Trailing feed | 3 lines |
| Cut at end | off |
| Cut on exit | off |
| Maximum lines | unlimited |

Concretely this means: `logprint /var/log/app.log` prints the file once,
completely, with a header, 48 characters per line, no timestamps, 3 lines
of trailing feed and **no cut**.

## Options

| Option | Meaning | Default |
|---|---|---|
| `-host HOST` | IP address or hostname of the printer | `192.168.1.185` |
| `-port PORT` | TCP port for raw printing | `9100` |
| `-f`, `-follow` | tail the file like `tail -f` | off (single run) |
| `-from-start` | with `-f` print existing content first | off |
| `-width N` | characters per line | `48` (font A), `64` (font B) |
| `-font a\|b` | font | `a` |
| `-indent STRING` | indent for wrapped continuation lines | `"  "` (2 spaces) |
| `-codepage NAME` | code page: `cp437`, `cp850`, `cp852`, `cp858`, `cp866`, `cp1252` | `cp858` |
| `-input-encoding NAME` | log file encoding: `utf-8`, `latin1`, `cp1252`, … | `utf-8` |
| `-timestamps` | prefix each line with `[HH:MM:SS]` | off |
| `-no-header` | do not print the file-name/start-time header | off (header is printed) |
| `-feed N` | lines of trailing feed | `3` |
| `-cut` | cut after a single run | off |
| `-cut-on-exit` | feed + cut on exit, also with `-f` (Ctrl-C/SIGTERM) | off |
| `-max-lines N` | stop after N lines | `0` (unlimited) |
| `-feed-only` | only feed paper, optionally cut, print nothing (no FILE needed) | off |
| `-c CONFIG.toml` | read settings from a TOML file | none |

## Configuration files (TOML)

With the `-c CONFIG.toml` option the settings are taken from a TOML
file. This lets you start the program multiple times, each with its own
config file – for example one printer in the kitchen, one in the
warehouse:

```bash
logprint -c kitchen.toml
logprint -c warehouse.toml
```

All keys may be written flat or under a `[printer]` section. Allowed are
`host`, `port`, `follow`, `from_start`, `width`, `font`, `indent`,
`codepage`, `input_encoding`, `timestamps`, `no_header`, `feed`, `cut`,
`cut_on_exit`, `max_lines` and optionally `file`.

```toml
# warehouse.toml – receipt printer in the warehouse
[printer]
host        = "192.168.1.190"
port        = 9100
font        = "b"
width       = 64
follow      = true
from_start  = false
timestamps  = true
feed        = 2
cut_on_exit = true
# file = "/var/log/warehouse.log"   # optional, otherwise pass as argument
```

**Precedence:** command-line flags win over the file, the file wins over
the defaults. Keys missing from the file keep their default values. A
positional `FILE` argument overrides a `file` set in the configuration.
A sample file is included as [`logprint.example.toml`](logprint.example.toml)
in the project.

### Config plus overrides (examples)

```bash
# everything from the config file
logprint -c kitchen.toml

# override only the printer address for this run
logprint -c kitchen.toml -host 192.168.1.200

# print wider (font B, 64 columns) for this run
logprint -c kitchen.toml -width 64 -font b

# representative file, no header, follow mode
logprint -c kitchen.toml -f -no-header /tmp/demo.log
```

## Notes

- For **58 mm paper**: `-width 32` (font A) or `-width 42`.
- **Secure Printing** in the printer's web config must stay **disabled**,
  otherwise the printer blocks all external jobs.
- In follow mode the TCP connection stays open. The printer drops it after
  90 s of idle time; `logprint` detects this and reconnects automatically on
  the next log entry (including renewed ESC/POS initialization).
- Log rotation and truncation are detected in follow mode.

## Manpage

The man page lives in the project as `logprint.1`. Installation into the
user directory (without root):

```bash
mkdir -p ~/.local/share/man/man1
install -m 644 logprint.1 ~/.local/share/man/man1/
mandb -q ~/.local/share/man
man logprint
```

Alternatively, view it directly from the project:

```bash
man -l logprint.1
```

## systemd (example)

```ini
[Unit]
Description=logprint - myapp.log on receipt printer
After=network-online.target

[Service]
ExecStart=/home/sschank/logprint-go/logprint -f --from-start /var/log/myapp.log
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

## License

MIT – see [LICENSE](LICENSE).

Copyright (c) 2026 Stefan Schank