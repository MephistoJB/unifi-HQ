import test from 'node:test';
import assert from 'node:assert/strict';
import {buildForest,displayStatus,matchesDevice,snapshotIsStale} from './inventory-model.mjs';
test('stale snapshots never present cached devices as online',()=>{assert.equal(displayStatus({status:'online'},true),'unknown');assert.equal(snapshotIsStale({configured:true,last_success_at:'2020-01-01T00:00:00Z'}),true);});
test('searching a child retains the ancestry without inventing attachments',()=>{const forest=buildForest([{id:'gateway'},{id:'switch',parent_id:'gateway'},{id:'client',parent_id:'switch'},{id:'orphan',parent_id:'missing'}],new Set(['client','orphan']));assert.equal(forest.length,2);assert.equal(forest[0].children[0].children[0].id,'client');assert.equal(forest[1].id,'orphan');});
test('cycles and self-links terminate and remain visible',()=>{const forest=buildForest([{id:'a',parent_id:'b'},{id:'b',parent_id:'a'},{id:'c',parent_id:'c'}]);assert.equal(forest.length,3);assert.equal(forest.reduce((n,d)=>n+d.children.length,0),0);});
test('search, class, network and status filters combine without traffic inference',()=>{const d={id:'a',name:'Büro',kind:'ap',status:'online',ip:'192.168.1.1',network:'LAN'};const filters={query:'BÜRO',kind:'infrastructure',status:'online',network:'LAN'};assert.equal(matchesDevice(d,filters,false),true);assert.equal(matchesDevice(d,filters,true),false);filters.status='unknown';assert.equal(matchesDevice(d,filters,true),true);});

const flatten = forest => forest.flatMap(node => [node,...flatten(node.children)]);

test('distinct devices sharing an IP or name retain their own parent attachments',()=>{
  const devices=[
    {id:'site:device:gateway',kind:'gateway',name:'Gateway'},
    {id:'site:device:ap',kind:'ap',name:'Access Point',parent_id:'site:device:gateway'},
    {id:'site:client:aa',kind:'client',name:'Tablet',ip:'192.0.2.8',mac:'02:00:00:00:00:01',parent_id:'site:device:ap',status:'offline',historical_attachment:true},
    {id:'site:client:bb',kind:'client',name:'Tablet',ip:'192.0.2.8',mac:'02:00:00:00:00:02',parent_id:'site:device:gateway',status:'online',historical_attachment:false},
  ];
  const forest=buildForest(devices),nodes=flatten(forest);
  assert.equal(nodes.length,4);
  assert.equal(nodes.filter(d=>d.kind==='client').length,2);
  assert.equal(nodes.find(d=>d.id==='site:device:ap').children[0].id,'site:client:aa');
  assert.ok(forest[0].children.some(d=>d.id==='site:client:bb'));
  assert.equal(nodes.find(d=>d.id==='site:client:aa').historical_attachment,true);
});

test('an offline client filter retains its online ancestors without adding unrelated matches',()=>{
  const devices=[
    {id:'gateway',kind:'gateway',name:'Gateway',status:'online'},
    {id:'switch',kind:'switch',name:'Core',status:'online',parent_id:'gateway'},
    {id:'ap',kind:'ap',name:'Guest AP',status:'online',parent_id:'switch'},
    {id:'tablet',kind:'client',name:'Tablet',status:'offline',network:'Guest',parent_id:'ap'},
    {id:'other',kind:'client',name:'Other client',status:'offline',network:'Guest',parent_id:'ap'},
  ];
  const filters={query:'tablet',kind:'client',status:'offline',network:'Guest'};
  const matching=devices.filter(d=>matchesDevice(d,filters,false));
  assert.deepEqual(matching.map(d=>d.id),['tablet']);
  const forest=buildForest(devices,new Set(matching.map(d=>d.id)));
  assert.deepEqual(flatten(forest).map(d=>d.id),['gateway','switch','ap','tablet']);
  assert.equal(flatten(forest).filter(d=>matchesDevice(d,filters,false)).length,1);
});

test('orphans and clients feeding a broken cycle remain visible without invented links',()=>{
  const devices=[
    {id:'a',parent_id:'b'},
    {id:'b',parent_id:'a'},
    {id:'client',kind:'client',parent_id:'a'},
    {id:'orphan',kind:'client',parent_id:'not-in-inventory'},
    {id:'self',parent_id:'self'},
  ];
  const forest=buildForest(devices),nodes=flatten(forest);
  assert.equal(nodes.length,devices.length);
  assert.equal(new Set(nodes.map(d=>d.id)).size,devices.length);
  assert.ok(forest.some(d=>d.id==='orphan'));
  assert.ok(forest.some(d=>d.id==='self'));
  assert.equal(nodes.some(d=>d.id==='not-in-inventory'),false);
  assert.ok(nodes.every(d=>d.children.length===0));
});

test('freshness expiry moves both online and offline filters to unknown without mutating data',()=>{
  const last=Date.parse('2026-11-11T12:00:00Z');
  const snapshot={configured:true,last_success_at:new Date(last).toISOString(),stale_after_seconds:60};
  assert.equal(snapshotIsStale(snapshot,last+60000),false);
  assert.equal(snapshotIsStale(snapshot,last+60001),true);
  assert.equal(snapshotIsStale({...snapshot,stale:true},last),true);
  const devices=[{id:'online',name:'Online client',kind:'client',status:'online'},{id:'offline',name:'Offline client',kind:'client',status:'offline'}];
  const filters={query:'',kind:'all',status:'unknown',network:''};
  assert.equal(devices.filter(d=>matchesDevice(d,filters,false)).length,0);
  assert.equal(devices.filter(d=>matchesDevice(d,filters,true)).length,2);
  filters.status='online';
  assert.equal(devices.filter(d=>matchesDevice(d,filters,true)).length,0);
  assert.deepEqual(devices.map(d=>d.status),['online','offline']);
  assert.equal(displayStatus(devices[0],false),'online');
});
