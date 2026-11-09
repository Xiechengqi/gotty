// Local management surface. The Hub receives only enrollment data, never a proxy target.
declare var gotty_hub_enabled: boolean;
export function initHubManager(basePath:string,authToken:string):()=>void{
 if(typeof gotty_hub_enabled==='undefined'||!gotty_hub_enabled)return()=>{};
 const button=document.createElement('button');button.className='btn btn-sm btn-outline-secondary';button.textContent='接入 Hub';button.title='Connect GoTTY Hub';button.style.cssText='position:fixed;right:12px;top:12px;z-index:1000';document.body.append(button);
 let timer:ReturnType<typeof setInterval>|undefined;
 const headers=()=>({'Content-Type':'application/json',...(authToken?{Authorization:`Bearer ${authToken}`}:{})});
 const view=Array.from(crypto.getRandomValues(new Uint8Array(32)),b=>b.toString(16).padStart(2,'0')).join('');
 async function sync(){try{
  const response=await fetch(basePath+'-/hub-status',{headers:headers(),cache:'no-store'});if(!response.ok){window.dispatchEvent(new CustomEvent('gotty-hub-status',{detail:null}));return;}
  const state=await response.json();if(['red','gray','yellow','green'].includes(state.color))window.dispatchEvent(new CustomEvent('gotty-hub-status',{detail:state.color}));
  await fetch(basePath+'-/hub-view',{method:'POST',headers:headers(),body:JSON.stringify({ID:view,Viewing:document.visibilityState==='visible'&&document.hasFocus()&&!!document.activeElement?.closest('.xterm')})});
 }catch{}}
 timer=setInterval(sync,3000);void sync();
 button.onclick=()=>{
  const dialog=document.createElement('dialog');dialog.style.cssText='max-width:440px;width:calc(100% - 32px);padding:24px;border:1px solid currentColor;border-radius:12px';
  const title=document.createElement('h2');title.textContent='接入 GoTTY Hub';dialog.append(title);
  const form=document.createElement('form');form.style.cssText='display:flex;flex-direction:column;gap:12px';dialog.append(form);
  const fields:Record<string,HTMLInputElement>={};
  for(const [key,label,type]of [['URL','Hub 地址','url'],['Name','实例名称','text'],['Email','所有者邮箱','email'],['Others','其他授权邮箱（逗号分隔）','text'],['Admin','本机管理令牌（无登录密码时必填）','password'],['Code','邮箱验证码','text']]){
   const wrap=document.createElement('label');wrap.textContent=label;const input=document.createElement('input');input.type=type;input.className='form-control';input.autocomplete=key==='Admin'?'off':key==='Code'?'one-time-code':'on';wrap.append(input);form.append(wrap);fields[key]=input;
  }
  const note=document.createElement('p');note.textContent='无管理密码时，令牌位于 --hub-state-file 路径加 .admin 后缀的本机文件中。';form.append(note);
  const status=document.createElement('p');status.setAttribute('role','status');form.append(status);
  const enroll=document.createElement('button');enroll.type='button';enroll.className='btn btn-primary';enroll.textContent='发送绑定验证码';form.append(enroll);
  const confirm=document.createElement('button');confirm.type='submit';confirm.className='btn btn-primary';confirm.textContent='确认绑定';form.append(confirm);
  const close=document.createElement('button');close.type='button';close.textContent='关闭';close.className='btn btn-secondary';close.onclick=()=>dialog.close();form.append(close);
  async function request(action:string){enroll.disabled=true;confirm.disabled=true;try{
   const body=Object.fromEntries(Object.entries(fields).filter(([k])=>k!=='Admin').map(([k,v])=>[k,v.value]));
   const response=await fetch(basePath+'-/hub',{method:'POST',headers:{...headers(),'X-GoTTY-Hub-Admin':fields.Admin.value},body:JSON.stringify({...body,Action:action})});
   if(!response.ok)throw new Error(await response.text());status.textContent=action==='enroll'?'验证码已发送，请查收邮箱。':'绑定成功，正在连接。';fields.Code.value='';void sync();
  }catch(e){status.textContent=e instanceof Error?e.message:String(e)}finally{enroll.disabled=false;confirm.disabled=false}}
  enroll.onclick=()=>request('enroll');form.onsubmit=e=>{e.preventDefault();void request('confirm')};dialog.onclose=()=>{fields.Admin.value='';dialog.remove()};document.body.append(dialog);dialog.showModal();
 };
 return()=>{if(timer)clearInterval(timer);button.remove()};
}
