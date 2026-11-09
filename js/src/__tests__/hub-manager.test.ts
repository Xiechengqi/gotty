import {afterEach, beforeEach, expect, it, vi} from 'vitest';
import {fireEvent} from '@testing-library/preact';
import {initHubManager} from '../hub-manager';

let cleanup = () => {};
let state: Record<string, unknown>;
let problem = '';
let posted: Record<string, unknown>[];
beforeEach(() => {
  document.body.innerHTML = '';
  state = {url:'',username:'',name:'',instance_id:'',online:false}; posted = []; problem = '';
  vi.stubGlobal('gotty_hub_enabled', true);
  vi.stubGlobal('fetch', vi.fn(async (path: string, init?: RequestInit) => {
    if (path.endsWith('/-/hub-status')) return {ok:true,json:async()=>({color:'gray'})};
    if (path.endsWith('/-/hub-view')) return {ok:true,json:async()=>({ok:true})};
    if (init?.method === 'POST') {
      const body = JSON.parse(String(init.body)); posted.push(body);
      if (problem) return {ok:false,json:async()=>({error:'instance_name_exists',message:problem})};
      if (body.Action === 'disconnect') state = {...state,paused:true,online:false};
      else state = {url:body.URL,username:body.Username,name:body.Name || 'generated-name',instance_id:'device-1',online:true,paused:false};
    }
    return {ok:true,json:async()=>({...state})};
  }));
  cleanup = initHubManager('/', 'local-auth');
});
afterEach(() => {cleanup();vi.unstubAllGlobals();vi.restoreAllMocks();});
const field = (name: string) => document.querySelector<HTMLInputElement>(`input[name=${name}]`)!;
const button = (label: string) => [...document.querySelectorAll<HTMLButtonElement>('#gotty-hub-panel button')].find(button=>button.textContent===label)!;
async function open() {fireEvent.click(document.getElementById('gotty-hub-btn')!);await vi.waitFor(()=>expect(button('接入 Hub')?.disabled ?? button('编辑')?.disabled).toBe(false));}

it('opens beside the toolbar, submits the optional name, and locks a successful binding', async () => {
  await open();
  expect(document.querySelector('dialog')).toBeNull();
  expect(field('Name').required).toBe(false);
  field('URL').value='http://hub.test:8088';field('Username').value='owner';field('Name').value='生产终端';
  fireEvent.submit(document.querySelector('#gotty-hub-panel form')!);
  await vi.waitFor(()=>expect(field('Name').readOnly).toBe(true));
  expect(posted[0]).toMatchObject({Name:'生产终端',URL:'http://hub.test:8088',Username:'owner'});
  const result = document.querySelector<HTMLElement>('.hub-result')!;
  expect(result.textContent).toBe('接入 http://hub.test:8088 成功');expect(result.dataset.tone).toBe('success');
  expect(button('重新接入')).toBeDefined();expect(button('断开')).toBeDefined();
  const calls = posted.length; fireEvent.click(button('编辑'));
  expect(field('URL').readOnly).toBe(false);expect(field('Name').readOnly).toBe(false);
  await Promise.resolve();expect(posted.length).toBe(calls);
});

it('shows duplicate-name failures and keeps the form editable', async () => {
  await open();field('URL').value='http://hub.test';field('Username').value='owner';field('Name').value='duplicate';
  problem='该用户名下已存在同名 GoTTY 实例';fireEvent.submit(document.querySelector('#gotty-hub-panel form')!);
  await vi.waitFor(()=>expect(document.querySelector('[role=alert]')?.textContent).toBe(problem));
  expect(field('Name').readOnly).toBe(false);expect(field('Name').value).toBe('duplicate');expect(button('接入 Hub').disabled).toBe(false);
});

it('disconnects without dropping configuration, then reconnects the existing identity', async () => {
  state={url:'http://hub.test',username:'owner',name:'named',instance_id:'device-1',online:true};
  await open();fireEvent.click(button('断开'));
  await vi.waitFor(()=>expect(document.querySelector('.hub-result')?.textContent).toContain('已断开 http://hub.test'));
  await vi.waitFor(()=>expect(button('重新接入').disabled).toBe(false));
  expect(posted[0]).toEqual({Action:'disconnect'});expect(field('Name').readOnly).toBe(true);
  fireEvent.click(button('重新接入'));
  await vi.waitFor(()=>expect(posted[1]?.Action).toBe('reconnect'));
  expect(posted[1]).toMatchObject({Name:'named',Username:'owner',URL:'http://hub.test'});
});

it('coordinates floating panels and restores focus on Escape', async () => {
  await open();fireEvent.keyDown(document,{key:'Escape'});expect(document.getElementById('gotty-hub-panel')!.hidden).toBe(true);
  expect(document.activeElement?.id).toBe('gotty-hub-btn');await open();
  window.dispatchEvent(new CustomEvent('gotty-panel-open',{detail:'theme'}));
  expect(document.getElementById('gotty-hub-panel')!.hidden).toBe(true);
});
