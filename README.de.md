# Pulse — selbst gehosteter RSS-Reader

**Sprachen:** [English](README.md) · [Deutsch](README.de.md) · [Русский](README.ru.md)

Pulse ist eine Mehrbenutzer-Webanwendung für RSS- und Atom-Feeds. Sie läuft lokal mit React und TypeScript, einer Go/Chi-API, PostgreSQL und Nginx. Frontend, Backend und Datenbank verwenden getrennte Docker-Compose-Dateien.

## Voraussetzungen

- Docker Engine mit dem Plugin `docker compose`
- Ausgehender HTTP/HTTPS-Zugriff des Servers auf öffentliche Feed-URLs

Go und Node.js müssen nicht auf dem Host installiert sein. Die Versionen sind in Manifesten und Lock-Dateien festgelegt.

## Lokaler Start

Führen Sie alle Befehle im Projektstammverzeichnis aus. Erstellen Sie lokale Einstellungen und das gemeinsame Docker-Netzwerk einmalig:

```sh
cp .env.example .env
docker network create rss-reader-net
```

Setzen Sie `INITIAL_ADMIN_EMAIL` und ein individuelles `INITIAL_ADMIN_PASSWORD` mit mindestens 12 Zeichen in `.env`, bevor die API erstmals mit einer leeren Datenbank startet. Diese Werte werden nur zum Anlegen des ersten Administrators verwendet. Für lokales HTTP bleibt `SESSION_COOKIE_SECURE=false`.

Starten Sie Datenbank, API und Webserver in dieser Reihenfolge:

```sh
docker compose -f compose.database.yml up -d
docker compose -f compose.backend.yml up -d --build
docker compose -f compose.frontend.yml up -d --build
```

Öffnen Sie <http://localhost:8090>. Bei einem belegten Port ändern Sie `WEB_PORT` in `.env`. Für Zugriff aus dem LAN setzen Sie `WEB_BIND=0.0.0.0`. Läuft Nginx bereits auf dem Host, verwenden Sie `WEB_BIND=127.0.0.1` und leiten Sie Anfragen an `http://127.0.0.1:8090` weiter.

Der Frontend-Quellcode ist in den Container eingebunden. Im Entwicklungsmodus prüft der Container TypeScript und startet Vite im Watch-Modus. Änderungen bauen `frontend/dist/` automatisch neu; Nginx liefert den aktualisierten Build aus. Laden Sie den Browser neu, um die Änderungen zu sehen. Für Quellcodeänderungen ist kein manueller Build nötig. Nach Änderungen an Abhängigkeiten oder am Docker-Image den Frontend-Container neu erstellen:

```sh
docker compose -f compose.frontend.yml up -d --build
```

Der Backend-Code ist in den API-Container eingebunden. Nach Go-Änderungen die API neu starten:

```sh
docker compose -f compose.backend.yml restart api
```

## Frontend-Tests

Komponententests und Playwright-Browsertests im Frontend-Container ausführen:

```sh
docker compose -f compose.frontend.yml exec frontend npm run lint
docker compose -f compose.frontend.yml exec frontend npm run test -- --run
docker compose -f compose.frontend.yml exec frontend npm run test:e2e
```

Playwright startet einen lokalen Vite-Server und verwendet deterministische API-Fixtures. Die Offline- und Installationstests (`frontend/test/e2e/pwa.spec.ts`) laufen gegen einen Produktions-Build, den Playwright mit `vite preview` auf Port 4173 ausliefert. Screenshot-Baselines liegen unter `frontend/test/e2e/`. Nach einer beabsichtigten visuellen Änderung prüfen und aktualisieren:

```sh
docker compose -f compose.frontend.yml exec frontend npm run test:e2e:update
```

Alle Compose-Dateien verwenden das externe Netzwerk `rss-reader-net`, damit die Gruppen unabhängig gestartet und gestoppt werden können. PostgreSQL muss vor der API laufen; die API wartet beim Start auf die Datenbank.

## Sprachen

Die Oberfläche ist auf Englisch, Deutsch und Russisch verfügbar. Beim ersten Besuch wird Englisch verwendet. Wählen Sie die Sprache über die Auswahl in der Werkzeugleiste oder auf der Anmeldeseite; die Auswahl wird im lokalen Browserspeicher abgelegt. Die JSON-Übersetzungen liegen in `frontend/src/locales/`.

Für eine weitere Sprache erstellen Sie eine JSON-Datei auf Basis der Schlüssel in `en.json`, ergänzen den Sprachcode in `supportedLanguages` und einen Loader in `catalogLoaders` in `frontend/src/i18n.ts` und fügen den nativen Namen in `frontend/src/LanguageSelector.tsx` hinzu. Englisch ist Quellsprache und Fallback.

## Agent-Skills

Projektspezifische Skills liegen in `.agents/skills/`. Dieses Verzeichnis steht in `.gitignore`; die Skills gehören also nicht zum Repository, und ein frischer Klon enthält keine. `skills-lock.json` erfasst die extern gepflegten Skills mit ihrer Upstream-Quelle; das Projektskript stellt sie mit Node.js 24 wieder her oder aktualisiert sie:

```sh
bash scripts/sync-skills.sh install  # Skills aus der Lock-Datei wiederherstellen, falls nötig
bash scripts/sync-skills.sh update   # Lock-verwaltete Skills aktualisieren
```

Das Skript fügt die Pulse-spezifischen Regeln nach dem Wiederherstellen oder Aktualisieren wieder zum Projekt-Skill `handoff` hinzu. Skills, die nicht in `skills-lock.json` stehen, etwa die Superpowers-Workflow-Skills (`obra/superpowers-skills`) und `diagnosing-superpowers`, stellt das Skript nicht wieder her; kopieren Sie sie bei Bedarf selbst nach `.agents/skills/`. Installieren Sie Projekt-Skills nicht global.

## Produktionsbuild und Bereitstellung

Für Produktion wird `compose.production.yml` verwendet. Es baut Release-Images und bindet keinen Quellcode ein. Erstellen Sie die Umgebungsdatei und ersetzen Sie alle `CHANGE_ME`-Werte:

```sh
cp .env.production.example .env.production
```

Legen Sie die initialen Administrator-Zugangsdaten fest. Verwenden Sie HTTPS über einen TLS-Reverse-Proxy, behalten Sie `SESSION_COOKIE_SECURE=true` bei und veröffentlichen Sie den internen HTTP-Port nicht in einem nicht vertrauenswürdigen Netzwerk. Standardmäßig ist der Webdienst nur an `127.0.0.1:8090` gebunden.

Konfiguration prüfen, Images bauen und Dienste starten:

```sh
docker compose --env-file .env.production -f compose.production.yml config -q
docker compose --env-file .env.production -f compose.production.yml build --pull
docker compose --env-file .env.production -f compose.production.yml up -d
docker compose --env-file .env.production -f compose.production.yml ps
```

Die API führt Migrationen beim Start aus. Entfernen Sie nach dem ersten erfolgreichen Anlegen des Administrators `INITIAL_ADMIN_EMAIL` und `INITIAL_ADMIN_PASSWORD` aus `.env.production` und erstellen Sie den API-Container neu:

```sh
docker compose --env-file .env.production -f compose.production.yml up -d --force-recreate api
```

Erstellen Sie vor Produktionsupdates eine Datenbanksicherung. Danach bauen und starten Sie die aktualisierten Images mit den obigen Befehlen. Alternativ gibt es das Skript:

```sh
./scripts/update-production.sh
```

Es benötigt einen sauberen Checkout auf `master`, erstellt mit `scripts/backup-production.sh` eine Datenbanksicherung (siehe [Produktionssicherungen](#produktionssicherungen); schlägt sie fehl, bricht das Update ab), aktualisiert per Fast-Forward von `origin/master`, prüft die Compose-Konfiguration, baut die Dienste neu und wartet auf Health Checks. Verwenden Sie den lokalen Datenbank-Compose nicht mit dem Produktionsdatenverzeichnis.

Die Artikelsuche verwendet die PostgreSQL-Erweiterung `pg_trgm`, die Migration `016_article_search.sql` anlegt. Sie ist in den offiziellen PostgreSQL-Images enthalten und eine vertrauenswürdige (trusted) Erweiterung, daher kann der Eigentümer der Datenbank sie ohne Superuser-Rechte anlegen; bei einer anderen PostgreSQL-Installation müssen die contrib-Erweiterungen installiert sein. Die Migration schreibt jeden gespeicherten Artikel einmal neu und baut einen Suchindex auf; im Test dauerte das für 500.000 Artikel etwa 3 Minuten. Die API nimmt erst danach Verbindungen an. Der Health-Check der Produktions-API erlaubt daher eine Startphase von 15 Minuten, und das Update-Skript wartet bis zu 900 Sekunden auf die Health-Checks (anpassbar mit `DEPLOY_WAIT_TIMEOUT=<Sekunden> scripts/update-production.sh`). Reicht auch das nicht, verfolgen Sie `docker compose --env-file .env.production -f compose.production.yml logs -f api`, bis die API meldet, dass sie lauscht.

## Daten, Konten und Aktualisierung

Der erste Administrator erhält bei der Migration die bisherige gemeinsame Bibliothek. Administratoren können Konten mit den Rollen `Administrator` oder `Reader` anlegen; eine öffentliche Registrierung gibt es nicht. Jeder Benutzer hat eigene Ordner, Abonnements, Lesestatus und gespeicherte Artikel. Normalisierte Feed-URLs und ihre Artikel werden geteilt, persönliche Lesestatus bleiben privat. Administratoren können keine Bibliotheken anderer Benutzer öffnen.

Jeder Benutzer kann bis zu 100 Feeds abonnieren. Die API prüft das Limit vor dem Abruf einer neuen Quelle und erneut beim Speichern des Abonnements, auch bei parallelen Anfragen.

Die gemeinsame Feed-TTL beträgt standardmäßig 15 Minuten (zulässig sind 5 bis 1.440 Minuten). Der Hintergrundplaner startet etwa 10 Sekunden nach dem API-Start und prüft danach jede Minute, welche Feeds fällig sind. Ein gemeinsamer Feed wird im kürzesten Intervall seiner Abonnenten aktualisiert (persönliches Intervall, sonst das globale Planerintervall aus der Administration), jedoch nie häufiger als die gemeinsame TTL; ein fehlerhafter Feed wird nach demselben Intervall erneut abgerufen. Feeds werden bedingt angefragt (`ETag`/`Last-Modified`), sodass unveränderte Feeds nicht erneut heruntergeladen werden. Die API aktualisiert eindeutige Feeds parallel über einen begrenzten Go-Workerpool. `RSS_REFRESH_CONCURRENCY` in `.env` akzeptiert Werte von 1 bis 32 (Standard: 4). Datenbank-Leases und TTL verhindern doppelte Anfragen während einer Aktualisierung und bis zum Ablauf der TTL. Die API hält höchstens `DB_MAX_CONNS` PostgreSQL-Verbindungen offen (Standard 12, zulässig 2–100; der Wert muss unter `max_connections` des Servers bleiben, standardmäßig 100); ein ungültiger Wert verhindert den Start der API.

Zulässig sind nur öffentliche HTTP/HTTPS-RSS- und Atom-URLs. Loopback-, private und lokale Netzwerkadressen werden blockiert. Pulse speichert Feed-Metadaten und Zusammenfassungen; der vollständige Artikel bleibt auf der Website der Quelle.

Sitzungen verwenden ein zufälliges HttpOnly-Cookie mit `SameSite=Strict` und laufen nach 14 Tagen ab. Passwörter werden als Argon2id-Hashes gespeichert. Außerhalb der lokalen Loopback-Entwicklung sind HTTPS und `SESSION_COOKIE_SECURE=true` erforderlich.

Anmeldungen sind begrenzt: Nach 10 fehlgeschlagenen Versuchen für ein Konto innerhalb von 15 Minuten oder 30 Versuchen von einer Client-Adresse innerhalb einer Minute antwortet die API mit `429`, bis das Zeitfenster wieder Versuche zulässt. Die API übernimmt die Client-Adresse aus `X-Real-IP` nur, wenn die Anfrage aus einem Netz in `TRUSTED_PROXY_CIDRS` kommt (kommagetrennte CIDRs; leer bedeutet Loopback, RFC 1918 und `fc00::/7`, also auch das Docker-Netz des mitgelieferten Nginx; `none` ignoriert den Header). Ein ungültiger Wert verhindert den Start der API. Jede Person kann höchstens 10 Feed-Anfragen gleichzeitig zur Freigabe offen haben.

Der mitgelieferte Nginx sendet eine Content Security Policy und weitere Browser-Sicherheitsheader. Er terminiert kein TLS und sendet deshalb kein `Strict-Transport-Security`; konfigurieren Sie HSTS am TLS-terminierenden Reverse Proxy (zum Beispiel `add_header Strict-Transport-Security "max-age=31536000" always;` in einem Nginx auf dem Host).

Pulse lässt sich als App installieren und hält zuletzt geladene Artikellisten offline lesbar. Dafür wird ein Service Worker verwendet, den Browser nur über HTTPS (oder auf `localhost`) zulassen. Gespeicherte Artikel und das gemerkte Konto werden beim Abmelden, bei abgelaufener Sitzung und bei der Anmeldung eines anderen Kontos vom Gerät gelöscht. Ein vorgeschalteter Reverse Proxy darf `/sw.js` und `/manifest.webmanifest` nicht länger cachen als der mitgelieferte Nginx (`no-cache`), sonst erreichen Updates installierte Apps verspätet. Nach einem Deployment bieten geöffnete Seiten eine Schaltfläche „Neu laden“ zum Wechsel auf die neue Version an. Details stehen unter „Installation and offline reading“ in `docs/PROJECT.md`.

### Datenaufbewahrung

Ein Wartungsjob läuft etwa eine Minute nach dem API-Start und danach alle 6 Stunden. Er löscht Artikel, deren Veröffentlichungs- und Erfassungszeitpunkt beide älter als `ARTICLE_RETENTION_DAYS` sind (Standard 180, zulässig 30–3.650), sofern sie niemand gespeichert hat; Lese- und Merkstatus dieser Artikel werden mitgelöscht. Eine Aktualisierung fügt datierte Einträge, die älter als dieser Zeitraum sind, nicht erneut ein, sofern sie nicht bereits gespeichert sind. Entschiedene (zugelassene/abgelehnte) Quarantäne-Einträge und nicht moderierbare Alt-Einträge werden gelöscht, sobald weder die Entscheidung noch ihr letztes Auftreten in einer Aktualisierung jünger als `QUARANTINE_RETENTION_DAYS` ist (Standard 90, zulässig 7–3.650); ausstehende Einträge bleiben erhalten. Abgelaufene Sitzungen entfernt derselbe Job. Der Wert `0` deaktiviert die jeweilige Bereinigung; ein ungültiger Wert verhindert den Start der API.

## Sicherung, Logs und Stoppen

### Lokaler Datenbankdump

Erstellen Sie vor einem Umzug oder Upgrade einen Dump der lokalen Entwicklungsdatenbank:

```sh
docker compose -f compose.database.yml exec -T db pg_dump -U pulse pulse > pulse-backup.sql
```

Falls `POSTGRES_USER` oder `POSTGRES_DB` in `.env` abweichen, verwenden Sie diese Werte.

### Produktionssicherungen

`scripts/backup-production.sh` schreibt ein komprimiertes `pg_dump`-Archiv (Custom-Format) der Produktionsdatenbank nach `backups/production/pulse-<UTC-Zeitstempel>.dump`. Das Verzeichnis erhält den Modus 700, jede Sicherung den Modus 600. Der Dump wird zunächst in eine temporäre Datei geschrieben und erst umbenannt, wenn er nicht leer ist und `pg_restore --list` ihn lesen kann. Danach löscht das Skript Sicherungen, die älter als `BACKUP_KEEP_DAYS` Tage sind (Standard 14), einschließlich reiner `.sql`-Dumps älterer Versionen des Update-Skripts, aber nie die neueste Sicherung. Der Dienst `db` muss laufen. Fortschrittsmeldungen gehen nach stderr, der Pfad der neuen Sicherung nach stdout.

```sh
./scripts/backup-production.sh
BACKUP_KEEP_DAYS=30 ./scripts/backup-production.sh
```

Die Sicherungen liegen auf demselben Host wie die Datenbank. Kopieren Sie `backups/production/` regelmäßig auf einen anderen Rechner oder Speicher.

Für eine tägliche Sicherung per cron tragen Sie eine Zeile wie diese in die crontab eines Benutzers ein, der `docker` ausführen darf (ersetzen Sie `/srv/pulse` durch den Pfad des Checkouts):

```cron
17 3 * * * cd /srv/pulse && ./scripts/backup-production.sh >> /var/log/pulse-backup.log 2>&1
```

Oder verwenden Sie einen systemd-Timer. `/etc/systemd/system/pulse-backup.service`:

```ini
[Unit]
Description=Pulse production database backup
Requires=docker.service
After=docker.service

[Service]
Type=oneshot
WorkingDirectory=/srv/pulse
Environment=BACKUP_KEEP_DAYS=14
ExecStart=/srv/pulse/scripts/backup-production.sh
```

`/etc/systemd/system/pulse-backup.timer`:

```ini
[Unit]
Description=Daily Pulse production database backup

[Timer]
OnCalendar=*-*-* 03:17:00
RandomizedDelaySec=15min
Persistent=true

[Install]
WantedBy=timers.target
```

Aktivieren Sie ihn mit `sudo systemctl daemon-reload && sudo systemctl enable --now pulse-backup.timer` und prüfen Sie ihn mit `systemctl list-timers pulse-backup.timer` und `journalctl -u pulse-backup.service`. Der Dienst läuft als root, sofern Sie nicht `User=` mit einem Konto der Gruppe `docker` ergänzen.

### Produktion wiederherstellen

```sh
./scripts/restore-production.sh --yes backups/production/pulse-20261101T031700Z.dump
```

Ohne `--yes` erklärt das Skript nur, was es ersetzen würde. Mit `--yes` prüft es, ob das Archiv lesbar ist, sichert die aktuelle Datenbank mit `backup-production.sh` (`--skip-safety-backup` überspringt das, wenn sich die aktuelle Datenbank nicht sichern lässt), stoppt `api` und `web`, löscht die Datenbank und legt sie neu an, stellt das Archiv mit `pg_restore` wieder her, startet alle Dienste und wartet bis zu `DEPLOY_WAIT_TIMEOUT` Sekunden (Standard 900) auf ihre Health Checks. Alles, was nach der Sicherung geschrieben wurde, geht verloren, auch neuere Sitzungen; Benutzer müssen sich eventuell erneut anmelden.

Reine SQL-Dumps (`*.sql` älterer Versionen des Update-Skripts) stellt das Skript nicht wieder her. Stoppen Sie `api` und `web`, legen Sie die Datenbank im Container `db` neu an (`dropdb --force` und `createdb` als `POSTGRES_USER`), laden Sie die Datei mit `psql -v ON_ERROR_STOP=1 -U <benutzer> -d <datenbank> < <datei>.sql` über `docker compose ... exec -T db` und starten Sie die Dienste wieder.

**Ein Upgrade zurücknehmen.** Migrationen laufen nur vorwärts; es gibt keine Down-Skripte. Um zur vorherigen Version zurückzukehren, bauen Sie zuerst die Images des vorherigen Commits und stellen danach die Sicherung wieder her, die `update-production.sh` vor dem Upgrade angelegt hat. So startet die alte API nie gegen das neuere Schema:

```sh
git checkout <vorheriger-commit>
docker compose --env-file .env.production -f compose.production.yml build
./scripts/restore-production.sh --yes backups/production/<sicherung-des-updates>.dump
```

Wechseln Sie vor dem nächsten Lauf von `update-production.sh` zurück auf `master` (`git checkout master`).

### Wiederherstellungsprobe

Testen Sie die Wiederherstellung regelmäßig, etwa alle drei Monate und nach Änderungen an der Sicherung, an einer Wegwerfkopie und nie am Produktionsprojekt. Die Probe vom 2026-11-01 lief so ab:

1. `compose.production.yml` in ein leeres Verzeichnis kopieren und `name:` auf einen unbenutzten Projektnamen ändern (`pulse-s9-prod`). `scripts/backup-production.sh` und `scripts/restore-production.sh` in ein Unterverzeichnis `scripts/` kopieren, `backend` und `frontend` als symbolische Links auf den Checkout anlegen und eine `.env.production` mit Testpasswort, Test-Administrator und unbenutztem `WEB_PORT` erstellen. Das relative Volume `./data/postgres-production` zeigt dann in die Kopie; das echte Datenverzeichnis bleibt unberührt.
2. `docker compose --env-file .env.production -f compose.production.yml up -d --build --wait`, über den Web-Port anmelden und einen Ordner „Before backup“ anlegen.
3. `scripts/backup-production.sh` erzeugte ein 42-KB-Archiv mit Modus 600 in einem Verzeichnis mit Modus 700.
4. Einen Ordner „After backup“ anlegen. `scripts/restore-production.sh <archiv>` ohne `--yes` verweigerte die Ausführung; mit `--yes` legte es eine Sicherheitssicherung an, stoppte `api` und `web`, legte die Datenbank neu an, stellte sie wieder her, und alle drei Dienste wurden wieder healthy.
5. Nach erneuter Anmeldung enthielt die Ordnerliste „Before backup“, aber nicht „After backup“: Die Datenbank war auf den Stand der Sicherung zurückgesetzt. Auch die Rotation wurde geprüft: Mit `BACKUP_KEEP_DAYS=14` wurden Sicherungen mit 20 Tage alter Änderungszeit gelöscht, die neueste Sicherung blieb auch mit alter Änderungszeit erhalten.
6. Aufräumen mit `docker compose --env-file .env.production -f compose.production.yml down -v --rmi local` und die Kopie löschen.

### Logs und Health Checks

Die API schreibt strukturierte Logs (Go `log/slog`) nach stderr. `LOG_FORMAT` wählt `text` (Standard, in den lokalen Compose-Dateien) oder `json` (Standard in `compose.production.yml`); `LOG_LEVEL` ist `debug`, `info` (Standard), `warn` oder `error`. Ein ungültiger Wert stoppt die API beim Start. Jede Anfrage erzeugt einen Eintrag `http request` mit `method`, `path` (nie die Query, die Suchbegriffe enthalten kann), `status`, `bytes`, `duration_ms`, `request_id` und bei angemeldeten Benutzern `user_id`; Health Checks werden nur auf `debug` protokolliert. Jede Antwort trägt einen Header `X-Request-ID` mit derselben ID: Ein wohlgeformter eingehender `X-Request-ID` (bis zu 64 Zeichen aus Buchstaben, Ziffern, `.`, `_`, `:` und `-`) wird übernommen, alles andere durch eine erzeugte ID ersetzt. Geben Sie sie bei Fehlermeldungen an. Der Aktualisierungsplaner schreibt pro Zyklus oder manueller Aktualisierung einen Eintrag `rss refresh cycle finished` mit der Zahl der Quellen (`targets`) und wie viele davon `refreshed`, `not_modified`, `failed` oder `skipped` waren; Zyklen ohne fällige Quellen erscheinen nur auf `debug`.

Logs verfolgen Sie mit `docker compose -f <compose-datei> logs -f api`. Bei JSON-Logs zeigt etwa `docker compose --env-file .env.production -f compose.production.yml logs --no-log-prefix api | jq 'select(.status >= 500)'` fehlgeschlagene Anfragen.

`GET /api/health/live` meldet, dass der API-Prozess läuft, ohne die Datenbank anzusprechen. `GET /api/health/ready` prüft zusätzlich die Datenbank (2 Sekunden Timeout) und antwortet `503`, wenn sie nicht erreichbar ist; die Compose-Health-Checks verwenden diesen Endpunkt. `GET /api/health` bleibt ein Alias der Bereitschaftsprüfung. Prometheus-Metriken exportiert die API noch nicht.

### Stoppen

Lokale Dienste stoppen:

```sh
docker compose -f compose.frontend.yml down
docker compose -f compose.backend.yml down
docker compose -f compose.database.yml down
```

PostgreSQL-Daten bleiben in `data/postgres/`. Löschen Sie den Ordner nicht, wenn Konten und Abonnements erhalten bleiben sollen.

## Projektstruktur

- `frontend/` — React/TypeScript-App, Nginx-Konfiguration und Container.
- `backend/` — Go-API, RSS/Atom-Parser und PostgreSQL-Migrationen.
- `frontend/test/` und `backend/test/` — Tests nach Komponentenstruktur.
- `data/postgres/` — lokal eingebundene PostgreSQL-Daten.
- `compose.frontend.yml`, `compose.backend.yml`, `compose.database.yml` — unabhängige lokale Dienstgruppen.
- `compose.production.yml` — Produktionsbuild und Bereitstellung.
- `scripts/` — Skripte für Produktionsupdate, Sicherung und Wiederherstellung sowie zum Abgleich der Agent-Skills.
- `backend/openapi/openapi.json` — OpenAPI-3.1-Beschreibung der HTTP-API.
