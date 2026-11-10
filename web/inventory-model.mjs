// UniFi HQ extension, 2026-11-10. GPL-3.0. Pure display model; never infer presence from traffic.
export const statusLabels = {online:'Online',offline:'Offline',unknown:'Unbekannt'};
export const kindLabels = {gateway:'Gateway',switch:'Switch',ap:'Access Point',client:'Client'};
export function snapshotIsStale(snapshot, now = Date.now()) {
  const last = Date.parse(snapshot?.last_success_at || '');
  return Boolean(snapshot?.stale) || (snapshot?.configured && (!Number.isFinite(last) || now-last > (snapshot.stale_after_seconds || 60)*1000));
}
export function displayStatus(device, stale) { return stale ? 'unknown' : ['online','offline','unknown'].includes(device.status) ? device.status : 'unknown'; }
export function matchesDevice(d, filters, stale) {
  const query = filters.query.trim().toLocaleLowerCase('de');
  return (!query || [d.name,d.ip,d.mac,d.model,...(d.addresses || [])].join(' ').toLocaleLowerCase('de').includes(query)) &&
    (filters.kind === 'all' || (filters.kind === 'infrastructure' ? d.kind !== 'client' : d.kind === filters.kind)) &&
    (filters.status === 'all' || displayStatus(d,stale) === filters.status) && (!filters.network || d.network === filters.network);
}
export function buildForest(devices, matchingIds) {
  // Deduplicate and break self-links/cycles. Unknown parents remain visible as roots.
  const byId = new Map();
  for (const d of devices) if (typeof d.id === 'string' && d.id && !byId.has(d.id)) byId.set(d.id,{...d,children:[]});
  const included = new Set(matchingIds || byId.keys());
  for (const id of [...included]) { let d=byId.get(id);const seen=new Set([id]);while(d && byId.has(d.parent_id) && !seen.has(d.parent_id)){seen.add(d.parent_id);included.add(d.parent_id);d=byId.get(d.parent_id);} }
  const roots=[];
  for (const d of byId.values()) {
    if (!included.has(d.id)) continue;
    let p=byId.get(d.parent_id);const seen=new Set([d.id]);let cursor=p;let cyclic=false;
    while(cursor){if(seen.has(cursor.id)){cyclic=true;break;}seen.add(cursor.id);cursor=byId.get(cursor.parent_id);}
    if(p && included.has(p.id) && !cyclic)p.children.push(d);else roots.push(d);
  }
  const sort=nodes=>{nodes.sort((a,b)=>(a.kind==='gateway'?-1:b.kind==='gateway'?1:0)||String(a.name||a.id).localeCompare(String(b.name||b.id),'de',{numeric:true}));nodes.forEach(d=>sort(d.children));};sort(roots);
  return roots;
}
