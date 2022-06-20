import{ai as G,aj as ye,ak as Ce,al as Be,r as E,a as Re,am as Se,an as Ve,j as Q,l as t,ao as Z,k as j,m,ap as J,d as N,u as Me,p as ne,G as $e,t as ze,o as Le,q as z,aq as p,ar as U,as as h,v as Fe,at as H,h as o,au as _,av as Te,aw as Ee,F as q,M as O,O as ae,P as X,Q as Oe,a7 as Pe,T as Ae,ab as ee,W as L,V as F,U as K,a0 as Ne,Z as De,$ as We,a3 as Ie,x as je,ax as Ue}from"./index.d4841130.js";let T=0;const He=typeof window!="undefined"&&window.matchMedia!==void 0,x=E(null);let c,k;function P(e){e.matches&&(x.value="dark")}function A(e){e.matches&&(x.value="light")}function qe(){c=window.matchMedia("(prefers-color-scheme: dark)"),k=window.matchMedia("(prefers-color-scheme: light)"),c.matches?x.value="dark":k.matches?x.value="light":x.value=null,c.addEventListener?(c.addEventListener("change",P),k.addEventListener("change",A)):c.addListener&&(c.addListener(P),k.addListener(A))}function Ke(){"removeEventListener"in c?(c.removeEventListener("change",P),k.removeEventListener("change",A)):"removeListener"in c&&(c.removeListener(P),k.removeListener(A)),c=void 0,k=void 0}let te=!0;function Xe(){return He?(T===0&&qe(),te&&(te=ye())&&(Ce(()=>{T+=1}),Be(()=>{T-=1,T===0&&Ke()})),G(x)):G(x)}const Ye=e=>{const{primaryColor:l,opacityDisabled:i,borderRadius:r,textColor3:s}=e,f="rgba(0, 0, 0, .14)";return Object.assign(Object.assign({},Se),{iconColor:s,textColor:"white",loadingColor:l,opacityDisabled:i,railColor:f,railColorActive:l,buttonBoxShadow:"0 1px 4px 0 rgba(0, 0, 0, 0.3), inset 0 0 1px 0 rgba(0, 0, 0, 0.05)",buttonColor:"#FFF",railBorderRadiusSmall:r,railBorderRadiusMedium:r,railBorderRadiusLarge:r,buttonBorderRadiusSmall:r,buttonBorderRadiusMedium:r,buttonBorderRadiusLarge:r,boxShadowFocus:`0 0 0 2px ${Ve(l,{alpha:.2})}`})},Ge={name:"Switch",common:Re,self:Ye};var Qe=Ge,Ze=Q("switch",`
 height: var(--n-height);
 min-width: var(--n-width);
 vertical-align: middle;
 user-select: none;
 display: inline-flex;
 outline: none;
 justify-content: center;
 align-items: center;
`,[t("children-placeholder",`
 height: var(--n-rail-height);
 display: flex;
 flex-direction: column;
 overflow: hidden;
 pointer-events: none;
 visibility: hidden;
 `),t("rail-placeholder",`
 display: flex;
 flex-wrap: none;
 `),t("button-placeholder",`
 width: calc(1.75 * var(--n-rail-height));
 height: var(--n-rail-height);
 `),Q("base-loading",`
 position: absolute;
 top: 50%;
 left: 50%;
 transform: translateX(-50%) translateY(-50%);
 font-size: calc(var(--n-button-width) - 4px);
 color: var(--n-loading-color);
 transition: color .3s var(--n-bezier);
 `,[Z({originalTransform:"translateX(-50%) translateY(-50%)"})]),t("checked, unchecked",`
 transition: color .3s var(--n-bezier);
 color: var(--n-text-color);
 box-sizing: border-box;
 position: absolute;
 white-space: nowrap;
 top: 0;
 bottom: 0;
 display: flex;
 align-items: center;
 line-height: 1;
 `),t("checked",`
 right: 0;
 padding-right: calc(1.25 * var(--n-rail-height) - var(--n-offset));
 `),t("unchecked",`
 left: 0;
 justify-content: flex-end;
 padding-left: calc(1.25 * var(--n-rail-height) - var(--n-offset));
 `),j("&:focus",[t("rail",`
 box-shadow: var(--n-box-shadow-focus);
 `)]),m("round",[t("rail",{borderRadius:"calc(var(--n-rail-height) / 2)"},[t("button",{borderRadius:"calc(var(--n-button-height) / 2)"})])]),J("disabled",[J("icon",[m("pressed",[t("rail",[t("button",{maxWidth:"var(--n-button-width-pressed)"})])]),t("rail",[j("&:active",[t("button",{maxWidth:"var(--n-button-width-pressed)"})])]),m("active",[m("pressed",[t("rail",[t("button",{left:"calc(100% - var(--n-offset) - var(--n-button-width-pressed))"})])]),t("rail",[j("&:active",[t("button",{left:"calc(100% - var(--n-offset) - var(--n-button-width-pressed))"})])])])])]),m("active",[t("rail",[t("button",{left:"calc(100% - (var(--n-rail-height) + var(--n-button-width)) / 2)"})])]),t("rail",`
 overflow: hidden;
 height: var(--n-rail-height);
 min-width: var(--n-rail-width);
 border-radius: var(--n-rail-border-radius);
 cursor: pointer;
 position: relative;
 transition:
 background .3s var(--n-bezier),
 box-shadow .3s var(--n-bezier);
 background-color: var(--n-rail-color);
 `,[t("button-icon",`
 color: var(--n-icon-color);
 transition: color .3s var(--n-bezier);
 font-size: calc(var(--n-button-height) - 4px);
 position: absolute;
 left: 0;
 right: 0;
 top: 0;
 bottom: 0;
 display: flex;
 justify-content: center;
 align-items: center;
 line-height: 1;
 `,[Z()]),t("button",`
 align-items: center; 
 top: var(--n-offset);
 left: var(--n-offset);
 height: var(--n-button-width);
 width: var(--n-button-width-pressed);
 max-width: var(--n-button-width);
 border-radius: var(--n-button-border-radius);
 background-color: var(--n-button-color);
 box-shadow: var(--n-button-box-shadow);
 box-sizing: border-box;
 cursor: inherit;
 content: "";
 position: absolute;
 transition:
 background-color .3s var(--n-bezier),
 left .3s var(--n-bezier),
 opacity .3s var(--n-bezier),
 max-width .3s var(--n-bezier),
 box-shadow .3s var(--n-bezier);
 `)]),m("active",[t("rail","background-color: var(--n-rail-color-active);")]),m("disabled",[t("rail",`
 cursor: not-allowed;
 opacity: .5;
 `)]),m("loading",[t("rail",`
 pointer-events: none;
 `)])]);const Je=Object.assign(Object.assign({},ne.props),{size:{type:String,default:"medium"},value:{type:[String,Number,Boolean],default:void 0},loading:Boolean,defaultValue:{type:[String,Number,Boolean],default:!1},disabled:{type:Boolean,default:void 0},round:{type:Boolean,default:!0},"onUpdate:value":[Function,Array],onUpdateValue:[Function,Array],checkedValue:{type:[String,Number,Boolean],default:!0},uncheckedValue:{type:[String,Number,Boolean],default:!1},railStyle:Function,onChange:[Function,Array]});var et=N({name:"Switch",props:Je,setup(e){const{mergedClsPrefixRef:l,inlineThemeDisabled:i}=Me(e),r=ne("Switch","-switch",Ze,Qe,e,l),s=$e(e),{mergedSizeRef:f,mergedDisabledRef:d}=s,y=E(e.defaultValue),C=ze(e,"value"),u=Le(C,y),w=z(()=>u.value===e.checkedValue),v=E(!1),a=E(!1),g=z(()=>{const{railStyle:n}=e;if(!!n)return n({focused:a.value,checked:w.value})});function b(n){const{"onUpdate:value":V,onChange:M,onUpdateValue:$}=e,{nTriggerFormInput:D,nTriggerFormChange:W}=s;V&&q(V,n),$&&q($,n),M&&q(M,n),y.value=n,D(),W()}function oe(){const{nTriggerFormFocus:n}=s;n()}function ie(){const{nTriggerFormBlur:n}=s;n()}function re(){e.loading||d.value||(u.value!==e.checkedValue?b(e.checkedValue):b(e.uncheckedValue))}function se(){a.value=!0,oe()}function le(){a.value=!1,ie(),v.value=!1}function ce(n){e.loading||d.value||n.code==="Space"&&(u.value!==e.checkedValue?b(e.checkedValue):b(e.uncheckedValue),v.value=!1)}function de(n){e.loading||d.value||n.code==="Space"&&(n.preventDefault(),v.value=!0)}const Y=z(()=>{const{value:n}=f,{self:{opacityDisabled:V,railColor:M,railColorActive:$,buttonBoxShadow:D,buttonColor:W,boxShadowFocus:ue,loadingColor:he,textColor:fe,iconColor:ve,[p("buttonHeight",n)]:R,[p("buttonWidth",n)]:ge,[p("buttonWidthPressed",n)]:be,[p("railHeight",n)]:S,[p("railWidth",n)]:I,[p("railBorderRadius",n)]:me,[p("buttonBorderRadius",n)]:we},common:{cubicBezierEaseInOut:pe}}=r.value,_e=U((h(S)-h(R))/2),ke=U(Math.max(h(S),h(R))),xe=h(S)>h(R)?I:U(h(I)+h(R)-h(S));return{"--n-bezier":pe,"--n-button-border-radius":we,"--n-button-box-shadow":D,"--n-button-color":W,"--n-button-width":ge,"--n-button-width-pressed":be,"--n-button-height":R,"--n-height":ke,"--n-offset":_e,"--n-opacity-disabled":V,"--n-rail-border-radius":me,"--n-rail-color":M,"--n-rail-color-active":$,"--n-rail-height":S,"--n-rail-width":I,"--n-width":xe,"--n-box-shadow-focus":ue,"--n-loading-color":he,"--n-text-color":fe,"--n-icon-color":ve}}),B=i?Fe("switch",z(()=>f.value[0]),Y,e):void 0;return{handleClick:re,handleBlur:le,handleFocus:se,handleKeyup:ce,handleKeydown:de,mergedRailStyle:g,pressed:v,mergedClsPrefix:l,mergedValue:u,checked:w,mergedDisabled:d,cssVars:i?void 0:Y,themeClass:B==null?void 0:B.themeClass,onRender:B==null?void 0:B.onRender}},render(){const{mergedClsPrefix:e,mergedDisabled:l,checked:i,mergedRailStyle:r,onRender:s,$slots:f}=this;s==null||s();const{checked:d,unchecked:y,icon:C,"checked-icon":u,"unchecked-icon":w}=f,v=!(H(C)&&H(u)&&H(w));return o("div",{role:"switch","aria-checked":i,class:[`${e}-switch`,this.themeClass,v&&`${e}-switch--icon`,i&&`${e}-switch--active`,l&&`${e}-switch--disabled`,this.round&&`${e}-switch--round`,this.loading&&`${e}-switch--loading`,this.pressed&&`${e}-switch--pressed`],tabindex:this.mergedDisabled?void 0:0,style:this.cssVars,onClick:this.handleClick,onFocus:this.handleFocus,onBlur:this.handleBlur,onKeyup:this.handleKeyup,onKeydown:this.handleKeydown},o("div",{class:`${e}-switch__rail`,"aria-hidden":"true",style:r},_(d,a=>_(y,g=>a||g?o("div",{"aria-hidden":!0,class:`${e}-switch__children-placeholder`},o("div",{class:`${e}-switch__rail-placeholder`},o("div",{class:`${e}-switch__button-placeholder`}),a),o("div",{class:`${e}-switch__rail-placeholder`},o("div",{class:`${e}-switch__button-placeholder`}),g)):null)),o("div",{class:`${e}-switch__button`},_(C,a=>_(u,g=>_(w,b=>o(Te,null,{default:()=>this.loading?o(Ee,{key:"loading",clsPrefix:e,strokeWidth:20}):this.checked&&(g||a)?o("div",{class:`${e}-switch__button-icon`,key:g?"checked-icon":"icon"},g||a):!this.checked&&(b||a)?o("div",{class:`${e}-switch__button-icon`,key:b?"unchecked-icon":"icon"},b||a):null})))),_(d,a=>a&&o("div",{key:"checked",class:`${e}-switch__checked`},a)),_(y,a=>a&&o("div",{key:"unchecked",class:`${e}-switch__unchecked`},a)))))}});const tt={xmlns:"http://www.w3.org/2000/svg","xmlns:xlink":"http://www.w3.org/1999/xlink",viewBox:"0 0 24 24"},nt=X("path",{d:"M14.71 6.71a.996.996 0 0 0-1.41 0L8.71 11.3a.996.996 0 0 0 0 1.41l4.59 4.59a.996.996 0 1 0 1.41-1.41L10.83 12l3.88-3.88c.39-.39.38-1.03 0-1.41z",fill:"currentColor"},null,-1),at=[nt];var ot=N({name:"ChevronLeftRound",render:function(l,i){return O(),ae("svg",tt,at)}});const it={xmlns:"http://www.w3.org/2000/svg","xmlns:xlink":"http://www.w3.org/1999/xlink",viewBox:"0 0 24 24"},rt=X("path",{d:"M9.37 5.51A7.35 7.35 0 0 0 9.1 7.5c0 4.08 3.32 7.4 7.4 7.4c.68 0 1.35-.09 1.99-.27A7.014 7.014 0 0 1 12 19c-3.86 0-7-3.14-7-7c0-2.93 1.81-5.45 4.37-6.49zM12 3a9 9 0 1 0 9 9c0-.46-.04-.92-.1-1.36a5.389 5.389 0 0 1-4.4 2.26a5.403 5.403 0 0 1-3.14-9.8c-.44-.06-.9-.1-1.36-.1z",fill:"currentColor"},null,-1),st=[rt];var lt=N({name:"DarkModeOutlined",render:function(l,i){return O(),ae("svg",it,st)}});const ct={class:"navbar"},ut=N({props:{title:{default:""},back:{type:Boolean,default:!1}},setup(e){const l=e,i=Oe(),r=Pe(),s=d=>{d?(localStorage.setItem("PAOPAO_THEME","dark"),i.commit("triggerTheme","dark")):(localStorage.setItem("PAOPAO_THEME","light"),i.commit("triggerTheme","light"))},f=()=>{window.history.length<=1?r.push({path:"/"}):r.go(-1)};return Ae(()=>{localStorage.getItem("PAOPAO_THEME")||s(Xe()==="dark")}),(d,y)=>{const C=Ie,u=je,w=et,v=Ue;return O(),ee(v,{size:"small",bordered:!0,class:"nav-title-card"},{header:L(()=>[X("div",ct,[e.back?(O(),ee(u,{key:0,class:"back-btn",onClick:f,quaternary:"",circle:"",size:"small"},{icon:L(()=>[F(C,null,{default:L(()=>[F(K(ot))]),_:1})]),_:1})):Ne("",!0),De(" "+We(l.title)+" ",1),F(w,{value:K(i).state.theme==="dark","onUpdate:value":s,size:"small",class:"theme-switch-wrap"},{icon:L(()=>[F(K(lt))]),_:1},8,["value"])])]),_:1})}}});export{ut as _};
