// UniFi HQ WAN strip. GPL-3.0. Reads local telemetry; never uses an external IP service.
import {readWanValues} from './wan-model.mjs';
const $=id=>document.getElementById(id),strip=$('wanStrip');
let demo=new URLSearchParams(location.search).has('demo')||location.protocol==='file:'||$('status')?.dataset.state==='demo';
let snapshot=null,failed=false,inFlight=false,received=false,activeRequest=null;
function render(){
  const value=readWanValues(snapshot,{failed});
  $('wanIp').textContent=value.publicIp;$('wanDownload').textContent=value.download;$('wanUpload').textContent=value.upload;
  const state=demo?'demo':!received?'loading':value.available?(snapshot.status==='partial'?'partial':'ok'):!snapshot?.configured&&!failed?'unconfigured':value.stale?'stale':'unavailable';
  const label={demo:'Demo',loading:'Wird geladen',ok:'Aktuell',partial:'Teilweise',unconfigured:'Nicht eingerichtet',stale:'Nicht aktuell',unavailable:'Nicht verfügbar'}[state];
  strip.dataset.state=state;$('wanState').textContent=label;
  if(demo)strip.title='Demo mit fiktiven Raten und einer Dokumentations-IP. Keine Live-Werte.';
  else if(value.available){const when=new Intl.DateTimeFormat('de-DE',{dateStyle:'medium',timeStyle:'medium'}).format(Date.parse(snapshot.sampled_at||snapshot.last_success_at));strip.title='Aktuelle Raten laut UniFi-Controller · '+when+(snapshot.active_interface?' · '+snapshot.active_interface:'')+(snapshot.status==='partial'?' · Nicht verfügbare Werte erscheinen als —.':'');}
  else strip.title=state==='loading'?'WAN-Daten werden vom lokalen Controller geladen.':'Keine aktuellen WAN-Werte verfügbar. — bedeutet unbekannt, nicht 0 Mbit/s.';
}
function demoSnapshot(){const now=new Date().toISOString();return{configured:true,status:'ok',stale:false,public_ip:'203.0.113.42',download_bps:42_350_000,upload_bps:8_170_000,sampled_at:now,last_success_at:now,stale_after_seconds:60};}
async function load(){
  if(demo){snapshot=demoSnapshot();received=true;failed=false;render();return;}
  if(inFlight||document.hidden)return;
  inFlight=true;const abort=new AbortController(),timeout=setTimeout(()=>abort.abort(),10_000);
  activeRequest=abort;
  try{
    const response=await fetch('api/wan',{cache:'no-store',credentials:'same-origin',signal:abort.signal});
    if(!response.ok)throw new Error('WAN response unavailable');
    const value=await response.json();
    if(demo)return;
    if(typeof value.configured!=='boolean'||typeof value.status!=='string')throw new Error('Invalid WAN response');
    snapshot=value;failed=false;
  }catch{if(!demo)failed=true;}
  finally{clearTimeout(timeout);activeRequest=null;inFlight=false;received=true;render();}
}
// The upstream "Start demo" action can also be activated without ?demo.
const trafficStatus=$('status');
if(trafficStatus)new MutationObserver(()=>{if(!demo&&trafficStatus.dataset.state==='demo'){demo=true;activeRequest?.abort();load();}}).observe(trafficStatus,{attributes:true,attributeFilter:['data-state']});
// The existing map has fixed side panels on larger screens. Measure the real
// wrapped header, including font-size settings, rather than assuming its height.
const header=document.querySelector('.hq-live-header');
function syncHeaderHeight(){
  if(!header||innerWidth<=900)return;
  const panel=$('left'),zoom=parseFloat(getComputedStyle(panel).zoom)||1;
  const top=Math.ceil((header.getBoundingClientRect().bottom+12)/zoom)+'px';
  if(document.documentElement.style.getPropertyValue('--hq-panel-top')!==top)document.documentElement.style.setProperty('--hq-panel-top',top);
}
if(header&&typeof ResizeObserver==='function')new ResizeObserver(syncHeaderHeight).observe(header);
new MutationObserver(syncHeaderHeight).observe(document.documentElement,{attributes:true,attributeFilter:['style']});
addEventListener('resize',syncHeaderHeight);document.fonts?.ready.then(syncHeaderHeight);syncHeaderHeight();
document.addEventListener('visibilitychange',()=>{if(!document.hidden)load();});
render();load();setInterval(load,15_000);setInterval(()=>{if(!document.hidden&&!demo)render();},5_000);
