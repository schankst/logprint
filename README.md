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

## Standardwerte

Werden keine Optionen angegeben, gilt:

| Einstellung | Standard |
|---|---|
| Ziel | `192.168.1.185:9100` |
| Modus | einmalig, Datei komplett drucken (kein Follow) |
| Startposition | Dateianfang |
| Zeilenbreite | 48 Zeichen (Font A, 80 mm) |
| Font | `a` |
| Einrückung | 2 Leerzeichen |
| Codepage | `cp858` (Umlaute + Euro) |
| Input-Encoding | `utf-8` |
| Kopf mit Dateiname/Startzeit | an |
| Zeitstempel pro Zeile | aus |
| Vorschub am Ende | 3 Zeilen |
| Schnitt am Ende | aus |
| Schnitt beim Beenden | aus |
| Maximale Zeilenzahl | unbegrenzt |

Konkret bedeutet das: `logprint /var/log/app.log` druckt die Datei
einmalig vollständig, mit Kopf, 48 Zeichen pro Zeile, ohne Zeitstempel,
mit 3 Zeilen Vorschub und **ohne Schnitt**.

## Optionen

| Option | Bedeutung | Default |
|---|---|---|
| `-host HOST` | IP-Adresse oder Hostname des Druckers | `192.168.1.185` |
| `-port PORT` | TCP-Port des RAW-Drucks | `9100` |
| `-f`, `-follow` | wie `tail -f` folgen | aus (einmaliger Lauf) |
| `-from-start` | mit `-f` zuerst vorhandenen Inhalt drucken | aus |
| `-width N` | Zeichen pro Zeile | `48` (Font A), `64` (Font B) |
| `-font a\|b` | Schriftart | `a` |
| `-indent STRING` | Einrückung für umgebrochene Folgezeilen | `"  "` (2 Leerzeichen) |
| `-codepage NAME` | Zeichensatz: `cp437`, `cp850`, `cp852`, `cp858`, `cp866`, `cp1252` | `cp858` |
| `-input-encoding NAME` | Encoding der Logdatei: `utf-8`, `latin1`, `cp1252`, … | `utf-8` |
| `-timestamps` | jede Zeile mit `[HH:MM:SS]` prefixen | aus |
| `-no-header` | keinen Kopf mit Dateiname/Startzeit drucken | aus (Kopf wird gedruckt) |
| `-feed N` | Zeilen Vorschub am Ende | `3` |
| `-cut` | am Ende eines einmaligen Drucks schneiden | aus |
| `-cut-on-exit` | beim Beenden Vorschub + Cut senden, auch bei `-f` (Ctrl-C/SIGTERM) | aus |
| `-max-lines N` | nach N Zeilen aufhören | `0` (unbegrenzt) |

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
