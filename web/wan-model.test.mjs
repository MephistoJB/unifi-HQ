import test from 'node:test';
import assert from 'node:assert/strict';
import {formatWanRate,readWanValues} from './wan-model.mjs';
const now=Date.parse('2026-11-21T12:00:00Z');
const sample={configured:true,status:'ok',stale:false,public_ip:'203.0.113.42',download_bps:12_340_000,upload_bps:0,sampled_at:new Date(now).toISOString(),stale_after_seconds:60};
test('WAN uses decimal megabits and preserves genuine measured zero',()=>{
  assert.equal(formatWanRate(12_340_000),'12,34');assert.equal(formatWanRate(0),'0');
  assert.equal(formatWanRate(1_000),'<0,01');
  for(const value of [null,undefined,NaN,Infinity,-1,'0'])assert.equal(formatWanRate(value),'—');
});
test('stale, failed, missing and unconfigured data never becomes zero or live',()=>{
  for(const value of [null,{...sample,configured:false},{...sample,stale:true},{...sample,status:'error'},{...sample,sampled_at:null,last_success_at:null},{...sample,sampled_at:new Date(now-60_001).toISOString()}]){
    const display=readWanValues(value,{now});assert.equal(display.publicIp,'—');assert.equal(display.download,'—');assert.equal(display.upload,'—');
  }
  assert.equal(readWanValues(sample,{now,failed:true}).upload,'—');
  assert.equal(readWanValues(sample,{now:now+60_000}).upload,'0');
});
test('a partial snapshot shows only individually available values',()=>{
  const display=readWanValues({...sample,status:'partial',download_bps:null,public_ip:null},{now});
  assert.equal(display.available,true);assert.equal(display.publicIp,'—');assert.equal(display.download,'—');assert.equal(display.upload,'0');
});
