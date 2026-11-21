import{a3 as re,p as Me,a as A,u as ze,j as c,c as Fe,f as w,i as F,d as s,e as Le,s as L,k as d,H as Te,I as _,J as M,g as t,t as j,q as Y,z as D,y as h,a4 as Pe,K as U,M as H,a5 as Ae,v as E,r as je,x as b,a6 as Ee,a7 as ne,a8 as Oe,a9 as Re,aa as de,C as I,A as Be,B as Ge,D as Ye}from"./index-B8JHdhSL.js";function De(k,r){const f=`# ${k||"Conversa AGY"}

`,v=r.map(p=>`### ${p.role==="user"?"Você":"AGY"}

${p.content.trim()}`).join(`

---

`);return f+v+`
`}function ie(k,r,f){const v=new Blob([r],{type:f}),p=URL.createObjectURL(v),l=document.createElement("a");l.href=p,l.download=k,document.body.appendChild(l),l.click(),document.body.removeChild(l),setTimeout(()=>URL.revokeObjectURL(p),1e3)}const se={sky:{darkSolid:"#0284c7",darkFg:"#38bdf8",lightSolid:"#0284c7",lightFg:"#0284c7"},emerald:{darkSolid:"#059669",darkFg:"#34d399",lightSolid:"#059669",lightFg:"#059669"},violet:{darkSolid:"#7c3aed",darkFg:"#a78bfa",lightSolid:"#7c3aed",lightFg:"#7c3aed"},indigo:{darkSolid:"#4f46e5",darkFg:"#818cf8",lightSolid:"#4f46e5",lightFg:"#4f46e5"},amber:{darkSolid:"#d97706",darkFg:"#fbbf24",lightSolid:"#d97706",lightFg:"#b45309"},rose:{darkSolid:"#e11d48",darkFg:"#fb7185",lightSolid:"#e11d48",lightFg:"#e11d48"}};function Ue(k,r,f,v,p){const l=se[p]??se.sky,u=v!=="light",$=u?l.darkSolid:l.lightSolid,g=u?l.darkFg:l.lightFg,y=u?"--bg-app:#09090b;--bg-card:#18181b;--code-bg:#0e0e12;--code-hdr:#16161c;--inline-bg:#27272a;--border:#27272a;--text:#f4f4f5;--muted:#a1a1aa;--tok-kw:#c084fc;--tok-str:#86efac;--tok-num:#fbbf24;--tok-cmt:#71717a;--tok-fn:#60a5fa;--tok-type:#2dd4bf;--tok-attr:#7dd3fc;--tok-var:#f472b6;":"--bg-app:#f4f5f7;--bg-card:#ffffff;--code-bg:#f8fafc;--code-hdr:#eef2f6;--inline-bg:#e8edf3;--border:#e4e4e7;--text:#18181b;--muted:#52525b;--tok-kw:#7c3aed;--tok-str:#059669;--tok-num:#b45309;--tok-cmt:#64748b;--tok-fn:#2563eb;--tok-type:#0f766e;--tok-attr:#0284c7;--tok-var:#be185d;",m=JSON.stringify(f).replace(/</g,"\\u003c"),x=re(k||"Snapshot AGY"),T=re(new Date().toLocaleString("pt-BR"));return`<!DOCTYPE html>
<html lang="pt-BR">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>${x} — AGY Snapshot</title>
  <style>
    :root{${y}--accent:${$};--accent-fg:${g};}
    *{box-sizing:border-box}
    body{margin:0;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;background:var(--bg-app);color:var(--text);line-height:1.7;padding:24px 16px 48px}
    .wrap{max-width:820px;margin:0 auto}
    .topbar{display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:12px;margin-bottom:18px;padding-bottom:14px;border-bottom:1px solid var(--border)}
    .badge{display:inline-block;font-size:11px;font-weight:600;padding:2px 9px;border-radius:999px;background:color-mix(in srgb,var(--accent) 16%,transparent);color:var(--accent-fg);border:1px solid color-mix(in srgb,var(--accent-fg) 35%,transparent)}
    h1.doc-title{font-size:1.15rem;margin:6px 0 2px}
    .meta{font-size:12px;color:var(--muted)}
    .card{background:var(--bg-card);border:1px solid var(--border);border-radius:16px;padding:22px 24px;overflow-wrap:anywhere}
    .btn{background:var(--bg-card);color:var(--text);border:1px solid var(--border);border-radius:8px;padding:6px 12px;font-size:12px;cursor:pointer;font-weight:500}
    .btn:hover{border-color:var(--accent-fg);color:var(--accent-fg)}
    .card h1,.card h2,.card h3,.card h4{margin:1.3em 0 0.5em;line-height:1.35}
    .card>:first-child{margin-top:0}.card>:last-child{margin-bottom:0}
    .card p{margin:0 0 0.85em}
    .card hr{border:none;height:1px;background:var(--border);margin:1.35em 0}
    .card ul,.card ol{padding-left:1.4em;margin:0.6em 0 0.9em}.card li{margin-bottom:0.4em}
    .card blockquote{margin:0.9em 0;padding:8px 14px;border-left:3px solid var(--accent-fg);background:color-mix(in srgb,var(--accent) 10%,transparent);border-radius:0 8px 8px 0}
    .card code{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:0.84em;background:var(--inline-bg);border:1px solid var(--border);padding:1px 6px;border-radius:6px}
    .code-block{margin:0.95em 0;border:1px solid var(--border);border-radius:12px;background:var(--code-bg);overflow:hidden}
    .code-block-header{display:flex;align-items:center;justify-content:space-between;padding:6px 12px;background:var(--code-hdr);border-bottom:1px solid var(--border);font-family:ui-monospace,monospace;font-size:11px;color:var(--muted);font-weight:600}
    .code-copy-btn{display:inline-flex;align-items:center;gap:4px;background:var(--bg-card);color:var(--text);border:1px solid var(--border);border-radius:6px;padding:3px 8px;font-size:11px;cursor:pointer}
    .code-block pre{margin:0;padding:14px 16px;overflow-x:auto;font-size:13px;line-height:1.65;white-space:pre}
    .code-block pre code{background:transparent;border:none;padding:0;font-size:inherit}
    .tok-kw{color:var(--tok-kw);font-weight:500}.tok-str{color:var(--tok-str)}.tok-num{color:var(--tok-num)}.tok-cmt{color:var(--tok-cmt);font-style:italic}.tok-fn{color:var(--tok-fn)}.tok-type{color:var(--tok-type)}.tok-attr{color:var(--tok-attr)}.tok-var{color:var(--tok-var)}
    table{width:100%;border-collapse:collapse;margin:1em 0;font-size:0.84rem;display:block;overflow-x:auto}
    th,td{border:1px solid var(--border);padding:6px 10px;text-align:left;white-space:nowrap}
    th{background:var(--code-hdr)}
    a{color:var(--accent-fg)}
  </style>
</head>
<body>
  <div class="wrap">
    <div class="topbar">
      <div>
        <span class="badge">AGY Snapshot Offline</span>
        <h1 class="doc-title">${x}</h1>
        <div class="meta">Gerado em ${T}</div>
      </div>
      <button type="button" class="btn" onclick="copyMd(this)">Copiar Markdown</button>
    </div>
    <article class="card">${r}</article>
  </div>
  <script type="application/json" id="raw-md">${m}<\/script>
  <script>
    function copyStr(txt,btn,orig){
      if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(txt).then(done).catch(fb)}else{fb()}
      function fb(){var t=document.createElement('textarea');t.value=txt;t.style.position='fixed';t.style.opacity='0';document.body.appendChild(t);t.select();document.execCommand('copy');document.body.removeChild(t);done()}
      function done(){btn.textContent='Copiado';setTimeout(function(){btn.textContent=orig},1500)}
    }
    document.addEventListener('click',function(e){
      var btn=e.target.closest&&e.target.closest('button[data-copy-code]');
      if(!btn)return;
      var block=btn.closest('.code-block');
      var code=block&&block.querySelector('pre code');
      if(code)copyStr(code.textContent||'',btn,'Copiar');
    });
    function copyMd(btn){copyStr(JSON.parse(document.getElementById('raw-md').textContent),btn,'Copiar Markdown')}
  <\/script>
</body>
</html>`}var He=b("<!> <!>",1),Ie=b("<!> <span>Baixar HTML Formatado</span>",1),Je=b("<!> <span>Baixar Markdown (.md)</span>",1),qe=b("<!> <span>Copiar Markdown</span>",1),Ne=b("<!> <span> </span>",1),Ve=b('<div class="p-2.5 rounded-xl bg-muted border border-[var(--accent-border)] space-y-1.5"><div class="text-[11px] font-medium text-muted-foreground">Link somente leitura (sem acesso à API/terminal):</div> <div class="flex items-center gap-2"><input type="text" readonly="" class="flex-1 bg-transparent text-xs font-mono text-accent-foreground focus:outline-none truncate"/> <!></div></div>'),Ke=b('<p class="text-xs text-rose-400"> </p>'),Qe=b('<div class="p-2 rounded-xl bg-emerald-500/15 border border-emerald-500/35 text-emerald-500 text-xs flex items-center justify-center gap-1.5"><!> <span> </span></div>'),We=b('<div class="flex items-center justify-between gap-2"><div class="flex items-center gap-2 min-w-0"><!> <h2 class="text-sm font-bold text-foreground truncate">Compartilhar Snapshot</h2></div> <!></div> <!> <div class="grid grid-cols-1 sm:grid-cols-2 gap-2"><!> <!> <!> <!></div> <!> <!> <!>',1),Xe=b('<div class="fixed inset-0 bg-[var(--bg-backdrop)] backdrop-blur-sm z-50 flex items-end sm:items-center justify-center p-0 sm:p-4"><button type="button" aria-label="Fechar compartilhamento" class="absolute inset-0 cursor-default"></button> <div class="relative w-full sm:max-w-md"><!></div></div>');function tt(k,r){Me(r,!0);let f=A(r,"open",15,!1),v=A(r,"title",3,"Snapshot AGY"),p=A(r,"singleMarkdown",3,""),l=A(r,"messages",19,()=>[]),u=L("response"),$=L(!1),g=L(""),y=L(""),m=L("");ze(()=>{f()&&(c(u,p().trim()?"response":"conversation",!0),c(g,""),c(y,""),c(m,""))});let x=U(()=>t(u)==="response"&&p().trim()?p():De(v(),l())),T=U(()=>(v()||"agy-snapshot").toLowerCase().replace(/[^a-z0-9]+/g,"-").replace(/^-|-$/g,"").slice(0,42)||"agy-snapshot");function P(i){c(m,i,!0),setTimeout(()=>{t(m)===i&&c(m,"")},2e3)}function le(){const i=Ee(t(x)),z=Ue(v(),i,t(x),r.themeMode,r.themePalette);ie(`${t(T)}.html`,z,"text/html;charset=utf-8"),P("HTML offline baixado")}function ce(){ie(`${t(T)}.md`,t(x),"text/markdown;charset=utf-8"),P("Markdown baixado")}async function pe(){await ne(t(x))&&P("Markdown copiado")}async function fe(){c($,!0),c(y,"");try{const i=await Oe({title:v()||"Snapshot AGY",content_md:t(x),theme_mode:r.themeMode,theme_palette:r.themePalette});c(g,`${window.location.origin}${i.url}`),await ne(t(g)),P("Link somente leitura copiado")}catch(i){c(y,(i==null?void 0:i.message)||"Erro ao gerar link",!0)}finally{c($,!1)}}var J=Fe(),ve=w(J);{var be=i=>{var z=Xe(),q=h(z),N=d(q,2),ge=h(N);Te(ge,{class:"rounded-t-2xl sm:rounded-2xl p-4 space-y-4 shadow-2xl",children:(ue,Ze)=>{var V=We(),K=w(V),Q=h(K),me=h(Q);_(me,{name:"share",size:17,class:"text-accent-foreground"});var xe=d(Q,2);M(xe,{variant:"ghost",size:"icon-sm",onclick:()=>f(!1),children:(e,a)=>{_(e,{name:"close",size:17})},$$slots:{default:!0}});var W=d(K,2);{var he=e=>{Pe(e,{get value(){return t(u)},onValueChange:a=>{c(u,a==="conversation"?"conversation":"response",!0),c(g,"")},children:(a,o)=>{Re(a,{class:"grid grid-cols-2 w-full",children:(n,S)=>{var C=He(),O=w(C);de(O,{value:"response",children:(B,Ce)=>{var G=H("Esta resposta");s(B,G)},$$slots:{default:!0}});var R=d(O,2);de(R,{value:"conversation",children:(B,Ce)=>{var G=H("Conversa toda");s(B,G)},$$slots:{default:!0}}),s(n,C)},$$slots:{default:!0}})},$$slots:{default:!0}})},ke=U(()=>p().trim()&&l().length>0);F(W,e=>{t(ke)&&e(he)})}var X=d(W,2),Z=h(X);M(Z,{variant:"default",onclick:le,class:"h-10 rounded-xl",children:(e,a)=>{var o=Ie(),n=w(o);_(n,{name:"download",size:15}),s(e,o)},$$slots:{default:!0}});var ee=d(Z,2);M(ee,{variant:"outline",onclick:ce,class:"h-10 rounded-xl",children:(e,a)=>{var o=Je(),n=w(o);_(n,{name:"file",size:15}),s(e,o)},$$slots:{default:!0}});var te=d(ee,2);M(te,{variant:"outline",onclick:pe,class:"h-10 rounded-xl",children:(e,a)=>{var o=qe(),n=w(o);_(n,{name:"clipboard",size:15}),s(e,o)},$$slots:{default:!0}});var ye=d(te,2);M(ye,{variant:"outline",onclick:fe,get disabled(){return t($)},class:"h-10 rounded-xl",children:(e,a)=>{var o=Ne(),n=w(o);_(n,{name:"link",size:15});var S=d(n,2),C=D(S,!0);j(()=>Y(C,t($)?"Gerando...":"Link Local (/s/...)")),s(e,o)},$$slots:{default:!0}});var ae=d(X,2);{var we=e=>{var a=Ve(),o=d(h(a),2),n=h(o),S=d(n,2);M(S,{get href(){return t(g)},target:"_blank",rel:"noopener noreferrer",size:"xs",children:(C,O)=>{var R=H("Abrir");s(C,R)},$$slots:{default:!0}}),j(()=>Ae(n,t(g))),E(3,a,()=>I,()=>({duration:140})),s(e,a)};F(ae,e=>{t(g)&&e(we)})}var oe=d(ae,2);{var _e=e=>{var a=Ke(),o=D(a,!0);j(()=>Y(o,t(y))),s(e,a)};F(oe,e=>{t(y)&&e(_e)})}var $e=d(oe,2);{var Se=e=>{var a=Qe(),o=h(a);_(o,{name:"check",size:14});var n=d(o,2),S=D(n,!0);j(()=>Y(S,t(m))),E(3,a,()=>I,()=>({duration:140})),s(e,a)};F($e,e=>{t(m)&&e(Se)})}s(ue,V)},$$slots:{default:!0}}),je("click",q,()=>f(!1)),E(3,N,()=>Ge,()=>({y:20,duration:200,easing:Be})),E(3,z,()=>I,()=>({duration:150})),s(i,z)};F(ve,i=>{f()&&i(be)})}s(k,J),Le()}Ye(["click"]);export{tt as default};
