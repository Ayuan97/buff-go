import{k as d,j as n,m as C,l as i,ay as y,az as _,d as P,u as R,p as c,s as j,q as $,v as M,h as a,f as k,aA as B}from"./index.107dabee.js";var I=d([n("list",`
 --n-merged-border-color: var(--n-border-color);
 --n-merged-color: var(--n-color)
 font-size: var(--n-font-size);
 transition:
 background-color .3s var(--n-bezier),
 color .3s var(--n-bezier),
 border-color .3s var(--n-bezier);
 padding: 0;
 list-style-type: none;
 color: var(--n-text-color);
 background-color: var(--n-merged-color);
 `,[C("bordered",`
 border-radius: var(--n-border-radius);
 border: 1px solid var(--n-merged-border-color);
 `,[n("list-item",`
 padding: 12px 20px;
 `,[d("&:not(:last-child)",`
 border-bottom: 1px solid var(--n-merged-border-color);
 `)]),i("header, footer",`
 padding: 12px 20px;
 `,[d("&:not(:last-child)",`
 border-bottom: 1px solid var(--n-merged-border-color);
 `)])]),i("header, footer",`
 padding: 12px 0;
 box-sizing: border-box;
 transition: border-color .3s var(--n-bezier);
 `,[d("&:not(:last-child)",`
 border-bottom: 1px solid var(--n-merged-border-color);
 `)]),n("list-item",`
 padding: 12px 0; 
 box-sizing: border-box;
 display: flex;
 flex-wrap: nowrap;
 align-items: center;
 transition: border-color .3s var(--n-bezier);
 `,[i("prefix",`
 margin-right: 20px;
 flex: 0;
 `),i("suffix",`
 margin-left: 20px;
 flex: 0;
 `),i("main",`
 flex: 1;
 `),d("&:not(:last-child)",`
 border-bottom: 1px solid var(--n-merged-border-color);
 `)])]),y(n("list",`
 --merged-color: var(--n-color-modal);
 --merged-border-color: var(--n-border-color-modal);
 `)),_(n("list",`
 --merged-color: var(--n-color-popover);
 --merged-border-color: var(--n-border-color-popover);
 `))]);const L=Object.assign(Object.assign({},c.props),{size:{type:String,default:"medium"},bordered:{type:Boolean,default:!1}}),O=k("n-list");var V=P({name:"List",props:L,setup(r){const{mergedClsPrefixRef:o,inlineThemeDisabled:e}=R(r),s=c("List","-list",I,B,r,o);j(O,{mergedClsPrefixRef:o});const t=$(()=>{const{common:{cubicBezierEaseInOut:b},self:{fontSize:m,textColor:p,color:v,colorModal:f,colorPopover:u,borderColor:g,borderColorModal:x,borderColorPopover:h,borderRadius:z}}=s.value;return{"--n-font-size":m,"--n-bezier":b,"--n-text-color":p,"--n-color":v,"--n-border-radius":z,"--n-border-color":g,"--n-border-color-modal":x,"--n-border-color-popover":h,"--n-color-modal":f,"--n-color-popover":u}}),l=e?M("list",void 0,t,r):void 0;return{mergedClsPrefix:o,cssVars:e?void 0:t,themeClass:l==null?void 0:l.themeClass,onRender:l==null?void 0:l.onRender}},render(){var r;const{$slots:o,mergedClsPrefix:e,onRender:s}=this;return s==null||s(),a("ul",{class:[`${e}-list`,this.bordered&&`${e}-list--bordered`,this.themeClass],style:this.cssVars},o.header?a("div",{class:`${e}-list__header`},o.header()):null,(r=o.default)===null||r===void 0?void 0:r.call(o),o.footer?a("div",{class:`${e}-list__footer`},o.footer()):null)}});export{V as _,O as l};
