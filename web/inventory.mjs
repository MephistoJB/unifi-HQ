// UniFi HQ extension, 2026-11-10. GPL-3.0. Controller inventory UI.
import {statusLabels,kindLabels,snapshotIsStale,displayStatus,matchesDevice,buildForest} from './inventory-model.mjs';
const $=id=>document.getElementById(id), params=new URLSearchParams(location.search);
const view=params.get('view')==='devices'?'devices':'tree', demo=params.has('demo');
try{const settings=JSON.parse(localStorage.getItem('lz.settings')||'{}');if(['graphite','black'].includes(settings.theme))document.documentElement.dataset.theme=settings.theme;}catch{}
const icons={gateway:'◈',switch:'▤',ap:'◉',client:'▣'};
let snapshot=null,loadError='',busy=false,selectedId='',collapsed=new Set(),hasBeenRendered=false;
let treeMode='graph',network=null,graphNodes=null,graphEdges=null,graphDirection='UD';
const expandedClientGroups=new Set(),groupMembers=new Map(),nodeDigests=new Map(),edgeDigests=new Map();
let graphFocusTimer,graphHighlightedId='';
const filters={query:'',kind:view==='devices'?'all':'infrastructure',status:'all',network:''};
const el=(tag,cls,text)=>{const n=document.createElement(tag);if(cls)n.className=cls;if(text!==undefined)n.textContent=String(text);return n;};
const append=(p,...children)=>{children.filter(Boolean).forEach(c=>p.append(c));return p;};
const fmtTime=value=>{const ms=Date.parse(value||'');if(!Number.isFinite(ms))return'Nicht bekannt';return new Intl.DateTimeFormat('de-DE',{dateStyle:'medium',timeStyle:'short'}).format(ms);};
const fmtAge=value=>{const ms=Date.parse(value||'');if(!Number.isFinite(ms))return'Nicht bekannt';const sec=Math.max(0,Math.floor((Date.now()-ms)/1000));if(sec<60)return'gerade eben';if(sec<3600)return`vor ${Math.floor(sec/60)} Min.`;if(sec<86400)return`vor ${Math.floor(sec/3600)} Std.`;return fmtTime(value);};
const deviceName=d=>String(d.name||d.model||d.mac||d.id||'Unbenanntes Gerät');
const deviceAddress=d=>d.ip||(d.addresses||[])[0]||'Keine IP bekannt';
function statusTag(d,stale){const s=displayStatus(d,stale);return append(el('span','hq-status-tag '+s),el('i','hq-led '+s),el('span','',statusLabels[s]));}
function notice(text,level='warning'){$('notice').hidden=!text;$('notice').textContent=text;$('notice').dataset.level=level;}
function showEmpty(title,text){$('empty').hidden=false;$('emptyTitle').textContent=title;$('emptyText').textContent=text;}
function activeDevices(){return Array.isArray(snapshot?.devices)?snapshot.devices.filter(d=>d && typeof d.id==='string'):[];}
function hasInventory(){return activeDevices().length>0 || Number.isFinite(Date.parse(snapshot?.last_success_at||''));}
function sourceStale(){return Boolean(loadError)||snapshotIsStale(snapshot);}
function match(d){return matchesDevice(d,filters,sourceStale());}
function describeAttachment(d){const p=activeDevices().find(p=>p.id===d.parent_id);let text=p?deviceName(p):'Anschluss nicht bekannt';if(d.port!==undefined&&d.port!==null&&d.port!=='')text+=' · Port '+d.port;if(d.historical_attachment)text+=' (zuletzt)';return text;}
function selectedDevice(){return activeDevices().find(d=>d.id===selectedId);}
function selectDevice(id,open=true){selectedId=id;renderSelection();renderDetails();if(open && matchMedia('(max-width:1239px)').matches && !$('detailDialog').open)$('detailDialog').showModal();}
function renderSelection(){
  document.querySelectorAll('[data-device-id]').forEach(n=>n.setAttribute('aria-pressed',String(n.dataset.deviceId===selectedId)));
  if(network&&graphNodes?.get(selectedId)){
    const updates=[];for(const id of new Set([graphHighlightedId,selectedId])){const d=activeDevices().find(d=>d.id===id);if(d&&graphNodes.get(id))updates.push({id,image:deviceImage(d.kind,displayStatus(d,sourceStale()),id===selectedId)});}if(updates.length)graphNodes.update(updates);
    graphHighlightedId=selectedId;network.selectNodes([selectedId],false);
  }
}
function detailContent(d){
  const box=el('div');
  if(!d){append(box,el('span','hq-detail-label','DEIN NETZWERK IM DETAIL'));const intro=el('div','hq-detail-intro');append(intro,el('div','hq-device-icon','⌘'),el('h3','','Jede Verbindung hat eine Geschichte.'),el('p','','Wähle ein Gerät aus. Hier findest du seinen aktuellen Status, den Anschluss und die bekannten Adressen.'));append(box,intro);return box;}
  append(box,el('span','hq-detail-label',d.kind==='client'?'NETZWERK-CLIENT':'UNIFI-INFRASTRUKTUR'),el('h3','hq-detail-name',deviceName(d)),statusTag(d,sourceStale()));
  const dl=el('dl');const fields=[['Gerätetyp',kindLabels[d.kind]||'Gerät'],['Modell',d.model],['IP-Adresse',d.ip],['Weitere Adressen',(d.addresses||[]).filter(a=>a!==d.ip).join(', ')],['MAC-Adresse',d.mac],['Netzwerk',d.network],['Anschluss',describeAttachment(d)],['Verbindung',d.connection_type==='wireless'?'WLAN':d.connection_type==='wired'?'LAN':null],['Linkrate',d.link_speed_mbps?`${d.link_speed_mbps} Mbit/s`:null],['Zuletzt gesehen',fmtTime(d.last_seen)]];
  for(const [label,value]of fields){if(!value)continue;append(dl,el('dt','',label),el('dd',/Adresse/.test(label)?'hq-mono':'',value));}append(box,dl);
  const actions=el('div','hq-detail-actions');if(view==='devices'){const a=el('a','hq-button','In der Topologie zeigen →');a.href='inventory.html?view=tree&device='+encodeURIComponent(d.id)+(demo?'&demo':'');actions.append(a);}else{const a=el('a','hq-button','In der Geräteliste zeigen →');a.href='inventory.html?view=devices&device='+encodeURIComponent(d.id)+(demo?'&demo':'');actions.append(a);}
  if(d.ip){const a=el('a','hq-button','Traffic für diese IP öffnen →');a.href='./?device='+encodeURIComponent(d.ip)+(demo?'&demo':'');actions.append(a);actions.append(el('p','hq-detail-foot','IP-bezogener Verlauf: Nach einem Adresswechsel können Verbindungen anderer Geräte enthalten sein.'));}if(view==='tree'&&activeDevices().some(child=>child.parent_id===d.id)){const toggle=el('button','hq-button',collapsed.has(d.id)?'Zweig aufklappen':'Zweig einklappen');toggle.type='button';toggle.addEventListener('click',()=>{collapsed.has(d.id)?collapsed.delete(d.id):collapsed.add(d.id);renderViews();});actions.append(toggle);}box.append(actions);
  let foot=d.historical_attachment?'Der Anschluss ist zuletzt bekannt und keine aktuell bestätigte Verbindung. ':'';if(sourceStale())foot+='Die Controller-Daten sind veraltet. Der aktuelle Status ist unbekannt. ';foot+='Online bestätigt eine Verbindung zum Controller-Netz, nicht die Funktion einzelner Dienste.';box.append(el('p','hq-detail-foot',foot));return box;
}
function renderDetails(){const d=selectedDevice();$('desktopDetail').replaceChildren(detailContent(d));$('mobileDetail').replaceChildren(detailContent(d));if($('detailDialog').open && !d)$('detailDialog').close();}
function nodeButton(d,matching){const button=el('button','hq-node'+(!matching?' context':''));button.type='button';button.dataset.deviceId=d.id;button.setAttribute('aria-pressed',String(d.id===selectedId));button.setAttribute('aria-label',`${deviceName(d)}, ${statusLabels[displayStatus(d,sourceStale())]}, Details anzeigen`);
  const main=append(el('span','hq-node-main'),el('span','hq-node-name',deviceName(d)),el('span','hq-node-meta',[kindLabels[d.kind],d.model,deviceAddress(d)].filter(Boolean).join(' · ')));
  const right=append(el('span','hq-node-right'),statusTag(d,sourceStale()));let link=[];if(d.port)link.push('P'+d.port);if(d.link_speed_mbps)link.push(d.link_speed_mbps+' Mbit/s');if(d.historical_attachment)link.push('zuletzt');if(link.length)right.append(el('span','hq-attachment',link.join(' · ')));
  append(button,el('span','hq-device-icon',icons[d.kind]||'▣'),main,right);button.addEventListener('click',()=>selectDevice(d.id));return button;
}
function renderOutline(forest,matchingIds){
  const branch=nodes=>{const ul=el('ul','hq-branch');for(const d of nodes){const li=el('li'),row=el('div','hq-tree-row'),toggle=el('button','hq-expander',collapsed.has(d.id)?'▸':'▾');toggle.type='button';toggle.disabled=!d.children.length;toggle.setAttribute('aria-label',(collapsed.has(d.id)?'Aufklappen: ':'Zuklappen: ')+deviceName(d));toggle.setAttribute('aria-expanded',String(!collapsed.has(d.id)));toggle.addEventListener('click',()=>{collapsed.has(d.id)?collapsed.delete(d.id):collapsed.add(d.id);renderViews();});append(row,toggle,nodeButton(d,matchingIds.has(d.id)));append(li,row);if(d.children.length&&!collapsed.has(d.id))li.append(branch(d.children));ul.append(li);}return ul;};
  $('outline').replaceChildren(branch(forest));
}
// Locally vendored vis-network supplies the layout and all pointer/touch handling.
// Controller strings stay in canvas labels or textContent-only DOM tooltips.
const graphLabelContext=document.createElement('canvas').getContext('2d');
function shorten(text,max){
  const characters=Array.from(String(text).replace(/\s+/g,' ')),label=characters.slice(0,max);
  if(graphLabelContext){graphLabelContext.font='14px "IBM Plex Sans Condensed"';while(label.length&&graphLabelContext.measureText(label.join('')+(label.length<characters.length?'…':'')).width>155)label.pop();}
  return label.join('')+(label.length<characters.length?'…':'');
}
function deviceImage(kind,status,selected=false){
  const color=status==='online'?'#67d4a4':status==='offline'?'#ef8293':'#e8c579';
  const parts={
    gateway:'<rect x="12" y="24" width="86" height="34" rx="5" fill="#edf3f8"/><rect x="17" y="29" width="30" height="21" rx="2" fill="#c2d4e4"/><rect x="20" y="32" width="24" height="15" rx="1" fill="#347ac0"/><path d="M57 43h33M57 48h33" stroke="#9eafbd" stroke-width="2"/><circle cx="91" cy="32" r="2" fill="#2a83cf"/>',
    switch:'<rect x="10" y="28" width="90" height="28" rx="4" fill="#e7eff5"/><path d="M18 38h6v7h-6zm10 0h6v7h-6zm10 0h6v7h-6zm10 0h6v7h-6zm10 0h6v7h-6zm10 0h6v7h-6zm10 0h6v7h-6z" fill="#687c8f"/><circle cx="92" cy="37" r="2" fill="#2f92df"/>',
    ap:'<circle cx="55" cy="40" r="26" fill="#eaf1f6"/><circle cx="55" cy="40" r="21" fill="none" stroke="#bfcedb"/><circle cx="55" cy="40" r="6" fill="none" stroke="#3289d2" stroke-width="2"/>',
    client:'<rect x="29" y="19" width="52" height="36" rx="3" fill="#dbe8f3"/><rect x="33" y="23" width="44" height="28" rx="1" fill="#315a7d"/><path d="M22 57h66l-5 7H27z" fill="#b7ccde"/>',
    device:'<rect x="31" y="18" width="48" height="45" rx="7" fill="#dbe8f3"/><path d="M40 30h30M40 37h30M40 44h20" stroke="#94afc5" stroke-width="3"/>',
    group:'<rect x="26" y="25" width="48" height="31" rx="4" fill="#93b7d9"/><rect x="33" y="19" width="48" height="31" rx="4" fill="#c5d9eb" stroke="#0c1a2b" stroke-width="2"/><path d="M42 31h30M42 37h24" stroke="#4c7498" stroke-width="2"/>'
  };
  const svg='<svg xmlns="http://www.w3.org/2000/svg" width="110" height="82" viewBox="0 0 110 82">'+(selected?'<ellipse cx="55" cy="40" rx="51" ry="36" fill="#53b7ff16" stroke="#69bcf5" stroke-width="2"/>':'')+(parts[kind]||parts.device)+'<circle cx="89" cy="60" r="6" fill="'+color+'" stroke="#0a1729" stroke-width="3"/></svg>';
  return 'data:image/svg+xml;charset=utf-8,'+encodeURIComponent(svg);
}
function graphTooltip(d){return el('div','hq-graph-tooltip',deviceName(d)+' · '+statusLabels[displayStatus(d,sourceStale())]+' · '+describeAttachment(d));}
function renderGraph(forest,matchingIds){
  if(!globalThis.vis?.Network||!globalThis.vis?.DataSet){
    treeMode='outline';notice('Die grafische Topologie ist nicht verfügbar. Die barrierearme Hierarchie bleibt nutzbar.');renderViews();return;
  }
  const nodes=[],edges=[],stale=sourceStale();groupMembers.clear();
  const addEdge=(from,to,d,extra={})=>{
    const historical=Boolean(d.historical_attachment||stale),wireless=d.connection_type==='wireless',unknown=!['wired','wireless'].includes(d.connection_type);
    const label=[wireless?'WLAN':unknown?'Anschlussart unbekannt':'LAN',d.port!==undefined&&d.port!==null?'P'+d.port:'',d.link_speed_mbps?d.link_speed_mbps+' Mbit/s':'',historical?'zuletzt':''].filter(Boolean).join(' · ');
    edges.push({id:JSON.stringify([from,to]),from,to,width:1.3,title:el('div','hq-graph-tooltip',label||'Verbindung laut Controller'),dashes:historical?[3,5]:wireless?[8,5]:unknown?[2,5]:false,color:{color:historical?'#746d79':unknown?'#61748c':'#4a769d',highlight:'#8ac9f3',hover:'#8ac9f3',inherit:false},font:{face:'IBM Plex Mono',size:10,color:'#8da8c5',strokeWidth:0,align:'middle',multi:false},...extra});
  };
  function place(d,depth,parent){
    const status=displayStatus(d,stale),isContext=!matchingIds.has(d.id),children=d.children,hidden=collapsed.has(d.id)&&children.length;
    const secondary=hidden?children.length+' direkte Verbindungen · zugeklappt':!d.parent_id&&d.kind!=='gateway'?'Anschluss nicht bekannt':d.model||kindLabels[d.kind]||'Gerät';
    nodes.push({id:d.id,label:shorten(deviceName(d),23)+'\n'+shorten(secondary,25),level:depth,shape:'image',image:deviceImage(d.kind,status,d.id===selectedId),size:d.kind==='client'?28:37,opacity:isContext?0.48:1,font:{face:'IBM Plex Sans Condensed',size:14,color:status==='offline'?'#ed9eaa':status==='unknown'?'#dac48e':'#dce7f3',multi:false,strokeWidth:0},title:graphTooltip(d)});
    if(parent)addEdge(parent,d.id,d);
    if(hidden)return;
    const infra=children.filter(c=>c.kind!=='client'),clients=children.filter(c=>c.kind==='client');
    for(const child of infra)place(child,depth+1,d.id);
    if(clients.length>2&&!filters.query.trim()){
      let groupId='__hq_clients_'+d.id;while(activeDevices().some(n=>n.id===groupId))groupId+='_' ;
      const expanded=expandedClientGroups.has(d.id);groupMembers.set(groupId,{parentId:d.id,ids:clients.map(c=>c.id)});
      const online=clients.filter(c=>displayStatus(c,stale)==='online').length;
      nodes.push({id:groupId,label:(expanded?'− ':'+ ')+clients.length+' Clients\n'+online+' online · '+(expanded?'gruppieren':'aufklappen'),level:depth+1,shape:'image',image:deviceImage('group',stale||clients.every(c=>displayStatus(c,stale)==='unknown')?'unknown':online>0?'online':clients.every(c=>displayStatus(c,stale)==='offline')?'offline':'unknown'),size:30,font:{face:'IBM Plex Sans Condensed',size:13,color:'#9cb9d8',multi:false,strokeWidth:0},title:el('div','hq-graph-tooltip','Logische Client-Gruppe. Antippen zum '+(expanded?'Gruppieren':'Aufklappen')+'. Keine zusätzliche physische Netzwerkstation.')});
      edges.push({id:JSON.stringify([d.id,groupId]),from:d.id,to:groupId,width:1,color:{color:'#37516d',inherit:false},dashes:[2,4]});
      if(expanded)for(const child of clients)place(child,depth+2,groupId);
    }else for(const child of clients)place(child,depth+1,d.id);
  }
  forest.forEach(root=>place(root,0,null));
  const first=!network,position=network?.getViewPosition(),scale=network?.getScale();
  if(first){
    graphNodes=new vis.DataSet();graphEdges=new vis.DataSet();
    network=new vis.Network($('graph'),{nodes:graphNodes,edges:graphEdges},{locale:'de',autoResize:true,physics:{enabled:false},layout:{hierarchical:{enabled:true,direction:graphDirection,sortMethod:'directed',shakeTowards:'roots',levelSeparation:165,nodeSpacing:180,treeSpacing:230,blockShifting:true,edgeMinimization:true,parentCentralization:true}},nodes:{borderWidth:0,labelHighlightBold:false,chosen:{node:false,label:false}},edges:{smooth:{enabled:true,type:'cubicBezier',forceDirection:'vertical',roundness:.45},selectionWidth:1,hoverWidth:1},interaction:{dragNodes:false,dragView:true,zoomView:true,hover:true,hoverConnectedEdges:false,selectConnectedEdges:false,multiselect:false,tooltipDelay:400,keyboard:{enabled:true,bindToWindow:false},navigationButtons:false}});
    network.on('click',event=>{const id=event.nodes[0];if(!id)return;if(groupMembers.has(id)){const group=groupMembers.get(id);expandedClientGroups.has(group.parentId)?expandedClientGroups.delete(group.parentId):expandedClientGroups.add(group.parentId);renderViews();requestAnimationFrame(fitGraph);}else selectDevice(id);});
    network.on('doubleClick',event=>{const id=event.nodes[0];if(!id||groupMembers.has(id))return;const d=activeDevices().find(n=>n.id===id);if(d&&activeDevices().some(n=>n.parent_id===id)){collapsed.has(id)?collapsed.delete(id):collapsed.add(id);renderViews();}});
    network.on('zoom',updateZoomLabel);
  }
  const sync=(data,desired,digests)=>{
    const ids=new Set(desired.map(n=>n.id)),removed=data.getIds().filter(id=>!ids.has(id));if(removed.length){data.remove(removed);removed.forEach(id=>digests.delete(id));}
    const changed=[];for(const item of desired){const {title,...serializable}=item,digest=JSON.stringify({...serializable,tooltip:title?.textContent||''});if(digests.get(item.id)!==digest){digests.set(item.id,digest);changed.push(item);}}
    if(changed.length)data.update(changed);
  };
  sync(graphEdges,edges.filter(edge=>graphNodes.get(edge.from)&&graphNodes.get(edge.to)),edgeDigests);
  sync(graphNodes,nodes,nodeDigests);sync(graphEdges,edges,edgeDigests);
  if(!first&&position&&scale)network.moveTo({position,scale,animation:false});
  if(graphNodes.get(selectedId)){graphHighlightedId=selectedId;network.selectNodes([selectedId],false);}
  $('graph').dataset.engine='vis-network';$('graph').dataset.nodeCount=String(nodes.length);$('graph').dataset.edgeCount=String(edges.length);
  if(first)requestAnimationFrame(fitGraph);updateZoomLabel();
}
function updateZoomLabel(){if(network)$('zoomValue').textContent=Math.round(network.getScale()*100)+' %';}
function revealSelection(){const map=new Map(activeDevices().map(d=>[d.id,d]));let d=map.get(selectedId);const seen=new Set();while(d&&!seen.has(d.id)){seen.add(d.id);collapsed.delete(d.id);if(d.parent_id)expandedClientGroups.add(d.parent_id);d=map.get(d.parent_id);}}
function focusSelection(){if(network&&graphNodes?.get(selectedId)){network.focus(selectedId,{scale:Math.min(1,Math.max(.6,network.getScale())),animation:false});updateZoomLabel();}}
function fitGraph(){if(network&&!$('graph').hidden){network.fit({animation:false});updateZoomLabel();}}
function scaleGraph(factor){if(network){network.moveTo({scale:Math.max(.08,Math.min(3,network.getScale()*factor)),animation:false});updateZoomLabel();}}
function renderList(devices){const list=$('deviceList'),head=el('div','hq-list-head');for(const text of ['Gerät','Status','IP-Adresse','Anschluss'])head.append(el('span','',text));list.replaceChildren(head);for(const d of devices){const row=el('button','hq-device-row');row.type='button';row.dataset.deviceId=d.id;row.setAttribute('aria-pressed',String(d.id===selectedId));const name=append(el('span','hq-cell'),el('span','hq-cell-name',deviceName(d)),el('span','hq-cell-sub',[kindLabels[d.kind],d.model].filter(Boolean).join(' · ')));append(row,name,statusTag(d,sourceStale()),el('span','hq-cell-value mono',deviceAddress(d)),el('span','hq-cell-value',describeAttachment(d)));row.addEventListener('click',()=>selectDevice(d.id));list.append(row);}}
function renderViews(){
  const devices=activeDevices(),visible=devices.filter(match).sort((a,b)=>deviceName(a).localeCompare(deviceName(b),'de',{numeric:true})),ids=new Set(visible.map(d=>d.id));
  $('treeTools').hidden=view!=='tree'||!visible.length;$('deviceList').hidden=view!=='devices'||!visible.length;$('outline').hidden=view!=='tree'||treeMode!=='outline'||!visible.length;$('graph').hidden=view!=='tree'||treeMode!=='graph'||!visible.length;$('zoomControls').hidden=treeMode!=='graph';$('expandAll').hidden=false;$('graphDirection').closest('label').hidden=treeMode!=='graph';$('includeClients').checked=filters.kind==='all'||filters.kind==='client';$('showOutline').setAttribute('aria-pressed',String(treeMode==='outline'));$('showGraph').setAttribute('aria-pressed',String(treeMode==='graph'));
  $('results').textContent=hasInventory()?`${visible.length} von ${devices.length} Geräten${view==='tree'?' · übergeordnete Geräte bleiben zur Orientierung sichtbar':''}`:'Noch kein Inventar verfügbar';
  $('empty').hidden=Boolean(visible.length);
  if(!visible.length){if(!snapshot)showEmpty(loadError?'Controller nicht erreichbar':'Dein Netzwerk wird geladen',loadError||'Die Geräte erscheinen hier, sobald der Controller antwortet.');else if(!snapshot.configured&&snapshot.error)showEmpty('UniFi-Verbindung prüfen',snapshot.error);else if(!snapshot.configured)showEmpty('Controller noch nicht verbunden','Die UniFi-Verbindung ist noch nicht eingerichtet. Sobald die Verbindung in der App-Konfiguration hinterlegt ist, wird dein Netzwerk automatisch eingelesen.');else if(devices.length)showEmpty('Keine passenden Geräte','Passe die Suche oder die Filter an, um weitere Geräte zu sehen.');else if(sourceStale())showEmpty('Noch keine aktuellen Gerätedaten',snapshot.error||'Der Controller hat noch keinen vollständigen Snapshot geliefert. Die Abfrage wird automatisch wiederholt.');else showEmpty('Noch keine Geräte erfasst','Der Controller hat ein leeres Inventar geliefert. Die Abfrage wird automatisch wiederholt.');}
  if(view==='tree'&&visible.length){const forest=buildForest(devices,ids);if(treeMode==='outline')renderOutline(forest,ids);else renderGraph(forest,ids);}else if(visible.length)renderList(visible);
  renderDetails();
}
function render(){
  const devices=activeDevices(),stale=sourceStale(),configured=snapshot?.configured;
  const counts={online:0,offline:0,unknown:0,infrastructure:0};for(const d of devices){counts[displayStatus(d,stale)]++;if(d.kind!=='client')counts.infrastructure++;}for(const key of Object.keys(counts))$('metric'+key[0].toUpperCase()+key.slice(1)).textContent=hasInventory()?String(counts[key]):'—';
  const connection=$('connection');connection.replaceChildren(el('span','hq-led '+(configured&&!stale?'online':configured?'unknown':'')),document.createTextNode(demo?'Demo · Beispieldaten':!snapshot?'Controller verbinden …':!configured?'Controller nicht eingerichtet':stale?'Controller-Daten veraltet':'Controller verbunden'));
  if(demo)notice('Demo · simulierte Geräte, Statuswerte und Verbindungen.','demo');else if(loadError)notice(loadError,'error');else if(snapshot&&!configured&&snapshot.error)notice(snapshot.error,'error');else if(snapshot&&!configured)notice('Die Live-Weltkarte und das Controller-Inventar haben getrennte Datenquellen. Richte die UniFi-Verbindung ein, um Geräte und Topologie zu sehen.');else if(stale&&snapshot)notice(snapshot.error||'Der letzte vollständige Snapshot ist veraltet. Geräte bleiben sichtbar; ihre aktuelle Verfügbarkeit ist unbekannt.');else {const limitations=snapshot?.sources?.topology?.limitations||[];notice(limitations.length?'Topologie teilweise verfügbar: '+limitations.join(' '):'');}
  $('snapshotTime').textContent=snapshot?.last_success_at?'Letzter vollständiger Snapshot\n'+fmtTime(snapshot.last_success_at):'Noch kein vollständiger Snapshot';$('inventoryMeta').textContent=hasInventory()?`${devices.filter(d=>d.kind!=='client').length} Infrastruktur · ${devices.filter(d=>d.kind==='client').length} Clients${snapshot.last_success_at?' · aktualisiert '+fmtAge(snapshot.last_success_at):''}`:'Noch kein Inventar verfügbar';
  const select=$('networkFilter'),values=[...new Set(devices.map(d=>d.network).filter(Boolean))].sort((a,b)=>a.localeCompare(b,'de'));if(values.join('\n')!==select.dataset.values){select.dataset.values=values.join('\n');select.disabled=!values.length;select.title=values.length?'':'Der Controller liefert keine Netzwerkzuordnung';select.replaceChildren(new Option(values.length?'Alle Netzwerke':'Nicht verfügbar',''));for(const value of values)select.add(new Option(value,value));if(values.includes(filters.network))select.value=filters.network;else filters.network='';}
  renderViews();if(!hasBeenRendered&&devices.length){hasBeenRendered=true;if(params.has('device')){selectedId=params.get('device');filters.kind='all';$('kind').value='all';revealSelection();renderViews();if(selectedDevice())selectDevice(selectedId);requestAnimationFrame(focusSelection);}if(treeMode==='graph'&&!params.has('device'))requestAnimationFrame(fitGraph);}
}
async function load(){if(busy)return;busy=true;$('refresh').disabled=true;const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),10000);try{if(demo){snapshot=demoSnapshot();loadError='';}else{const response=await fetch('api/inventory',{cache:'no-store',signal:controller.signal,credentials:'same-origin'});if(response.status===401){location.replace('login.html?next='+encodeURIComponent(location.pathname+location.search));return;}if(!response.ok)throw new Error('HTTP '+response.status);const data=await response.json();if(typeof data.configured!=='boolean'||!Array.isArray(data.devices))throw new Error('Ungültige Antwort');snapshot=data;loadError='';}}catch{loadError='Die Gerätedaten konnten nicht aktualisiert werden. Die Verbindung wird automatisch erneut geprüft. Vorhandene Daten werden als veraltet angezeigt.';}finally{clearTimeout(timeout);busy=false;$('refresh').disabled=false;render();}}
function demoSnapshot(){
  const now=new Date().toISOString();
  const defs=[['gw','Demo-Gateway','gateway',null,'UDM SE'],['sw','Core-Switch','switch','gw','USW 24'],['ez','Essbereich-Switch','switch','sw','USW Flex Mini'],['ezap','Essbereich-AP','ap','ez','U6 Pro'],['wz','Wohnbereich-Switch','switch','sw','USW Flex'],['wzap','Wohnbereich-AP','ap','wz','U6 Pro'],['bu','Arbeitszimmer-Switch','switch','sw','USW Flex'],['buap','Arbeitszimmer-AP','ap','bu','U6 Mesh'],['hk','Technik-Switch','switch','sw','USW Flex Mini'],['kg','Untergeschoss-AP','ap','gw','U6 Lite'],['sz','Obergeschoss-AP','ap','gw','U6 Lite']];
  const devices=defs.map(([id,name,kind,parent_id,model],i)=>({id,name,kind,parent_id,model,ip:`192.0.2.${i+1}`,mac:`02:00:00:00:00:${i.toString(16).padStart(2,'0')}`,status:'online',port:parent_id?i:undefined,link_speed_mbps:id==='wz'?100:1000,network:'Demo-LAN',connection_type:'wired',last_seen:now}));
  const parents=['ezap','wzap','buap','kg','sz','sw','hk'];
  for(let i=0;i<70;i++){
    const status=i%13===0?'unknown':i%9===0?'offline':'online';
    devices.push({id:'demo-client-'+i,name:['Tablet','Laptop','Smartphone','Sensor','Server','Lautsprecher','Display'][i%7]+' '+String(i+1).padStart(2,'0'),kind:'client',parent_id:parents[i%parents.length],model:'Demo-Gerät',ip:`198.51.100.${i+1}`,mac:`02:00:00:00:01:${i.toString(16).padStart(2,'0')}`,status,network:i%3===0?'Demo-IoT':'Demo-LAN',connection_type:i%parents.length<5?'wireless':'wired',last_seen:status==='offline'?new Date(Date.now()-23*60000).toISOString():now,historical_attachment:status==='offline'});
  }
  return{configured:true,generated_at:now,last_success_at:now,stale:false,devices,sources:{topology:{status:'ok',limitations:[]}}};
}
if(view==='tree'){document.body.classList.add('hq-topology');document.querySelector('.hq-work-head').append($('refresh'));}
$('nav'+(view==='tree'?'Tree':'Devices')).setAttribute('aria-current','page');$('kind').value=filters.kind;document.title=(view==='tree'?'Topologie':'Geräte')+' · UniFi HQ';$('pageTitle').textContent=view==='tree'?'Topologie':'Geräte & Verfügbarkeit';$('pageSubtitle').textContent=view==='tree'?'Vom Gateway bis zum Client – entdecke die Verbindungen in deinem Netzwerk.':'Sieh, was verbunden ist. Und was zuletzt verbunden war.';$('workspaceTitle').textContent=view==='tree'?'Physische Topologie':'Geräte & Verfügbarkeit';
if(demo)for(const id of ['navLive','navTree','navDevices']){const a=$(id);a.href+=(a.href.includes('?')?'&':'?')+'demo';}
$('refresh').addEventListener('click',load);$('search').addEventListener('input',e=>{filters.query=e.target.value;renderViews();clearTimeout(graphFocusTimer);graphFocusTimer=setTimeout(()=>{if(treeMode==='graph'&&filters.query.trim())fitGraph();},180);});for(const[id,key]of[['kind','kind'],['statusFilter','status'],['networkFilter','network']])$(id).addEventListener('change',e=>{filters[key]=e.target.value;renderViews();if(treeMode==='graph')requestAnimationFrame(fitGraph);});
$('showOutline').addEventListener('click',()=>{treeMode='outline';renderViews();});$('showGraph').addEventListener('click',()=>{treeMode='graph';renderViews();requestAnimationFrame(fitGraph);});$('expandAll').addEventListener('click',()=>{collapsed.clear();for(const d of activeDevices())expandedClientGroups.add(d.id);renderViews();if(treeMode==='graph')requestAnimationFrame(fitGraph);});$('includeClients').addEventListener('change',e=>{filters.kind=e.target.checked?'all':'infrastructure';$('kind').value=filters.kind;renderViews();requestAnimationFrame(fitGraph);});$('graphDirection').addEventListener('change',e=>{graphDirection=e.target.value;if(network)network.setOptions({layout:{hierarchical:{direction:graphDirection}},edges:{smooth:{forceDirection:graphDirection==='UD'?'vertical':'horizontal'}}});requestAnimationFrame(fitGraph);});$('zoomIn').addEventListener('click',()=>scaleGraph(1.25));$('zoomOut').addEventListener('click',()=>scaleGraph(.8));$('fit').addEventListener('click',fitGraph);$('closeDetail').addEventListener('click',()=>$('detailDialog').close());$('detailDialog').addEventListener('click',e=>{if(e.target===$('detailDialog')){const r=$('detailDialog').getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)$('detailDialog').close();}});
let resizeTimer;window.addEventListener('resize',()=>{clearTimeout(resizeTimer);resizeTimer=setTimeout(()=>{if(treeMode==='graph')fitGraph();if(!matchMedia('(max-width:1239px)').matches)$('detailDialog').close();},150);});document.addEventListener('visibilitychange',()=>{if(!document.hidden)load();});render();load();setInterval(()=>{if(!document.hidden)load();},15000);setInterval(()=>{if(snapshot&&!demo&&snapshotIsStale(snapshot))render();},5000);
