import { describe, it, expect } from 'vitest';
import { appendRollingSample, type RollingState } from './use-rolling-metrics';

const s = (cpu: number, mem = 0, pods = 0) => ({ cpuPercentage: cpu, memoryPercentage: mem, podCount: pods });

describe('appendRollingSample', () => {
  it('appends samples for the same cluster', () => {
    let st: RollingState = { cid: 'a', samples: [] };
    st = appendRollingSample(st, 'a', s(10), 't1');
    st = appendRollingSample(st, 'a', s(20), 't2');
    expect(st.samples.map((x) => x.cpu)).toEqual([10, 20]);
  });

  it('resets the buffer when the cluster changes', () => {
    let st: RollingState = { cid: 'a', samples: [{ t: 't1', cpu: 10, mem: 0, pods: 0 }] };
    st = appendRollingSample(st, 'b', s(99), 't2');
    expect(st.cid).toBe('b');
    expect(st.samples).toEqual([{ t: 't2', cpu: 99, mem: 0, pods: 0 }]);
  });

  it('caps at max, dropping oldest', () => {
    let st: RollingState = { cid: 'a', samples: [] };
    for (let i = 0; i < 5; i++) st = appendRollingSample(st, 'a', s(i), `t${i}`, 3);
    expect(st.samples.map((x) => x.cpu)).toEqual([2, 3, 4]);
  });
});
