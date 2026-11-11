#!/usr/bin/env python3
"""Generate native DockerMan templates using the same environment as unraid.sh.

Development/packaging helper, Python is not required on the Unraid host.
Never reads secret values. Runtime secret paths are the only secret configuration.
"""
from pathlib import Path
import os
import shlex
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[1]
OUT = Path(os.getenv('HQ_TEMPLATE_OUTPUT_DIR', str(ROOT / 'unraid')))
VERSION = os.getenv('HQ_VERSION', '0.1.1')
PREFIX = os.getenv('HQ_IMAGE_PREFIX', 'ghcr.io/mephistojb/unifi-hq')
DATA = os.getenv('HQ_DATA_DIR', '/mnt/user/appdata/unifi-hq')
SECRETS = os.getenv('HQ_SECRETS_DIR', '/run/unifi-hq-secrets')
KEY_FILE = '' if os.getenv('HQ_ALLOW_UNCONFIGURED', '0') == '1' and not os.getenv('UNIFI_URL', '') else '/run/secrets/unifi_api_key'
SOURCE = os.getenv('HQ_SOURCE_DIR', '/mnt/user/appdata/unifi-hq/source')
PORTS = {k: os.getenv(k, v) for k, v in dict(HTTP_PORT='8080', IPFIX_PORT='2055', SYSLOG_PORT='5514', LIVE_PORT='8090', METRICS_PORT='8091', API_PORT='8092', HISTORY_PORT='9428').items()}
REPO = 'https://github.com/MephistoJB/unifi-HQ'
ICON = 'https://raw.githubusercontent.com/MephistoJB/unifi-HQ/main/web/favicon.svg'
BASE = ['--read-only', '--cap-drop=ALL', '--security-opt=no-new-privileges:true', '--restart=unless-stopped', '--log-driver=local', '--log-opt=max-size=5m', '--log-opt=max-file=2', '--label=io.unifi-hq.stack=unifi-hq']

def config(parent, label, target, default, kind='Variable', mode='', description='', required=True):
    attrs = dict(Name=label, Target=target, Default=default, Mode=mode, Description=description, Type=kind, Display='always', Required=str(required).lower(), Mask='false')
    ET.SubElement(parent, 'Config', attrs).text = default

def template(name, image, description, mem, env=(), paths=(), flags=(), args=(), web=False, oneshot=False):
    root = ET.Element('Container', version='2')
    extra = [f for f in BASE if not (oneshot and f.startswith('--restart='))] + [f'--memory={mem}'] + list(flags)
    fields = {'Name':name, 'Repository':image, 'Registry':REPO, 'Network':'host', 'Privileged':'false', 'Support':REPO+'/issues', 'Project':REPO, 'Overview':description, 'Category':'Network:Management:', 'WebUI':f"http://[IP]:{PORTS['HTTP_PORT']}/" if web else '', 'Icon':ICON, 'ExtraParams':shlex.join(extra), 'PostArgs':shlex.join(args), 'TemplateURL':f'https://raw.githubusercontent.com/MephistoJB/unifi-HQ/main/unraid/{name}.xml'}
    for key,value in fields.items(): ET.SubElement(root,key).text=value
    for label,host,target,mode in paths: config(root,label,target,host,'Path',mode)
    for key,default in env: config(root,key,key,str(default),description='No secret values; runtime configuration only.',required=bool(default))
    ET.indent(root,space='  ')
    OUT.mkdir(parents=True,exist_ok=True)
    path=OUT/(name+'.xml')
    ET.ElementTree(root).write(path,encoding='utf-8',xml_declaration=True)
    print(path.relative_to(ROOT) if path.is_relative_to(ROOT) else path)

template('unifi-hq-history','victoriametrics/victoria-logs:v1.53.0','UniFi HQ Verlaufsspeicher. Nur localhost. Zuerst starten; Daten bleiben bei Neuinstallation erhalten.', '512m', paths=[('Verlauf',DATA+'/history','/vlogs','rw')], args=[f"-httpListenAddr=127.0.0.1:{PORTS['HISTORY_PORT']}",'-storageDataPath=/vlogs',f"-retentionPeriod={os.getenv('RETENTION','30d')}",f"-retention.maxDiskSpaceUsageBytes={os.getenv('MAX_DISK','20GiB')}",'-inmemoryDataFlushInterval=30s','-loggerLevel=WARN','-search.maxQueryDuration=60s'])

collector_env=[('TZ',os.getenv('TZ','Europe/Berlin')),('VECTOR_LOG','warn'),('VECTOR_THREADS','1'),('WAN_IP',os.getenv('WAN_IP','')),('ALLOWED_SENDERS',os.getenv('ALLOWED_SENDERS','127.0.0.1')),('LOCAL_EXTRA_NETS',os.getenv('LOCAL_EXTRA_NETS','')),('LOCAL_SERVER_PORTS',os.getenv('LOCAL_SERVER_PORTS','51820')),('IGNORE_IPS',os.getenv('IGNORE_IPS',''))]+[(k,v) for k,v in PORTS.items() if k in ('IPFIX_PORT','SYSLOG_PORT','LIVE_PORT','METRICS_PORT','HISTORY_PORT')]
template('unifi-hq-collector',f'{PREFIX}-collector:{VERSION}','UniFi HQ IPFIX/Syslog-Collector. Nach GeoIP-Initialisierung und Verlaufsspeicher starten. Nach Neuerstellung muss auch unifi-hq-api neu erstellt werden.', '384m', env=collector_env, paths=[('Gerätenamen',DATA+'/config','/etc/vector/config','ro'),('GeoIP-Daten',DATA+'/geoip','/geoip','ro')], flags=['--tmpfs=/var/lib/vector:uid=65534,gid=65534,size=16m'])

api_env=[('TZ',os.getenv('TZ','Europe/Berlin')),('HOSTS_FILE','/config/hosts.csv'),('LISTEN','127.0.0.1:'+PORTS['API_PORT']),('AUTH_PASSWORD_HASH_FILE','/run/secrets/auth_password_hash'),('AUTH_SECRET_FILE','/run/secrets/auth_secret'),('UNIFI_API_KEY_FILE',KEY_FILE),('UNIFI_URL',os.getenv('UNIFI_URL','')),('UNIFI_SITE_ID',os.getenv('UNIFI_SITE_ID','')),('UNIFI_API_BASE_PATH',os.getenv('UNIFI_API_BASE_PATH','/proxy/network/integration/v1')),('UNIFI_CA_FILE',os.getenv('UNIFI_CA_FILE','')),('UNIFI_POLL_SECONDS',os.getenv('UNIFI_POLL_SECONDS','15')),('UNIFI_STALE_SECONDS',os.getenv('UNIFI_STALE_SECONDS','60')),('INVENTORY_FILE','/state/inventory.json')]
template('unifi-hq-api',f'{PREFIX}-api:{VERSION}','UniFi HQ Login, Gerätenamen und Controller-Inventar. Erst nach Collector starten. Secret-Verzeichnis auf dem Zielhost bereitstellen; keine Kennwörter/API-Keys in Template-Variablen eintragen.', '128m', env=api_env, paths=[('Gerätenamen',DATA+'/config','/config','rw'),('Inventar',DATA+'/state','/state','rw'),('Runtime-Secrets',SECRETS,'/run/secrets','ro')], flags=['--user=0:0','--cap-add=KILL','--pid=container:unifi-hq-collector'])

web_env=[(k,PORTS[k]) for k in ('HTTP_PORT','API_PORT','LIVE_PORT','HISTORY_PORT')]+[('ALLOWED_CLIENTS',os.getenv('ALLOWED_CLIENTS','private_ranges')),('HOME_LAT',os.getenv('HOME_LAT','50.11')),('HOME_LON',os.getenv('HOME_LON','8.68')),('HOME_LABEL',os.getenv('HOME_LABEL','Home')),('GATEWAY_NAME',os.getenv('GATEWAY_NAME','Gateway'))]
template('unifi-hq-web',f'{PREFIX}-web:{VERSION}','UniFi HQ: Live-Weltkarte, Netzwerkbaum und Geräteverfügbarkeit. GPL-Fork von ProfessorQuantumUniverse/unifi-HQ. Zuletzt starten, nur im freigegebenen lokalen Netz öffnen.', '64m', env=web_env, flags=['--tmpfs=/data:size=1m','--tmpfs=/config:size=1m'], web=True)

template('unifi-hq-geoip','alpine:3.24','UniFi HQ GeoIP-Einmaljob. Vor dem ersten Collector-Start ausführen; bei Erfolg beendet. Autostart deaktiviert lassen. Keine Host-Paketinstallation erforderlich.', '128m', env=[('GEOIP_MAX_AGE_DAYS',os.getenv('GEOIP_MAX_AGE_DAYS','32'))], paths=[('GeoIP-Daten',DATA+'/geoip','/geoip','rw'),('GeoIP-Updateskript',SOURCE+'/scripts/geoip-update.sh','/update.sh','ro')], args=['/bin/sh','/update.sh'],oneshot=True)
