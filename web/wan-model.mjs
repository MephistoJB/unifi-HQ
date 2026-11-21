// UniFi HQ WAN display model. GPL-3.0. No estimates or traffic-derived presence.
const numberFormat=new Intl.NumberFormat('de-DE',{maximumFractionDigits:2});
export function formatWanRate(bitsPerSecond){
  if(typeof bitsPerSecond!=='number'||!Number.isFinite(bitsPerSecond)||bitsPerSecond<0)return'—';
  const mbps=bitsPerSecond/1_000_000;
  return mbps>0&&mbps<.01?'<0,01':numberFormat.format(mbps);
}
export function readWanValues(snapshot,{now=Date.now(),failed=false}={}){
  const sampledAt=Date.parse(snapshot?.sampled_at||snapshot?.last_success_at||'');
  const staleAfter=typeof snapshot?.stale_after_seconds==='number'&&snapshot.stale_after_seconds>0?snapshot.stale_after_seconds:60;
  const outdated=!Number.isFinite(sampledAt)||now-sampledAt>staleAfter*1000||sampledAt-now>30_000;
  const available=!failed&&snapshot?.configured===true&&!snapshot.stale&&['ok','partial'].includes(snapshot.status)&&!outdated;
  return {
    available,
    stale:failed||Boolean(snapshot?.stale)||(snapshot?.configured===true&&outdated),
    publicIp:available&&typeof snapshot.public_ip==='string'&&snapshot.public_ip.trim()?snapshot.public_ip.trim():'—',
    download:available?formatWanRate(snapshot.download_bps):'—',
    upload:available?formatWanRate(snapshot.upload_bps):'—',
  };
}
