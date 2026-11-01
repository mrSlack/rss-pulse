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

Playwright startet einen lokalen Vite-Server und verwendet deterministische API-Fixtures. Screenshot-Baselines liegen unter `frontend/test/e2e/`. Nach einer beabsichtigten visuellen Änderung prüfen und aktualisieren:

```sh
docker compose -f compose.frontend.yml exec frontend npm run test:e2e:update
```

Alle Compose-Dateien verwenden das externe Netzwerk `rss-reader-net`, damit die Gruppen unabhängig gestartet und gestoppt werden können. PostgreSQL muss vor der API laufen; die API wartet beim Start auf die Datenbank.

## Sprachen

Die Oberfläche ist auf Englisch, Deutsch und Russisch verfügbar. Beim ersten Besuch wird Englisch verwendet. Wählen Sie die Sprache über die Auswahl in der Werkzeugleiste oder auf der Anmeldeseite; die Auswahl wird im lokalen Browserspeicher abgelegt. Die JSON-Übersetzungen liegen in `frontend/src/locales/`.

Für eine weitere Sprache erstellen Sie eine JSON-Datei auf Basis der Schlüssel in `en.json`, ergänzen den Sprachcode in `supportedLanguages` und `resources` in `frontend/src/i18n.ts` und fügen den nativen Namen in `frontend/src/LanguageSelector.tsx` hinzu. Englisch ist Quellsprache und Fallback.

## Agent-Skills

Projektspezifische Skills liegen in `.agents/skills/` und sind Teil des Repositorys. Öffnen Sie nach dem Klonen das Projektverzeichnis in Codex; eine globale Installation ist nicht erforderlich. `skills-lock.json` erfasst Skills mit Upstream-Quelle. Das Projektskript verwendet Node.js 24 zum Wiederherstellen oder Aktualisieren:

```sh
bash scripts/sync-skills.sh install  # Skills aus der Lock-Datei wiederherstellen, falls nötig
bash scripts/sync-skills.sh update   # Lock-verwaltete Skills aktualisieren
```

Das Skript fügt die Pulse-spezifischen Regeln nach dem Wiederherstellen oder Aktualisieren wieder zum Projekt-Skill `handoff` hinzu. Die Superpowers-Workflow-Skills stammen aus der aktuellen Quelle `obra/superpowers-skills`. `diagnosing-superpowers` bleibt erhalten, da es dort nicht mehr veröffentlicht wird; der ältere Name `using-superpowers` bleibt neben seinem Nachfolger `using-skills` bestehen. Installieren Sie Projekt-Skills nicht global.

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

Es benötigt einen sauberen Checkout auf `master`, speichert einen Datenbankdump unter `backups/production/`, aktualisiert per Fast-Forward von `origin/master`, prüft die Compose-Konfiguration, baut die Dienste neu und wartet auf Health Checks. Verwenden Sie den lokalen Datenbank-Compose nicht mit dem Produktionsdatenverzeichnis.

Die Artikelsuche verwendet die PostgreSQL-Erweiterung `pg_trgm`, die Migration `016_article_search.sql` anlegt. Sie ist in den offiziellen PostgreSQL-Images enthalten und eine vertrauenswürdige (trusted) Erweiterung, daher kann der Eigentümer der Datenbank sie ohne Superuser-Rechte anlegen; bei einer anderen PostgreSQL-Installation müssen die contrib-Erweiterungen installiert sein. Die Migration schreibt jeden gespeicherten Artikel einmal neu und baut einen Suchindex auf; im Test dauerte das für 500.000 Artikel etwa 3 Minuten. Die API nimmt erst danach Verbindungen an. Der Health-Check der Produktions-API erlaubt daher eine Startphase von 15 Minuten, und das Update-Skript wartet bis zu 900 Sekunden auf die Health-Checks (anpassbar mit `DEPLOY_WAIT_TIMEOUT=<Sekunden> scripts/update-production.sh`). Reicht auch das nicht, verfolgen Sie `docker compose --env-file .env.production -f compose.production.yml logs -f api`, bis die API meldet, dass sie lauscht.

## Daten, Konten und Aktualisierung

Der erste Administrator erhält bei der Migration die bisherige gemeinsame Bibliothek. Administratoren können Konten mit den Rollen `Administrator` oder `Reader` anlegen; eine öffentliche Registrierung gibt es nicht. Jeder Benutzer hat eigene Ordner, Abonnements, Lesestatus und gespeicherte Artikel. Normalisierte Feed-URLs und ihre Artikel werden geteilt, persönliche Lesestatus bleiben privat. Administratoren können keine Bibliotheken anderer Benutzer öffnen.

Jeder Benutzer kann bis zu 100 Feeds abonnieren. Die API prüft das Limit vor dem Abruf einer neuen Quelle und erneut beim Speichern des Abonnements, auch bei parallelen Anfragen.

Die gemeinsame Feed-TTL beträgt standardmäßig 15 Minuten (zulässig sind 5 bis 1.440 Minuten). Der Hintergrundplaner startet etwa 10 Sekunden nach dem API-Start und prüft danach jede Minute, welche Feeds fällig sind. Ein gemeinsamer Feed wird im kürzesten Intervall seiner Abonnenten aktualisiert (persönliches Intervall, sonst das globale Planerintervall aus der Administration), jedoch nie häufiger als die gemeinsame TTL; ein fehlerhafter Feed wird nach demselben Intervall erneut abgerufen. Feeds werden bedingt angefragt (`ETag`/`Last-Modified`), sodass unveränderte Feeds nicht erneut heruntergeladen werden. Die API aktualisiert eindeutige Feeds parallel über einen begrenzten Go-Workerpool. `RSS_REFRESH_CONCURRENCY` in `.env` akzeptiert Werte von 1 bis 32 (Standard: 4). Datenbank-Leases und TTL verhindern doppelte Anfragen während einer Aktualisierung und bis zum Ablauf der TTL. Die API hält höchstens `DB_MAX_CONNS` PostgreSQL-Verbindungen offen (Standard 12, zulässig 2–100; der Wert muss unter `max_connections` des Servers bleiben, standardmäßig 100); ein ungültiger Wert verhindert den Start der API.

Zulässig sind nur öffentliche HTTP/HTTPS-RSS- und Atom-URLs. Loopback-, private und lokale Netzwerkadressen werden blockiert. Pulse speichert Feed-Metadaten und Zusammenfassungen; der vollständige Artikel bleibt auf der Website der Quelle.

Sitzungen verwenden ein zufälliges HttpOnly-Cookie mit `SameSite=Strict` und laufen nach 14 Tagen ab. Passwörter werden als Argon2id-Hashes gespeichert. Außerhalb der lokalen Loopback-Entwicklung sind HTTPS und `SESSION_COOKIE_SECURE=true` erforderlich.

Anmeldungen sind begrenzt: Nach 10 fehlgeschlagenen Versuchen für ein Konto innerhalb von 15 Minuten oder 30 Versuchen von einer Client-Adresse innerhalb einer Minute antwortet die API mit `429`, bis das Zeitfenster wieder Versuche zulässt. Die API übernimmt die Client-Adresse aus `X-Real-IP` nur, wenn die Anfrage aus einem Netz in `TRUSTED_PROXY_CIDRS` kommt (kommagetrennte CIDRs; leer bedeutet Loopback, RFC 1918 und `fc00::/7`, also auch das Docker-Netz des mitgelieferten Nginx; `none` ignoriert den Header). Ein ungültiger Wert verhindert den Start der API. Jede Person kann höchstens 10 Feed-Anfragen gleichzeitig zur Freigabe offen haben.

Der mitgelieferte Nginx sendet eine Content Security Policy und weitere Browser-Sicherheitsheader. Er terminiert kein TLS und sendet deshalb kein `Strict-Transport-Security`; konfigurieren Sie HSTS am TLS-terminierenden Reverse Proxy (zum Beispiel `add_header Strict-Transport-Security "max-age=31536000" always;` in einem Nginx auf dem Host).

### Datenaufbewahrung

Ein Wartungsjob läuft etwa eine Minute nach dem API-Start und danach alle 6 Stunden. Er löscht Artikel, deren Veröffentlichungs- und Erfassungszeitpunkt beide älter als `ARTICLE_RETENTION_DAYS` sind (Standard 180, zulässig 30–3.650), sofern sie niemand gespeichert hat; Lese- und Merkstatus dieser Artikel werden mitgelöscht. Eine Aktualisierung fügt datierte Einträge, die älter als dieser Zeitraum sind, nicht erneut ein, sofern sie nicht bereits gespeichert sind. Entschiedene (zugelassene/abgelehnte) Quarantäne-Einträge und nicht moderierbare Alt-Einträge werden gelöscht, sobald weder die Entscheidung noch ihr letztes Auftreten in einer Aktualisierung jünger als `QUARANTINE_RETENTION_DAYS` ist (Standard 90, zulässig 7–3.650); ausstehende Einträge bleiben erhalten. Abgelaufene Sitzungen entfernt derselbe Job. Der Wert `0` deaktiviert die jeweilige Bereinigung; ein ungültiger Wert verhindert den Start der API.

## Sicherung, Logs und Stoppen

Erstellen Sie vor einem Umzug oder Upgrade einen Datenbankdump:

```sh
docker compose -f compose.database.yml exec -T db pg_dump -U pulse pulse > pulse-backup.sql
```

Falls `POSTGRES_USER` oder `POSTGRES_DB` in `.env` abweichen, verwenden Sie diese Werte. Logs zeigen Sie mit `docker compose -f <compose-datei> logs -f` an. Lokale Dienste stoppen:

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
