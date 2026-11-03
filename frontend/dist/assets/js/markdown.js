!function(){"use strict";
marked.setOptions({gfm:!0,breaks:!0});
const LANG_LABELS={javascript:"JavaScript",js:"JavaScript",typescript:"TypeScript",ts:"TypeScript",python:"Python",py:"Python",go:"Go",golang:"Go",rust:"Rust",rs:"Rust",java:"Java",kotlin:"Kotlin",swift:"Swift",c:"C",cpp:"C++",csharp:"C#",cs:"C#",php:"PHP",ruby:"Ruby",bash:"Shell",sh:"Shell",shell:"Shell",zsh:"Shell",powershell:"PowerShell",ps1:"PowerShell",json:"JSON",yaml:"YAML",yml:"YAML",toml:"TOML",xml:"XML",html:"HTML",css:"CSS",scss:"SCSS",sql:"SQL",dockerfile:"Dockerfile",docker:"Dockerfile",makefile:"Makefile",ini:"INI",diff:"Diff",markdown:"Markdown",md:"Markdown",lua:"Lua",dart:"Dart"};
const AUTO_LANGS=["javascript","typescript","python","go","bash","json","yaml","html","css","scss","sql","rust","java","cpp","c","php","ruby","swift","kotlin","toml","xml","markdown","lua","dart"];
const HL_AUTO_MAX=8e3;
const HL_LINE_MAX=2e3;

function escHtml(s){return String(s).replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;")}

function wrapCodeLines(html){
  if(!html||html.length>HL_LINE_MAX*40)return html;
  const lines=html.split("\n"),openSpans=[];
  const spanRe=/<span [^>]*>|<\/span>/g;
  return lines.map(line=>{
    const prefix=openSpans.join("");
    let m;spanRe.lastIndex=0;
    while(null!==(m=spanRe.exec(line)))"</span>"===m[0]?openSpans.pop():openSpans.push(m[0]);
    return'<span class="code-line">'+prefix+(line||" ")+"</span>"
  }).join("\n")
}

function enhanceCodeBlock(preEl){
  const codeEl=preEl.querySelector("code");
  if(!codeEl)return;
  let lang="";
  const m=codeEl.className.match(/language-([\w+-]+)/);
  if(m)lang=m[1].toLowerCase();
  const raw=codeEl.textContent;
  let highlighted="";
  try{
    if(lang&&hljs.getLanguage(lang)){
      highlighted=hljs.highlight(raw,{language:lang,ignoreIllegals:!0}).value
    }else if(raw.length<HL_AUTO_MAX){
      const auto=hljs.highlightAuto(raw,AUTO_LANGS);
      highlighted=auto.value;
      if(!lang&&auto.language)lang=auto.language
    }else{
      highlighted=escHtml(raw)
    }
  }catch(e){
    highlighted=escHtml(raw)
  }
  const wrap=document.createElement("div");
  wrap.className="codeblock";
  wrap.setAttribute("dir","ltr");
  const head=document.createElement("div");
  head.className="codeblock-head";
  const langSpan=document.createElement("span");
  langSpan.className="codeblock-lang";
  langSpan.textContent=(LANG_LABELS[lang]||lang||"code").toUpperCase();
  const copyBtn=document.createElement("button");
  copyBtn.className="codeblock-copy";
  copyBtn.type="button";
  copyBtn.innerHTML=Icons.get("copy",14)+"<span>کپی</span>";
  copyBtn.setAttribute("dir","rtl");
  copyBtn.addEventListener("click",()=>{
    copyText(raw).then(()=>{
      copyBtn.classList.add("done");
      copyBtn.innerHTML=Icons.get("check",14)+"<span>کپی شد</span>";
      setTimeout(()=>{
        copyBtn.classList.remove("done");
        copyBtn.innerHTML=Icons.get("copy",14)+"<span>کپی</span>"
      },1600)
    })
  });
  head.appendChild(langSpan);
  head.appendChild(copyBtn);
  const pre=document.createElement("pre");
  const code=document.createElement("code");
  code.innerHTML=wrapCodeLines(highlighted);
  pre.appendChild(code);
  wrap.appendChild(head);
  wrap.appendChild(pre);
  preEl.replaceWith(wrap)
}

function copyText(text){
  if(navigator.clipboard&&!1!==window.isSecureContext)
    return navigator.clipboard.writeText(text).catch(()=>fallbackCopy(text));
  return Promise.resolve(fallbackCopy(text))
}
function fallbackCopy(text){
  const ta=document.createElement("textarea");
  ta.value=text;
  ta.style.cssText="position:fixed;opacity:0;top:0;left:0";
  document.body.appendChild(ta);
  ta.select();
  try{document.execCommand("copy")}catch(e){}
  ta.remove()
}

/**
 * renderMarkdown(el, text, opts)
 * opts.stream  — lightweight path for live streaming (no highlight, less DOM work)
 * opts.skipHighlight — skip hljs only
 */
window.MyAIMD={
  renderMarkdown:function(el,text,opts){
    opts=opts||{};
    const isStream=!!opts.stream;
    const skipHL=isStream||!!opts.skipHighlight;
    let html="";
    try{html=marked.parse(text||"")}catch(e){html="<p>"+escHtml(text||"")+"</p>"}
    const safe=DOMPurify.sanitize(html,{
      ADD_ATTR:["target"],
      FORBID_TAGS:["style","form","iframe"],
      FORBID_ATTR:["onerror","onload","onclick","onmouseover","onfocus","onblur","onsubmit","oninput","onchange","onkeydown","onkeyup","onkeypress"]
    });
    el.classList.add("md");
    el.innerHTML=safe;

    el.querySelectorAll("a[href]").forEach(a=>{
      a.setAttribute("target","_blank");
      a.setAttribute("rel","noopener noreferrer");
      a.setAttribute("dir","ltr")
    });

    if(!isStream){
      el.querySelectorAll("table").forEach(table=>{
        if(table.parentElement&&table.parentElement.classList.contains("table-wrap"))return;
        const wrap=document.createElement("div");
        wrap.className="table-wrap";
        table.replaceWith(wrap);
        wrap.appendChild(table)
      })
    }

    if(!skipHL){
      el.querySelectorAll("pre").forEach(enhanceCodeBlock)
    }else{
      el.querySelectorAll("pre").forEach(pre=>{
        pre.setAttribute("dir","ltr");
        if(!pre.querySelector("code")&&pre.textContent){
          const code=document.createElement("code");
          code.textContent=pre.textContent;
          pre.textContent="";
          pre.appendChild(code)
        }
      })
    }
  },
  copyText:copyText
}
}();
