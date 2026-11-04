# logprint (Go)

[![CI](https://github.com/schankst/logprint/actions/workflows/ci.yml/badge.svg)](https://github.com/schankst/logprint/actions/workflows/ci.yml)

Druckt Logdateien auf einem Epson TM-T20IV (ESC/POS über TCP 9100).

- bricht lange Zeilen auf die Papierbreite um (Default: 48 Zeichen, Font A, 80 mm)
- schneidet standardmäßig **nicht**
- druckt eine Datei einmalig, von stdin oder wie `tail -f`
- entfernt ANSI-Farbcodes und Steuerzeichen
- CP858 (deutsche Umlaute + Euro), weitere Codepages möglich
- keine externen Abhängigkeiten, statisch linkbar

## Bauen

```bash
cd logprint-go
go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o logprint .
```

Das Ergebnis ist ein statisch gelinktes Binary (`./logprint`, ca. 2,6 MB).

Cross-Compiling (ohne CGO):

```bash
CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -o logprint-linux-arm64 .
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o logprint-darwin-arm64 .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o logprint.exe .
```

## Benutzung

```bash
# ganze Datei einmal drucken
./logprint /var/log/syslog

# laufend folgen (nur neue Zeilen)
./logprint -f /var/log/myapp.log

# vorhandenen Inhalt drucken und dann folgen
./logprint -f --from-start /var/log/myapp.log

# aus einer Pipe
tail -F /var/log/syslog | ./logprint -

# beim Beenden von -f (Ctrl-C/SIGTERM) noch schneiden
./logprint -f -cut-on-exit /var/log/myapp.log

# einmalig drucken und am Ende schneiden
./logprint -cut /var/log/syslog
```

## Optionen

| Option | Bedeutung |
|---|---|
| `-host 192.168.1.185` | IP-Adresse des Druckers |
| `-port 9100` | TCP-Port |
| `-f`, `-follow` | wie `tail -f` folgen |
| `-from-start` | mit `-f` zuerst vorhandenen Inhalt drucken |
| `-width 48` | Zeichen pro Zeile (Default: 48 bei Font A, 64 bei Font B) |
| `-font b` | Font B (64 Spalten, kleinere Schrift) |
| `-indent "  "` | Einrückung für umgebrochene Folgezeilen (`""` = keine) |
| `-codepage cp858` | Zeichensatz: `cp437`, `cp850`, `cp852`, `cp858`, `cp866`, `cp1252` |
| `-input-encoding utf-8` | Encoding der Logdatei: `utf-8`, `latin1`, `cp1252`, … |
| `-timestamps` | jede Zeile mit `[HH:MM:SS]` prefixen |
| `-no-header` | keinen Kopf mit Dateiname/Startzeit drucken |
| `-feed 3` | Zeilen Vorschub am Ende |
| `-cut` | am Ende eines einmaligen Drucks schneiden (Default: aus) |
| `-cut-on-exit` | beim Beenden Vorschub + Cut senden, auch bei `-f` (Ctrl-C/SIGTERM) |
| `-max-lines N` | nach N Zeilen aufhören |

## Hinweise

- Bei **58-mm-Papier**: `-width 32` (Font A) bzw. `-width 42`.
- „Secure Printing" in der Web-Config des Druckers muss **deaktiviert** bleiben,
  sonst blockiert der Drucker alle externen Aufträge.
- Im Follow-Modus bleibt die TCP-Verbindung offen. Der Drucker trennt sie nach
  90 s Leerlauf; `logprint` erkennt das und verbindet sich beim nächsten
  Log-Eintrag automatisch neu (inkl. erneuter ESC/POS-Initialisierung).
- Log-Rotation und Truncation werden im Follow-Modus erkannt.

## Manpage

Die Manpage liegt als `logprint.1` im Projekt. Installation in das
Benutzerverzeichnis (ohne root):

```bash
mkdir -p ~/.local/share/man/man1
install -m 644 logprint.1 ~/.local/share/man/man1/
mandb -q ~/.local/share/man
man logprint
```

Alternativ direkt aus dem Projekt anzeigen:

```bash
man -l logprint.1
```

## systemd (Beispiel)

```ini
[Unit]
Description=logprint - myapp.log auf Bon-Drucker
After=network-online.target

[Service]
ExecStart=/home/sschank/logprint-go/logprint -f --from-start /var/log/myapp.log
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

## Lizenz

MIT – siehe [LICENSE](LICENSE).

Copyright (c) 2026 Stefan Schank
