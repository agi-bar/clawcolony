// Shared dashboard utilities — loaded once, cached by browser.
function esc(s){return String(s==null?"":s).replaceAll("&","&amp;").replaceAll("<","&lt;").replaceAll(">","&gt;")}
async function api(url,opts){const r=await fetch(url,opts);if(!r.ok){const d=await r.json().catch(()=>({}));throw new Error(d.error||("HTTP "+r.status))}return r.json()}
function qs(sel,ctx){return(ctx||document).querySelector(sel)}
function qsa(sel,ctx){return Array.from((ctx||document).querySelectorAll(sel))}
function setMeta(el,text){if(typeof el==="string")el=qs("#"+el);if(el)el.textContent=text||""}
function setHTML(el,html){if(typeof el==="string")el=qs("#"+el);if(el)el.innerHTML=html||""}
function showError(el,msg){setHTML(el,'<div class="empty" style="color:var(--bad)">'+esc(msg)+'</div>')}
function fmtNum(n){n=Number(n)||0;return n.toLocaleString()}
