# UniFi HQ auf Unraid

Der Fork ergänzt [ProfessorQuantumUniverse/unifi-HQ](https://github.com/ProfessorQuantumUniverse/unifi-HQ) um Controller-Inventar und Netzwerkbaum. Er bleibt ein Stack mit vier dauerhaften Containern und einem GeoIP-Einmaljob. Home Assistant und ein Compose-Plugin sind nicht erforderlich. Es werden weder Unraid selbst noch bestehende Anwendungen aktualisiert.

## Release und Images

Aktuelles Fork-Release: `0.1.1`. Die drei eigenen Images heißen `ghcr.io/mephistojb/unifi-hq-api:0.1.1`, `ghcr.io/mephistojb/unifi-hq-collector:0.1.1` und `ghcr.io/mephistojb/unifi-hq-web:0.1.1`. Diese Namen allein bestätigen noch keine Veröffentlichung; vor der Installation werden lokal gebaute oder tatsächlich aus der Registry abgerufene Images geprüft.

Build auf dem x86-64-Zielhost mit vorhandenem Docker:

```bash
docker build -t ghcr.io/mephistojb/unifi-hq-api:0.1.1 ./api
docker build -t ghcr.io/mephistojb/unifi-hq-collector:0.1.1 ./collector
docker build -f caddy/Dockerfile -t ghcr.io/mephistojb/unifi-hq-web:0.1.1 .
docker pull victoriametrics/victoria-logs:v1.53.0
docker pull alpine:3.24
```

Das Webimage enthält die Oberfläche und Caddy-Konfiguration. Das Collectorimage enthält seine Pipeline. Keine eingebauten Quelldateien laufender Anwendungen werden durch Bind-Mounts ersetzt. GoFlow2 und Vector werden aus den im Collector-Dockerfile fixierten Upstream-Tags übernommen; Build- und Laufzeittests erfolgen auf der tatsächlichen Zielarchitektur.

## Daten und Netzwerk

Vorgeschlagenes Appdata-Verzeichnis: `/mnt/user/appdata/unifi-hq`. Darunter liegen `config/hosts.csv`, `state/inventory.json`, `geoip/` und `history/`. Das eigene Quellrepository kann separat unter `source/` liegen. Nur die Runtime-Dateien sind schreibbar; die Container-Dateisysteme bleiben read-only.

| Port | Bindung | Zweck |
|---|---|---|
| TCP 8080 | Host | Weboberfläche; Caddy beschränkt zugelassene lokale Clients |
| UDP 2055 | Host | IPFIX/NetFlow; `ALLOWED_SENDERS` auf bestätigte Gateway-Adresse setzen |
| UDP 5514 | Host | Optionales UniFi-Syslog |
| TCP 8090 | 127.0.0.1 | Live-Stream |
| TCP 8091 | 127.0.0.1 | GoFlow2-Metriken |
| TCP 8092 | 127.0.0.1 | Login-, Namen- und Inventar-API |
| TCP 9428 | 127.0.0.1 | Verlaufsspeicher |

Host-Netzwerk erhält die IPFIX-Absenderadresse. Vor Installation und vor Änderungen Ports prüfen. Abweichende Ports sind über `HTTP_PORT`, `IPFIX_PORT`, `SYSLOG_PORT`, `LIVE_PORT`, `METRICS_PORT`, `API_PORT`, `HISTORY_PORT` konfigurierbar und müssen in allen betroffenen Templates konsistent sein. Der Standard `ALLOWED_SENDERS=127.0.0.1` nimmt zunächst keine externen Gateway-Daten an; kein fehlender Traffic wird als erfolgreicher Empfang ausgegeben.

Arbeitsspeicherlimits: Verlauf 512 MiB, Collector 384 MiB, API 128 MiB, Web 64 MiB. Der GeoIP-Job erhält vorübergehend 128 MiB. Verlauf standardmäßig 30 Tage bzw. 20 GiB, je nachdem welche Grenze zuerst erreicht wird. Dies sind anfängliche Limits, keine gemessenen Lastwerte.

## Geschützte Runtime-Secrets

Das Secret-Verzeichnis wird außerhalb des Repositories auf dem Zielhost bereitgestellt. `HQ_SECRETS_DIR` zeigt darauf; der native Installer verwendet standardmäßig `/run/unifi-hq-secrets`. Verzeichnismodus 700, Dateien 600 oder 400, Eigentümer root. Die Dateien heißen:

- `auth_password_hash`: mit dem API-Befehl `hash-password` erzeugter Passwort-Hash;
- `auth_secret`: zufälliger Cookie-Signierschlüssel;
- `unifi_api_key`: API-Key der lokalen UniFi-Integration.

Secret-Werte gehören weder in XML-Templates noch in `.env`, Shellargumente, Logs oder Git. Der Installer liest nur Existenz und Rechte, keine Inhalte. Secrets können aus der bestehenden Secret-Verwaltung in ein Runtime-tmpfs bereitgestellt werden. Ein tmpfs geht beim Hostneustart verloren: vor Autostart muss die freigegebene Secret-Bereitstellung erneut erfolgt sein. Ohne lesbare konfigurierte Secret-Dateien startet die API nicht mit offenem Login. Eine zusätzliche Secret-Verwaltungsanwendung wird nicht installiert.

`UNIFI_URL` muss HTTPS verwenden und zum Zertifikat passen. Die API prüft TLS. Eine eigene vertrauenswürdige CA kann als Datei im Secret-Verzeichnis bereitgestellt und über `UNIFI_CA_FILE=/run/secrets/unifi-ca.pem` eingebunden werden. Kein pauschales Abschalten der Zertifikatsprüfung. `UNIFI_SITE_ID` ist die offizielle Site-UUID, nicht der Classic-API-Name `default`; ohne explizite ID ist automatische Auswahl nur bei genau einer Site zulässig.

### Start mit noch nicht eingerichteter Controller-Anbindung

Der Standard verlangt einen Controller-Key und eine HTTPS-URL. Für eine ausdrücklich gewünschte Vorabinstallation der echten Oberfläche ohne diese Anbindung ist `HQ_ALLOW_UNCONFIGURED=1` vorgesehen. Das ist ausschließlich zulässig, wenn `UNIFI_URL` leer ist und im Runtime-Secret-Verzeichnis **keine** Datei `unifi_api_key` existiert. Die Login-Dateien `auth_password_hash` und `auth_secret` bleiben zwingend erforderlich und geschützt. Die Anwendung zeigt den Zustand „nicht konfiguriert“; sie behauptet keine Verbindung und verwendet weder einen Platzhalter-Key noch simulierte Live-Geräte.

Für diesen Modus beim lokalen Erzeugen der Templates **und** beim Ausführen des Installers dieselbe Variable setzen, zum Beispiel `HQ_ALLOW_UNCONFIGURED=1 python3 scripts/generate-unraid-templates.py` und `HQ_ALLOW_UNCONFIGURED=1 bash scripts/unraid.sh install --apply`. Der API-Container und sein Template erhalten einen leeren Wert für `UNIFI_API_KEY_FILE`. Die allgemeingültigen eingecheckten Templates bleiben standardmäßig für den konfigurierten Betrieb vorgesehen.

Sobald die Anbindung bereitsteht, die echte geschützte Key-Datei bereitstellen, `UNIFI_URL` und gegebenenfalls `UNIFI_SITE_ID` setzen, Templates erneut erzeugen und mit `HQ_ALLOW_UNCONFIGURED=0` aktualisieren. Erst die tatsächliche erfolgreiche Controller-Abfrage begründet den verbundenen Zustand.

## Native Unraid-Installation

`install.sh` und die Proxmox-Skripte des Originals werden auf Unraid nicht ausgeführt. `scripts/unraid.sh` benötigt Bash, das vorhandene Docker und übliche Host-Werkzeuge, aber kein Python und kein Compose-Plugin.

1. Zielzustand und ausreichend Platz prüfen, Konflikte ausschließen. Gegebenenfalls bestehendes Appdata vor Änderungen sichern. Nur eigene HQ-Dateien/Container betreffen.
2. Fixiertes Release auf den Zielhost übertragen, Images bauen oder geprüft abrufen, Runtime-Secrets bereitstellen. Konfigurationswerte ohne Secrets als Umgebung setzen.
3. Vorschau ansehen: `bash scripts/unraid.sh install`. Diese zeigt die genauen Aktionen, führt sie aber nicht aus.
4. Mit denselben Konfigurationswerten ausführen: `bash scripts/unraid.sh install --apply`. Bestehende Container mit den vorgesehenen Namen werden nicht überschrieben.
5. Der Installer kopiert die fünf nativen XML-Templates aus `unraid/` nach `/boot/config/plugins/dockerMan/templates-user/my-unifi-hq-*.xml`. Die Templates müssen vor der Übertragung mit den tatsächlich gewählten nicht geheimen Konfigurationswerten erzeugt sein. Abweichende vorhandene gleichnamige Dateien blockieren eine Erstinstallation; bei explizitem Update werden ausschließlich zugehörige HQ-Templates unter Appdata gesichert und ersetzt. Die Templates können dann über Docker → Add Container gewählt werden. Die laufenden Container tragen DockerMan-Labels; `unifi-hq-web` hat die WebUI-Verknüpfung.
6. Autostart ausschließlich für `unifi-hq-history`, `unifi-hq-collector`, `unifi-hq-api`, `unifi-hq-web` in dieser Reihenfolge einrichten, nachdem Runtime-Secrets beim Boot sichergestellt sind. Den GeoIP-Einmaljob nicht in Autostart aufnehmen. Andere Container-/Autostart-Einstellungen bleiben unverändert.
7. Ports und Containerzustände prüfen. Login, `/api/inventory`, unbekannt/veraltet bei Controller-Ausfall, Live-WebSocket und Verlauf getrennt prüfen. Erst danach Gateway-Export auf die bestätigten Ports einrichten und echten Eingang nachweisen.

Die XML-Dateien werden auf dem Entwicklungsrechner mit `python3 scripts/generate-unraid-templates.py` aus den gleichen Umgebungswerten erzeugt. Python ist dafür kein Erfordernis auf Unraid. `HQ_SOURCE_DIR`, `HQ_DATA_DIR`, `HQ_SECRETS_DIR`, `HQ_IMAGE_PREFIX`, `HQ_VERSION` und die übrigen nicht geheimen Variablen können beim Generieren gesetzt werden.

Compose ist eine alternative Verpackung desselben Stacks für Hosts mit bereits vorhandenem Compose. Vor dem ersten Start das Runtime-Verzeichnis anlegen, `config/hosts.csv` mit `ip,name` initialisieren und Secrets bereitstellen. `docker compose up -d --build` führt den GeoIP-Job vor dem Collector aus. Der native Installer erledigt die gleiche Reihenfolge ohne Compose.

## Updates und Rückweg

Vor einem Update das vorherige Release, Image-IDs, passende Templates und Konfiguration festhalten; Daten aufbewahren. API und Collector teilen sich für die bereits im Original vorhandene Namensaktualisierung einen PID-Namespace. Nach einer **Neuerstellung** des Collectors muss die API ebenfalls **neu erstellt** werden; ein bloßer API-Neustart kann auf den alten Namespace zeigen.

`bash scripts/unraid.sh update` zeigt den Ablauf, `update --apply` stoppt und entfernt ausschließlich Container mit dem eigenen Label `io.unifi-hq.stack=unifi-hq`, in umgekehrter Abhängigkeitsreihenfolge. Die Appdata-Verzeichnisse werden nicht gelöscht. Die bereits lokal verfügbaren Images und die Runtime-Secrets müssen vorab geprüft sein. Anschließend werden GeoIP und alle vier Container in korrekter Reihenfolge angelegt. Unraid-Einzelupdates für Collector/API immer gemeinsam mit diesem Ablauf durchführen.

Rollback: altes `HQ_VERSION` und die passende Konfiguration verwenden und denselben Update-Ablauf ausführen. Bei inkompatibler Datenänderung den zuvor gesicherten eigenen Zustand wiederherstellen. Bei Problemen mit Gateway-Export dessen vorherige Konfiguration wiederherstellen. `bash scripts/unraid.sh stop --apply` stoppt ausschließlich eigene Container und erhält alle Daten. Eine automatische Deinstallation, globale Docker-Bereinigung und Änderungen fremder Container sind nicht Bestandteil der Skripte.

GeoIP manuell auffrischen: `bash scripts/unraid.sh geoip --apply`. Der Einmaljob nutzt nur seine eingebundene Datenablage. Im ersten Release wird kein zusätzlicher Scheduler installiert.

## Release und Empfangstest

Release `0.1.1` liegt als drei getrennte Images unter `ghcr.io/mephistojb/unifi-hq-{collector,api,web}:0.1.1`. Die GHCR-Pakete sind zunächst privat und dem Fork zugeordnet. Ein erneuter Abruf benötigt deshalb die Registry-Berechtigung des Eigentümers. Beim Deployment werden Anmeldedaten nur über Standard-Eingabe an Docker übergeben und dessen temporäres `DOCKER_CONFIG` im RAM anschließend entfernt. Auf dem Host wird keine dauerhafte Registry-Anmeldung eingerichtet. Alternativ lassen sich die Images aus dem entsprechenden Git-Tag lokal bauen. Unraid erhält in diesem Release keine automatische Update-Automation.

`bash scripts/smoke-unraid.sh` sendet ausschließlich Loopback-Testdatagramme an IPFIX und Syslog und prüft ihre Ankunft im Verlaufsspeicher. Es entsteht kein Verkehr zu den IP-Adressen in den Test-Nutzdaten. Die synthetischen Einträge bleiben in der Historie; sie sind kein Nachweis für einen aktivierten Gateway-Export. Für den produktiven Eingang müssen weiterhin Gateway-Ziel, Absenderbeschränkung und tatsächliche Daten geprüft werden.
