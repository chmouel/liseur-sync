var up=Object.create;var fn=Object.defineProperty;var mp=Object.getOwnPropertyDescriptor;var pp=Object.getOwnPropertyNames;var gp=Object.getPrototypeOf,fp=Object.prototype.hasOwnProperty;var Me=(i,e,t)=>()=>{if(t)throw t[0];try{return i&&(e=i(i=0)),e}catch(o){throw t=[o],o}};var ri=(i,e)=>()=>{try{return e||i((e={exports:{}}).exports,e),e.exports}catch(t){throw e=0,t}},_=(i,e)=>{for(var t in e)fn(i,t,{get:e[t],enumerable:!0})},yp=(i,e,t,o)=>{if(e&&typeof e=="object"||typeof e=="function")for(let r of pp(e))!fp.call(i,r)&&r!==t&&fn(i,r,{get:()=>e[r],enumerable:!(o=mp(e,r))||o.enumerable});return i};var Sp=(i,e,t)=>(t=i!=null?up(gp(i)):{},yp(e||!i||!i.__esModule?fn(t,"default",{value:i,enumerable:!0}):t,i));var rd={};_(rd,{default:()=>Rg});var Rg,nd=Me(()=>{Rg=`/*!
 * Readium CSS v.2.0.5
 * Copyright (c) 2017\u20132026. Readium Foundation. All rights reserved.
 * Use of this source code is governed by a BSD-style license which is detailed in the
 * LICENSE file present in the project repository where this source code is maintained.
 * Core maintainer: Jiminy Panoz <jiminy.panoz@edrlab.org> 
 * Contributors: 
 * Daniel Weck
 * Hadrien Gardeur
 * Innovimax
 * L. Le Meur
 * Mickae\u0308l Menu
 * k_taka
 */

@namespace url("http://www.w3.org/1999/xhtml");

@namespace epub url("http://www.idpf.org/2007/ops");

@namespace m url("http://www.w3.org/1998/Math/MathML");

@namespace svg url("http://www.w3.org/2000/svg");

@-ms-viewport{
  width:device-width;
}

@viewport{
  width:device-width;
  zoom:1;
}

:root{

  --RS__sans-serif-ja-v:'Hiragino Sans', 'Hiragino Kaku Gothic ProN', 'Hiragino Kaku Gothic Pro', '\u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 W3', 'YuGothic', 'Yu Gothic Medium', 'BIZ UDGothic', 'Yu Gothic', '\uFF2D\uFF33\u30B4\u30B7\u30C3\u30AF', 'MS Gothic', sans-serif;

  --RS__serif-ja-v:'Hiragino Mincho ProN', 'Hiragino Mincho Pro', 'YuMincho', 'BIZ UDMincho', 'Yu Mincho', '\uFF2D\uFF33\u660E\u671D', 'MS Mincho', serif;

  --RS__sans-serif-ja:'Hiragino Sans', 'Hiragino Kaku Gothic ProN', 'Hiragino Kaku Gothic Pro', '\u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 W3', 'YuGothic', 'Yu Gothic Medium', 'BIZ UDPGothic', 'Yu Gothic', '\uFF2D\uFF33 \uFF30\u30B4\u30B7\u30C3\u30AF', 'MS PGothic', sans-serif;

  --RS__serif-ja:'Hiragino Mincho ProN', 'Hiragino Mincho Pro', 'YuMincho', 'BIZ UDPMincho', 'Yu Mincho', '\uFF2D\uFF33 \uFF30\u660E\u671D', 'MS PMincho', serif;

  --RS__monospaceTf:ui-monospace, 'Andale Mono', 'Cascadia Code', 'Source Code Pro', Menlo, Consolas, 'DejaVu Sans Mono', monospace;

  --RS__humanistTf:Seravek, Calibri, 'Gill Sans Nova', Roboto, Ubuntu, 'DejaVu Sans', source-sans-pro, sans-serif;

  --RS__sansTf:-ui-sans-serif, -apple-system, system-ui, BlinkMacSystemFont, 'Segoe UI Variable', 'Segoe UI', Inter, Roboto, 'Helvetica Neue', 'Arial Nova', 'Liberation Sans', Arial, sans-serif;

  --RS__modernTf:Athelas, Constantia, Charter, 'Bitstream Charter', Cambria, 'Georgia Pro', Georgia, serif;

  --RS__oldStyleTf:'Iowan Old Style', Sitka, 'Sitka Text', Palatino, 'Book Antiqua', 'URW Palladio L', P052, serif;

  --RS__zh-HK-lineHeightCompensation:1.167;

  --RS__zh-HK-baseFontFamily:'\u65B9\u9AD4', 'PingFang HK', '\u65B9\u9AD4', 'PingFang TC', '\u9ED1\u9AD4', 'Heiti TC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK TC', sans-serif;

  --RS__zh-TW-lineHeightCompensation:1.167;

  --RS__zh-TW-baseFontFamily:'\u65B9\u9AD4', 'PingFang TC', '\u9ED1\u9AD4', 'Heiti TC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK TC', sans-serif;

  --RS__zh-Hant-lineHeightCompensation:1.167;

  --RS__zh-Hant-baseFontFamily:'\u65B9\u9AD4', 'PingFang TC', '\u9ED1\u9AD4', 'Heiti TC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK TC', sans-serif;

  --RS__zh-lineHeightCompensation:1.167;

  --RS__zh-baseFontFamily:'\u65B9\u4F53', 'PingFang SC', '\u9ED1\u4F53', 'Heiti SC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK SC', sans-serif;

  --RS__th-lineHeightCompensation:1.067;

  --RS__th-baseFontFamily:Thonburi, 'Leelawadee UI', 'Cordia New', Roboto, Noto, 'Noto Sans Thai', sans-serif;

  --RS__te-baseFontFamily:'Kohinoor Telugu', 'Telugu Sangam MN', 'Nirmala UI', Gautami, Roboto, Noto, 'Noto Sans Telugu', sans-serif;

  --RS__ta-lineHeightCompensation:1.067;

  --RS__ta-baseFontFamily:'Tamil Sangam MN', 'Nirmala UI', Latha, Roboto, Noto, 'Noto Sans Tamil', sans-serif;

  --RS__si-lineHeightCompensation:1.167;

  --RS__si-baseFontFamily:'Sinhala Sangam MN', 'Nirmala UI', 'Iskoola Pota', Roboto, Noto, 'Noto Sans Sinhala', sans-serif;

  --RS__pa-lineHeightCompensation:1.1;

  --RS__pa-baseFontFamily:'Gurmukhi MN', 'Nirmala UI', Kartika, Roboto, Noto, 'Noto Sans Gurmukhi', sans-serif;

  --RS__or-lineHeightCompensation:1.167;

  --RS__or-baseFontFamily:'Oriya Sangam MN', 'Nirmala UI', Kalinga, Roboto, Noto, 'Noto Sans Oriya', sans-serif;

  --RS__ml-lineHeightCompensation:1.067;

  --RS__ml-baseFontFamily:'Malayalam Sangam MN', 'Nirmala UI', Kartika, Roboto, Noto, 'Noto Sans Malayalam', sans-serif;

  --RS__lo-baseFontFamily:'Lao Sangam MN', 'Leelawadee UI', 'Lao UI', Roboto, Noto, 'Noto Sans Lao', sans-serif;

  --RS__ko-lineHeightCompensation:1.167;

  --RS__ko-baseFontFamily:'Nanum Gothic', 'Apple SD Gothic Neo', 'Malgun Gothic', Roboto, Noto, 'Noto Sans CJK KR', sans-serif;

  --RS__kn-lineHeightCompensation:1.1;

  --RS__kn-baseFontFamily:'Kannada Sangam MN', 'Nirmala UI', Tunga, Roboto, Noto, 'Noto Sans Kannada', sans-serif;

  --RS__km-lineHeightCompensation:1.067;

  --RS__km-baseFontFamily:'Khmer Sangam MN', 'Leelawadee UI', 'Khmer UI', Roboto, Noto, 'Noto Sans Khmer', sans-serif;

  --RS__ja-lineHeightCompensation:1.167;

  --RS__ja-baseFontFamily:YuGothic, 'Hiragino Maru Gothic ProN', 'Hiragino Sans', 'Yu Gothic UI', 'Meiryo UI', 'MS Gothic', Roboto, Noto, 'Noto Sans CJK JP', sans-serif;

  --RS__iu-baseFontFamily:'Euphemia UCAS', Euphemia, Roboto, Noto, 'Noto Sans Canadian Aboriginal', sans-serif;

  --RS__hy-baseFontFamily:Mshtakan, Sylfaen, Roboto, Noto, 'Noto Serif Armenian', serif;

  --RS__hi-lineHeightCompensation:1.1;

  --RS__hi-baseFontFamily:'Kohinoor Devanagari', 'Devanagari Sangam MN', Kokila, 'Nirmala UI', Roboto, Noto, 'Noto Sans Devanagari', sans-serif;

  --RS__he-lineHeightCompensation:1.1;

  --RS__he-baseFontFamily:'New Peninim MT', 'Arial Hebrew', Gisha, 'Times New Roman', Roboto, Noto, 'Noto Sans Hebrew', sans-serif;

  --RS__gu-lineHeightCompensation:1.167;

  --RS__gu-baseFontFamily:'Gujarati Sangam MN', 'Nirmala UI', Shruti, Roboto, Noto, 'Noto Sans Gujarati', sans-serif;

  --RS__fa-baseFontFamily:'Geeza Pro', 'Arabic Typesetting', Roboto, Noto, 'Noto Naskh Arabic', 'Times New Roman', serif;

  --RS__chr-lineHeightCompensation:1.167;

  --RS__chr-baseFontFamily:'Plantagenet Cherokee', Roboto, Noto, 'Noto Sans Cherokee';

  --RS__bo-baseFontFamily:Kailasa, 'Microsoft Himalaya', Roboto, Noto, 'Noto Sans Tibetan', sans-serif;

  --RS__bn-lineHeightCompensation:1.067;

  --RS__bn-baseFontFamily:'Kohinoor Bangla', 'Bangla Sangam MN', Vrinda, Roboto, Noto, 'Noto Sans Bengali', sans-serif;

  --RS__ar-baseFontFamily:'Geeza Pro', 'Arabic Typesetting', Roboto, Noto, 'Noto Naskh Arabic', 'Times New Roman', serif;

  --RS__am-lineHeightCompensation:1.167;

  --RS__am-baseFontFamily:Kefa, Nyala, Roboto, Noto, 'Noto Sans Ethiopic', serif;

  --RS__latin-lineHeightCompensation:1;

  --RS__latin-baseFontFamily:var(--RS__oldStyleTf);
  --RS__baseFontFamily:var(--RS__latin-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__latin-lineHeightCompensation);
  --RS__baseLineHeight:calc(1.5 * var(--RS__lineHeightCompensation));

  --RS__selectionTextColor:inherit;

  --RS__selectionBackgroundColor:#b4d8fe;

  --RS__visitedColor:#551A8B;

  --RS__linkColor:#0000EE;

  --RS__textColor:#121212;

  --RS__backgroundColor:#FFFFFF;
  color:var(--RS__textColor) !important;

  background-color:var(--RS__backgroundColor) !important;
}

::-moz-selection{
  color:var(--RS__selectionTextColor);
  background-color:var(--RS__selectionBackgroundColor);
}

::selection{
  color:var(--RS__selectionTextColor);
  background-color:var(--RS__selectionBackgroundColor);
}

html{
  font-family:var(--RS__baseFontFamily);
  line-height:1.6;
  line-height:var(--RS__baseLineHeight);
  text-rendering:optimizelegibility;
}

h1, h2, h3{
  line-height:normal;
}

:lang(ja),
:lang(zh),
:lang(ko){
  word-wrap:break-word;
  -webkit-line-break:strict;
  -epub-line-break:strict;
  line-break:strict;
}

math{
  font-family:"Latin Modern Math", "STIX Two Math", "XITS Math", "STIX Math", "Libertinus Math", "TeX Gyre Termes Math", "TeX Gyre Bonum Math", "TeX Gyre Schola", "DejaVu Math TeX Gyre", "TeX Gyre Pagella Math", "Asana Math", "Cambria Math", "Lucida Bright Math", "Minion Math", STIXGeneral, STIXSizeOneSym, Symbol, "Times New Roman", serif;
}

:lang(am){
  --RS__baseFontFamily:var(--RS__am-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__am-lineHeightCompensation);
}

:lang(ar){
  --RS__baseFontFamily:var(--RS__ar-baseFontFamily);
}

:lang(bn){
  --RS__baseFontFamily:var(--RS__bn-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__bn-lineHeightCompensation);
}

:lang(bo){
  --RS__baseFontFamily:var(--RS__bo-baseFontFamily);
}

:lang(chr){
  --RS__baseFontFamily:var(--RS__chr-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__chr-lineHeightCompensation);
}

:lang(fa){
  --RS__baseFontFamily:var(--RS__fa-baseFontFamily);
}

:lang(gu){
  --RS__baseFontFamily:var(--RS__gu-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__gu-lineHeightCompensation);
}

:lang(he){
  --RS__baseFontFamily:var(--RS__he-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__he-lineHeightCompensation);
}

:lang(hi){
  --RS__baseFontFamily:var(--RS__hi-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__hi-lineHeightCompensation);
}

:lang(hy){
  --RS__baseFontFamily:var(--RS__hy-baseFontFamily);
}

:lang(iu){
  --RS__baseFontFamily:var(--RS__iu-baseFontFamily);
}

:lang(ja){
  --RS__baseFontFamily:var(--RS__ja-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ja-lineHeightCompensation);
}

:lang(km){
  --RS__baseFontFamily:var(--RS__km-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__km-lineHeightCompensation);
}

:lang(kn){
  --RS__baseFontFamily:var(--RS__kn-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__kn-lineHeightCompensation);
}

:lang(ko){
  --RS__baseFontFamily:var(--RS__ko-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ko-lineHeightCompensation);
}

:lang(lo){
  --RS__baseFontFamily:var(--RS__lo-baseFontFamily);
}

:lang(ml){
  --RS__baseFontFamily:var(--RS__ml-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ml-lineHeightCompensation);
}

:lang(or){
  --RS__baseFontFamily:var(--RS__or-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__or-lineHeightCompensation);
}

:lang(pa){
  --RS__baseFontFamily:var(--RS__pa-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__pa-lineHeightCompensation);
}

:lang(si){
  --RS__baseFontFamily:var(--RS__si-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__si-lineHeightCompensation);
}

:lang(ta){
  --RS__baseFontFamily:var(--RS__ta-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ta-lineHeightCompensation);
}

:lang(te){
  --RS__baseFontFamily:var(--RS__te-baseFontFamily);
}

:lang(th){
  --RS__baseFontFamily:var(--RS__th-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__th-lineHeightCompensation);
}

:lang(zh){
  --RS__baseFontFamily:var(--RS__zh-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-lineHeightCompensation);
}

:lang(zh-Hant){
  --RS__baseFontFamily:var(--RS__zh-Hant-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-Hant-lineHeightCompensation);
}

:lang(zh-TW){
  --RS__baseFontFamily:var(--RS__zh-TW-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-TW-lineHeightCompensation);
}

:lang(zh-HK){
  --RS__baseFontFamily:var(--RS__zh-HK-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-HK-lineHeightCompensation);
}

body{
  widows:2;
  orphans:2;
}

figcaption, th, td{
  widows:1;
  orphans:1;
}

h2, h3, h4, h5, h6, dt,
hr, caption{
  -webkit-column-break-after:avoid;
  page-break-after:avoid;
  break-after:avoid;
}

h1, h2, h3, h4, h5, h6, dt,
figure, tr{
  -webkit-column-break-inside:avoid;
  page-break-inside:avoid;
  break-inside:avoid;
}

body{
  -webkit-hyphenate-character:"\\002D";
  -moz-hyphenate-character:"\\002D";
  -ms-hyphenate-character:"\\002D";
  hyphenate-character:"\\002D";
  -webkit-hyphenate-limit-lines:3;
  -ms-hyphenate-limit-lines:3;
  hyphenate-limit-lines:3;
}

h1, h2, h3, h4, h5, h6, dt,
figcaption, pre, caption, address,
center, code, var{
  -ms-hyphens:none;
  -moz-hyphens:none;
  -webkit-hyphens:none;
  -epub-hyphens:none;
  hyphens:none;
}

body{
  font-variant-numeric:oldstyle-nums proportional-nums;
}

:lang(ja) body,
:lang(zh) body,
:lang(ko) body{
  font-variant-numeric:lining-nums proportional-nums;
}

h1, h2, h3, h4, h5, h6, dt{
  font-variant-numeric:lining-nums proportional-nums;
}

table{
  font-variant-numeric:lining-nums tabular-nums;
}

code, var{
  font-variant-ligatures:none;
  font-variant-numeric:lining-nums tabular-nums slashed-zero;
}

rt{
  font-variant-east-asian:ruby;
}

:lang(ar){
  font-variant-ligatures:common-ligatures;
}

:lang(ko){
  font-kerning:normal;
}

hr{
  color:inherit;
  border-color:currentcolor;
}

table, th, td{
  border-color:currentcolor;
}

figure, blockquote{
  margin:1em 5%;
}

ul, ol{
  padding-left:5%;
}

dd{
  margin-left:5%;
}

pre{
  white-space:pre-wrap;
  -ms-tab-size:2;
  -moz-tab-size:2;
  -webkit-tab-size:2;
  tab-size:2;
}

abbr[title], acronym[title]{
  text-decoration:dotted underline;
}

nobr wbr{
  white-space:normal;
}

ruby > rt, ruby > rp{
  -webkit-user-select:none;
  -moz-user-select:none;
  -ms-user-select:none;
  user-select:none;
}

*:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)),
*:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)),
*:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)),
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) cite, 
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) dfn, 
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) em, 
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) i,
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) cite, 
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) dfn, 
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) em, 
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) i,
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) cite, 
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) dfn, 
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) em, 
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) i{
  font-style:normal;
}

:lang(ja) a,
:lang(zh) a,
:lang(ko) a{
  text-decoration:none;
}

:root{
  --RS__maxMediaWidth:100%;
  --RS__maxMediaHeight:95vh;
  --RS__boxSizingMedia:border-box;
  --RS__boxSizingTable:border-box;
}

a, a span, span a, h1, h2, h3, h4, h5, h6{
  word-wrap:break-word;
}

div{
  max-width:var(--RS__maxMediaWidth);
}

img, svg|svg, video{
  object-fit:contain;

  width:auto;
  height:auto;
  max-width:var(--RS__maxMediaWidth);
  max-height:var(--RS__maxMediaHeight) !important;
  box-sizing:var(--RS__boxSizingMedia);
  -webkit-column-break-inside:avoid;
  page-break-inside:avoid;
  break-inside:avoid;
}

@supports (zoom: 1) and (not ((-webkit-column-axis: horizontal) and (-webkit-column-progression: normal))){

  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] img,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] svg|svg,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] video{
    zoom:calc(100% / var(--USER__fontSize));
  }
}

audio{
    max-width:100%;
    -webkit-column-break-inside:avoid;
    page-break-inside:avoid;
    break-inside:avoid;
  }

table{
  max-width:var(--RS__maxMediaWidth);
  box-sizing:var(--RS__boxSizingTable);
}`});var ad={};_(ad,{default:()=>Eg});var Eg,sd=Me(()=>{Eg=`/*!
 * Readium CSS v.2.0.5
 * Copyright (c) 2017\u20132026. Readium Foundation. All rights reserved.
 * Use of this source code is governed by a BSD-style license which is detailed in the
 * LICENSE file present in the project repository where this source code is maintained.
 * Core maintainer: Jiminy Panoz <jiminy.panoz@edrlab.org> 
 * Contributors: 
 * Daniel Weck
 * Hadrien Gardeur
 * Innovimax
 * L. Le Meur
 * Mickae\u0308l Menu
 * k_taka
 */

@namespace url("http://www.w3.org/1999/xhtml");

@namespace epub url("http://www.idpf.org/2007/ops");

@namespace m url("http://www.w3.org/1998/Math/MathML");

@namespace svg url("http://www.w3.org/2000/svg");

:root{
  --RS__compFontFamily:var(--RS__baseFontFamily);
  --RS__codeFontFamily:var(--RS__monospaceTf);

  --RS__typeScale:1.125;
  --RS__baseFontSize:100%;

  --RS__flowSpacing:1.5rem;
  --RS__paraSpacing:0;
  --RS__paraIndent:1em;

  --RS__linkColor:#0000EE;
  --RS__visitedColor:#551A8B;

  --RS__primaryColor:;
  --RS__secondaryColor:;
}

body{
  font-size:var(--RS__baseFontSize);
  text-align:justify;
}

h1, h2, h3, h4, h5, h6{
  font-family:var(--RS__compFontFamily);
  text-align:right;
}

blockquote, figure, p, pre,
aside, footer, form, hr{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
}

p{
  margin-top:var(--RS__paraSpacing);
  margin-bottom:var(--RS__paraSpacing);
  text-indent:var(--RS__paraIndent);
}

h1 + p, h2 + p, h3 + p, h4 + p, h5 + p, h6 + p,
hr + p{
  text-indent:0;
}

pre{
  font-family:var(--RS__codeFontFamily);
}

code, kbd, samp, tt{
  font-family:var(--RS__codeFontFamily);
}

sub, sup{
  position:relative;
  font-size:67.5%;
  line-height:1;
}

sub{
  bottom:-0.2ex;
}

sup{
  bottom:0;
}

:link{
  color:var(--RS__linkColor);
}

:visited{
  color:var(--RS__visitedColor);
}

h1{
  margin-top:calc(var(--RS__flowSpacing) * 2);
  margin-bottom:calc(var(--RS__flowSpacing) * 2);
  font-size:calc(((1em * var(--RS__typeScale)) * var(--RS__typeScale)) * var(--RS__typeScale));
}

h2{
  margin-top:calc(var(--RS__flowSpacing) * 2);
  margin-bottom:var(--RS__flowSpacing);
  font-size:calc((1em * var(--RS__typeScale)) * var(--RS__typeScale));
}

h3{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
  font-size:calc(1em * var(--RS__typeScale));
}

h4{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
  font-size:1em;
}

h5{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
  font-size:smaller;
}

h6{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:0;
  font-size:smaller;
  font-weight:normal;
}

dl, ol, ul{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
}

table{
  margin:var(--RS__flowSpacing) 0;
  border:1px solid currentcolor;
  border-collapse:collapse;
  empty-cells:show;
}

thead, tbody, tfoot, table > tr{
  vertical-align:top;
}

th{
  text-align:initial;
}

th, td{
  padding:4px;
  border:1px solid currentcolor;
}`});var ld={};_(ld,{default:()=>Pg});var Pg,cd=Me(()=>{Pg=`/*!
 * Readium CSS v.2.0.5
 * Copyright (c) 2017\u20132026. Readium Foundation. All rights reserved.
 * Use of this source code is governed by a BSD-style license which is detailed in the
 * LICENSE file present in the project repository where this source code is maintained.
 * Core maintainer: Jiminy Panoz <jiminy.panoz@edrlab.org> 
 * Contributors: 
 * Daniel Weck
 * Hadrien Gardeur
 * Innovimax
 * L. Le Meur
 * Mickae\u0308l Menu
 * k_taka
 */

@namespace url("http://www.w3.org/1999/xhtml");

@namespace epub url("http://www.idpf.org/2007/ops");

@namespace m url("http://www.w3.org/1998/Math/MathML");

@namespace svg url("http://www.w3.org/2000/svg");

:root{

  --RS__viewportWidth:100%;

  --RS__pageGutter:0;

  --RS__defaultLineLength:100%;

  --RS__colGap:0;

  --RS__colCount:1;

  --RS__colWidth:100vw;
}

@page{
  margin:0 !important;
}

:root{
  position:relative;

  -webkit-column-width:var(--RS__colWidth);
  -moz-column-width:var(--RS__colWidth);
  column-width:var(--RS__colWidth);
  -webkit-column-count:var(--RS__colCount);
  -moz-column-count:var(--RS__colCount);
  column-count:var(--RS__colCount);

  -webkit-column-gap:var(--RS__colGap);
  -moz-column-gap:var(--RS__colGap);
  column-gap:var(--RS__colGap);
  -moz-column-fill:auto;
  column-fill:auto;
  width:var(--RS__viewportWidth);
  height:100vh;
  max-width:var(--RS__viewportWidth);
  max-height:100vh;
  min-width:var(--RS__viewportWidth);
  min-height:100vh;
  padding:0 !important;
  margin:0 !important;
  font-size:1rem !important;
  box-sizing:border-box;
  -webkit-touch-callout:none;
}

body{
  width:100%;
  max-width:var(--RS__defaultLineLength) !important;
  margin:0 auto !important;
  box-sizing:border-box;
}

:root:not([style*="readium-scroll-on"]) body{
  padding:0 var(--RS__pageGutter) !important;
}

:root:not([style*="readium-noOverflow-on"]) body{
  overflow:hidden;
}

@supports (overflow: clip){

   :root:not([style*="readium-noOverflow-on"]){
      overflow:clip;
   }

   :root:not([style*="readium-noOverflow-on"]) body{
      overflow:clip;
      overflow-clip-margin:content-box;
   }
}

:root[style*="readium-scroll-on"]{
  -webkit-columns:auto auto !important;
  -moz-columns:auto auto !important;
  columns:auto auto !important;
  width:auto !important;
  height:auto !important;
  max-width:none !important;
  max-height:none !important;
  min-width:0 !important;
  min-height:0 !important;
}

:root[style*="readium-scroll-on"] body{
  max-width:var(--RS__defaultLineLength) !important;
  box-sizing:border-box !important;
}

:root[style*="readium-scroll-on"]:not([style*="readium-noOverflow-on"]) body{
  overflow:auto;
}

@supports (overflow: clip){

  :root[style*="readium-scroll-on"]:not([style*="readium-noOverflow-on"]){
     overflow:auto;
  }

  :root[style*="readium-scroll-on"]:not([style*="readium-noOverflow-on"]) body{
     overflow:clip;
  }
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingTop"] body{
  padding-top:var(--RS__scrollPaddingTop) !important;
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingBottom"] body{
  padding-bottom:var(--RS__scrollPaddingBottom) !important;
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingLeft"] body{
  padding-left:var(--RS__scrollPaddingLeft) !important;
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingRight"] body{
  padding-right:var(--RS__scrollPaddingRight) !important;
}

:root[style*="--USER__backgroundColor"]{
  background-color:var(--USER__backgroundColor) !important;
}

:root[style*="--USER__backgroundColor"] *{
  background-color:transparent !important;
}

:root[style*="--USER__textColor"]{
  color:var(--USER__textColor) !important;
}

:root[style*="--USER__textColor"] *:not(a){
  color:inherit !important;
  background-color:transparent !important;
  border-color:currentcolor !important;
}

:root[style*="--USER__textColor"] svg text{
  fill:currentcolor !important;
  stroke:none !important;
}

:root[style*="--USER__linkColor"] a:link,
:root[style*="--USER__linkColor"] a:link *{
  color:var(--USER__linkColor) !important;
}

:root[style*="--USER__visitedColor"] a:visited,
:root[style*="--USER__visitedColor"] a:visited *{
  color:var(--USER__visitedColor) !important;
}

:root[style*="--USER__selectionBackgroundColor"][style*="--USER__selectionTextColor"] ::-moz-selection{
  color:var(--USER__selectionTextColor) !important;
  background-color:var(--USER__selectionBackgroundColor) !important;
}

:root[style*="--USER__selectionBackgroundColor"][style*="--USER__selectionTextColor"] ::selection{
  color:var(--USER__selectionTextColor) !important;
  background-color:var(--USER__selectionBackgroundColor) !important;
}

:root[style*="--USER__colCount"]{
  -webkit-column-count:var(--USER__colCount);
  -moz-column-count:var(--USER__colCount);
  column-count:var(--USER__colCount);

  --RS__colWidth:auto;
}

:root[style*="--USER__colCount: 0"],
:root[style*="--USER__colCount:0"]{
  -webkit-column-count:1;
  -moz-column-count:1;
  column-count:1;
}

:root[style*="--USER__colCount: 0"],
:root[style*="--USER__colCount:0"],
:root[style*="--USER__colCount: 1"],
:root[style*="--USER__colCount:1"]{
  --RS__colWidth:100vw;
}

:root[style*="--USER__lineLength"] body{
    max-width:var(--USER__lineLength) !important;
  }

:root[style*="--USER__textAlign"]{
  text-align:var(--USER__textAlign);
}

:root[style*="--USER__textAlign"] body,
:root[style*="--USER__textAlign"] p:not(
  blockquote p,
  figcaption p,
  header p,
  hgroup p,
  :root[style*="readium-experimentalHeaderFiltering-on"] p[class*="title"],
  :root[style*="readium-experimentalHeaderFiltering-on"] div:has(+ *) > h1 + p,
  :root[style*="readium-experimentalHeaderFiltering-on"] div:has(+ *) > p:has(+ h1)
),
:root[style*="--USER__textAlign"] li,
:root[style*="--USER__textAlign"] dd{
  text-align:var(--USER__textAlign) !important;
  -moz-text-align-last:auto !important;
  -epub-text-align-last:auto !important;
  text-align-last:auto !important;
}

:root[style*="--USER__fontFamily"]{
  font-family:var(--USER__fontFamily) !important;
}

:root[style*="--USER__fontFamily"] *{
  font-family:revert !important;
}

:root:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] body{
  zoom:var(--USER__fontSize) !important;
}

:root:not([style*="readium-deprecatedFontSize-on"])[style*="readium-iOSPatch-on"][style*="--USER__fontSize"] body{
  -webkit-text-size-adjust:var(--USER__fontSize) !important;
}

@supports selector(figure:has(> img)){

  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> img),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> video),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> svg),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> canvas),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> iframe),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> audio),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> img:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> video:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> svg:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> canvas:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> iframe:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> audio:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] table{
    zoom:calc(100% / var(--USER__fontSize)) !important;
  }

  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figcaption,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] caption,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] td,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] th{
    zoom:var(--USER__fontSize) !important;
  }
}

@supports not (zoom: 1){

  :root[style*="--USER__fontSize"]{
    font-size:var(--USER__fontSize) !important;
  }
}

:root[style*="readium-deprecatedFontSize-on"][style*="--USER__fontSize"]{
  font-size:var(--USER__fontSize) !important;
}

:root[style*="--USER__lineHeight"]{
  line-height:var(--USER__lineHeight) !important;
}

:root[style*="--USER__lineHeight"] body,
:root[style*="--USER__lineHeight"] p,
:root[style*="--USER__lineHeight"] li,
:root[style*="--USER__lineHeight"] div{
  line-height:var(--USER__lineHeight) !important;
}

:root[style*="--USER__paraSpacing"] p{
  margin-top:var(--USER__paraSpacing) !important;
  margin-bottom:var(--USER__paraSpacing) !important;
}

:root[style*="--USER__paraIndent"] p:not(
  blockquote p,
  figcaption p,
  header p,
  hgroup p,
  :root[style*="readium-experimentalHeaderFiltering-on"] p[class*="title"],
  :root[style*="readium-experimentalHeaderFiltering-on"] div:has(+ *) > h1 + p,
  :root[style*="readium-experimentalHeaderFiltering-on"] div:has(+ *) > p:has(+ h1)
){
  text-indent:var(--USER__paraIndent) !important;
}

:root[style*="--USER__paraIndent"] p *{
  text-indent:0 !important;
}

:root[style*="--USER__wordSpacing"] h1,
:root[style*="--USER__wordSpacing"] h2,
:root[style*="--USER__wordSpacing"] h3,
:root[style*="--USER__wordSpacing"] h4,
:root[style*="--USER__wordSpacing"] h5,
:root[style*="--USER__wordSpacing"] h6,
:root[style*="--USER__wordSpacing"] p,
:root[style*="--USER__wordSpacing"] li,
:root[style*="--USER__wordSpacing"] div,
:root[style*="--USER__wordSpacing"] dt,
:root[style*="--USER__wordSpacing"] dd{
  word-spacing:var(--USER__wordSpacing) !important;
}

:root[style*="--USER__ligatures"]{
  font-variant-ligatures:var(--USER__ligatures) !important;
}

:root[style*="--USER__ligatures"] *{
  font-variant-ligatures:inherit !important;
}

:root[style*="--USER__fontWeight"] body{
  font-weight:var(--USER__fontWeight) !important;
}

:root[style*="--USER__fontWeight"] b,
:root[style*="--USER__fontWeight"] strong{
  font-weight:bolder;
}

:root[style*="--USER__fontWidth"] body{
  font-stretch:var(--USER__fontWidth) !important;
}

:root[style*="--USER__fontOpticalSizing"] body{
  font-optical-sizing:var(--USER__fontOpticalSizing) !important;
}

:root[style*="readium-blend-on"] svg,
:root[style*="readium-blend-on"] img{
  background-color:transparent !important;
  mix-blend-mode:multiply !important;
}

:root[style*="--USER__darkenImages"] img{
  -webkit-filter:brightness(var(--USER__darkenImages)) !important;
  filter:brightness(var(--USER__darkenImages)) !important;
}

:root[style*="readium-darken-on"] img{
  -webkit-filter:brightness(80%) !important;
  filter:brightness(80%) !important;
}

:root[style*="--USER__invertImages"] img{
  -webkit-filter:invert(var(--USER__invertImages)) !important;
  filter:invert(var(--USER__invertImages)) !important;
}

:root[style*="readium-invert-on"] img{
  -webkit-filter:invert(100%) !important;
  filter:invert(100%) !important;
}

:root[style*="--USER__darkenImages"][style*="--USER__invertImages"] img{
  -webkit-filter:brightness(var(--USER__darkenImages)) invert(var(--USER__invertImages)) !important;
  filter:brightness(var(--USER__darkenImages)) invert(var(--USER__invertImages)) !important;
}

:root[style*="readium-darken-on"][style*="--USER__invertImages"] img{
  -webkit-filter:brightness(80%) invert(var(--USER__invertImages)) !important;
  filter:brightness(80%) invert(var(--USER__invertImages)) !important;
}

:root[style*="--USER__darkenImages"][style*="readium-invert-on"] img{
  -webkit-filter:brightness(var(--USER__darkenImages)) invert(100%) !important;
  filter:brightness(var(--USER__darkenImages)) invert(100%) !important;
}

:root[style*="readium-darken-on"][style*="readium-invert-on"] img{
  -webkit-filter:brightness(80%) invert(100%) !important;
  filter:brightness(80%) invert(100%) !important;
}

:root[style*="--USER__invertGaiji"] img[class*="gaiji"]{
  -webkit-filter:invert(var(--USER__invertGaiji)) !important;
  filter:invert(var(--USER__invertGaiji)) !important;
}

:root[style*="readium-invertGaiji-on"] img[class*="gaiji"]{
  -webkit-filter:invert(100%) !important;
  filter:invert(100%) !important;
}

:root[style*="readium-normalize-on"]{
  --USER__typeScale:1.2;
}

:root[style*="readium-normalize-on"] p,
:root[style*="readium-normalize-on"] li,
:root[style*="readium-normalize-on"] div,
:root[style*="readium-normalize-on"] pre,
:root[style*="readium-normalize-on"] dd{
  font-size:1rem !important;
}

:root[style*="readium-normalize-on"] h1{
  font-size:1.75rem !important;
  font-size:calc(((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"] h2{
  font-size:1.5rem !important;
  font-size:calc((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"] h3{
  font-size:1.25rem !important;
  font-size:calc(1rem * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"] h4,
:root[style*="readium-normalize-on"] h5,
:root[style*="readium-normalize-on"] h6{
  font-size:1rem !important;
}

:root[style*="readium-normalize-on"] small{
  font-size:smaller !important;
}

:root[style*="readium-normalize-on"] sub,
:root[style*="readium-normalize-on"] sup{
  font-size:67.5% !important;
}

:root[style*="readium-normalize-on"][style*="--USER__typeScale"] h1{
  font-size:calc(((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"][style*="--USER__typeScale"] h2{
  font-size:calc((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"][style*="--USER__typeScale"] h3{
  font-size:calc(1rem * var(--USER__typeScale)) !important;
}

:root[style*="readium-iPadOSPatch-on"] body{
  -webkit-text-size-adjust:none;
}

:root[style*="readium-iPadOSPatch-on"] p, 
:root[style*="readium-iPadOSPatch-on"] h1, 
:root[style*="readium-iPadOSPatch-on"] h2, 
:root[style*="readium-iPadOSPatch-on"] h3, 
:root[style*="readium-iPadOSPatch-on"] h4, 
:root[style*="readium-iPadOSPatch-on"] h5, 
:root[style*="readium-iPadOSPatch-on"] h6, 
:root[style*="readium-iPadOSPatch-on"] li, 
:root[style*="readium-iPadOSPatch-on"] th, 
:root[style*="readium-iPadOSPatch-on"] td, 
:root[style*="readium-iPadOSPatch-on"] dt, 
:root[style*="readium-iPadOSPatch-on"] dd, 
:root[style*="readium-iPadOSPatch-on"] pre, 
:root[style*="readium-iPadOSPatch-on"] address, 
:root[style*="readium-iPadOSPatch-on"] details, 
:root[style*="readium-iPadOSPatch-on"] summary,
:root[style*="readium-iPadOSPatch-on"] figcaption,
:root[style*="readium-iPadOSPatch-on"] div:not(:has(p, h1, h2, h3, h4, h5, h6, li, th, td, dt, dd, pre, address, aside, details, figcaption, summary)),
:root[style*="readium-iPadOSPatch-on"] aside:not(:has(p, h1, h2, h3, h4, h5, h6, li, th, td, dt, dd, pre, address, aside, details, figcaption, summary)){
  -webkit-text-zoom:reset;
}

:root[style*="readium-iPadOSPatch-on"] abbr, 
:root[style*="readium-iPadOSPatch-on"] b, 
:root[style*="readium-iPadOSPatch-on"] bdi, 
:root[style*="readium-iPadOSPatch-on"] bdo, 
:root[style*="readium-iPadOSPatch-on"] cite, 
:root[style*="readium-iPadOSPatch-on"] code, 
:root[style*="readium-iPadOSPatch-on"] dfn, 
:root[style*="readium-iPadOSPatch-on"] em, 
:root[style*="readium-iPadOSPatch-on"] i, 
:root[style*="readium-iPadOSPatch-on"] kbd, 
:root[style*="readium-iPadOSPatch-on"] mark, 
:root[style*="readium-iPadOSPatch-on"] q, 
:root[style*="readium-iPadOSPatch-on"] rp, 
:root[style*="readium-iPadOSPatch-on"] rt, 
:root[style*="readium-iPadOSPatch-on"] ruby, 
:root[style*="readium-iPadOSPatch-on"] s, 
:root[style*="readium-iPadOSPatch-on"] samp, 
:root[style*="readium-iPadOSPatch-on"] small, 
:root[style*="readium-iPadOSPatch-on"] span, 
:root[style*="readium-iPadOSPatch-on"] strong, 
:root[style*="readium-iPadOSPatch-on"] sub, 
:root[style*="readium-iPadOSPatch-on"] sup, 
:root[style*="readium-iPadOSPatch-on"] time, 
:root[style*="readium-iPadOSPatch-on"] u, 
:root[style*="readium-iPadOSPatch-on"] var{
  -webkit-text-zoom:normal;
}

:root[style*="readium-iPadOSPatch-on"] p:not(:has(b, cite, em, i, q, s, small, span, strong)):first-line{
  -webkit-text-zoom:normal;
}`});var hd={};_(hd,{default:()=>Tg});var Tg,dd=Me(()=>{Tg=`/*!
 * Readium CSS v.2.0.5
 * Copyright (c) 2017\u20132026. Readium Foundation. All rights reserved.
 * Use of this source code is governed by a BSD-style license which is detailed in the
 * LICENSE file present in the project repository where this source code is maintained.
 * Core maintainer: Jiminy Panoz <jiminy.panoz@edrlab.org> 
 * Contributors: 
 * Daniel Weck
 * Hadrien Gardeur
 * Innovimax
 * L. Le Meur
 * Mickae\u0308l Menu
 * k_taka
 */

@namespace url("http://www.w3.org/1999/xhtml");

@namespace epub url("http://www.idpf.org/2007/ops");

@namespace m url("http://www.w3.org/1998/Math/MathML");

@namespace svg url("http://www.w3.org/2000/svg");

@-ms-viewport{
  width:device-width;
}

@viewport{
  width:device-width;
  zoom:1;
}

:root{

  --RS__sans-serif-ja-v:'Hiragino Sans', 'Hiragino Kaku Gothic ProN', 'Hiragino Kaku Gothic Pro', '\u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 W3', 'YuGothic', 'Yu Gothic Medium', 'BIZ UDGothic', 'Yu Gothic', '\uFF2D\uFF33\u30B4\u30B7\u30C3\u30AF', 'MS Gothic', sans-serif;

  --RS__serif-ja-v:'Hiragino Mincho ProN', 'Hiragino Mincho Pro', 'YuMincho', 'BIZ UDMincho', 'Yu Mincho', '\uFF2D\uFF33\u660E\u671D', 'MS Mincho', serif;

  --RS__sans-serif-ja:'Hiragino Sans', 'Hiragino Kaku Gothic ProN', 'Hiragino Kaku Gothic Pro', '\u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 W3', 'YuGothic', 'Yu Gothic Medium', 'BIZ UDPGothic', 'Yu Gothic', '\uFF2D\uFF33 \uFF30\u30B4\u30B7\u30C3\u30AF', 'MS PGothic', sans-serif;

  --RS__serif-ja:'Hiragino Mincho ProN', 'Hiragino Mincho Pro', 'YuMincho', 'BIZ UDPMincho', 'Yu Mincho', '\uFF2D\uFF33 \uFF30\u660E\u671D', 'MS PMincho', serif;

  --RS__monospaceTf:ui-monospace, 'Andale Mono', 'Cascadia Code', 'Source Code Pro', Menlo, Consolas, 'DejaVu Sans Mono', monospace;

  --RS__humanistTf:Seravek, Calibri, 'Gill Sans Nova', Roboto, Ubuntu, 'DejaVu Sans', source-sans-pro, sans-serif;

  --RS__sansTf:-ui-sans-serif, -apple-system, system-ui, BlinkMacSystemFont, 'Segoe UI Variable', 'Segoe UI', Inter, Roboto, 'Helvetica Neue', 'Arial Nova', 'Liberation Sans', Arial, sans-serif;

  --RS__modernTf:Athelas, Constantia, Charter, 'Bitstream Charter', Cambria, 'Georgia Pro', Georgia, serif;

  --RS__oldStyleTf:'Iowan Old Style', Sitka, 'Sitka Text', Palatino, 'Book Antiqua', 'URW Palladio L', P052, serif;

  --RS__zh-HK-lineHeightCompensation:1.167;

  --RS__zh-HK-baseFontFamily:'\u65B9\u9AD4', 'PingFang HK', '\u65B9\u9AD4', 'PingFang TC', '\u9ED1\u9AD4', 'Heiti TC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK TC', sans-serif;

  --RS__zh-TW-lineHeightCompensation:1.167;

  --RS__zh-TW-baseFontFamily:'\u65B9\u9AD4', 'PingFang TC', '\u9ED1\u9AD4', 'Heiti TC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK TC', sans-serif;

  --RS__zh-Hant-lineHeightCompensation:1.167;

  --RS__zh-Hant-baseFontFamily:'\u65B9\u9AD4', 'PingFang TC', '\u9ED1\u9AD4', 'Heiti TC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK TC', sans-serif;

  --RS__zh-lineHeightCompensation:1.167;

  --RS__zh-baseFontFamily:'\u65B9\u4F53', 'PingFang SC', '\u9ED1\u4F53', 'Heiti SC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK SC', sans-serif;

  --RS__th-lineHeightCompensation:1.067;

  --RS__th-baseFontFamily:Thonburi, 'Leelawadee UI', 'Cordia New', Roboto, Noto, 'Noto Sans Thai', sans-serif;

  --RS__te-baseFontFamily:'Kohinoor Telugu', 'Telugu Sangam MN', 'Nirmala UI', Gautami, Roboto, Noto, 'Noto Sans Telugu', sans-serif;

  --RS__ta-lineHeightCompensation:1.067;

  --RS__ta-baseFontFamily:'Tamil Sangam MN', 'Nirmala UI', Latha, Roboto, Noto, 'Noto Sans Tamil', sans-serif;

  --RS__si-lineHeightCompensation:1.167;

  --RS__si-baseFontFamily:'Sinhala Sangam MN', 'Nirmala UI', 'Iskoola Pota', Roboto, Noto, 'Noto Sans Sinhala', sans-serif;

  --RS__pa-lineHeightCompensation:1.1;

  --RS__pa-baseFontFamily:'Gurmukhi MN', 'Nirmala UI', Kartika, Roboto, Noto, 'Noto Sans Gurmukhi', sans-serif;

  --RS__or-lineHeightCompensation:1.167;

  --RS__or-baseFontFamily:'Oriya Sangam MN', 'Nirmala UI', Kalinga, Roboto, Noto, 'Noto Sans Oriya', sans-serif;

  --RS__ml-lineHeightCompensation:1.067;

  --RS__ml-baseFontFamily:'Malayalam Sangam MN', 'Nirmala UI', Kartika, Roboto, Noto, 'Noto Sans Malayalam', sans-serif;

  --RS__lo-baseFontFamily:'Lao Sangam MN', 'Leelawadee UI', 'Lao UI', Roboto, Noto, 'Noto Sans Lao', sans-serif;

  --RS__ko-lineHeightCompensation:1.167;

  --RS__ko-baseFontFamily:'Nanum Gothic', 'Apple SD Gothic Neo', 'Malgun Gothic', Roboto, Noto, 'Noto Sans CJK KR', sans-serif;

  --RS__kn-lineHeightCompensation:1.1;

  --RS__kn-baseFontFamily:'Kannada Sangam MN', 'Nirmala UI', Tunga, Roboto, Noto, 'Noto Sans Kannada', sans-serif;

  --RS__km-lineHeightCompensation:1.067;

  --RS__km-baseFontFamily:'Khmer Sangam MN', 'Leelawadee UI', 'Khmer UI', Roboto, Noto, 'Noto Sans Khmer', sans-serif;

  --RS__ja-lineHeightCompensation:1.167;

  --RS__ja-baseFontFamily:YuGothic, 'Hiragino Maru Gothic ProN', 'Hiragino Sans', 'Yu Gothic UI', 'Meiryo UI', 'MS Gothic', Roboto, Noto, 'Noto Sans CJK JP', sans-serif;

  --RS__iu-baseFontFamily:'Euphemia UCAS', Euphemia, Roboto, Noto, 'Noto Sans Canadian Aboriginal', sans-serif;

  --RS__hy-baseFontFamily:Mshtakan, Sylfaen, Roboto, Noto, 'Noto Serif Armenian', serif;

  --RS__hi-lineHeightCompensation:1.1;

  --RS__hi-baseFontFamily:'Kohinoor Devanagari', 'Devanagari Sangam MN', Kokila, 'Nirmala UI', Roboto, Noto, 'Noto Sans Devanagari', sans-serif;

  --RS__he-lineHeightCompensation:1.1;

  --RS__he-baseFontFamily:'New Peninim MT', 'Arial Hebrew', Gisha, 'Times New Roman', Roboto, Noto, 'Noto Sans Hebrew', sans-serif;

  --RS__gu-lineHeightCompensation:1.167;

  --RS__gu-baseFontFamily:'Gujarati Sangam MN', 'Nirmala UI', Shruti, Roboto, Noto, 'Noto Sans Gujarati', sans-serif;

  --RS__fa-baseFontFamily:'Geeza Pro', 'Arabic Typesetting', Roboto, Noto, 'Noto Naskh Arabic', 'Times New Roman', serif;

  --RS__chr-lineHeightCompensation:1.167;

  --RS__chr-baseFontFamily:'Plantagenet Cherokee', Roboto, Noto, 'Noto Sans Cherokee';

  --RS__bo-baseFontFamily:Kailasa, 'Microsoft Himalaya', Roboto, Noto, 'Noto Sans Tibetan', sans-serif;

  --RS__bn-lineHeightCompensation:1.067;

  --RS__bn-baseFontFamily:'Kohinoor Bangla', 'Bangla Sangam MN', Vrinda, Roboto, Noto, 'Noto Sans Bengali', sans-serif;

  --RS__ar-baseFontFamily:'Geeza Pro', 'Arabic Typesetting', Roboto, Noto, 'Noto Naskh Arabic', 'Times New Roman', serif;

  --RS__am-lineHeightCompensation:1.167;

  --RS__am-baseFontFamily:Kefa, Nyala, Roboto, Noto, 'Noto Sans Ethiopic', serif;

  --RS__latin-lineHeightCompensation:1;

  --RS__latin-baseFontFamily:var(--RS__oldStyleTf);
  --RS__baseFontFamily:var(--RS__latin-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__latin-lineHeightCompensation);
  --RS__baseLineHeight:calc(1.5 * var(--RS__lineHeightCompensation));

  --RS__selectionTextColor:inherit;

  --RS__selectionBackgroundColor:#b4d8fe;

  --RS__visitedColor:#551A8B;

  --RS__linkColor:#0000EE;

  --RS__textColor:#121212;

  --RS__backgroundColor:#FFFFFF;
  color:var(--RS__textColor) !important;

  background-color:var(--RS__backgroundColor) !important;
}

::-moz-selection{
  color:var(--RS__selectionTextColor);
  background-color:var(--RS__selectionBackgroundColor);
}

::selection{
  color:var(--RS__selectionTextColor);
  background-color:var(--RS__selectionBackgroundColor);
}

html{
  font-family:var(--RS__baseFontFamily);
  line-height:1.6;
  line-height:var(--RS__baseLineHeight);
  text-rendering:optimizelegibility;
}

h1, h2, h3{
  line-height:normal;
}

:lang(ja),
:lang(zh),
:lang(ko){
  word-wrap:break-word;
  -webkit-line-break:strict;
  -epub-line-break:strict;
  line-break:strict;
}

math{
  font-family:"Latin Modern Math", "STIX Two Math", "XITS Math", "STIX Math", "Libertinus Math", "TeX Gyre Termes Math", "TeX Gyre Bonum Math", "TeX Gyre Schola", "DejaVu Math TeX Gyre", "TeX Gyre Pagella Math", "Asana Math", "Cambria Math", "Lucida Bright Math", "Minion Math", STIXGeneral, STIXSizeOneSym, Symbol, "Times New Roman", serif;
}

:lang(am){
  --RS__baseFontFamily:var(--RS__am-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__am-lineHeightCompensation);
}

:lang(ar){
  --RS__baseFontFamily:var(--RS__ar-baseFontFamily);
}

:lang(bn){
  --RS__baseFontFamily:var(--RS__bn-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__bn-lineHeightCompensation);
}

:lang(bo){
  --RS__baseFontFamily:var(--RS__bo-baseFontFamily);
}

:lang(chr){
  --RS__baseFontFamily:var(--RS__chr-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__chr-lineHeightCompensation);
}

:lang(fa){
  --RS__baseFontFamily:var(--RS__fa-baseFontFamily);
}

:lang(gu){
  --RS__baseFontFamily:var(--RS__gu-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__gu-lineHeightCompensation);
}

:lang(he){
  --RS__baseFontFamily:var(--RS__he-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__he-lineHeightCompensation);
}

:lang(hi){
  --RS__baseFontFamily:var(--RS__hi-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__hi-lineHeightCompensation);
}

:lang(hy){
  --RS__baseFontFamily:var(--RS__hy-baseFontFamily);
}

:lang(iu){
  --RS__baseFontFamily:var(--RS__iu-baseFontFamily);
}

:lang(ja){
  --RS__baseFontFamily:var(--RS__ja-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ja-lineHeightCompensation);
}

:lang(km){
  --RS__baseFontFamily:var(--RS__km-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__km-lineHeightCompensation);
}

:lang(kn){
  --RS__baseFontFamily:var(--RS__kn-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__kn-lineHeightCompensation);
}

:lang(ko){
  --RS__baseFontFamily:var(--RS__ko-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ko-lineHeightCompensation);
}

:lang(lo){
  --RS__baseFontFamily:var(--RS__lo-baseFontFamily);
}

:lang(ml){
  --RS__baseFontFamily:var(--RS__ml-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ml-lineHeightCompensation);
}

:lang(or){
  --RS__baseFontFamily:var(--RS__or-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__or-lineHeightCompensation);
}

:lang(pa){
  --RS__baseFontFamily:var(--RS__pa-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__pa-lineHeightCompensation);
}

:lang(si){
  --RS__baseFontFamily:var(--RS__si-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__si-lineHeightCompensation);
}

:lang(ta){
  --RS__baseFontFamily:var(--RS__ta-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ta-lineHeightCompensation);
}

:lang(te){
  --RS__baseFontFamily:var(--RS__te-baseFontFamily);
}

:lang(th){
  --RS__baseFontFamily:var(--RS__th-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__th-lineHeightCompensation);
}

:lang(zh){
  --RS__baseFontFamily:var(--RS__zh-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-lineHeightCompensation);
}

:lang(zh-Hant){
  --RS__baseFontFamily:var(--RS__zh-Hant-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-Hant-lineHeightCompensation);
}

:lang(zh-TW){
  --RS__baseFontFamily:var(--RS__zh-TW-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-TW-lineHeightCompensation);
}

:lang(zh-HK){
  --RS__baseFontFamily:var(--RS__zh-HK-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-HK-lineHeightCompensation);
}

body{
  widows:2;
  orphans:2;
}

figcaption, th, td{
  widows:1;
  orphans:1;
}

h2, h3, h4, h5, h6, dt,
hr, caption{
  -webkit-column-break-after:avoid;
  page-break-after:avoid;
  break-after:avoid;
}

h1, h2, h3, h4, h5, h6, dt,
figure, tr{
  -webkit-column-break-inside:avoid;
  page-break-inside:avoid;
  break-inside:avoid;
}

body{
  -webkit-hyphenate-character:"\\002D";
  -moz-hyphenate-character:"\\002D";
  -ms-hyphenate-character:"\\002D";
  hyphenate-character:"\\002D";
  -webkit-hyphenate-limit-lines:3;
  -ms-hyphenate-limit-lines:3;
  hyphenate-limit-lines:3;
}

h1, h2, h3, h4, h5, h6, dt,
figcaption, pre, caption, address,
center, code, var{
  -ms-hyphens:none;
  -moz-hyphens:none;
  -webkit-hyphens:none;
  -epub-hyphens:none;
  hyphens:none;
}

body{
  font-variant-numeric:oldstyle-nums proportional-nums;
}

:lang(ja) body,
:lang(zh) body,
:lang(ko) body{
  font-variant-numeric:lining-nums proportional-nums;
}

h1, h2, h3, h4, h5, h6, dt{
  font-variant-numeric:lining-nums proportional-nums;
}

table{
  font-variant-numeric:lining-nums tabular-nums;
}

code, var{
  font-variant-ligatures:none;
  font-variant-numeric:lining-nums tabular-nums slashed-zero;
}

rt{
  font-variant-east-asian:ruby;
}

:lang(ar){
  font-variant-ligatures:common-ligatures;
}

:lang(ko){
  font-kerning:normal;
}

hr{
  color:inherit;
  border-color:currentcolor;
}

table, th, td{
  border-color:currentcolor;
}

figure, blockquote{
  margin:1em 5%;
}

ul, ol{
  padding-left:5%;
}

dd{
  margin-left:5%;
}

pre{
  white-space:pre-wrap;
  -ms-tab-size:2;
  -moz-tab-size:2;
  -webkit-tab-size:2;
  tab-size:2;
}

abbr[title], acronym[title]{
  text-decoration:dotted underline;
}

nobr wbr{
  white-space:normal;
}

ruby > rt, ruby > rp{
  -webkit-user-select:none;
  -moz-user-select:none;
  -ms-user-select:none;
  user-select:none;
}

*:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)),
*:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)),
*:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)),
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) cite, 
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) dfn, 
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) em, 
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) i,
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) cite, 
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) dfn, 
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) em, 
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) i,
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) cite, 
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) dfn, 
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) em, 
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) i{
  font-style:normal;
}

:lang(ja) a,
:lang(zh) a,
:lang(ko) a{
  text-decoration:none;
}

:root{
  --RS__maxMediaWidth:100%;
  --RS__maxMediaHeight:95vh;
  --RS__boxSizingMedia:border-box;
  --RS__boxSizingTable:border-box;
}

a, a span, span a, h1, h2, h3, h4, h5, h6{
  word-wrap:break-word;
}

div{
  max-width:var(--RS__maxMediaWidth);
}

img, svg|svg, video{
  object-fit:contain;

  width:auto;
  height:auto;
  max-width:var(--RS__maxMediaWidth);
  max-height:var(--RS__maxMediaHeight) !important;
  box-sizing:var(--RS__boxSizingMedia);
  -webkit-column-break-inside:avoid;
  page-break-inside:avoid;
  break-inside:avoid;
}

@supports (zoom: 1) and (not ((-webkit-column-axis: horizontal) and (-webkit-column-progression: normal))){

  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] img,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] svg|svg,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] video{
    zoom:calc(100% / var(--USER__fontSize));
  }
}

audio{
    max-width:100%;
    -webkit-column-break-inside:avoid;
    page-break-inside:avoid;
    break-inside:avoid;
  }

table{
  max-width:var(--RS__maxMediaWidth);
  box-sizing:var(--RS__boxSizingTable);
}`});var ud={};_(ud,{default:()=>Fg});var Fg,md=Me(()=>{Fg=`/*!
 * Readium CSS v.2.0.5
 * Copyright (c) 2017\u20132026. Readium Foundation. All rights reserved.
 * Use of this source code is governed by a BSD-style license which is detailed in the
 * LICENSE file present in the project repository where this source code is maintained.
 * Core maintainer: Jiminy Panoz <jiminy.panoz@edrlab.org> 
 * Contributors: 
 * Daniel Weck
 * Hadrien Gardeur
 * Innovimax
 * L. Le Meur
 * Mickae\u0308l Menu
 * k_taka
 */

@namespace url("http://www.w3.org/1999/xhtml");

@namespace epub url("http://www.idpf.org/2007/ops");

@namespace m url("http://www.w3.org/1998/Math/MathML");

@namespace svg url("http://www.w3.org/2000/svg");

:root{

  --RS__compFontFamily:var(--RS__baseFontFamily);
  --RS__codeFontFamily:var(--RS__monospaceTf);

  --RS__typeScale:1.125;
  --RS__baseFontSize:87.5%;

  --RS__flowSpacing:1.5rem;
  --RS__paraSpacing:0;
  --RS__paraIndent:1em;

  --RS__linkColor:#0000EE;
  --RS__visitedColor:#551A8B;

  --RS__primaryColor:;
  --RS__secondaryColor:;
}

:root:lang(zh){
  --RS__paraIndent:2em;
}

:root{
  quotes:"\\201c" "\\201d" "\\2018" "\\2019";
}

body{
  font-size:var(--RS__baseFontSize);
  text-align:justify;
  text-justify:inter-character;
}

h1, h2, h3, h4, h5, h6{
  font-family:var(--RS__baseFontFamily);
  text-align:left;
  text-align:start;
}

blockquote, figure, p, pre,
aside, footer, form, hr{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
}

p{
  margin-top:var(--RS__paraSpacing);
  margin-bottom:var(--RS__paraSpacing);
  text-indent:var(--RS__paraIndent);
}

pre{
  font-family:var(--RS__codeFontFamily);
}

code, kbd, samp, tt{
  font-family:var(--RS__codeFontFamily);
}

sub, sup{
  position:relative;
  font-size:67.5%;
  line-height:1;
}

sub{
  bottom:-0.2ex;
}

sup{
  bottom:0;
}

em{
  -webkit-text-emphasis:dot;
  -epub-text-emphasis:dot;
  text-emphasis:dot;
}

:link{
  color:var(--RS__linkColor);
}

:visited{
  color:var(--RS__visitedColor);
}

h1{
  margin-top:calc(var(--RS__flowSpacing) * 2);
  margin-bottom:calc(var(--RS__flowSpacing) * 2);
  font-size:calc(((1em * var(--RS__typeScale)) * var(--RS__typeScale)) * var(--RS__typeScale));
  text-align:center;
}

h2{
  margin-top:calc(var(--RS__flowSpacing) * 2);
  margin-bottom:var(--RS__flowSpacing);
  font-size:calc((1em * var(--RS__typeScale)) * var(--RS__typeScale));
  text-align:center;
}

h3{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
  font-size:calc(1em * var(--RS__typeScale));
  text-align:center;
}

h4{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
  font-family:var(--RS__compFontFamily);
  font-size:1em;
}

h5{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
  font-family:var(--RS__compFontFamily);
  font-size:smaller;
}

h6{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:0;
  font-family:var(--RS__compFontFamily);
  font-size:smaller;
  font-weight:normal;
}

dl, ol, ul{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
}

table{
  margin:var(--RS__flowSpacing) 0;
  border:1px solid currentcolor;
  border-collapse:collapse;
  empty-cells:show;
}

thead, tbody, tfoot, table > tr{
  vertical-align:top;
}

th{
  text-align:left;
}

th, td{
  padding:4px;
  border:1px solid currentcolor;
}`});var pd={};_(pd,{default:()=>zg});var zg,gd=Me(()=>{zg=`/*!
 * Readium CSS v.2.0.5
 * Copyright (c) 2017\u20132026. Readium Foundation. All rights reserved.
 * Use of this source code is governed by a BSD-style license which is detailed in the
 * LICENSE file present in the project repository where this source code is maintained.
 * Core maintainer: Jiminy Panoz <jiminy.panoz@edrlab.org> 
 * Contributors: 
 * Daniel Weck
 * Hadrien Gardeur
 * Innovimax
 * L. Le Meur
 * Mickae\u0308l Menu
 * k_taka
 */

@namespace url("http://www.w3.org/1999/xhtml");

@namespace epub url("http://www.idpf.org/2007/ops");

@namespace m url("http://www.w3.org/1998/Math/MathML");

@namespace svg url("http://www.w3.org/2000/svg");

:root{

  --RS__viewportWidth:100%;

  --RS__pageGutter:0;

  --RS__defaultLineLength:100%;

  --RS__colGap:0;

  --RS__colCount:1;

  --RS__colWidth:100vw;
}

@page{
  margin:0 !important;
}

:root{
  position:relative;

  -webkit-column-width:var(--RS__colWidth);
  -moz-column-width:var(--RS__colWidth);
  column-width:var(--RS__colWidth);
  -webkit-column-count:var(--RS__colCount);
  -moz-column-count:var(--RS__colCount);
  column-count:var(--RS__colCount);

  -webkit-column-gap:var(--RS__colGap);
  -moz-column-gap:var(--RS__colGap);
  column-gap:var(--RS__colGap);
  -moz-column-fill:auto;
  column-fill:auto;
  width:var(--RS__viewportWidth);
  height:100vh;
  max-width:var(--RS__viewportWidth);
  max-height:100vh;
  min-width:var(--RS__viewportWidth);
  min-height:100vh;
  padding:0 !important;
  margin:0 !important;
  font-size:1rem !important;
  box-sizing:border-box;
  -webkit-touch-callout:none;
}

body{
  width:100%;
  max-width:var(--RS__defaultLineLength) !important;
  margin:0 auto !important;
  box-sizing:border-box;
}

:root:not([style*="readium-scroll-on"]) body{
  padding:0 var(--RS__pageGutter) !important;
}

:root:not([style*="readium-noOverflow-on"]) body{
  overflow:hidden;
}

@supports (overflow: clip){

   :root:not([style*="readium-noOverflow-on"]){
      overflow:clip;
   }

   :root:not([style*="readium-noOverflow-on"]) body{
      overflow:clip;
      overflow-clip-margin:content-box;
   }
}

:root[style*="readium-scroll-on"]{
  -webkit-columns:auto auto !important;
  -moz-columns:auto auto !important;
  columns:auto auto !important;
  width:auto !important;
  height:auto !important;
  max-width:none !important;
  max-height:none !important;
  min-width:0 !important;
  min-height:0 !important;
}

:root[style*="readium-scroll-on"] body{
  max-width:var(--RS__defaultLineLength) !important;
  box-sizing:border-box !important;
}

:root[style*="readium-scroll-on"]:not([style*="readium-noOverflow-on"]) body{
  overflow:auto;
}

@supports (overflow: clip){

  :root[style*="readium-scroll-on"]:not([style*="readium-noOverflow-on"]){
     overflow:auto;
  }

  :root[style*="readium-scroll-on"]:not([style*="readium-noOverflow-on"]) body{
     overflow:clip;
  }
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingTop"] body{
  padding-top:var(--RS__scrollPaddingTop) !important;
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingBottom"] body{
  padding-bottom:var(--RS__scrollPaddingBottom) !important;
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingLeft"] body{
  padding-left:var(--RS__scrollPaddingLeft) !important;
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingRight"] body{
  padding-right:var(--RS__scrollPaddingRight) !important;
}

:root[style*="--USER__backgroundColor"]{
  background-color:var(--USER__backgroundColor) !important;
}

:root[style*="--USER__backgroundColor"] *{
  background-color:transparent !important;
}

:root[style*="--USER__textColor"]{
  color:var(--USER__textColor) !important;
}

:root[style*="--USER__textColor"] *:not(a){
  color:inherit !important;
  background-color:transparent !important;
  border-color:currentcolor !important;
}

:root[style*="--USER__textColor"] svg text{
  fill:currentcolor !important;
  stroke:none !important;
}

:root[style*="--USER__linkColor"] a:link,
:root[style*="--USER__linkColor"] a:link *{
  color:var(--USER__linkColor) !important;
}

:root[style*="--USER__visitedColor"] a:visited,
:root[style*="--USER__visitedColor"] a:visited *{
  color:var(--USER__visitedColor) !important;
}

:root[style*="--USER__selectionBackgroundColor"][style*="--USER__selectionTextColor"] ::-moz-selection{
  color:var(--USER__selectionTextColor) !important;
  background-color:var(--USER__selectionBackgroundColor) !important;
}

:root[style*="--USER__selectionBackgroundColor"][style*="--USER__selectionTextColor"] ::selection{
  color:var(--USER__selectionTextColor) !important;
  background-color:var(--USER__selectionBackgroundColor) !important;
}

:root[style*="--USER__colCount"]{
  -webkit-column-count:var(--USER__colCount);
  -moz-column-count:var(--USER__colCount);
  column-count:var(--USER__colCount);

  --RS__colWidth:auto;
}

:root[style*="--USER__colCount: 0"],
:root[style*="--USER__colCount:0"]{
  -webkit-column-count:1;
  -moz-column-count:1;
  column-count:1;
}

:root[style*="--USER__colCount: 0"],
:root[style*="--USER__colCount:0"],
:root[style*="--USER__colCount: 1"],
:root[style*="--USER__colCount:1"]{
  --RS__colWidth:100vw;
}

:root[style*="--USER__lineLength"] body{
    max-width:var(--USER__lineLength) !important;
  }

:root[style*="--USER__fontFamily"]{
  font-family:var(--USER__fontFamily) !important;
}

:root[style*="--USER__fontFamily"] *{
  font-family:revert !important;
}

:root:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] body{
  zoom:var(--USER__fontSize) !important;
}

:root:not([style*="readium-deprecatedFontSize-on"])[style*="readium-iOSPatch-on"][style*="--USER__fontSize"] body{
  -webkit-text-size-adjust:var(--USER__fontSize) !important;
}

@supports selector(figure:has(> img)){

  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> img),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> video),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> svg),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> canvas),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> iframe),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> audio),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> img:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> video:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> svg:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> canvas:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> iframe:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> audio:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] table{
    zoom:calc(100% / var(--USER__fontSize)) !important;
  }

  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figcaption,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] caption,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] td,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] th{
    zoom:var(--USER__fontSize) !important;
  }
}

@supports not (zoom: 1){

  :root[style*="--USER__fontSize"]{
    font-size:var(--USER__fontSize) !important;
  }
}

:root[style*="readium-deprecatedFontSize-on"][style*="--USER__fontSize"]{
  font-size:var(--USER__fontSize) !important;
}

:root[style*="--USER__lineHeight"]{
  line-height:var(--USER__lineHeight) !important;
}

:root[style*="--USER__lineHeight"] body,
:root[style*="--USER__lineHeight"] p,
:root[style*="--USER__lineHeight"] li,
:root[style*="--USER__lineHeight"] div{
  line-height:var(--USER__lineHeight) !important;
}

:root[style*="--USER__paraSpacing"] p{
  margin-top:var(--USER__paraSpacing) !important;
  margin-bottom:var(--USER__paraSpacing) !important;
}

:root[style*="--USER__fontWeight"] body{
  font-weight:var(--USER__fontWeight) !important;
}

:root[style*="--USER__fontWeight"] b,
:root[style*="--USER__fontWeight"] strong{
  font-weight:bolder;
}

:root[style*="--USER__fontWidth"] body{
  font-stretch:var(--USER__fontWidth) !important;
}

:root[style*="--USER__fontOpticalSizing"] body{
  font-optical-sizing:var(--USER__fontOpticalSizing) !important;
}

:root[style*="--USER__letterSpacing"] h1,
:root[style*="--USER__letterSpacing"] h2,
:root[style*="--USER__letterSpacing"] h3,
:root[style*="--USER__letterSpacing"] h4,
:root[style*="--USER__letterSpacing"] h5,
:root[style*="--USER__letterSpacing"] h6,
:root[style*="--USER__letterSpacing"] p,
:root[style*="--USER__letterSpacing"] li,
:root[style*="--USER__letterSpacing"] div,
:root[style*="--USER__letterSpacing"] dt,
:root[style*="--USER__letterSpacing"] dd{
  letter-spacing:var(--USER__letterSpacing) !important;
  font-variant:none !important;
}

:root[style*="readium-noRuby-on"] body rt,
:root[style*="readium-noRuby-on"] body rp{
  display:none;
}

:root[style*="readium-blend-on"] svg,
:root[style*="readium-blend-on"] img{
  background-color:transparent !important;
  mix-blend-mode:multiply !important;
}

:root[style*="--USER__darkenImages"] img{
  -webkit-filter:brightness(var(--USER__darkenImages)) !important;
  filter:brightness(var(--USER__darkenImages)) !important;
}

:root[style*="readium-darken-on"] img{
  -webkit-filter:brightness(80%) !important;
  filter:brightness(80%) !important;
}

:root[style*="--USER__invertImages"] img{
  -webkit-filter:invert(var(--USER__invertImages)) !important;
  filter:invert(var(--USER__invertImages)) !important;
}

:root[style*="readium-invert-on"] img{
  -webkit-filter:invert(100%) !important;
  filter:invert(100%) !important;
}

:root[style*="--USER__darkenImages"][style*="--USER__invertImages"] img{
  -webkit-filter:brightness(var(--USER__darkenImages)) invert(var(--USER__invertImages)) !important;
  filter:brightness(var(--USER__darkenImages)) invert(var(--USER__invertImages)) !important;
}

:root[style*="readium-darken-on"][style*="--USER__invertImages"] img{
  -webkit-filter:brightness(80%) invert(var(--USER__invertImages)) !important;
  filter:brightness(80%) invert(var(--USER__invertImages)) !important;
}

:root[style*="--USER__darkenImages"][style*="readium-invert-on"] img{
  -webkit-filter:brightness(var(--USER__darkenImages)) invert(100%) !important;
  filter:brightness(var(--USER__darkenImages)) invert(100%) !important;
}

:root[style*="readium-darken-on"][style*="readium-invert-on"] img{
  -webkit-filter:brightness(80%) invert(100%) !important;
  filter:brightness(80%) invert(100%) !important;
}

:root[style*="--USER__invertGaiji"] img[class*="gaiji"]{
  -webkit-filter:invert(var(--USER__invertGaiji)) !important;
  filter:invert(var(--USER__invertGaiji)) !important;
}

:root[style*="readium-invertGaiji-on"] img[class*="gaiji"]{
  -webkit-filter:invert(100%) !important;
  filter:invert(100%) !important;
}

:root[style*="readium-normalize-on"]{
  --USER__typeScale:1.2;
}

:root[style*="readium-normalize-on"] p,
:root[style*="readium-normalize-on"] li,
:root[style*="readium-normalize-on"] div,
:root[style*="readium-normalize-on"] pre,
:root[style*="readium-normalize-on"] dd{
  font-size:1rem !important;
}

:root[style*="readium-normalize-on"] h1{
  font-size:1.75rem !important;
  font-size:calc(((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"] h2{
  font-size:1.5rem !important;
  font-size:calc((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"] h3{
  font-size:1.25rem !important;
  font-size:calc(1rem * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"] h4,
:root[style*="readium-normalize-on"] h5,
:root[style*="readium-normalize-on"] h6{
  font-size:1rem !important;
}

:root[style*="readium-normalize-on"] small{
  font-size:smaller !important;
}

:root[style*="readium-normalize-on"] sub,
:root[style*="readium-normalize-on"] sup{
  font-size:67.5% !important;
}

:root[style*="readium-normalize-on"][style*="--USER__typeScale"] h1{
  font-size:calc(((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"][style*="--USER__typeScale"] h2{
  font-size:calc((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"][style*="--USER__typeScale"] h3{
  font-size:calc(1rem * var(--USER__typeScale)) !important;
}

:root[style*="readium-iPadOSPatch-on"] body{
  -webkit-text-size-adjust:none;
}

:root[style*="readium-iPadOSPatch-on"] p, 
:root[style*="readium-iPadOSPatch-on"] h1, 
:root[style*="readium-iPadOSPatch-on"] h2, 
:root[style*="readium-iPadOSPatch-on"] h3, 
:root[style*="readium-iPadOSPatch-on"] h4, 
:root[style*="readium-iPadOSPatch-on"] h5, 
:root[style*="readium-iPadOSPatch-on"] h6, 
:root[style*="readium-iPadOSPatch-on"] li, 
:root[style*="readium-iPadOSPatch-on"] th, 
:root[style*="readium-iPadOSPatch-on"] td, 
:root[style*="readium-iPadOSPatch-on"] dt, 
:root[style*="readium-iPadOSPatch-on"] dd, 
:root[style*="readium-iPadOSPatch-on"] pre, 
:root[style*="readium-iPadOSPatch-on"] address, 
:root[style*="readium-iPadOSPatch-on"] details, 
:root[style*="readium-iPadOSPatch-on"] summary,
:root[style*="readium-iPadOSPatch-on"] figcaption,
:root[style*="readium-iPadOSPatch-on"] div:not(:has(p, h1, h2, h3, h4, h5, h6, li, th, td, dt, dd, pre, address, aside, details, figcaption, summary)),
:root[style*="readium-iPadOSPatch-on"] aside:not(:has(p, h1, h2, h3, h4, h5, h6, li, th, td, dt, dd, pre, address, aside, details, figcaption, summary)){
  -webkit-text-zoom:reset;
}

:root[style*="readium-iPadOSPatch-on"] abbr, 
:root[style*="readium-iPadOSPatch-on"] b, 
:root[style*="readium-iPadOSPatch-on"] bdi, 
:root[style*="readium-iPadOSPatch-on"] bdo, 
:root[style*="readium-iPadOSPatch-on"] cite, 
:root[style*="readium-iPadOSPatch-on"] code, 
:root[style*="readium-iPadOSPatch-on"] dfn, 
:root[style*="readium-iPadOSPatch-on"] em, 
:root[style*="readium-iPadOSPatch-on"] i, 
:root[style*="readium-iPadOSPatch-on"] kbd, 
:root[style*="readium-iPadOSPatch-on"] mark, 
:root[style*="readium-iPadOSPatch-on"] q, 
:root[style*="readium-iPadOSPatch-on"] rp, 
:root[style*="readium-iPadOSPatch-on"] rt, 
:root[style*="readium-iPadOSPatch-on"] ruby, 
:root[style*="readium-iPadOSPatch-on"] s, 
:root[style*="readium-iPadOSPatch-on"] samp, 
:root[style*="readium-iPadOSPatch-on"] small, 
:root[style*="readium-iPadOSPatch-on"] span, 
:root[style*="readium-iPadOSPatch-on"] strong, 
:root[style*="readium-iPadOSPatch-on"] sub, 
:root[style*="readium-iPadOSPatch-on"] sup, 
:root[style*="readium-iPadOSPatch-on"] time, 
:root[style*="readium-iPadOSPatch-on"] u, 
:root[style*="readium-iPadOSPatch-on"] var{
  -webkit-text-zoom:normal;
}

:root[style*="readium-iPadOSPatch-on"] p:not(:has(b, cite, em, i, q, s, small, span, strong)):first-line{
  -webkit-text-zoom:normal;
}`});var fd={};_(fd,{default:()=>Lg});var Lg,yd=Me(()=>{Lg=`/*!
 * Readium CSS v.2.0.5
 * Copyright (c) 2017\u20132026. Readium Foundation. All rights reserved.
 * Use of this source code is governed by a BSD-style license which is detailed in the
 * LICENSE file present in the project repository where this source code is maintained.
 * Core maintainer: Jiminy Panoz <jiminy.panoz@edrlab.org> 
 * Contributors: 
 * Daniel Weck
 * Hadrien Gardeur
 * Innovimax
 * L. Le Meur
 * Mickae\u0308l Menu
 * k_taka
 */

@namespace url("http://www.w3.org/1999/xhtml");

@namespace epub url("http://www.idpf.org/2007/ops");

@namespace m url("http://www.w3.org/1998/Math/MathML");

@namespace svg url("http://www.w3.org/2000/svg");

@-ms-viewport{
  width:device-width;
}

@viewport{
  width:device-width;
  zoom:1;
}

:root{

  --RS__sans-serif-ja-v:'Hiragino Sans', 'Hiragino Kaku Gothic ProN', 'Hiragino Kaku Gothic Pro', '\u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 W3', 'YuGothic', 'Yu Gothic Medium', 'BIZ UDGothic', 'Yu Gothic', '\uFF2D\uFF33\u30B4\u30B7\u30C3\u30AF', 'MS Gothic', sans-serif;

  --RS__serif-ja-v:'Hiragino Mincho ProN', 'Hiragino Mincho Pro', 'YuMincho', 'BIZ UDMincho', 'Yu Mincho', '\uFF2D\uFF33\u660E\u671D', 'MS Mincho', serif;

  --RS__sans-serif-ja:'Hiragino Sans', 'Hiragino Kaku Gothic ProN', 'Hiragino Kaku Gothic Pro', '\u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 W3', 'YuGothic', 'Yu Gothic Medium', 'BIZ UDPGothic', 'Yu Gothic', '\uFF2D\uFF33 \uFF30\u30B4\u30B7\u30C3\u30AF', 'MS PGothic', sans-serif;

  --RS__serif-ja:'Hiragino Mincho ProN', 'Hiragino Mincho Pro', 'YuMincho', 'BIZ UDPMincho', 'Yu Mincho', '\uFF2D\uFF33 \uFF30\u660E\u671D', 'MS PMincho', serif;

  --RS__monospaceTf:ui-monospace, 'Andale Mono', 'Cascadia Code', 'Source Code Pro', Menlo, Consolas, 'DejaVu Sans Mono', monospace;

  --RS__humanistTf:Seravek, Calibri, 'Gill Sans Nova', Roboto, Ubuntu, 'DejaVu Sans', source-sans-pro, sans-serif;

  --RS__sansTf:-ui-sans-serif, -apple-system, system-ui, BlinkMacSystemFont, 'Segoe UI Variable', 'Segoe UI', Inter, Roboto, 'Helvetica Neue', 'Arial Nova', 'Liberation Sans', Arial, sans-serif;

  --RS__modernTf:Athelas, Constantia, Charter, 'Bitstream Charter', Cambria, 'Georgia Pro', Georgia, serif;

  --RS__oldStyleTf:'Iowan Old Style', Sitka, 'Sitka Text', Palatino, 'Book Antiqua', 'URW Palladio L', P052, serif;

  --RS__zh-HK-lineHeightCompensation:1.167;

  --RS__zh-HK-baseFontFamily:'\u65B9\u9AD4', 'PingFang HK', '\u65B9\u9AD4', 'PingFang TC', '\u9ED1\u9AD4', 'Heiti TC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK TC', sans-serif;

  --RS__zh-TW-lineHeightCompensation:1.167;

  --RS__zh-TW-baseFontFamily:'\u65B9\u9AD4', 'PingFang TC', '\u9ED1\u9AD4', 'Heiti TC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK TC', sans-serif;

  --RS__zh-Hant-lineHeightCompensation:1.167;

  --RS__zh-Hant-baseFontFamily:'\u65B9\u9AD4', 'PingFang TC', '\u9ED1\u9AD4', 'Heiti TC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK TC', sans-serif;

  --RS__zh-lineHeightCompensation:1.167;

  --RS__zh-baseFontFamily:'\u65B9\u4F53', 'PingFang SC', '\u9ED1\u4F53', 'Heiti SC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK SC', sans-serif;

  --RS__th-lineHeightCompensation:1.067;

  --RS__th-baseFontFamily:Thonburi, 'Leelawadee UI', 'Cordia New', Roboto, Noto, 'Noto Sans Thai', sans-serif;

  --RS__te-baseFontFamily:'Kohinoor Telugu', 'Telugu Sangam MN', 'Nirmala UI', Gautami, Roboto, Noto, 'Noto Sans Telugu', sans-serif;

  --RS__ta-lineHeightCompensation:1.067;

  --RS__ta-baseFontFamily:'Tamil Sangam MN', 'Nirmala UI', Latha, Roboto, Noto, 'Noto Sans Tamil', sans-serif;

  --RS__si-lineHeightCompensation:1.167;

  --RS__si-baseFontFamily:'Sinhala Sangam MN', 'Nirmala UI', 'Iskoola Pota', Roboto, Noto, 'Noto Sans Sinhala', sans-serif;

  --RS__pa-lineHeightCompensation:1.1;

  --RS__pa-baseFontFamily:'Gurmukhi MN', 'Nirmala UI', Kartika, Roboto, Noto, 'Noto Sans Gurmukhi', sans-serif;

  --RS__or-lineHeightCompensation:1.167;

  --RS__or-baseFontFamily:'Oriya Sangam MN', 'Nirmala UI', Kalinga, Roboto, Noto, 'Noto Sans Oriya', sans-serif;

  --RS__ml-lineHeightCompensation:1.067;

  --RS__ml-baseFontFamily:'Malayalam Sangam MN', 'Nirmala UI', Kartika, Roboto, Noto, 'Noto Sans Malayalam', sans-serif;

  --RS__lo-baseFontFamily:'Lao Sangam MN', 'Leelawadee UI', 'Lao UI', Roboto, Noto, 'Noto Sans Lao', sans-serif;

  --RS__ko-lineHeightCompensation:1.167;

  --RS__ko-baseFontFamily:'Nanum Gothic', 'Apple SD Gothic Neo', 'Malgun Gothic', Roboto, Noto, 'Noto Sans CJK KR', sans-serif;

  --RS__kn-lineHeightCompensation:1.1;

  --RS__kn-baseFontFamily:'Kannada Sangam MN', 'Nirmala UI', Tunga, Roboto, Noto, 'Noto Sans Kannada', sans-serif;

  --RS__km-lineHeightCompensation:1.067;

  --RS__km-baseFontFamily:'Khmer Sangam MN', 'Leelawadee UI', 'Khmer UI', Roboto, Noto, 'Noto Sans Khmer', sans-serif;

  --RS__ja-lineHeightCompensation:1.167;

  --RS__ja-baseFontFamily:YuGothic, 'Hiragino Maru Gothic ProN', 'Hiragino Sans', 'Yu Gothic UI', 'Meiryo UI', 'MS Gothic', Roboto, Noto, 'Noto Sans CJK JP', sans-serif;

  --RS__iu-baseFontFamily:'Euphemia UCAS', Euphemia, Roboto, Noto, 'Noto Sans Canadian Aboriginal', sans-serif;

  --RS__hy-baseFontFamily:Mshtakan, Sylfaen, Roboto, Noto, 'Noto Serif Armenian', serif;

  --RS__hi-lineHeightCompensation:1.1;

  --RS__hi-baseFontFamily:'Kohinoor Devanagari', 'Devanagari Sangam MN', Kokila, 'Nirmala UI', Roboto, Noto, 'Noto Sans Devanagari', sans-serif;

  --RS__he-lineHeightCompensation:1.1;

  --RS__he-baseFontFamily:'New Peninim MT', 'Arial Hebrew', Gisha, 'Times New Roman', Roboto, Noto, 'Noto Sans Hebrew', sans-serif;

  --RS__gu-lineHeightCompensation:1.167;

  --RS__gu-baseFontFamily:'Gujarati Sangam MN', 'Nirmala UI', Shruti, Roboto, Noto, 'Noto Sans Gujarati', sans-serif;

  --RS__fa-baseFontFamily:'Geeza Pro', 'Arabic Typesetting', Roboto, Noto, 'Noto Naskh Arabic', 'Times New Roman', serif;

  --RS__chr-lineHeightCompensation:1.167;

  --RS__chr-baseFontFamily:'Plantagenet Cherokee', Roboto, Noto, 'Noto Sans Cherokee';

  --RS__bo-baseFontFamily:Kailasa, 'Microsoft Himalaya', Roboto, Noto, 'Noto Sans Tibetan', sans-serif;

  --RS__bn-lineHeightCompensation:1.067;

  --RS__bn-baseFontFamily:'Kohinoor Bangla', 'Bangla Sangam MN', Vrinda, Roboto, Noto, 'Noto Sans Bengali', sans-serif;

  --RS__ar-baseFontFamily:'Geeza Pro', 'Arabic Typesetting', Roboto, Noto, 'Noto Naskh Arabic', 'Times New Roman', serif;

  --RS__am-lineHeightCompensation:1.167;

  --RS__am-baseFontFamily:Kefa, Nyala, Roboto, Noto, 'Noto Sans Ethiopic', serif;

  --RS__latin-lineHeightCompensation:1;

  --RS__latin-baseFontFamily:var(--RS__oldStyleTf);
  --RS__baseFontFamily:var(--RS__latin-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__latin-lineHeightCompensation);
  --RS__baseLineHeight:calc(1.5 * var(--RS__lineHeightCompensation));

  --RS__selectionTextColor:inherit;

  --RS__selectionBackgroundColor:#b4d8fe;

  --RS__visitedColor:#551A8B;

  --RS__linkColor:#0000EE;

  --RS__textColor:#121212;

  --RS__backgroundColor:#FFFFFF;
  color:var(--RS__textColor) !important;

  background-color:var(--RS__backgroundColor) !important;
}

::-moz-selection{
  color:var(--RS__selectionTextColor);
  background-color:var(--RS__selectionBackgroundColor);
}

::selection{
  color:var(--RS__selectionTextColor);
  background-color:var(--RS__selectionBackgroundColor);
}

html{
  font-family:var(--RS__baseFontFamily);
  line-height:1.6;
  line-height:var(--RS__baseLineHeight);
  text-rendering:optimizelegibility;
}

h1, h2, h3{
  line-height:normal;
}

:lang(ja),
:lang(zh),
:lang(ko){
  word-wrap:break-word;
  -webkit-line-break:strict;
  -epub-line-break:strict;
  line-break:strict;
}

math{
  font-family:"Latin Modern Math", "STIX Two Math", "XITS Math", "STIX Math", "Libertinus Math", "TeX Gyre Termes Math", "TeX Gyre Bonum Math", "TeX Gyre Schola", "DejaVu Math TeX Gyre", "TeX Gyre Pagella Math", "Asana Math", "Cambria Math", "Lucida Bright Math", "Minion Math", STIXGeneral, STIXSizeOneSym, Symbol, "Times New Roman", serif;
}

:lang(am){
  --RS__baseFontFamily:var(--RS__am-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__am-lineHeightCompensation);
}

:lang(ar){
  --RS__baseFontFamily:var(--RS__ar-baseFontFamily);
}

:lang(bn){
  --RS__baseFontFamily:var(--RS__bn-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__bn-lineHeightCompensation);
}

:lang(bo){
  --RS__baseFontFamily:var(--RS__bo-baseFontFamily);
}

:lang(chr){
  --RS__baseFontFamily:var(--RS__chr-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__chr-lineHeightCompensation);
}

:lang(fa){
  --RS__baseFontFamily:var(--RS__fa-baseFontFamily);
}

:lang(gu){
  --RS__baseFontFamily:var(--RS__gu-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__gu-lineHeightCompensation);
}

:lang(he){
  --RS__baseFontFamily:var(--RS__he-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__he-lineHeightCompensation);
}

:lang(hi){
  --RS__baseFontFamily:var(--RS__hi-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__hi-lineHeightCompensation);
}

:lang(hy){
  --RS__baseFontFamily:var(--RS__hy-baseFontFamily);
}

:lang(iu){
  --RS__baseFontFamily:var(--RS__iu-baseFontFamily);
}

:lang(ja){
  --RS__baseFontFamily:var(--RS__ja-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ja-lineHeightCompensation);
}

:lang(km){
  --RS__baseFontFamily:var(--RS__km-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__km-lineHeightCompensation);
}

:lang(kn){
  --RS__baseFontFamily:var(--RS__kn-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__kn-lineHeightCompensation);
}

:lang(ko){
  --RS__baseFontFamily:var(--RS__ko-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ko-lineHeightCompensation);
}

:lang(lo){
  --RS__baseFontFamily:var(--RS__lo-baseFontFamily);
}

:lang(ml){
  --RS__baseFontFamily:var(--RS__ml-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ml-lineHeightCompensation);
}

:lang(or){
  --RS__baseFontFamily:var(--RS__or-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__or-lineHeightCompensation);
}

:lang(pa){
  --RS__baseFontFamily:var(--RS__pa-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__pa-lineHeightCompensation);
}

:lang(si){
  --RS__baseFontFamily:var(--RS__si-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__si-lineHeightCompensation);
}

:lang(ta){
  --RS__baseFontFamily:var(--RS__ta-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ta-lineHeightCompensation);
}

:lang(te){
  --RS__baseFontFamily:var(--RS__te-baseFontFamily);
}

:lang(th){
  --RS__baseFontFamily:var(--RS__th-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__th-lineHeightCompensation);
}

:lang(zh){
  --RS__baseFontFamily:var(--RS__zh-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-lineHeightCompensation);
}

:lang(zh-Hant){
  --RS__baseFontFamily:var(--RS__zh-Hant-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-Hant-lineHeightCompensation);
}

:lang(zh-TW){
  --RS__baseFontFamily:var(--RS__zh-TW-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-TW-lineHeightCompensation);
}

:lang(zh-HK){
  --RS__baseFontFamily:var(--RS__zh-HK-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-HK-lineHeightCompensation);
}

body{
  widows:2;
  orphans:2;
}

figcaption, th, td{
  widows:1;
  orphans:1;
}

h2, h3, h4, h5, h6, dt,
hr, caption{
  -webkit-column-break-after:avoid;
  page-break-after:avoid;
  break-after:avoid;
}

h1, h2, h3, h4, h5, h6, dt,
figure, tr{
  -webkit-column-break-inside:avoid;
  page-break-inside:avoid;
  break-inside:avoid;
}

body{
  -webkit-hyphenate-character:"\\002D";
  -moz-hyphenate-character:"\\002D";
  -ms-hyphenate-character:"\\002D";
  hyphenate-character:"\\002D";
  -webkit-hyphenate-limit-lines:3;
  -ms-hyphenate-limit-lines:3;
  hyphenate-limit-lines:3;
}

h1, h2, h3, h4, h5, h6, dt,
figcaption, pre, caption, address,
center, code, var{
  -ms-hyphens:none;
  -moz-hyphens:none;
  -webkit-hyphens:none;
  -epub-hyphens:none;
  hyphens:none;
}

body{
  font-variant-numeric:oldstyle-nums proportional-nums;
}

:lang(ja) body,
:lang(zh) body,
:lang(ko) body{
  font-variant-numeric:lining-nums proportional-nums;
}

h1, h2, h3, h4, h5, h6, dt{
  font-variant-numeric:lining-nums proportional-nums;
}

table{
  font-variant-numeric:lining-nums tabular-nums;
}

code, var{
  font-variant-ligatures:none;
  font-variant-numeric:lining-nums tabular-nums slashed-zero;
}

rt{
  font-variant-east-asian:ruby;
}

:lang(ar){
  font-variant-ligatures:common-ligatures;
}

:lang(ko){
  font-kerning:normal;
}

hr{
  color:inherit;
  border-color:currentcolor;
}

table, th, td{
  border-color:currentcolor;
}

figure, blockquote{
  margin:1em 5%;
}

ul, ol{
  padding-left:5%;
}

dd{
  margin-left:5%;
}

pre{
  white-space:pre-wrap;
  -ms-tab-size:2;
  -moz-tab-size:2;
  -webkit-tab-size:2;
  tab-size:2;
}

abbr[title], acronym[title]{
  text-decoration:dotted underline;
}

nobr wbr{
  white-space:normal;
}

ruby > rt, ruby > rp{
  -webkit-user-select:none;
  -moz-user-select:none;
  -ms-user-select:none;
  user-select:none;
}

*:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)),
*:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)),
*:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)),
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) cite, 
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) dfn, 
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) em, 
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) i,
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) cite, 
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) dfn, 
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) em, 
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) i,
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) cite, 
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) dfn, 
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) em, 
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) i{
  font-style:normal;
}

:lang(ja) a,
:lang(zh) a,
:lang(ko) a{
  text-decoration:none;
}

:root{
  --RS__maxMediaWidth:100%;
  --RS__maxMediaHeight:100vw;
  --RS__boxSizingMedia:border-box;
  --RS__boxSizingTable:border-box;
}

a, a span, span a, h1, h2, h3, h4, h5, h6{
  word-wrap:break-word;
}

div{
  max-width:var(--RS__maxMediaHeight);
}

img, svg|svg, video{
  object-fit:contain;

  width:auto;
  height:auto;
  max-width:var(--RS__maxMediaHeight);
  max-height:var(--RS__maxMediaWidth) !important;
  box-sizing:var(--RS__boxSizingMedia);
  -webkit-column-break-inside:avoid;
  page-break-inside:avoid;
  break-inside:avoid;
}

@supports (zoom: 1) and (not ((-webkit-column-axis: horizontal) and (-webkit-column-progression: normal))){

  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] img,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] svg|svg,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] video,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div{
    zoom:calc(100% / var(--USER__fontSize));
  }
}

audio{
  max-width:100%;
  -webkit-column-break-inside:avoid;
  page-break-inside:avoid;
  break-inside:avoid;
}

table{
  max-height:var(--RS__maxMediaWidth);
  box-sizing:var(--RS__boxSizingTable);
}`});var Sd={};_(Sd,{default:()=>Og});var Og,bd=Me(()=>{Og=`/*!
 * Readium CSS v.2.0.5
 * Copyright (c) 2017\u20132026. Readium Foundation. All rights reserved.
 * Use of this source code is governed by a BSD-style license which is detailed in the
 * LICENSE file present in the project repository where this source code is maintained.
 * Core maintainer: Jiminy Panoz <jiminy.panoz@edrlab.org> 
 * Contributors: 
 * Daniel Weck
 * Hadrien Gardeur
 * Innovimax
 * L. Le Meur
 * Mickae\u0308l Menu
 * k_taka
 */

@namespace url("http://www.w3.org/1999/xhtml");

@namespace epub url("http://www.idpf.org/2007/ops");

@namespace m url("http://www.w3.org/1998/Math/MathML");

@namespace svg url("http://www.w3.org/2000/svg");

:root{

  --RS__compFontFamily:var(--RS__baseFontFamily);
  --RS__codeFontFamily:var(--RS__monospaceTf);

  --RS__typeScale:1.125;
  --RS__baseFontSize:87.5%;

  --RS__flowSpacing:1.5rem;
  --RS__paraSpacing:0;
  --RS__paraIndent:1em;

  --RS__linkColor:#0000EE;
  --RS__visitedColor:#551A8B;

  --RS__primaryColor:;
  --RS__secondaryColor:;
}

:root:lang(zh){
  --RS__paraIndent:2em;
}

:lang("mn-Mong"){
  --RS__baseFontSize:100%;
}

body{
  font-size:var(--RS__baseFontSize);
  text-align:justify;
  text-justify:inter-character;
}

h1, h2, h3, h4, h5, h6{
  font-family:var(--RS__baseFontFamily);
  text-align:left;
  text-align:start;
}

blockquote, figure, p, pre,
aside, footer, form, hr{
  margin-right:var(--RS__flowSpacing);
  margin-left:var(--RS__flowSpacing);
}

p{
  margin-right:var(--RS__paraSpacing);
  margin-left:var(--RS__paraSpacing);
  text-indent:var(--RS__paraIndent);
}

pre{
  font-family:var(--RS__codeFontFamily);
}

code, kbd, samp, tt{
  font-family:var(--RS__codeFontFamily);
}

sub, sup{
  position:relative;
  font-size:67.5%;
  line-height:1;
}

sub{
  left:-0.2ex;
}

sup{
  right:0;
}

em{
  -webkit-text-emphasis:sesame;
  -epub-text-emphasis:sesame;
  text-emphasis:sesame;
}

:link{
  color:var(--RS__linkColor);
}

:visited{
  color:var(--RS__visitedColor);
}

h1{
  margin-right:calc(var(--RS__flowSpacing) * 2);
  margin-left:calc(var(--RS__flowSpacing) * 2);
  font-size:calc(((1em * var(--RS__typeScale)) * var(--RS__typeScale)) * var(--RS__typeScale));
  text-indent:2rem;
}

h2{
  margin-right:calc(var(--RS__flowSpacing) * 2);
  margin-left:var(--RS__flowSpacing);
  font-size:calc((1em * var(--RS__typeScale)) * var(--RS__typeScale));
  text-indent:3rem;
}

h3{
  margin-right:var(--RS__flowSpacing);
  margin-left:var(--RS__flowSpacing);
  font-size:calc(1em * var(--RS__typeScale));
  text-indent:4rem;
}

h4{
  margin-right:var(--RS__flowSpacing);
  margin-left:var(--RS__flowSpacing);
  font-family:var(--RS__compFontFamily);
  font-size:1em;
  text-indent:4rem;
}

h5{
  margin-right:var(--RS__flowSpacing);
  margin-left:var(--RS__flowSpacing);
  font-family:var(--RS__compFontFamily);
  font-size:smaller;
  text-indent:4rem;
}

h6{
  margin-right:var(--RS__flowSpacing);
  margin-left:0;
  font-family:var(--RS__compFontFamily);
  font-size:smaller;
  font-weight:normal;
  text-indent:4rem;
}

dl, ol, ul{
  margin-right:var(--RS__flowSpacing);
  margin-left:var(--RS__flowSpacing);
}

table{
  margin:0 var(--RS__flowSpacing);
  border:1px solid currentcolor;
  border-collapse:collapse;
  empty-cells:show;
}

thead, tbody, tfoot, table > tr{
  vertical-align:top;
}

th{
  text-align:left;
}

th, td{
  padding:4px;
  border:1px solid currentcolor;
}`});var _d={};_(_d,{default:()=>Ag});var Ag,vd=Me(()=>{Ag=`/*!
 * Readium CSS v.2.0.5
 * Copyright (c) 2017\u20132026. Readium Foundation. All rights reserved.
 * Use of this source code is governed by a BSD-style license which is detailed in the
 * LICENSE file present in the project repository where this source code is maintained.
 * Core maintainer: Jiminy Panoz <jiminy.panoz@edrlab.org> 
 * Contributors: 
 * Daniel Weck
 * Hadrien Gardeur
 * Innovimax
 * L. Le Meur
 * Mickae\u0308l Menu
 * k_taka
 */

@namespace url("http://www.w3.org/1999/xhtml");

@namespace epub url("http://www.idpf.org/2007/ops");

@namespace m url("http://www.w3.org/1998/Math/MathML");

@namespace svg url("http://www.w3.org/2000/svg");

:root{

   --RS__viewportWidth:100%;

   --RS__pageGutter:0;

   --RS__defaultLineLength:100%;

   --RS__colGap:0;

   --RS__colCount:1;

   --RS__colWidth:100vw;
}

@page{
  margin:0 !important;
}

:root{
  position:relative;

  -webkit-column-width:var(--RS__colWidth);
  -moz-column-width:var(--RS__colWidth);
  column-width:var(--RS__colWidth);
  -webkit-column-count:var(--RS__colCount);
  -moz-column-count:var(--RS__colCount);
  column-count:var(--RS__colCount);

  -webkit-column-gap:var(--RS__colGap);
  -moz-column-gap:var(--RS__colGap);
  column-gap:var(--RS__colGap);
  -moz-column-fill:auto;
  column-fill:auto;
  width:100%;
  height:100vh;
  max-width:100%;
  max-height:100vh;
  min-width:100%;
  min-height:100vh;
  padding:0 !important;
  margin:0 !important;
  font-size:1rem !important;
  box-sizing:border-box;

  hanging-punctuation:last allow-end;
  -webkit-touch-callout:none;
  -ms-writing-mode:tb-rl;
  -webkit-writing-mode:vertical-rl;
  writing-mode:vertical-rl;
}

:root:lang(mn-Mong){
  -ms-writing-mode:tb;
  -webkit-writing-mode:vertical-lr;
  writing-mode:vertical-lr;
}

body{
  width:100%;
  max-height:var(--RS__defaultLineLength) !important;
  margin:auto 0 !important;
  box-sizing:border-box;
}

:root:not([style*="readium-scroll-on"]) body{
  padding:var(--RS__pageGutter) 0 !important;
}

:root:not([style*="readium-noOverflow-on"]) body{
  overflow:hidden;
}

@supports (overflow: clip){

  :root:not([style*="readium-noOverflow-on"]){
     overflow:clip;
  }

  :root:not([style*="readium-noOverflow-on"]) body{
     overflow:clip;
     overflow-clip-margin:content-box;
  }
}

:root[style*="readium-scroll-on"],
:root[style*="readium-noVerticalPagination-on"]{
  -webkit-columns:auto auto !important;
  -moz-columns:auto auto !important;
  columns:auto auto !important;
  width:auto !important;
  max-width:none !important;
  max-height:100vh !important;
  min-width:0 !important;
}

:root[style*="readium-scroll-on"] body,
:root[style*="readium-noVerticalPagination-on"] body{
  max-width:var(--RS__defaultLineLength) !important;
  box-sizing:border-box !important;
}

@supports (overflow: clip){

  :root[style*="readium-scroll-on"]:not([style*="readium-noOverflow-on"]){
     overflow:auto;
  }

  :root[style*="readium-scroll-on"]:not([style*="readium-noOverflow-on"]) body{
     overflow:clip;
  }
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingTop"] body{
  padding-top:var(--RS__scrollPaddingTop) !important;
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingBottom"] body{
  padding-bottom:var(--RS__scrollPaddingBottom) !important;
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingLeft"] body{
  padding-left:var(--RS__scrollPaddingLeft) !important;
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingRight"] body{
  padding-right:var(--RS__scrollPaddingRight) !important;
}

:root[style*="--USER__backgroundColor"]{
  background-color:var(--USER__backgroundColor) !important;
}

:root[style*="--USER__backgroundColor"] *{
  background-color:transparent !important;
}

:root[style*="--USER__textColor"]{
  color:var(--USER__textColor) !important;
}

:root[style*="--USER__textColor"] *:not(a){
  color:inherit !important;
  background-color:transparent !important;
  border-color:currentcolor !important;
}

:root[style*="--USER__textColor"] svg text{
  fill:currentcolor !important;
  stroke:none !important;
}

:root[style*="--USER__linkColor"] a:link,
:root[style*="--USER__linkColor"] a:link *{
  color:var(--USER__linkColor) !important;
}

:root[style*="--USER__visitedColor"] a:visited,
:root[style*="--USER__visitedColor"] a:visited *{
  color:var(--USER__visitedColor) !important;
}

:root[style*="--USER__selectionBackgroundColor"][style*="--USER__selectionTextColor"] ::-moz-selection{
  color:var(--USER__selectionTextColor) !important;
  background-color:var(--USER__selectionBackgroundColor) !important;
}

:root[style*="--USER__selectionBackgroundColor"][style*="--USER__selectionTextColor"] ::selection{
  color:var(--USER__selectionTextColor) !important;
  background-color:var(--USER__selectionBackgroundColor) !important;
}

:root[style*="--USER__lineLength"] body{
    max-height:var(--USER__lineLength) !important;
  }

:root[style*="--USER__fontFamily"]{
  font-family:var(--USER__fontFamily) !important;
}

:root[style*="--USER__fontFamily"] *{
  font-family:revert !important;
}

:root:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] body{
  zoom:var(--USER__fontSize) !important;
}

:root:not([style*="readium-deprecatedFontSize-on"])[style*="readium-iOSPatch-on"][style*="--USER__fontSize"] body{
  -webkit-text-size-adjust:var(--USER__fontSize) !important;
}

@supports selector(figure:has(> img)){

  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> img),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> video),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> svg),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> canvas),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> iframe),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> audio),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> img:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> video:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> svg:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> canvas:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> iframe:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> audio:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] table{
    zoom:calc(100% / var(--USER__fontSize)) !important;
  }

  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figcaption,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] caption,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] td,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] th{
    zoom:var(--USER__fontSize) !important;
  }
}

@supports not (zoom: 1){

  :root[style*="--USER__fontSize"]{
    font-size:var(--USER__fontSize) !important;
  }
}

:root[style*="readium-deprecatedFontSize-on"][style*="--USER__fontSize"]{
  font-size:var(--USER__fontSize) !important;
}

:root[style*="--USER__lineHeight"]{
  line-height:var(--USER__lineHeight) !important;
}

:root[style*="--USER__lineHeight"] body,
:root[style*="--USER__lineHeight"] p,
:root[style*="--USER__lineHeight"] li,
:root[style*="--USER__lineHeight"] div{
  line-height:var(--USER__lineHeight) !important;
}

:root[style*="--USER__paraSpacing"] p{
  margin-right:var(--USER__paraSpacing) !important;
  margin-left:var(--USER__paraSpacing) !important;
}

:root[style*="--USER__fontWeight"] body{
  font-weight:var(--USER__fontWeight) !important;
}

:root[style*="--USER__fontWeight"] b,
:root[style*="--USER__fontWeight"] strong{
  font-weight:bolder;
}

:root[style*="--USER__fontWidth"] body{
  font-stretch:var(--USER__fontWidth) !important;
}

:root[style*="--USER__fontOpticalSizing"] body{
  font-optical-sizing:var(--USER__fontOpticalSizing) !important;
}

:root[style*="--USER__letterSpacing"] h1,
:root[style*="--USER__letterSpacing"] h2,
:root[style*="--USER__letterSpacing"] h3,
:root[style*="--USER__letterSpacing"] h4,
:root[style*="--USER__letterSpacing"] h5,
:root[style*="--USER__letterSpacing"] h6,
:root[style*="--USER__letterSpacing"] p,
:root[style*="--USER__letterSpacing"] li,
:root[style*="--USER__letterSpacing"] div,
:root[style*="--USER__letterSpacing"] dt,
:root[style*="--USER__letterSpacing"] dd{
  letter-spacing:var(--USER__letterSpacing) !important;
  font-variant:none !important;
}

:root[style*="readium-noRuby-on"] body rt,
:root[style*="readium-noRuby-on"] body rp{
  display:none;
}

:root[style*="readium-blend-on"] svg,
:root[style*="readium-blend-on"] img{
  background-color:transparent !important;
  mix-blend-mode:multiply !important;
}

:root[style*="--USER__darkenImages"] img{
  -webkit-filter:brightness(var(--USER__darkenImages)) !important;
  filter:brightness(var(--USER__darkenImages)) !important;
}

:root[style*="readium-darken-on"] img{
  -webkit-filter:brightness(80%) !important;
  filter:brightness(80%) !important;
}

:root[style*="--USER__invertImages"] img{
  -webkit-filter:invert(var(--USER__invertImages)) !important;
  filter:invert(var(--USER__invertImages)) !important;
}

:root[style*="readium-invert-on"] img{
  -webkit-filter:invert(100%) !important;
  filter:invert(100%) !important;
}

:root[style*="--USER__darkenImages"][style*="--USER__invertImages"] img{
  -webkit-filter:brightness(var(--USER__darkenImages)) invert(var(--USER__invertImages)) !important;
  filter:brightness(var(--USER__darkenImages)) invert(var(--USER__invertImages)) !important;
}

:root[style*="readium-darken-on"][style*="--USER__invertImages"] img{
  -webkit-filter:brightness(80%) invert(var(--USER__invertImages)) !important;
  filter:brightness(80%) invert(var(--USER__invertImages)) !important;
}

:root[style*="--USER__darkenImages"][style*="readium-invert-on"] img{
  -webkit-filter:brightness(var(--USER__darkenImages)) invert(100%) !important;
  filter:brightness(var(--USER__darkenImages)) invert(100%) !important;
}

:root[style*="readium-darken-on"][style*="readium-invert-on"] img{
  -webkit-filter:brightness(80%) invert(100%) !important;
  filter:brightness(80%) invert(100%) !important;
}

:root[style*="--USER__invertGaiji"] img[class*="gaiji"]{
  -webkit-filter:invert(var(--USER__invertGaiji)) !important;
  filter:invert(var(--USER__invertGaiji)) !important;
}

:root[style*="readium-invertGaiji-on"] img[class*="gaiji"]{
  -webkit-filter:invert(100%) !important;
  filter:invert(100%) !important;
}

:root[style*="readium-normalize-on"]{
  --USER__typeScale:1.2;
}

:root[style*="readium-normalize-on"] p,
:root[style*="readium-normalize-on"] li,
:root[style*="readium-normalize-on"] div,
:root[style*="readium-normalize-on"] pre,
:root[style*="readium-normalize-on"] dd{
  font-size:1rem !important;
}

:root[style*="readium-normalize-on"] h1{
  font-size:1.75rem !important;
  font-size:calc(((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"] h2{
  font-size:1.5rem !important;
  font-size:calc((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"] h3{
  font-size:1.25rem !important;
  font-size:calc(1rem * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"] h4,
:root[style*="readium-normalize-on"] h5,
:root[style*="readium-normalize-on"] h6{
  font-size:1rem !important;
}

:root[style*="readium-normalize-on"] small{
  font-size:smaller !important;
}

:root[style*="readium-normalize-on"] sub,
:root[style*="readium-normalize-on"] sup{
  font-size:67.5% !important;
}

:root[style*="readium-normalize-on"][style*="--USER__typeScale"] h1{
  font-size:calc(((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"][style*="--USER__typeScale"] h2{
  font-size:calc((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"][style*="--USER__typeScale"] h3{
  font-size:calc(1rem * var(--USER__typeScale)) !important;
}

:root[style*="readium-iPadOSPatch-on"] body{
  -webkit-text-size-adjust:none;
}

:root[style*="readium-iPadOSPatch-on"] p, 
:root[style*="readium-iPadOSPatch-on"] h1, 
:root[style*="readium-iPadOSPatch-on"] h2, 
:root[style*="readium-iPadOSPatch-on"] h3, 
:root[style*="readium-iPadOSPatch-on"] h4, 
:root[style*="readium-iPadOSPatch-on"] h5, 
:root[style*="readium-iPadOSPatch-on"] h6, 
:root[style*="readium-iPadOSPatch-on"] li, 
:root[style*="readium-iPadOSPatch-on"] th, 
:root[style*="readium-iPadOSPatch-on"] td, 
:root[style*="readium-iPadOSPatch-on"] dt, 
:root[style*="readium-iPadOSPatch-on"] dd, 
:root[style*="readium-iPadOSPatch-on"] pre, 
:root[style*="readium-iPadOSPatch-on"] address, 
:root[style*="readium-iPadOSPatch-on"] details, 
:root[style*="readium-iPadOSPatch-on"] summary,
:root[style*="readium-iPadOSPatch-on"] figcaption,
:root[style*="readium-iPadOSPatch-on"] div:not(:has(p, h1, h2, h3, h4, h5, h6, li, th, td, dt, dd, pre, address, aside, details, figcaption, summary)),
:root[style*="readium-iPadOSPatch-on"] aside:not(:has(p, h1, h2, h3, h4, h5, h6, li, th, td, dt, dd, pre, address, aside, details, figcaption, summary)){
  -webkit-text-zoom:reset;
}

:root[style*="readium-iPadOSPatch-on"] abbr, 
:root[style*="readium-iPadOSPatch-on"] b, 
:root[style*="readium-iPadOSPatch-on"] bdi, 
:root[style*="readium-iPadOSPatch-on"] bdo, 
:root[style*="readium-iPadOSPatch-on"] cite, 
:root[style*="readium-iPadOSPatch-on"] code, 
:root[style*="readium-iPadOSPatch-on"] dfn, 
:root[style*="readium-iPadOSPatch-on"] em, 
:root[style*="readium-iPadOSPatch-on"] i, 
:root[style*="readium-iPadOSPatch-on"] kbd, 
:root[style*="readium-iPadOSPatch-on"] mark, 
:root[style*="readium-iPadOSPatch-on"] q, 
:root[style*="readium-iPadOSPatch-on"] rp, 
:root[style*="readium-iPadOSPatch-on"] rt, 
:root[style*="readium-iPadOSPatch-on"] ruby, 
:root[style*="readium-iPadOSPatch-on"] s, 
:root[style*="readium-iPadOSPatch-on"] samp, 
:root[style*="readium-iPadOSPatch-on"] small, 
:root[style*="readium-iPadOSPatch-on"] span, 
:root[style*="readium-iPadOSPatch-on"] strong, 
:root[style*="readium-iPadOSPatch-on"] sub, 
:root[style*="readium-iPadOSPatch-on"] sup, 
:root[style*="readium-iPadOSPatch-on"] time, 
:root[style*="readium-iPadOSPatch-on"] u, 
:root[style*="readium-iPadOSPatch-on"] var{
  -webkit-text-zoom:normal;
}

:root[style*="readium-iPadOSPatch-on"] p:not(:has(b, cite, em, i, q, s, small, span, strong)):first-line{
  -webkit-text-zoom:normal;
}`});var wd={};_(wd,{default:()=>Mg});var Mg,xd=Me(()=>{Mg=`/*!
 * Readium CSS v.2.0.5
 * Copyright (c) 2017\u20132026. Readium Foundation. All rights reserved.
 * Use of this source code is governed by a BSD-style license which is detailed in the
 * LICENSE file present in the project repository where this source code is maintained.
 * Core maintainer: Jiminy Panoz <jiminy.panoz@edrlab.org> 
 * Contributors: 
 * Daniel Weck
 * Hadrien Gardeur
 * Innovimax
 * L. Le Meur
 * Mickae\u0308l Menu
 * k_taka
 */

@namespace url("http://www.w3.org/1999/xhtml");

@namespace epub url("http://www.idpf.org/2007/ops");

@namespace m url("http://www.w3.org/1998/Math/MathML");

@namespace svg url("http://www.w3.org/2000/svg");

@-ms-viewport{
  width:device-width;
}

@viewport{
  width:device-width;
  zoom:1;
}

:root{

  --RS__sans-serif-ja-v:'Hiragino Sans', 'Hiragino Kaku Gothic ProN', 'Hiragino Kaku Gothic Pro', '\u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 W3', 'YuGothic', 'Yu Gothic Medium', 'BIZ UDGothic', 'Yu Gothic', '\uFF2D\uFF33\u30B4\u30B7\u30C3\u30AF', 'MS Gothic', sans-serif;

  --RS__serif-ja-v:'Hiragino Mincho ProN', 'Hiragino Mincho Pro', 'YuMincho', 'BIZ UDMincho', 'Yu Mincho', '\uFF2D\uFF33\u660E\u671D', 'MS Mincho', serif;

  --RS__sans-serif-ja:'Hiragino Sans', 'Hiragino Kaku Gothic ProN', 'Hiragino Kaku Gothic Pro', '\u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 W3', 'YuGothic', 'Yu Gothic Medium', 'BIZ UDPGothic', 'Yu Gothic', '\uFF2D\uFF33 \uFF30\u30B4\u30B7\u30C3\u30AF', 'MS PGothic', sans-serif;

  --RS__serif-ja:'Hiragino Mincho ProN', 'Hiragino Mincho Pro', 'YuMincho', 'BIZ UDPMincho', 'Yu Mincho', '\uFF2D\uFF33 \uFF30\u660E\u671D', 'MS PMincho', serif;

  --RS__monospaceTf:ui-monospace, 'Andale Mono', 'Cascadia Code', 'Source Code Pro', Menlo, Consolas, 'DejaVu Sans Mono', monospace;

  --RS__humanistTf:Seravek, Calibri, 'Gill Sans Nova', Roboto, Ubuntu, 'DejaVu Sans', source-sans-pro, sans-serif;

  --RS__sansTf:-ui-sans-serif, -apple-system, system-ui, BlinkMacSystemFont, 'Segoe UI Variable', 'Segoe UI', Inter, Roboto, 'Helvetica Neue', 'Arial Nova', 'Liberation Sans', Arial, sans-serif;

  --RS__modernTf:Athelas, Constantia, Charter, 'Bitstream Charter', Cambria, 'Georgia Pro', Georgia, serif;

  --RS__oldStyleTf:'Iowan Old Style', Sitka, 'Sitka Text', Palatino, 'Book Antiqua', 'URW Palladio L', P052, serif;

  --RS__zh-HK-lineHeightCompensation:1.167;

  --RS__zh-HK-baseFontFamily:'\u65B9\u9AD4', 'PingFang HK', '\u65B9\u9AD4', 'PingFang TC', '\u9ED1\u9AD4', 'Heiti TC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK TC', sans-serif;

  --RS__zh-TW-lineHeightCompensation:1.167;

  --RS__zh-TW-baseFontFamily:'\u65B9\u9AD4', 'PingFang TC', '\u9ED1\u9AD4', 'Heiti TC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK TC', sans-serif;

  --RS__zh-Hant-lineHeightCompensation:1.167;

  --RS__zh-Hant-baseFontFamily:'\u65B9\u9AD4', 'PingFang TC', '\u9ED1\u9AD4', 'Heiti TC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK TC', sans-serif;

  --RS__zh-lineHeightCompensation:1.167;

  --RS__zh-baseFontFamily:'\u65B9\u4F53', 'PingFang SC', '\u9ED1\u4F53', 'Heiti SC', 'Microsoft JhengHei UI', 'Microsoft JhengHei', Roboto, Noto, 'Noto Sans CJK SC', sans-serif;

  --RS__th-lineHeightCompensation:1.067;

  --RS__th-baseFontFamily:Thonburi, 'Leelawadee UI', 'Cordia New', Roboto, Noto, 'Noto Sans Thai', sans-serif;

  --RS__te-baseFontFamily:'Kohinoor Telugu', 'Telugu Sangam MN', 'Nirmala UI', Gautami, Roboto, Noto, 'Noto Sans Telugu', sans-serif;

  --RS__ta-lineHeightCompensation:1.067;

  --RS__ta-baseFontFamily:'Tamil Sangam MN', 'Nirmala UI', Latha, Roboto, Noto, 'Noto Sans Tamil', sans-serif;

  --RS__si-lineHeightCompensation:1.167;

  --RS__si-baseFontFamily:'Sinhala Sangam MN', 'Nirmala UI', 'Iskoola Pota', Roboto, Noto, 'Noto Sans Sinhala', sans-serif;

  --RS__pa-lineHeightCompensation:1.1;

  --RS__pa-baseFontFamily:'Gurmukhi MN', 'Nirmala UI', Kartika, Roboto, Noto, 'Noto Sans Gurmukhi', sans-serif;

  --RS__or-lineHeightCompensation:1.167;

  --RS__or-baseFontFamily:'Oriya Sangam MN', 'Nirmala UI', Kalinga, Roboto, Noto, 'Noto Sans Oriya', sans-serif;

  --RS__ml-lineHeightCompensation:1.067;

  --RS__ml-baseFontFamily:'Malayalam Sangam MN', 'Nirmala UI', Kartika, Roboto, Noto, 'Noto Sans Malayalam', sans-serif;

  --RS__lo-baseFontFamily:'Lao Sangam MN', 'Leelawadee UI', 'Lao UI', Roboto, Noto, 'Noto Sans Lao', sans-serif;

  --RS__ko-lineHeightCompensation:1.167;

  --RS__ko-baseFontFamily:'Nanum Gothic', 'Apple SD Gothic Neo', 'Malgun Gothic', Roboto, Noto, 'Noto Sans CJK KR', sans-serif;

  --RS__kn-lineHeightCompensation:1.1;

  --RS__kn-baseFontFamily:'Kannada Sangam MN', 'Nirmala UI', Tunga, Roboto, Noto, 'Noto Sans Kannada', sans-serif;

  --RS__km-lineHeightCompensation:1.067;

  --RS__km-baseFontFamily:'Khmer Sangam MN', 'Leelawadee UI', 'Khmer UI', Roboto, Noto, 'Noto Sans Khmer', sans-serif;

  --RS__ja-lineHeightCompensation:1.167;

  --RS__ja-baseFontFamily:YuGothic, 'Hiragino Maru Gothic ProN', 'Hiragino Sans', 'Yu Gothic UI', 'Meiryo UI', 'MS Gothic', Roboto, Noto, 'Noto Sans CJK JP', sans-serif;

  --RS__iu-baseFontFamily:'Euphemia UCAS', Euphemia, Roboto, Noto, 'Noto Sans Canadian Aboriginal', sans-serif;

  --RS__hy-baseFontFamily:Mshtakan, Sylfaen, Roboto, Noto, 'Noto Serif Armenian', serif;

  --RS__hi-lineHeightCompensation:1.1;

  --RS__hi-baseFontFamily:'Kohinoor Devanagari', 'Devanagari Sangam MN', Kokila, 'Nirmala UI', Roboto, Noto, 'Noto Sans Devanagari', sans-serif;

  --RS__he-lineHeightCompensation:1.1;

  --RS__he-baseFontFamily:'New Peninim MT', 'Arial Hebrew', Gisha, 'Times New Roman', Roboto, Noto, 'Noto Sans Hebrew', sans-serif;

  --RS__gu-lineHeightCompensation:1.167;

  --RS__gu-baseFontFamily:'Gujarati Sangam MN', 'Nirmala UI', Shruti, Roboto, Noto, 'Noto Sans Gujarati', sans-serif;

  --RS__fa-baseFontFamily:'Geeza Pro', 'Arabic Typesetting', Roboto, Noto, 'Noto Naskh Arabic', 'Times New Roman', serif;

  --RS__chr-lineHeightCompensation:1.167;

  --RS__chr-baseFontFamily:'Plantagenet Cherokee', Roboto, Noto, 'Noto Sans Cherokee';

  --RS__bo-baseFontFamily:Kailasa, 'Microsoft Himalaya', Roboto, Noto, 'Noto Sans Tibetan', sans-serif;

  --RS__bn-lineHeightCompensation:1.067;

  --RS__bn-baseFontFamily:'Kohinoor Bangla', 'Bangla Sangam MN', Vrinda, Roboto, Noto, 'Noto Sans Bengali', sans-serif;

  --RS__ar-baseFontFamily:'Geeza Pro', 'Arabic Typesetting', Roboto, Noto, 'Noto Naskh Arabic', 'Times New Roman', serif;

  --RS__am-lineHeightCompensation:1.167;

  --RS__am-baseFontFamily:Kefa, Nyala, Roboto, Noto, 'Noto Sans Ethiopic', serif;

  --RS__latin-lineHeightCompensation:1;

  --RS__latin-baseFontFamily:var(--RS__oldStyleTf);
  --RS__baseFontFamily:var(--RS__latin-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__latin-lineHeightCompensation);
  --RS__baseLineHeight:calc(1.5 * var(--RS__lineHeightCompensation));

  --RS__selectionTextColor:inherit;

  --RS__selectionBackgroundColor:#b4d8fe;

  --RS__visitedColor:#551A8B;

  --RS__linkColor:#0000EE;

  --RS__textColor:#121212;

  --RS__backgroundColor:#FFFFFF;
  color:var(--RS__textColor) !important;

  background-color:var(--RS__backgroundColor) !important;
}

::-moz-selection{
  color:var(--RS__selectionTextColor);
  background-color:var(--RS__selectionBackgroundColor);
}

::selection{
  color:var(--RS__selectionTextColor);
  background-color:var(--RS__selectionBackgroundColor);
}

html{
  font-family:var(--RS__baseFontFamily);
  line-height:1.6;
  line-height:var(--RS__baseLineHeight);
  text-rendering:optimizelegibility;
}

h1, h2, h3{
  line-height:normal;
}

:lang(ja),
:lang(zh),
:lang(ko){
  word-wrap:break-word;
  -webkit-line-break:strict;
  -epub-line-break:strict;
  line-break:strict;
}

math{
  font-family:"Latin Modern Math", "STIX Two Math", "XITS Math", "STIX Math", "Libertinus Math", "TeX Gyre Termes Math", "TeX Gyre Bonum Math", "TeX Gyre Schola", "DejaVu Math TeX Gyre", "TeX Gyre Pagella Math", "Asana Math", "Cambria Math", "Lucida Bright Math", "Minion Math", STIXGeneral, STIXSizeOneSym, Symbol, "Times New Roman", serif;
}

:lang(am){
  --RS__baseFontFamily:var(--RS__am-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__am-lineHeightCompensation);
}

:lang(ar){
  --RS__baseFontFamily:var(--RS__ar-baseFontFamily);
}

:lang(bn){
  --RS__baseFontFamily:var(--RS__bn-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__bn-lineHeightCompensation);
}

:lang(bo){
  --RS__baseFontFamily:var(--RS__bo-baseFontFamily);
}

:lang(chr){
  --RS__baseFontFamily:var(--RS__chr-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__chr-lineHeightCompensation);
}

:lang(fa){
  --RS__baseFontFamily:var(--RS__fa-baseFontFamily);
}

:lang(gu){
  --RS__baseFontFamily:var(--RS__gu-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__gu-lineHeightCompensation);
}

:lang(he){
  --RS__baseFontFamily:var(--RS__he-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__he-lineHeightCompensation);
}

:lang(hi){
  --RS__baseFontFamily:var(--RS__hi-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__hi-lineHeightCompensation);
}

:lang(hy){
  --RS__baseFontFamily:var(--RS__hy-baseFontFamily);
}

:lang(iu){
  --RS__baseFontFamily:var(--RS__iu-baseFontFamily);
}

:lang(ja){
  --RS__baseFontFamily:var(--RS__ja-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ja-lineHeightCompensation);
}

:lang(km){
  --RS__baseFontFamily:var(--RS__km-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__km-lineHeightCompensation);
}

:lang(kn){
  --RS__baseFontFamily:var(--RS__kn-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__kn-lineHeightCompensation);
}

:lang(ko){
  --RS__baseFontFamily:var(--RS__ko-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ko-lineHeightCompensation);
}

:lang(lo){
  --RS__baseFontFamily:var(--RS__lo-baseFontFamily);
}

:lang(ml){
  --RS__baseFontFamily:var(--RS__ml-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ml-lineHeightCompensation);
}

:lang(or){
  --RS__baseFontFamily:var(--RS__or-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__or-lineHeightCompensation);
}

:lang(pa){
  --RS__baseFontFamily:var(--RS__pa-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__pa-lineHeightCompensation);
}

:lang(si){
  --RS__baseFontFamily:var(--RS__si-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__si-lineHeightCompensation);
}

:lang(ta){
  --RS__baseFontFamily:var(--RS__ta-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__ta-lineHeightCompensation);
}

:lang(te){
  --RS__baseFontFamily:var(--RS__te-baseFontFamily);
}

:lang(th){
  --RS__baseFontFamily:var(--RS__th-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__th-lineHeightCompensation);
}

:lang(zh){
  --RS__baseFontFamily:var(--RS__zh-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-lineHeightCompensation);
}

:lang(zh-Hant){
  --RS__baseFontFamily:var(--RS__zh-Hant-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-Hant-lineHeightCompensation);
}

:lang(zh-TW){
  --RS__baseFontFamily:var(--RS__zh-TW-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-TW-lineHeightCompensation);
}

:lang(zh-HK){
  --RS__baseFontFamily:var(--RS__zh-HK-baseFontFamily);
  --RS__lineHeightCompensation:var(--RS__zh-HK-lineHeightCompensation);
}

body{
  widows:2;
  orphans:2;
}

figcaption, th, td{
  widows:1;
  orphans:1;
}

h2, h3, h4, h5, h6, dt,
hr, caption{
  -webkit-column-break-after:avoid;
  page-break-after:avoid;
  break-after:avoid;
}

h1, h2, h3, h4, h5, h6, dt,
figure, tr{
  -webkit-column-break-inside:avoid;
  page-break-inside:avoid;
  break-inside:avoid;
}

body{
  -webkit-hyphenate-character:"\\002D";
  -moz-hyphenate-character:"\\002D";
  -ms-hyphenate-character:"\\002D";
  hyphenate-character:"\\002D";
  -webkit-hyphenate-limit-lines:3;
  -ms-hyphenate-limit-lines:3;
  hyphenate-limit-lines:3;
}

h1, h2, h3, h4, h5, h6, dt,
figcaption, pre, caption, address,
center, code, var{
  -ms-hyphens:none;
  -moz-hyphens:none;
  -webkit-hyphens:none;
  -epub-hyphens:none;
  hyphens:none;
}

body{
  font-variant-numeric:oldstyle-nums proportional-nums;
}

:lang(ja) body,
:lang(zh) body,
:lang(ko) body{
  font-variant-numeric:lining-nums proportional-nums;
}

h1, h2, h3, h4, h5, h6, dt{
  font-variant-numeric:lining-nums proportional-nums;
}

table{
  font-variant-numeric:lining-nums tabular-nums;
}

code, var{
  font-variant-ligatures:none;
  font-variant-numeric:lining-nums tabular-nums slashed-zero;
}

rt{
  font-variant-east-asian:ruby;
}

:lang(ar){
  font-variant-ligatures:common-ligatures;
}

:lang(ko){
  font-kerning:normal;
}

hr{
  color:inherit;
  border-color:currentcolor;
}

table, th, td{
  border-color:currentcolor;
}

figure, blockquote{
  margin:1em 5%;
}

ul, ol{
  padding-left:5%;
}

dd{
  margin-left:5%;
}

pre{
  white-space:pre-wrap;
  -ms-tab-size:2;
  -moz-tab-size:2;
  -webkit-tab-size:2;
  tab-size:2;
}

abbr[title], acronym[title]{
  text-decoration:dotted underline;
}

nobr wbr{
  white-space:normal;
}

ruby > rt, ruby > rp{
  -webkit-user-select:none;
  -moz-user-select:none;
  -ms-user-select:none;
  user-select:none;
}

*:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)),
*:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)),
*:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)),
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) cite, 
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) dfn, 
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) em, 
:lang(ja):not(:lang(ja-Latn)):not(:lang(ja-Cyrl)) i,
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) cite, 
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) dfn, 
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) em, 
:lang(zh):not(:lang(zh-Latn)):not(:lang(zh-Cyrl)) i,
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) cite, 
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) dfn, 
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) em, 
:lang(ko):not(:lang(ko-Latn)):not(:lang(ko-Cyrl)) i{
  font-style:normal;
}

:lang(ja) a,
:lang(zh) a,
:lang(ko) a{
  text-decoration:none;
}

:root{
  --RS__maxMediaWidth:100%;
  --RS__maxMediaHeight:95vh;
  --RS__boxSizingMedia:border-box;
  --RS__boxSizingTable:border-box;
}

a, a span, span a, h1, h2, h3, h4, h5, h6{
  word-wrap:break-word;
}

div{
  max-width:var(--RS__maxMediaWidth);
}

img, svg|svg, video{
  object-fit:contain;

  width:auto;
  height:auto;
  max-width:var(--RS__maxMediaWidth);
  max-height:var(--RS__maxMediaHeight) !important;
  box-sizing:var(--RS__boxSizingMedia);
  -webkit-column-break-inside:avoid;
  page-break-inside:avoid;
  break-inside:avoid;
}

@supports (zoom: 1) and (not ((-webkit-column-axis: horizontal) and (-webkit-column-progression: normal))){

  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] img,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] svg|svg,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] video{
    zoom:calc(100% / var(--USER__fontSize));
  }
}

audio{
    max-width:100%;
    -webkit-column-break-inside:avoid;
    page-break-inside:avoid;
    break-inside:avoid;
  }

table{
  max-width:var(--RS__maxMediaWidth);
  box-sizing:var(--RS__boxSizingTable);
}`});var kd={};_(kd,{default:()=>Ng});var Ng,Cd=Me(()=>{Ng=`/*!
 * Readium CSS v.2.0.5
 * Copyright (c) 2017\u20132026. Readium Foundation. All rights reserved.
 * Use of this source code is governed by a BSD-style license which is detailed in the
 * LICENSE file present in the project repository where this source code is maintained.
 * Core maintainer: Jiminy Panoz <jiminy.panoz@edrlab.org> 
 * Contributors: 
 * Daniel Weck
 * Hadrien Gardeur
 * Innovimax
 * L. Le Meur
 * Mickae\u0308l Menu
 * k_taka
 */

@namespace url("http://www.w3.org/1999/xhtml");

@namespace epub url("http://www.idpf.org/2007/ops");

@namespace m url("http://www.w3.org/1998/Math/MathML");

@namespace svg url("http://www.w3.org/2000/svg");

:root{
  --RS__compFontFamily:var(--RS__baseFontFamily);
  --RS__codeFontFamily:var(--RS__monospaceTf);

  --RS__typeScale:1.125;
  --RS__baseFontSize:100%;

  --RS__flowSpacing:1.5rem;
  --RS__paraSpacing:0;
  --RS__paraIndent:1em;

  --RS__linkColor:#0000EE;
  --RS__visitedColor:#551A8B;

  --RS__primaryColor:;
  --RS__secondaryColor:;
}

body{
  font-size:var(--RS__baseFontSize);
}

h1, h2, h3, h4, h5, h6{
  font-family:var(--RS__compFontFamily);
}

blockquote, figure, p, pre,
aside, footer, form, hr{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
}

p{
  margin-top:var(--RS__paraSpacing);
  margin-bottom:var(--RS__paraSpacing);
  text-indent:var(--RS__paraIndent);
}

h1 + p, h2 + p, h3 + p, h4 + p, h5 + p, h6 + p,
hr + p{
  text-indent:0;
}

pre{
  font-family:var(--RS__codeFontFamily);
}

code, kbd, samp, tt{
  font-family:var(--RS__codeFontFamily);
}

sub, sup{
  position:relative;
  font-size:67.5%;
  line-height:1;
}

sub{
  bottom:-0.2ex;
}

sup{
  bottom:0;
}

:link{
  color:var(--RS__linkColor);
}

:visited{
  color:var(--RS__visitedColor);
}

h1{
  margin-top:calc(var(--RS__flowSpacing) * 2);
  margin-bottom:calc(var(--RS__flowSpacing) * 2);
  font-size:calc(((1em * var(--RS__typeScale)) * var(--RS__typeScale)) * var(--RS__typeScale));
}

h2{
  margin-top:calc(var(--RS__flowSpacing) * 2);
  margin-bottom:var(--RS__flowSpacing);
  font-size:calc((1em * var(--RS__typeScale)) * var(--RS__typeScale));
}

h3{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
  font-size:calc(1em * var(--RS__typeScale));
}

h4{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
  font-size:1em;
}

h5{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
  font-size:1em;
  font-variant:small-caps;
}

h6{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:0;
  font-size:1em;
  text-transform:lowercase;
  font-variant:small-caps;
}

dl, ol, ul{
  margin-top:var(--RS__flowSpacing);
  margin-bottom:var(--RS__flowSpacing);
}

table{
  margin:var(--RS__flowSpacing) 0;
  border:1px solid currentcolor;
  border-collapse:collapse;
  empty-cells:show;
}

thead, tbody, tfoot, table > tr{
  vertical-align:top;
}

th{
  text-align:left;
}

th, td{
  padding:4px;
  border:1px solid currentcolor;
}`});var Rd={};_(Rd,{default:()=>Ig});var Ig,Ed=Me(()=>{Ig=`/*!
 * Readium CSS v.2.0.5
 * Copyright (c) 2017\u20132026. Readium Foundation. All rights reserved.
 * Use of this source code is governed by a BSD-style license which is detailed in the
 * LICENSE file present in the project repository where this source code is maintained.
 * Core maintainer: Jiminy Panoz <jiminy.panoz@edrlab.org> 
 * Contributors: 
 * Daniel Weck
 * Hadrien Gardeur
 * Innovimax
 * L. Le Meur
 * Mickae\u0308l Menu
 * k_taka
 */

@namespace url("http://www.w3.org/1999/xhtml");

@namespace epub url("http://www.idpf.org/2007/ops");

@namespace m url("http://www.w3.org/1998/Math/MathML");

@namespace svg url("http://www.w3.org/2000/svg");

:root{

  --RS__viewportWidth:100%;

  --RS__pageGutter:0;

  --RS__defaultLineLength:100%;

  --RS__colGap:0;

  --RS__colCount:1;

  --RS__colWidth:100vw;
}

@page{
  margin:0 !important;
}

:root{
  position:relative;

  -webkit-column-width:var(--RS__colWidth);
  -moz-column-width:var(--RS__colWidth);
  column-width:var(--RS__colWidth);
  -webkit-column-count:var(--RS__colCount);
  -moz-column-count:var(--RS__colCount);
  column-count:var(--RS__colCount);

  -webkit-column-gap:var(--RS__colGap);
  -moz-column-gap:var(--RS__colGap);
  column-gap:var(--RS__colGap);
  -moz-column-fill:auto;
  column-fill:auto;
  width:var(--RS__viewportWidth);
  height:100vh;
  max-width:var(--RS__viewportWidth);
  max-height:100vh;
  min-width:var(--RS__viewportWidth);
  min-height:100vh;
  padding:0 !important;
  margin:0 !important;
  font-size:1rem !important;
  box-sizing:border-box;
  -webkit-touch-callout:none;
}

body{
  width:100%;
  max-width:var(--RS__defaultLineLength) !important;
  margin:0 auto !important;
  box-sizing:border-box;
}

:root:not([style*="readium-scroll-on"]) body{
  padding:0 var(--RS__pageGutter) !important;
}

:root:not([style*="readium-noOverflow-on"]) body{
  overflow:hidden;
}

@supports (overflow: clip){

   :root:not([style*="readium-noOverflow-on"]){
      overflow:clip;
   }

   :root:not([style*="readium-noOverflow-on"]) body{
      overflow:clip;
      overflow-clip-margin:content-box;
   }
}

:root[style*="readium-scroll-on"]{
  -webkit-columns:auto auto !important;
  -moz-columns:auto auto !important;
  columns:auto auto !important;
  width:auto !important;
  height:auto !important;
  max-width:none !important;
  max-height:none !important;
  min-width:0 !important;
  min-height:0 !important;
}

:root[style*="readium-scroll-on"] body{
  max-width:var(--RS__defaultLineLength) !important;
  box-sizing:border-box !important;
}

:root[style*="readium-scroll-on"]:not([style*="readium-noOverflow-on"]) body{
  overflow:auto;
}

@supports (overflow: clip){

  :root[style*="readium-scroll-on"]:not([style*="readium-noOverflow-on"]){
     overflow:auto;
  }

  :root[style*="readium-scroll-on"]:not([style*="readium-noOverflow-on"]) body{
     overflow:clip;
  }
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingTop"] body{
  padding-top:var(--RS__scrollPaddingTop) !important;
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingBottom"] body{
  padding-bottom:var(--RS__scrollPaddingBottom) !important;
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingLeft"] body{
  padding-left:var(--RS__scrollPaddingLeft) !important;
}

:root[style*="readium-scroll-on"][style*="--RS__scrollPaddingRight"] body{
  padding-right:var(--RS__scrollPaddingRight) !important;
}

:root[style*="--USER__backgroundColor"]{
  background-color:var(--USER__backgroundColor) !important;
}

:root[style*="--USER__backgroundColor"] *{
  background-color:transparent !important;
}

:root[style*="--USER__textColor"]{
  color:var(--USER__textColor) !important;
}

:root[style*="--USER__textColor"] *:not(a){
  color:inherit !important;
  background-color:transparent !important;
  border-color:currentcolor !important;
}

:root[style*="--USER__textColor"] svg text{
  fill:currentcolor !important;
  stroke:none !important;
}

:root[style*="--USER__linkColor"] a:link,
:root[style*="--USER__linkColor"] a:link *{
  color:var(--USER__linkColor) !important;
}

:root[style*="--USER__visitedColor"] a:visited,
:root[style*="--USER__visitedColor"] a:visited *{
  color:var(--USER__visitedColor) !important;
}

:root[style*="--USER__selectionBackgroundColor"][style*="--USER__selectionTextColor"] ::-moz-selection{
  color:var(--USER__selectionTextColor) !important;
  background-color:var(--USER__selectionBackgroundColor) !important;
}

:root[style*="--USER__selectionBackgroundColor"][style*="--USER__selectionTextColor"] ::selection{
  color:var(--USER__selectionTextColor) !important;
  background-color:var(--USER__selectionBackgroundColor) !important;
}

:root[style*="--USER__colCount"]{
  -webkit-column-count:var(--USER__colCount);
  -moz-column-count:var(--USER__colCount);
  column-count:var(--USER__colCount);

  --RS__colWidth:auto;
}

:root[style*="--USER__colCount: 0"],
:root[style*="--USER__colCount:0"]{
  -webkit-column-count:1;
  -moz-column-count:1;
  column-count:1;
}

:root[style*="--USER__colCount: 0"],
:root[style*="--USER__colCount:0"],
:root[style*="--USER__colCount: 1"],
:root[style*="--USER__colCount:1"]{
  --RS__colWidth:100vw;
}

:root[style*="--USER__lineLength"] body{
    max-width:var(--USER__lineLength) !important;
  }

:root[style*="--USER__textAlign"]{
  text-align:var(--USER__textAlign);
}

:root[style*="--USER__textAlign"] body,
:root[style*="--USER__textAlign"] p:not(
  blockquote p,
  figcaption p,
  header p,
  hgroup p,
  :root[style*="readium-experimentalHeaderFiltering-on"] p[class*="title"],
  :root[style*="readium-experimentalHeaderFiltering-on"] div:has(+ *) > h1 + p,
  :root[style*="readium-experimentalHeaderFiltering-on"] div:has(+ *) > p:has(+ h1)
),
:root[style*="--USER__textAlign"] li,
:root[style*="--USER__textAlign"] dd{
  text-align:var(--USER__textAlign) !important;
  -moz-text-align-last:auto !important;
  -epub-text-align-last:auto !important;
  text-align-last:auto !important;
}

:root[style*="--USER__bodyHyphens"]{
  -webkit-hyphens:var(--USER__bodyHyphens) !important;
  -moz-hyphens:var(--USER__bodyHyphens) !important;
  -ms-hyphens:var(--USER__bodyHyphens) !important;
  -epub-hyphens:var(--USER__bodyHyphens) !important;
  hyphens:var(--USER__bodyHyphens) !important;
}

:root[style*="--USER__bodyHyphens"] body,
:root[style*="--USER__bodyHyphens"] p,
:root[style*="--USER__bodyHyphens"] li,
:root[style*="--USER__bodyHyphens"] div,
:root[style*="--USER__bodyHyphens"] dd{
  -webkit-hyphens:var(--USER__bodyHyphens) !important;
  -moz-hyphens:var(--USER__bodyHyphens) !important;
  -ms-hyphens:var(--USER__bodyHyphens) !important;
  -epub-hyphens:var(--USER__bodyHyphens) !important;
  hyphens:var(--USER__bodyHyphens) !important;
}

:root[style*="--USER__fontFamily"]{
  font-family:var(--USER__fontFamily) !important;
}

:root[style*="--USER__fontFamily"] *{
  font-family:revert !important;
}

:root[style*="readium-a11y-on"]{
  font-style:normal !important;
  font-weight:normal !important;
}

:root[style*="readium-a11y-on"] body *:not(code):not(var):not(kbd):not(samp){
  font-family:inherit !important;
  font-style:inherit !important;
  font-weight:inherit !important;
}

:root[style*="readium-a11y-on"] body *:not(a){
  text-decoration:none !important;
}

:root[style*="readium-a11y-on"] body *{
  font-variant-caps:normal !important;
  font-variant-numeric:normal !important;
  font-variant-position:normal !important;
}

:root[style*="readium-a11y-on"] sup,
:root[style*="readium-a11y-on"] sub{
  font-size:1rem !important;
  vertical-align:baseline !important;
}

:root:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] body{
  zoom:var(--USER__fontSize) !important;
}

:root:not([style*="readium-deprecatedFontSize-on"])[style*="readium-iOSPatch-on"][style*="--USER__fontSize"] body{
  -webkit-text-size-adjust:var(--USER__fontSize) !important;
}

@supports selector(figure:has(> img)){

  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> img),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> video),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> svg),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> canvas),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> iframe),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figure:has(> audio),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> img:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> video:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> svg:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> canvas:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> iframe:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] div:has(> audio:only-child),
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] table{
    zoom:calc(100% / var(--USER__fontSize)) !important;
  }

  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] figcaption,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] caption,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] td,
  :root[style*="readium-experimentalZoom-on"]:not([style*="readium-deprecatedFontSize-on"]):not([style*="readium-iOSPatch-on"])[style*="--USER__fontSize"] th{
    zoom:var(--USER__fontSize) !important;
  }
}

@supports not (zoom: 1){

  :root[style*="--USER__fontSize"]{
    font-size:var(--USER__fontSize) !important;
  }
}

:root[style*="readium-deprecatedFontSize-on"][style*="--USER__fontSize"]{
  font-size:var(--USER__fontSize) !important;
}

:root[style*="--USER__lineHeight"]{
  line-height:var(--USER__lineHeight) !important;
}

:root[style*="--USER__lineHeight"] body,
:root[style*="--USER__lineHeight"] p,
:root[style*="--USER__lineHeight"] li,
:root[style*="--USER__lineHeight"] div{
  line-height:var(--USER__lineHeight) !important;
}

:root[style*="--USER__paraSpacing"] p{
  margin-top:var(--USER__paraSpacing) !important;
  margin-bottom:var(--USER__paraSpacing) !important;
}

:root[style*="--USER__paraIndent"] p:not(
  blockquote p,
  figcaption p,
  header p,
  hgroup p,
  :root[style*="readium-experimentalHeaderFiltering-on"] p[class*="title"],
  :root[style*="readium-experimentalHeaderFiltering-on"] div:has(+ *) > h1 + p,
  :root[style*="readium-experimentalHeaderFiltering-on"] div:has(+ *) > p:has(+ h1)
){
  text-indent:var(--USER__paraIndent) !important;
}

:root[style*="--USER__paraIndent"] p *{
  text-indent:0 !important;
}

:root[style*="--USER__wordSpacing"] h1,
:root[style*="--USER__wordSpacing"] h2,
:root[style*="--USER__wordSpacing"] h3,
:root[style*="--USER__wordSpacing"] h4,
:root[style*="--USER__wordSpacing"] h5,
:root[style*="--USER__wordSpacing"] h6,
:root[style*="--USER__wordSpacing"] p,
:root[style*="--USER__wordSpacing"] li,
:root[style*="--USER__wordSpacing"] div,
:root[style*="--USER__wordSpacing"] dt,
:root[style*="--USER__wordSpacing"] dd{
  word-spacing:var(--USER__wordSpacing) !important;
}

:root[style*="--USER__letterSpacing"] h1,
:root[style*="--USER__letterSpacing"] h2,
:root[style*="--USER__letterSpacing"] h3,
:root[style*="--USER__letterSpacing"] h4,
:root[style*="--USER__letterSpacing"] h5,
:root[style*="--USER__letterSpacing"] h6,
:root[style*="--USER__letterSpacing"] p,
:root[style*="--USER__letterSpacing"] li,
:root[style*="--USER__letterSpacing"] div,
:root[style*="--USER__letterSpacing"] dt,
:root[style*="--USER__letterSpacing"] dd{
  letter-spacing:var(--USER__letterSpacing) !important;
  font-variant:none !important;
}

:root[style*="--USER__ligatures"]{
  font-variant-ligatures:var(--USER__ligatures) !important;
}

:root[style*="--USER__ligatures"] *{
  font-variant-ligatures:inherit !important;
}

:root[style*="--USER__fontWeight"] body{
  font-weight:var(--USER__fontWeight) !important;
}

:root[style*="--USER__fontWeight"] b,
:root[style*="--USER__fontWeight"] strong{
  font-weight:bolder;
}

:root[style*="--USER__fontWidth"] body{
  font-stretch:var(--USER__fontWidth) !important;
}

:root[style*="--USER__fontOpticalSizing"] body{
  font-optical-sizing:var(--USER__fontOpticalSizing) !important;
}

:root[style*="readium-blend-on"] svg,
:root[style*="readium-blend-on"] img{
  background-color:transparent !important;
  mix-blend-mode:multiply !important;
}

:root[style*="--USER__darkenImages"] img{
  -webkit-filter:brightness(var(--USER__darkenImages)) !important;
  filter:brightness(var(--USER__darkenImages)) !important;
}

:root[style*="readium-darken-on"] img{
  -webkit-filter:brightness(80%) !important;
  filter:brightness(80%) !important;
}

:root[style*="--USER__invertImages"] img{
  -webkit-filter:invert(var(--USER__invertImages)) !important;
  filter:invert(var(--USER__invertImages)) !important;
}

:root[style*="readium-invert-on"] img{
  -webkit-filter:invert(100%) !important;
  filter:invert(100%) !important;
}

:root[style*="--USER__darkenImages"][style*="--USER__invertImages"] img{
  -webkit-filter:brightness(var(--USER__darkenImages)) invert(var(--USER__invertImages)) !important;
  filter:brightness(var(--USER__darkenImages)) invert(var(--USER__invertImages)) !important;
}

:root[style*="readium-darken-on"][style*="--USER__invertImages"] img{
  -webkit-filter:brightness(80%) invert(var(--USER__invertImages)) !important;
  filter:brightness(80%) invert(var(--USER__invertImages)) !important;
}

:root[style*="--USER__darkenImages"][style*="readium-invert-on"] img{
  -webkit-filter:brightness(var(--USER__darkenImages)) invert(100%) !important;
  filter:brightness(var(--USER__darkenImages)) invert(100%) !important;
}

:root[style*="readium-darken-on"][style*="readium-invert-on"] img{
  -webkit-filter:brightness(80%) invert(100%) !important;
  filter:brightness(80%) invert(100%) !important;
}

:root[style*="--USER__invertGaiji"] img[class*="gaiji"]{
  -webkit-filter:invert(var(--USER__invertGaiji)) !important;
  filter:invert(var(--USER__invertGaiji)) !important;
}

:root[style*="readium-invertGaiji-on"] img[class*="gaiji"]{
  -webkit-filter:invert(100%) !important;
  filter:invert(100%) !important;
}

:root[style*="readium-normalize-on"]{
  --USER__typeScale:1.2;
}

:root[style*="readium-normalize-on"] p,
:root[style*="readium-normalize-on"] li,
:root[style*="readium-normalize-on"] div,
:root[style*="readium-normalize-on"] pre,
:root[style*="readium-normalize-on"] dd{
  font-size:1rem !important;
}

:root[style*="readium-normalize-on"] h1{
  font-size:1.75rem !important;
  font-size:calc(((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"] h2{
  font-size:1.5rem !important;
  font-size:calc((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"] h3{
  font-size:1.25rem !important;
  font-size:calc(1rem * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"] h4,
:root[style*="readium-normalize-on"] h5,
:root[style*="readium-normalize-on"] h6{
  font-size:1rem !important;
}

:root[style*="readium-normalize-on"] small{
  font-size:smaller !important;
}

:root[style*="readium-normalize-on"] sub,
:root[style*="readium-normalize-on"] sup{
  font-size:67.5% !important;
}

:root[style*="readium-normalize-on"][style*="--USER__typeScale"] h1{
  font-size:calc(((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"][style*="--USER__typeScale"] h2{
  font-size:calc((1rem * var(--USER__typeScale)) * var(--USER__typeScale)) !important;
}

:root[style*="readium-normalize-on"][style*="--USER__typeScale"] h3{
  font-size:calc(1rem * var(--USER__typeScale)) !important;
}

:root[style*="readium-iPadOSPatch-on"] body{
  -webkit-text-size-adjust:none;
}

:root[style*="readium-iPadOSPatch-on"] p, 
:root[style*="readium-iPadOSPatch-on"] h1, 
:root[style*="readium-iPadOSPatch-on"] h2, 
:root[style*="readium-iPadOSPatch-on"] h3, 
:root[style*="readium-iPadOSPatch-on"] h4, 
:root[style*="readium-iPadOSPatch-on"] h5, 
:root[style*="readium-iPadOSPatch-on"] h6, 
:root[style*="readium-iPadOSPatch-on"] li, 
:root[style*="readium-iPadOSPatch-on"] th, 
:root[style*="readium-iPadOSPatch-on"] td, 
:root[style*="readium-iPadOSPatch-on"] dt, 
:root[style*="readium-iPadOSPatch-on"] dd, 
:root[style*="readium-iPadOSPatch-on"] pre, 
:root[style*="readium-iPadOSPatch-on"] address, 
:root[style*="readium-iPadOSPatch-on"] details, 
:root[style*="readium-iPadOSPatch-on"] summary,
:root[style*="readium-iPadOSPatch-on"] figcaption,
:root[style*="readium-iPadOSPatch-on"] div:not(:has(p, h1, h2, h3, h4, h5, h6, li, th, td, dt, dd, pre, address, aside, details, figcaption, summary)),
:root[style*="readium-iPadOSPatch-on"] aside:not(:has(p, h1, h2, h3, h4, h5, h6, li, th, td, dt, dd, pre, address, aside, details, figcaption, summary)){
  -webkit-text-zoom:reset;
}

:root[style*="readium-iPadOSPatch-on"] abbr, 
:root[style*="readium-iPadOSPatch-on"] b, 
:root[style*="readium-iPadOSPatch-on"] bdi, 
:root[style*="readium-iPadOSPatch-on"] bdo, 
:root[style*="readium-iPadOSPatch-on"] cite, 
:root[style*="readium-iPadOSPatch-on"] code, 
:root[style*="readium-iPadOSPatch-on"] dfn, 
:root[style*="readium-iPadOSPatch-on"] em, 
:root[style*="readium-iPadOSPatch-on"] i, 
:root[style*="readium-iPadOSPatch-on"] kbd, 
:root[style*="readium-iPadOSPatch-on"] mark, 
:root[style*="readium-iPadOSPatch-on"] q, 
:root[style*="readium-iPadOSPatch-on"] rp, 
:root[style*="readium-iPadOSPatch-on"] rt, 
:root[style*="readium-iPadOSPatch-on"] ruby, 
:root[style*="readium-iPadOSPatch-on"] s, 
:root[style*="readium-iPadOSPatch-on"] samp, 
:root[style*="readium-iPadOSPatch-on"] small, 
:root[style*="readium-iPadOSPatch-on"] span, 
:root[style*="readium-iPadOSPatch-on"] strong, 
:root[style*="readium-iPadOSPatch-on"] sub, 
:root[style*="readium-iPadOSPatch-on"] sup, 
:root[style*="readium-iPadOSPatch-on"] time, 
:root[style*="readium-iPadOSPatch-on"] u, 
:root[style*="readium-iPadOSPatch-on"] var{
  -webkit-text-zoom:normal;
}

:root[style*="readium-iPadOSPatch-on"] p:not(:has(b, cite, em, i, q, s, small, span, strong)):first-line{
  -webkit-text-zoom:normal;
}`});var Pd={};_(Pd,{default:()=>Ug});var Ug,Td=Me(()=>{Ug=`/* Readium CSS
   EBPAJ Fonts Patch module

   A stylesheet improving EBPAJ @font-face declarations to cover all platforms

   Repo: https://github.com/readium/css */

/* EBPAJ template only references fonts from MS Windows\u2026
   so we must reference fonts from other platforms
   and override authors\u2019 stylesheets.
   What we do there is keeping their default value and providing fallbacks.

   /!\\ /!\\ /!\\ /!\\ /!\\
   FYI, you might want to load this polyfill only if you find
   one of the following metadata items in the OPF package:
   - version 1:
     <dc:description id="ebpaj-guide">ebpaj-guide-1.0</dc:description>
   - version 1.1:
     <meta property="ebpaj:guide-version">1.1</meta>
*/

/* 
   Hiragino PostScript Font name lists:
   https://www.screen.co.jp/ga_product/sento/support/QA/ss_psname.html
*/

/* \u6A2A\u7D44\u307F\u7528 (horizontal writing) */

@font-face {
  font-family: "serif-ja";
  src: local("\uFF2D\uFF33 \uFF30\u660E\u671D"), /* for IE */
      local("MS PMincho"), /* \uFF2D\uFF33 \uFF30\u660E\u671D */
      local("HiraMinProN-W3"), local("Hiragino Mincho ProN"), /* \u30D2\u30E9\u30AE\u30CE\u660E\u671D ProN W3 */
      local("HiraMinPro-W3"), local("Hiragino Mincho Pro"), /* \u30D2\u30E9\u30AE\u30CE\u660E\u671D Pro W3 */
      local("YuMin-Medium"), local("YuMincho"), /* \u6E38\u660E\u671D\u4F53(macOS) */
      local("Yu Mincho"), /* \u6E38\u660E\u671D(Windows) */
      local("BIZ UDPMincho"); /* BIZ UDP\u660E\u671D */
}

@font-face {
  font-family: "sans-serif-ja";
  src: local("\uFF2D\uFF33 \uFF30\u30B4\u30B7\u30C3\u30AF"), /* for IE */
       local("MS PGothic"), /* \uFF2D\uFF33 \uFF30\u30B4\u30B7\u30C3\u30AF */
       local("HiraginoSans-W3"), local("Hiragino Sans"), /* \u30D2\u30E9\u30AE\u30CE\u89D2\u30B4\u30B7\u30C3\u30AF */
       local("HiraKakuProN-W3"), local("Hiragino Kaku Gothic ProN"), /* \u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 ProN W3 */
       local("HiraKakuPro-W3"), local("Hiragino Kaku Gothic Pro"), /* \u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 Pro W3 */
       local("\u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 W3"), /* for old  Safari */
       local("HiraginoKaku-W3-90msp-RKSJ-H"), /* \u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 W3(TrueType) */
       local("YuGothic-Medium"), local("YuGothic"), /* \u6E38\u30B4\u30B7\u30C3\u30AF\u4F53(macOS) */
       local("Yu Gothic Medium"), local("Yu Gothic"), /* \u6E38\u30B4\u30B7\u30C3\u30AF(Windows) "Yu Gothic" is a fallback. */
       local("BIZ UDPGothic"); /* BIZ UDP\u30B4\u30B7\u30C3\u30AF */
}

/* \u7E26\u7D44\u307F\u7528 (vertical writing) */

@font-face {
  font-family: "serif-ja-v";
  src: local("\uFF2D\uFF33 \u660E\u671D"), /* for IE */
       local("MS Mincho"), /* \uFF2D\uFF33 \u660E\u671D */
       local("HiraMinProN-W3"), local("Hiragino Mincho ProN"), /* \u30D2\u30E9\u30AE\u30CE\u660E\u671D ProN W3 */
       local("HiraMinPro-W3"), local("Hiragino Mincho Pro"), /* \u30D2\u30E9\u30AE\u30CE\u660E\u671D Pro W3 */
       local("YuMin-Medium"), local("YuMincho"), /* \u6E38\u660E\u671D\u4F53(macOS) */
       local("Yu Mincho"), /* \u6E38\u660E\u671D(Windows) */
       local("BIZ UDMincho"); /*  BIZ UD\u660E\u671D */
}

@font-face {
  font-family: "sans-serif-ja-v";
  src: local("\uFF2D\uFF33 \u30B4\u30B7\u30C3\u30AF"), /* for IE */
       local("MS Gothic"), /* \uFF2D\uFF33 \u30B4\u30B7\u30C3\u30AF */
       local("HiraginoSans-W3"), local("Hiragino Sans"), /* \u30D2\u30E9\u30AE\u30CE\u89D2\u30B4\u30B7\u30C3\u30AF */
       local("HiraKakuProN-W3"), local("Hiragino Kaku Gothic ProN"), /* \u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 ProN W3 */
       local("HiraKakuPro-W3"), local("Hiragino Kaku Gothic Pro"), /* \u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 Pro W3 */
       local("\u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 W3"), /* for old Safari */
       local("HiraKakuDS-W3-83pv-RKSJ-H"), /* \u30D2\u30E9\u30AE\u30CE\u89D2\u30B4 W3(TrueType) */
       local("YuGothic-Medium"), local("YuGothic"), /* \u6E38\u30B4\u30B7\u30C3\u30AF\u4F53(macOS) */
       local("Yu Gothic Medium"), local("Yu Gothic"), /* \u6E38\u30B4\u30B7\u30C3\u30AF(Windows)  "Yu Gothic" is a fallback. */
       local("BIZ UDGothic"); /* BIZ UD\u30B4\u30B7\u30C3\u30AF */
}`});var Kd=ri(wa=>{var Vd="ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/".split("");wa.encode=function(i){if(0<=i&&i<Vd.length)return Vd[i];throw new TypeError("Must be between 0 and 63: "+i)};wa.decode=function(i){var e=65,t=90,o=97,r=122,n=48,a=57,s=43,c=47,l=26,h=52;return e<=i&&i<=t?i-e:o<=i&&i<=r?i-o+l:n<=i&&i<=a?i-n+h:i==s?62:i==c?63:-1}});var Jd=ri(ka=>{var Yd=Kd(),xa=5,Xd=1<<xa,$d=Xd-1,qd=Xd;function tf(i){return i<0?(-i<<1)+1:(i<<1)+0}function of(i){var e=(i&1)===1,t=i>>1;return e?-t:t}ka.encode=function(e){var t="",o,r=tf(e);do o=r&$d,r>>>=xa,r>0&&(o|=qd),t+=Yd.encode(o);while(r>0);return t};ka.decode=function(e,t,o){var r=e.length,n=0,a=0,s,c;do{if(t>=r)throw new Error("Expected more digits in base 64 VLQ value.");if(c=Yd.decode(e.charCodeAt(t++)),c===-1)throw new Error("Invalid base64 digit: "+e.charAt(t-1));s=!!(c&qd),c&=$d,n=n+(c<<a),a+=xa}while(s);o.value=of(n),o.rest=t}});var Dr=ri(le=>{function rf(i,e,t){if(e in i)return i[e];if(arguments.length===3)return t;throw new Error('"'+e+'" is a required argument.')}le.getArg=rf;var Zd=/^(?:([\w+\-.]+):)?\/\/(?:(\w+:\w+)@)?([\w.-]*)(?::(\d+))?(.*)$/,nf=/^data:.+\,.+$/;function co(i){var e=i.match(Zd);return e?{scheme:e[1],auth:e[2],host:e[3],port:e[4],path:e[5]}:null}le.urlParse=co;function Oi(i){var e="";return i.scheme&&(e+=i.scheme+":"),e+="//",i.auth&&(e+=i.auth+"@"),i.host&&(e+=i.host),i.port&&(e+=":"+i.port),i.path&&(e+=i.path),e}le.urlGenerate=Oi;var af=32;function sf(i){var e=[];return function(t){for(var o=0;o<e.length;o++)if(e[o].input===t){var r=e[0];return e[0]=e[o],e[o]=r,e[0].result}var n=i(t);return e.unshift({input:t,result:n}),e.length>af&&e.pop(),n}}var Ca=sf(function(e){var t=e,o=co(e);if(o){if(!o.path)return e;t=o.path}for(var r=le.isAbsolute(t),n=[],a=0,s=0;;)if(a=s,s=t.indexOf("/",a),s===-1){n.push(t.slice(a));break}else for(n.push(t.slice(a,s));s<t.length&&t[s]==="/";)s++;for(var c,l=0,s=n.length-1;s>=0;s--)c=n[s],c==="."?n.splice(s,1):c===".."?l++:l>0&&(c===""?(n.splice(s+1,l),l=0):(n.splice(s,2),l--));return t=n.join("/"),t===""&&(t=r?"/":"."),o?(o.path=t,Oi(o)):t});le.normalize=Ca;function Qd(i,e){i===""&&(i="."),e===""&&(e=".");var t=co(e),o=co(i);if(o&&(i=o.path||"/"),t&&!t.scheme)return o&&(t.scheme=o.scheme),Oi(t);if(t||e.match(nf))return e;if(o&&!o.host&&!o.path)return o.host=e,Oi(o);var r=e.charAt(0)==="/"?e:Ca(i.replace(/\/+$/,"")+"/"+e);return o?(o.path=r,Oi(o)):r}le.join=Qd;le.isAbsolute=function(i){return i.charAt(0)==="/"||Zd.test(i)};function lf(i,e){i===""&&(i="."),i=i.replace(/\/$/,"");for(var t=0;e.indexOf(i+"/")!==0;){var o=i.lastIndexOf("/");if(o<0||(i=i.slice(0,o),i.match(/^([^\/]+:\/)?\/*$/)))return e;++t}return Array(t+1).join("../")+e.substr(i.length+1)}le.relative=lf;var eu=(function(){var i=Object.create(null);return!("__proto__"in i)})();function tu(i){return i}function cf(i){return iu(i)?"$"+i:i}le.toSetString=eu?tu:cf;function hf(i){return iu(i)?i.slice(1):i}le.fromSetString=eu?tu:hf;function iu(i){if(!i)return!1;var e=i.length;if(e<9||i.charCodeAt(e-1)!==95||i.charCodeAt(e-2)!==95||i.charCodeAt(e-3)!==111||i.charCodeAt(e-4)!==116||i.charCodeAt(e-5)!==111||i.charCodeAt(e-6)!==114||i.charCodeAt(e-7)!==112||i.charCodeAt(e-8)!==95||i.charCodeAt(e-9)!==95)return!1;for(var t=e-10;t>=0;t--)if(i.charCodeAt(t)!==36)return!1;return!0}function df(i,e,t){var o=dt(i.source,e.source);return o!==0||(o=i.originalLine-e.originalLine,o!==0)||(o=i.originalColumn-e.originalColumn,o!==0||t)||(o=i.generatedColumn-e.generatedColumn,o!==0)||(o=i.generatedLine-e.generatedLine,o!==0)?o:dt(i.name,e.name)}le.compareByOriginalPositions=df;function uf(i,e,t){var o;return o=i.originalLine-e.originalLine,o!==0||(o=i.originalColumn-e.originalColumn,o!==0||t)||(o=i.generatedColumn-e.generatedColumn,o!==0)||(o=i.generatedLine-e.generatedLine,o!==0)?o:dt(i.name,e.name)}le.compareByOriginalPositionsNoSource=uf;function mf(i,e,t){var o=i.generatedLine-e.generatedLine;return o!==0||(o=i.generatedColumn-e.generatedColumn,o!==0||t)||(o=dt(i.source,e.source),o!==0)||(o=i.originalLine-e.originalLine,o!==0)||(o=i.originalColumn-e.originalColumn,o!==0)?o:dt(i.name,e.name)}le.compareByGeneratedPositionsDeflated=mf;function pf(i,e,t){var o=i.generatedColumn-e.generatedColumn;return o!==0||t||(o=dt(i.source,e.source),o!==0)||(o=i.originalLine-e.originalLine,o!==0)||(o=i.originalColumn-e.originalColumn,o!==0)?o:dt(i.name,e.name)}le.compareByGeneratedPositionsDeflatedNoLine=pf;function dt(i,e){return i===e?0:i===null?1:e===null?-1:i>e?1:-1}function gf(i,e){var t=i.generatedLine-e.generatedLine;return t!==0||(t=i.generatedColumn-e.generatedColumn,t!==0)||(t=dt(i.source,e.source),t!==0)||(t=i.originalLine-e.originalLine,t!==0)||(t=i.originalColumn-e.originalColumn,t!==0)?t:dt(i.name,e.name)}le.compareByGeneratedPositionsInflated=gf;function ff(i){return JSON.parse(i.replace(/^\)]}'[^\n]*\n/,""))}le.parseSourceMapInput=ff;function yf(i,e,t){if(e=e||"",i&&(i[i.length-1]!=="/"&&e[0]!=="/"&&(i+="/"),e=i+e),t){var o=co(t);if(!o)throw new Error("sourceMapURL could not be parsed");if(o.path){var r=o.path.lastIndexOf("/");r>=0&&(o.path=o.path.substring(0,r+1))}e=Qd(Oi(o),e)}return Ca(e)}le.computeSourceURL=yf});var ru=ri(ou=>{var Ra=Dr(),Ea=Object.prototype.hasOwnProperty,Qt=typeof Map<"u";function ut(){this._array=[],this._set=Qt?new Map:Object.create(null)}ut.fromArray=function(e,t){for(var o=new ut,r=0,n=e.length;r<n;r++)o.add(e[r],t);return o};ut.prototype.size=function(){return Qt?this._set.size:Object.getOwnPropertyNames(this._set).length};ut.prototype.add=function(e,t){var o=Qt?e:Ra.toSetString(e),r=Qt?this.has(e):Ea.call(this._set,o),n=this._array.length;(!r||t)&&this._array.push(e),r||(Qt?this._set.set(e,n):this._set[o]=n)};ut.prototype.has=function(e){if(Qt)return this._set.has(e);var t=Ra.toSetString(e);return Ea.call(this._set,t)};ut.prototype.indexOf=function(e){if(Qt){var t=this._set.get(e);if(t>=0)return t}else{var o=Ra.toSetString(e);if(Ea.call(this._set,o))return this._set[o]}throw new Error('"'+e+'" is not in the set.')};ut.prototype.at=function(e){if(e>=0&&e<this._array.length)return this._array[e];throw new Error("No element indexed by "+e)};ut.prototype.toArray=function(){return this._array.slice()};ou.ArraySet=ut});var su=ri(au=>{var nu=Dr();function Sf(i,e){var t=i.generatedLine,o=e.generatedLine,r=i.generatedColumn,n=e.generatedColumn;return o>t||o==t&&n>=r||nu.compareByGeneratedPositionsInflated(i,e)<=0}function Hr(){this._array=[],this._sorted=!0,this._last={generatedLine:-1,generatedColumn:0}}Hr.prototype.unsortedForEach=function(e,t){this._array.forEach(e,t)};Hr.prototype.add=function(e){Sf(this._last,e)?(this._last=e,this._array.push(e)):(this._sorted=!1,this._array.push(e))};Hr.prototype.toArray=function(){return this._sorted||(this._array.sort(nu.compareByGeneratedPositionsInflated),this._sorted=!0),this._array};au.MappingList=Hr});var cu=ri(lu=>{var ho=Jd(),Q=Dr(),Wr=ru().ArraySet,bf=su().MappingList;function Ge(i){i||(i={}),this._file=Q.getArg(i,"file",null),this._sourceRoot=Q.getArg(i,"sourceRoot",null),this._skipValidation=Q.getArg(i,"skipValidation",!1),this._ignoreInvalidMapping=Q.getArg(i,"ignoreInvalidMapping",!1),this._sources=new Wr,this._names=new Wr,this._mappings=new bf,this._sourcesContents=null}Ge.prototype._version=3;Ge.fromSourceMap=function(e,t){var o=e.sourceRoot,r=new Ge(Object.assign(t||{},{file:e.file,sourceRoot:o}));return e.eachMapping(function(n){var a={generated:{line:n.generatedLine,column:n.generatedColumn}};n.source!=null&&(a.source=n.source,o!=null&&(a.source=Q.relative(o,a.source)),a.original={line:n.originalLine,column:n.originalColumn},n.name!=null&&(a.name=n.name)),r.addMapping(a)}),e.sources.forEach(function(n){var a=n;o!==null&&(a=Q.relative(o,n)),r._sources.has(a)||r._sources.add(a);var s=e.sourceContentFor(n);s!=null&&r.setSourceContent(n,s)}),r};Ge.prototype.addMapping=function(e){var t=Q.getArg(e,"generated"),o=Q.getArg(e,"original",null),r=Q.getArg(e,"source",null),n=Q.getArg(e,"name",null);!this._skipValidation&&this._validateMapping(t,o,r,n)===!1||(r!=null&&(r=String(r),this._sources.has(r)||this._sources.add(r)),n!=null&&(n=String(n),this._names.has(n)||this._names.add(n)),this._mappings.add({generatedLine:t.line,generatedColumn:t.column,originalLine:o!=null&&o.line,originalColumn:o!=null&&o.column,source:r,name:n}))};Ge.prototype.setSourceContent=function(e,t){var o=e;this._sourceRoot!=null&&(o=Q.relative(this._sourceRoot,o)),t!=null?(this._sourcesContents||(this._sourcesContents=Object.create(null)),this._sourcesContents[Q.toSetString(o)]=t):this._sourcesContents&&(delete this._sourcesContents[Q.toSetString(o)],Object.keys(this._sourcesContents).length===0&&(this._sourcesContents=null))};Ge.prototype.applySourceMap=function(e,t,o){var r=t;if(t==null){if(e.file==null)throw new Error(`SourceMapGenerator.prototype.applySourceMap requires either an explicit source file, or the source map's "file" property. Both were omitted.`);r=e.file}var n=this._sourceRoot;n!=null&&(r=Q.relative(n,r));var a=new Wr,s=new Wr;this._mappings.unsortedForEach(function(c){if(c.source===r&&c.originalLine!=null){var l=e.originalPositionFor({line:c.originalLine,column:c.originalColumn});l.source!=null&&(c.source=l.source,o!=null&&(c.source=Q.join(o,c.source)),n!=null&&(c.source=Q.relative(n,c.source)),c.originalLine=l.line,c.originalColumn=l.column,l.name!=null&&(c.name=l.name))}var h=c.source;h!=null&&!a.has(h)&&a.add(h);var d=c.name;d!=null&&!s.has(d)&&s.add(d)},this),this._sources=a,this._names=s,e.sources.forEach(function(c){var l=e.sourceContentFor(c);l!=null&&(o!=null&&(c=Q.join(o,c)),n!=null&&(c=Q.relative(n,c)),this.setSourceContent(c,l))},this)};Ge.prototype._validateMapping=function(e,t,o,r){if(t&&typeof t.line!="number"&&typeof t.column!="number"){var n="original.line and original.column are not numbers -- you probably meant to omit the original mapping entirely and only map the generated position. If so, pass null for the original mapping instead of an object with empty or null values.";if(this._ignoreInvalidMapping)return typeof console<"u"&&console.warn&&console.warn(n),!1;throw new Error(n)}if(!(e&&"line"in e&&"column"in e&&e.line>0&&e.column>=0&&!t&&!o&&!r)){if(e&&"line"in e&&"column"in e&&t&&"line"in t&&"column"in t&&e.line>0&&e.column>=0&&t.line>0&&t.column>=0&&o)return;var n="Invalid mapping: "+JSON.stringify({generated:e,source:o,original:t,name:r});if(this._ignoreInvalidMapping)return typeof console<"u"&&console.warn&&console.warn(n),!1;throw new Error(n)}};Ge.prototype._serializeMappings=function(){for(var e=0,t=1,o=0,r=0,n=0,a=0,s="",c,l,h,d,u=this._mappings.toArray(),m=0,p=u.length;m<p;m++){if(l=u[m],c="",l.generatedLine!==t)for(e=0;l.generatedLine!==t;)c+=";",t++;else if(m>0){if(!Q.compareByGeneratedPositionsInflated(l,u[m-1]))continue;c+=","}c+=ho.encode(l.generatedColumn-e),e=l.generatedColumn,l.source!=null&&(d=this._sources.indexOf(l.source),c+=ho.encode(d-a),a=d,c+=ho.encode(l.originalLine-1-r),r=l.originalLine-1,c+=ho.encode(l.originalColumn-o),o=l.originalColumn,l.name!=null&&(h=this._names.indexOf(l.name),c+=ho.encode(h-n),n=h)),s+=c}return s};Ge.prototype._generateSourcesContent=function(e,t){return e.map(function(o){if(!this._sourcesContents)return null;t!=null&&(o=Q.relative(t,o));var r=Q.toSetString(o);return Object.prototype.hasOwnProperty.call(this._sourcesContents,r)?this._sourcesContents[r]:null},this)};Ge.prototype.toJSON=function(){var e={version:this._version,sources:this._sources.toArray(),names:this._names.toArray(),mappings:this._serializeMappings()};return this._file!=null&&(e.file=this._file),this._sourceRoot!=null&&(e.sourceRoot=this._sourceRoot),this._sourcesContents&&(e.sourcesContent=this._generateSourcesContent(e.sources,e.sourceRoot)),e};Ge.prototype.toString=function(){return JSON.stringify(this.toJSON())};lu.SourceMapGenerator=Ge});var zo=class{close(){}links(){return[]}get(e){throw Error("This is an empty fetcher")}};var Lo=class{readAsString(){return this.read().then(e=>e===void 0?e:new TextDecoder().decode(e))}readAsJSON(){return this.readAsString().then(e=>e===void 0?e:JSON.parse(e))}readAsXML(){return this.readAsString().then(e=>e===void 0?e:new DOMParser().parseFromString(e,"text/xml"))}};var Oo=class i{constructor(e={}){this.conformsTo=e.conformsTo??[],this.certification=e.certification??null,this.summary=e.summary??null,this.accessMode=e.accessMode??[],this.accessModeSufficient=e.accessModeSufficient??[],this.feature=e.feature??[],this.hazard=e.hazard??[],this.exemption=e.exemption??[]}static deserialize(e){if(!e||typeof e!="object")return;let t=e;return new i({conformsTo:t.conformsTo?t.conformsTo.map(o=>bp.deserialize(o)).filter(o=>o!==void 0):void 0,certification:t.certification?yn.deserialize(t.certification):void 0,summary:t.summary,accessMode:t.accessMode?t.accessMode.map(o=>_p.deserialize(o)).filter(o=>o!==void 0):void 0,accessModeSufficient:t.accessModeSufficient?t.accessModeSufficient.map(o=>vp.deserialize(o)).filter(o=>o!==void 0):void 0,feature:t.feature?t.feature.map(o=>wp.deserialize(o)).filter(o=>o!==void 0):void 0,hazard:t.hazard?t.hazard.map(o=>xp.deserialize(o)).filter(o=>o!==void 0):void 0,exemption:t.exemption?t.exemption.map(o=>kp.deserialize(o)).filter(o=>o!==void 0):void 0})}serialize(){let e={};return this.conformsTo?.length>0&&(e.conformsTo=this.conformsTo.map(t=>t.serialize())),this.certification!==void 0&&this.certification!==null&&(e.certification=this.certification.serialize()),this.summary!==void 0&&this.summary!==null&&(e.summary=this.summary),this.accessMode?.length>0&&(e.accessMode=this.accessMode.map(t=>t.serialize())),this.accessModeSufficient?.length>0&&(e.accessModeSufficient=this.accessModeSufficient.map(t=>t.serialize())),this.feature?.length>0&&(e.feature=this.feature.map(t=>t.serialize())),this.hazard?.length>0&&(e.hazard=this.hazard.map(t=>t.serialize())),this.exemption?.length>0&&(e.exemption=this.exemption.map(t=>t.serialize())),e}},j=class ke{constructor(e){this.uri=e}static deserialize(e){if(!(!e||typeof e!="string"))return new ke(e)}serialize(){return this.uri}get isWCAGLevelA(){return this===ke.EPUB_A11Y_10_WCAG_20_A||this===ke.EPUB_A11Y_11_WCAG_20_A||this===ke.EPUB_A11Y_11_WCAG_21_A||this===ke.EPUB_A11Y_11_WCAG_22_A}get isWCAGLevelAA(){return this===ke.EPUB_A11Y_10_WCAG_20_AA||this===ke.EPUB_A11Y_11_WCAG_20_AA||this===ke.EPUB_A11Y_11_WCAG_21_AA||this===ke.EPUB_A11Y_11_WCAG_22_AA}get isWCAGLevelAAA(){return this===ke.EPUB_A11Y_10_WCAG_20_AAA||this===ke.EPUB_A11Y_11_WCAG_20_AAA||this===ke.EPUB_A11Y_11_WCAG_21_AAA||this===ke.EPUB_A11Y_11_WCAG_22_AAA}};j.EPUB_A11Y_10_WCAG_20_A=new j("http://www.idpf.org/epub/a11y/accessibility-20170105.html#wcag-a"),j.EPUB_A11Y_10_WCAG_20_AA=new j("http://www.idpf.org/epub/a11y/accessibility-20170105.html#wcag-aa"),j.EPUB_A11Y_10_WCAG_20_AAA=new j("http://www.idpf.org/epub/a11y/accessibility-20170105.html#wcag-aaa"),j.EPUB_A11Y_11_WCAG_20_A=new j("https://www.w3.org/TR/epub-a11y-11#wcag-2.0-a"),j.EPUB_A11Y_11_WCAG_20_AA=new j("https://www.w3.org/TR/epub-a11y-11#wcag-2.0-aa"),j.EPUB_A11Y_11_WCAG_20_AAA=new j("https://www.w3.org/TR/epub-a11y-11#wcag-2.0-aaa"),j.EPUB_A11Y_11_WCAG_21_A=new j("https://www.w3.org/TR/epub-a11y-11#wcag-2.1-a"),j.EPUB_A11Y_11_WCAG_21_AA=new j("https://www.w3.org/TR/epub-a11y-11#wcag-2.1-aa"),j.EPUB_A11Y_11_WCAG_21_AAA=new j("https://www.w3.org/TR/epub-a11y-11#wcag-2.1-aaa"),j.EPUB_A11Y_11_WCAG_22_A=new j("https://www.w3.org/TR/epub-a11y-11#wcag-2.2-a"),j.EPUB_A11Y_11_WCAG_22_AA=new j("https://www.w3.org/TR/epub-a11y-11#wcag-2.2-aa"),j.EPUB_A11Y_11_WCAG_22_AAA=new j("https://www.w3.org/TR/epub-a11y-11#wcag-2.2-aaa");var bp=j,yn=class i{constructor(e=null,t=null,o=null){this.certifiedBy=e,this.credential=t,this.report=o}static deserialize(e){if(!(!e||typeof e!="object"))return new i(e.certifiedBy,e.credential,e.report)}serialize(){let e={};return this.certifiedBy&&(e.certifiedBy=this.certifiedBy),this.credential&&(e.credential=this.credential),this.report&&(e.report=this.report),e}},K=class gc{constructor(e){this.value=e}static deserialize(e){if(!(!e||typeof e!="string"))return new gc(e)}serialize(){return this.value}};K.AUDITORY=new K("auditory"),K.CHART_ON_VISUAL=new K("chartOnVisual"),K.CHEM_ON_VISUAL=new K("chemOnVisual"),K.COLOR_DEPENDENT=new K("colorDependent"),K.DIAGRAM_ON_VISUAL=new K("diagramOnVisual"),K.MATH_ON_VISUAL=new K("mathOnVisual"),K.MUSIC_ON_VISUAL=new K("musicOnVisual"),K.TACTILE=new K("tactile"),K.TEXT_ON_VISUAL=new K("textOnVisual"),K.TEXTUAL=new K("textual"),K.VISUAL=new K("visual");var _p=K,Ze=class ni{constructor(e){if(typeof e=="string"){if(!ni.VALID_MODES.has(e.toLowerCase()))return;this.value=e.toLowerCase()}else{let t=e.filter(o=>ni.VALID_MODES.has(o.toLowerCase()));if(t.length===0)return;this.value=Array.from(new Set(t))}}static deserialize(e){if(!e)return;if(typeof e=="string")return new ni(e);if(!Array.isArray(e))return;let t=e.filter(o=>o?ni.VALID_MODES.has(o.toLowerCase()):!1);if(t.length!==0)return new ni(t)}serialize(){return this.value}};Ze.VALID_MODES=new Set(["auditory","tactile","textual","visual"]),Ze.AUDITORY=new Ze("auditory"),Ze.TACTILE=new Ze("tactile"),Ze.TEXTUAL=new Ze("textual"),Ze.VISUAL=new Ze("visual");var vp=Ze,b=class fc{constructor(e){this.value=e}static deserialize(e){if(!(!e||typeof e!="string"))return new fc(e)}serialize(){return this.value}};b.NONE=new b("none"),b.ANNOTATIONS=new b("annotations"),b.ARIA=new b("ARIA"),b.INDEX=new b("index"),b.PAGE_BREAK_MARKERS=new b("pageBreakMarkers"),b.PAGE_NAVIGATION=new b("pageNavigation"),b.PRINT_PAGE_NUMBERS=new b("printPageNumbers"),b.READING_ORDER=new b("readingOrder"),b.STRUCTURAL_NAVIGATION=new b("structuralNavigation"),b.TABLE_OF_CONTENTS=new b("tableOfContents"),b.TAGGED_PDF=new b("taggedPDF"),b.ALTERNATIVE_TEXT=new b("alternativeText"),b.AUDIO_DESCRIPTION=new b("audioDescription"),b.CAPTIONS=new b("captions"),b.CLOSED_CAPTIONS=new b("closedCaptions"),b.DESCRIBED_MATH=new b("describedMath"),b.LONG_DESCRIPTION=new b("longDescription"),b.OPEN_CAPTIONS=new b("openCaptions"),b.SIGN_LANGUAGE=new b("signLanguage"),b.TRANSCRIPT=new b("transcript"),b.DISPLAY_TRANSFORMABILITY=new b("displayTransformability"),b.SYNCHRONIZED_AUDIO_TEXT=new b("synchronizedAudioText"),b.TIMING_CONTROL=new b("timingControl"),b.UNLOCKED=new b("unlocked"),b.CHEM_ML=new b("ChemML"),b.LATEX=new b("latex"),b.LATEX_CHEMISTRY=new b("latex-chemistry"),b.MATH_ML=new b("MathML"),b.MATH_ML_CHEMISTRY=new b("MathML-chemistry"),b.TTS_MARKUP=new b("ttsMarkup"),b.HIGH_CONTRAST_AUDIO=new b("highContrastAudio"),b.HIGH_CONTRAST_DISPLAY=new b("highContrastDisplay"),b.LARGE_PRINT=new b("largePrint"),b.BRAILLE=new b("braille"),b.TACTILE_GRAPHIC=new b("tactileGraphic"),b.TACTILE_OBJECT=new b("tactileObject"),b.FULL_RUBY_ANNOTATIONS=new b("fullRubyAnnotations"),b.HORIZONTAL_WRITING=new b("horizontalWriting"),b.RUBY_ANNOTATIONS=new b("rubyAnnotations"),b.VERTICAL_WRITING=new b("verticalWriting"),b.WITH_ADDITIONAL_WORD_SEGMENTATION=new b("withAdditionalWordSegmentation"),b.WITHOUT_ADDITIONAL_WORD_SEGMENTATION=new b("withoutAdditionalWordSegmentation");var wp=b,Y=class yc{constructor(e){this.value=e}static deserialize(e){if(!(!e||typeof e!="string"))return new yc(e)}serialize(){return this.value}};Y.FLASHING=new Y("flashing"),Y.NO_FLASHING_HAZARD=new Y("noFlashingHazard"),Y.UNKNOWN_FLASHING_HAZARD=new Y("unknownFlashingHazard"),Y.MOTION_SIMULATION=new Y("motionSimulation"),Y.NO_MOTION_SIMULATION_HAZARD=new Y("noMotionSimulationHazard"),Y.UNKNOWN_MOTION_SIMULATION_HAZARD=new Y("unknownMotionSimulationHazard"),Y.SOUND=new Y("sound"),Y.NO_SOUND_HAZARD=new Y("noSoundHazard"),Y.UNKNOWN_SOUND_HAZARD=new Y("unknownSoundHazard"),Y.UNKNOWN=new Y("unknown"),Y.NONE=new Y("none");var xp=Y,q=class Sc{constructor(e){this.value=e}static deserialize(e){if(!(!e||typeof e!="string"))return new Sc(e)}serialize(){return this.value}};q.NONE=new q("none"),q.DOCUMENTED=new q("documented"),q.LEGAL=new q("legal"),q.TEMPORARY=new q("temporary"),q.TECHNICAL=new q("technical"),q.EAA_DISPROPORTIONATE_BURDEN=new q("eaa-disproportionate-burden"),q.EAA_FUNDAMENTAL_ALTERATION=new q("eaa-fundamental-alteration"),q.EAA_MICROENTERPRISE=new q("eaa-microenterprise"),q.EAA_TECHNICAL_IMPOSSIBILITY=new q("eaa-technical-impossibility"),q.EAA_TEMPORARY=new q("eaa-temporary");var kp=q;var v=(i=>(i.reflowable="reflowable",i.fixed="fixed",i.scrolled="scrolled",i))(v||{});function Cp(i){return i&&Array.isArray(i)?i:void 0}function Ne(i){return i&&typeof i=="string"?[i]:Cp(i)}function Sn(i){return typeof i=="string"?new Date(i):void 0}function Ot(i){return isNaN(i)?void 0:i}function rt(i){return Ot(i)!==void 0&&Math.sign(i)>=0?i:void 0}function At(i){let e=new Array;return i.forEach(t=>e.push(t)),e}function Mt(i){let e=i.split(",")[0].trim(),t=(e.toLowerCase().startsWith("npt:")?e.slice(4):e).split(":");if(t.length===1){let o=parseFloat(t[0]);return isNaN(o)?void 0:o}if(t.length===2){let o=parseInt(t[0],10),r=parseFloat(t[1]);return isNaN(o)||isNaN(r)?void 0:o*60+r}if(t.length===3){let o=parseInt(t[0],10),r=parseInt(t[1],10),n=parseFloat(t[2]);return isNaN(o)||isNaN(r)||isNaN(n)?void 0:o*3600+r*60+n}}function bc(i){let e=Mt(i);return e!==void 0&&e===0}function _c(i){let e=Math.floor(i),t=Math.floor(e/3600),o=Math.floor(e%3600/60),r=e%60,n=String(o).padStart(2,"0"),a=String(r).padStart(2,"0");return t>0?`${t}:${n}:${a}`:`${o}:${a}`}function ft(i){return i.otherLocations?.get("cssSelector")}function bn(i){return new Map(i.fragments.map(e=>e.startsWith("#")?e.slice(1):e).join("&").split("&").filter(e=>!e.startsWith("#")).map(e=>e.split("=")).filter(e=>e.length===2).map(e=>[e[0].trim().toLowerCase(),e[1].trim()]))}function ji(i){if(!i.fragments.length)return;let e=i.fragments.find(t=>t.length&&!t.includes("="));if(!e){let t=bn(i);t.has("id")?e=t.get("id"):t.has("name")&&(e=t.get("name"))}return e?.startsWith("#")?e.slice(1):e}function _n(i){let e=bn(i).get("t");if(e)return Mt(e)}var Ce=class i{constructor(e){let t,o,r=e.mediaType.replace(/\s/g,"").split(";"),n=r[0].split("/");if(n.length===2){if(t=n[0].toLowerCase().trim(),o=n[1].toLowerCase().trim(),t.length===0||o.length===0)throw new Error("Invalid media type")}else throw new Error("Invalid media type");let a={};for(let u=1;u<r.length;u++){let m=r[u].split("=");if(m.length===2){let p=m[0].toLocaleLowerCase(),g=p==="charset"?m[1].toUpperCase():m[1];a[p]=g}}let s={},c=Object.keys(a);c.sort((u,m)=>u.localeCompare(m)),c.forEach(u=>s[u]=a[u]);let l="";for(let u in s){let m=s[u];l+=`;${u}=${m}`}let h=`${t}/${o}${l}`,d=s.encoding;this.string=h,this.type=t,this.subtype=o,this.parameters=s,this.encoding=d,this.name=e.name,this.fileExtension=e.fileExtension}static parse(e){return new i(e)}get structuredSyntaxSuffix(){let e=this.subtype.split("+");return e.length>1?`+${e[e.length-1]}`:void 0}get charset(){return this.parameters.charset}contains(e){let t=typeof e=="string"?i.parse({mediaType:e}):e;if(!((this.type==="*"||this.type===t.type)&&(this.subtype==="*"||this.subtype===t.subtype)))return!1;let o=new Set(Object.entries(this.parameters).map(([n,a])=>`${n}=${a}`)),r=new Set(Object.entries(t.parameters).map(([n,a])=>`${n}=${a}`));for(let n of Array.from(o.values()))if(!r.has(n))return!1;return!0}matches(e){let t=typeof e=="string"?i.parse({mediaType:e}):e;return this.contains(t)||t.contains(this)}matchesAny(...e){for(let t of e)if(this.matches(t))return!0;return!1}equals(e){return this.string===e.string}get isZIP(){return this.matchesAny(i.ZIP,i.LCP_PROTECTED_AUDIOBOOK,i.LCP_PROTECTED_PDF)||this.structuredSyntaxSuffix==="+zip"}get isJSON(){return this.matchesAny(i.JSON)||this.structuredSyntaxSuffix==="+json"}get isOPDS(){return this.matchesAny(i.OPDS1,i.OPDS1_ENTRY,i.OPDS2,i.OPDS2_PUBLICATION,i.OPDS_AUTHENTICATION)||this.structuredSyntaxSuffix==="+json"}get isHTML(){return this.matchesAny(i.HTML,i.XHTML)}get isBitmap(){return this.matchesAny(i.AVIF,i.BMP,i.GIF,i.JPEG,i.PNG,i.TIFF,i.WEBP)}get isAudio(){return this.type==="audio"}get isVideo(){return this.type==="video"}get isRWPM(){return this.matchesAny(i.READIUM_AUDIOBOOK_MANIFEST,i.DIVINA_MANIFEST,i.READIUM_WEBPUB_MANIFEST)}get isPublication(){return this.matchesAny(i.READIUM_AUDIOBOOK,i.READIUM_AUDIOBOOK_MANIFEST,i.CBZ,i.DIVINA,i.DIVINA_MANIFEST,i.EPUB,i.LCP_PROTECTED_AUDIOBOOK,i.LCP_PROTECTED_PDF,i.LPF,i.PDF,i.W3C_WPUB_MANIFEST,i.READIUM_WEBPUB,i.READIUM_WEBPUB_MANIFEST,i.ZAB)}static get AAC(){return i.parse({mediaType:"audio/aac",fileExtension:"aac"})}static get ACSM(){return i.parse({mediaType:"application/vnd.adobe.adept+xml",name:"Adobe Content Server Message",fileExtension:"acsm"})}static get AIFF(){return i.parse({mediaType:"audio/aiff",fileExtension:"aiff"})}static get AVI(){return i.parse({mediaType:"video/x-msvideo",fileExtension:"avi"})}static get AVIF(){return i.parse({mediaType:"image/avif",fileExtension:"avif"})}static get BINARY(){return i.parse({mediaType:"application/octet-stream"})}static get BMP(){return i.parse({mediaType:"image/bmp",fileExtension:"bmp"})}static get CBZ(){return i.parse({mediaType:"application/vnd.comicbook+zip",name:"Comic Book Archive",fileExtension:"cbz"})}static get CSS(){return i.parse({mediaType:"text/css",fileExtension:"css"})}static get DIVINA(){return i.parse({mediaType:"application/divina+zip",name:"Digital Visual Narratives",fileExtension:"divina"})}static get DIVINA_MANIFEST(){return i.parse({mediaType:"application/divina+json",name:"Digital Visual Narratives",fileExtension:"json"})}static get EPUB(){return i.parse({mediaType:"application/epub+zip",name:"EPUB",fileExtension:"epub"})}static get GIF(){return i.parse({mediaType:"image/gif",fileExtension:"gif"})}static get GZ(){return i.parse({mediaType:"application/gzip",fileExtension:"gz"})}static get HTML(){return i.parse({mediaType:"text/html",fileExtension:"html"})}static get JAVASCRIPT(){return i.parse({mediaType:"text/javascript",fileExtension:"js"})}static get JPEG(){return i.parse({mediaType:"image/jpeg",fileExtension:"jpeg"})}static get JSON(){return i.parse({mediaType:"application/json"})}static get LCP_LICENSE_DOCUMENT(){return i.parse({mediaType:"application/vnd.readium.lcp.license.v1.0+json",name:"LCP License",fileExtension:"lcpl"})}static get LCP_PROTECTED_AUDIOBOOK(){return i.parse({mediaType:"application/audiobook+lcp",name:"LCP Protected Audiobook",fileExtension:"lcpa"})}static get LCP_PROTECTED_PDF(){return i.parse({mediaType:"application/pdf+lcp",name:"LCP Protected PDF",fileExtension:"lcpdf"})}static get LCP_STATUS_DOCUMENT(){return i.parse({mediaType:"application/vnd.readium.license.status.v1.0+json"})}static get LPF(){return i.parse({mediaType:"application/lpf+zip",fileExtension:"lpf"})}static get MP3(){return i.parse({mediaType:"audio/mpeg",fileExtension:"mp3"})}static get MPEG(){return i.parse({mediaType:"video/mpeg",fileExtension:"mpeg"})}static get NCX(){return i.parse({mediaType:"application/x-dtbncx+xml",fileExtension:"ncx"})}static get OGG(){return i.parse({mediaType:"audio/ogg",fileExtension:"oga"})}static get OGV(){return i.parse({mediaType:"video/ogg",fileExtension:"ogv"})}static get OPDS1(){return i.parse({mediaType:"application/atom+xml;profile=opds-catalog"})}static get OPDS1_ENTRY(){return i.parse({mediaType:"application/atom+xml;type=entry;profile=opds-catalog"})}static get OPDS2(){return i.parse({mediaType:"application/opds+json"})}static get OPDS2_PUBLICATION(){return i.parse({mediaType:"application/opds-publication+json"})}static get OPDS_AUTHENTICATION(){return i.parse({mediaType:"application/opds-authentication+json"})}static get OPUS(){return i.parse({mediaType:"audio/opus",fileExtension:"opus"})}static get OTF(){return i.parse({mediaType:"font/otf",fileExtension:"otf"})}static get PDF(){return i.parse({mediaType:"application/pdf",name:"PDF",fileExtension:"pdf"})}static get PNG(){return i.parse({mediaType:"image/png",fileExtension:"png"})}static get READIUM_AUDIOBOOK(){return i.parse({mediaType:"application/audiobook+zip",name:"Readium Audiobook",fileExtension:"audiobook"})}static get READIUM_AUDIOBOOK_MANIFEST(){return i.parse({mediaType:"application/audiobook+json",name:"Readium Audiobook",fileExtension:"json"})}static get READIUM_CONTENT_DOCUMENT(){return i.parse({mediaType:"application/vnd.readium.content+json",name:"Readium Content Document",fileExtension:"json"})}static get READIUM_GUIDED_NAVIGATION_DOCUMENT(){return i.parse({mediaType:"application/guided-navigation+json",name:"Readium Guided Navigation Document",fileExtension:"json"})}static get READIUM_POSITION_LIST(){return i.parse({mediaType:"application/vnd.readium.position-list+json",name:"Readium Position List",fileExtension:"json"})}static get READIUM_WEBPUB(){return i.parse({mediaType:"application/webpub+zip",name:"Readium Web Publication",fileExtension:"webpub"})}static get READIUM_WEBPUB_MANIFEST(){return i.parse({mediaType:"application/webpub+json",name:"Readium Web Publication",fileExtension:"json"})}static get SMIL(){return i.parse({mediaType:"application/smil+xml",fileExtension:"smil"})}static get SVG(){return i.parse({mediaType:"image/svg+xml",fileExtension:"svg"})}static get TEXT(){return i.parse({mediaType:"text/plain",fileExtension:"txt"})}static get TIFF(){return i.parse({mediaType:"image/tiff",fileExtension:"tiff"})}static get TTF(){return i.parse({mediaType:"font/ttf",fileExtension:"ttf"})}static get W3C_WPUB_MANIFEST(){return i.parse({mediaType:"application/x.readium.w3c.wpub+json",name:"Web Publication",fileExtension:"json"})}static get WAV(){return i.parse({mediaType:"audio/wav",fileExtension:"wav"})}static get WEBM_AUDIO(){return i.parse({mediaType:"audio/webm",fileExtension:"webm"})}static get WEBM_VIDEO(){return i.parse({mediaType:"video/webm",fileExtension:"webm"})}static get WEBP(){return i.parse({mediaType:"image/webp",fileExtension:"webp"})}static get WOFF(){return i.parse({mediaType:"font/woff",fileExtension:"woff"})}static get WOFF2(){return i.parse({mediaType:"font/woff2",fileExtension:"woff2"})}static get XHTML(){return i.parse({mediaType:"application/xhtml+xml",fileExtension:"xhtml"})}static get XML(){return i.parse({mediaType:"application/xml",fileExtension:"xml"})}static get ZAB(){return i.parse({mediaType:"application/x.readium.zab+zip",name:"Zipped Audio Book",fileExtension:"zab"})}static get ZIP(){return i.parse({mediaType:"application/zip",fileExtension:"zip"})}};var _e=(i=>(i.left="left",i.right="right",i.center="center",i))(_e||{}),ai=class i{constructor(e){this.otherProperties=e}get page(){return this.otherProperties.page}static deserialize(e){if(e)return new i(e)}serialize(){return this.otherProperties}add(e){let t=Object.assign({},this.otherProperties);for(let o in e)t[o]=e[o];return new i(t)}};var yt=class{constructor(e){this.uri=e,this.parameters=this.getParameters(e)}getParameters(e){let t=/\{\??([^}]+)\}/g,o=e.match(t);return o?new Set(o.join(",").replace(t,"$1").split(",").map(r=>r.trim())):new Set}expand(e){let t=r=>r.split(",").map(n=>{let a=e[n];return a?encodeURIComponent(a):""}).join(","),o=r=>"?"+r.split(",").map(n=>{let a=n.split("=")[0],s=e[a];return s?`${a}=${encodeURIComponent(s)}`:""}).join("&");return this.uri.replace(/\{(\??)([^}]+)\}/g,(...r)=>r[1]?o(r[2]):t(r[2]))}};var ee=class i{constructor(e){this.fragments=e.fragments?e.fragments:new Array,this.progression=e.progression,this.totalProgression=e.totalProgression,this.position=e.position,this.otherLocations=e.otherLocations}static deserialize(e){if(!e)return;let t=Ot(e.progression),o=Ot(e.totalProgression),r=Ot(e.position),n=new Map,a=new Set(["fragment","fragments","progression","totalProgression","position","otherLocations"]);return Object.entries(e).forEach(([s,c])=>{a.has(s)||n.set(s,c)}),e.otherLocations instanceof Map&&e.otherLocations.forEach((s,c)=>n.set(c,s)),new i({fragments:Ne(e.fragments||e.fragment),progression:t!==void 0&&t>=0&&t<=1?t:void 0,totalProgression:o!==void 0&&o>=0&&o<=1?o:void 0,position:r!==void 0&&r>0?r:void 0,otherLocations:n.size===0?void 0:n})}serialize(){let e={};return this.fragments&&(e.fragments=this.fragments),this.progression!==void 0&&(e.progression=this.progression),this.totalProgression!==void 0&&(e.totalProgression=this.totalProgression),this.position!==void 0&&(e.position=this.position),this.otherLocations&&this.otherLocations.forEach((t,o)=>e[o]=t),e}},me=class i{constructor(e){this.after=e.after,this.before=e.before,this.highlight=e.highlight}static deserialize(e){if(e)return new i({after:e.after,before:e.before,highlight:e.highlight})}serialize(){let e={};return this.after!==void 0&&(e.after=this.after),this.before!==void 0&&(e.before=this.before),this.highlight!==void 0&&(e.highlight=this.highlight),e}},H=class i{constructor(e){let t=e.href.indexOf("#"),o=t>=0?e.href.slice(t+1):void 0;this.href=t>=0?e.href.slice(0,t):e.href,this.type=e.type,this.title=e.title;let r=e.locations?.fragments,n=o&&(!r||r.length===0);this.locations=e.locations?n?new ee({...e.locations,fragments:[o]}):e.locations:o?new ee({fragments:[o]}):new ee({}),this.text=e.text}static deserialize(e){if(e&&e.href&&e.type)return new i({href:e.href,type:e.type,title:e.title,locations:ee.deserialize(e.locations),text:me.deserialize(e.text)})}serialize(){let e={href:this.href,type:this.type};return this.title!==void 0&&(e.title=this.title),this.locations&&(e.locations=this.locations.serialize()),this.text&&(e.text=this.text.serialize()),e}copyWithLocations(e){return new i({href:this.href,type:this.type,title:this.title,text:this.text,locations:new ee({...this.locations,...e})})}};var Ie=class i{constructor(e){this.href=e.href,this.templated=e.templated,this.type=e.type,this.title=e.title,this.rels=e.rels,this.properties=e.properties,this.height=e.height,this.width=e.width,this.size=e.size,this.duration=e.duration,this.bitrate=e.bitrate,this.languages=e.languages,this.alternates=e.alternates,this.children=e.children}static deserialize(e){if(!(!e||typeof e.href!="string"))return new i({href:e.href,templated:e.templated,type:e.type,title:e.title,rels:e.rel?Array.isArray(e.rel)?new Set(e.rel):new Set([e.rel]):void 0,properties:ai.deserialize(e.properties),height:rt(e.height),width:rt(e.width),size:rt(e.size),duration:rt(e.duration),bitrate:rt(e.bitrate),languages:Ne(e.language),alternates:te.deserialize(e.alternate),children:te.deserialize(e.children)})}serialize(){let e={href:this.href};return this.templated!==void 0&&(e.templated=this.templated),this.type!==void 0&&(e.type=this.type),this.title!==void 0&&(e.title=this.title),this.rels&&(e.rel=At(this.rels)),this.properties&&(e.properties=this.properties.serialize()),this.height!==void 0&&(e.height=this.height),this.width!==void 0&&(e.width=this.width),this.size!==void 0&&(e.size=this.size),this.duration!==void 0&&(e.duration=this.duration),this.bitrate!==void 0&&(e.bitrate=this.bitrate),this.languages&&(e.language=this.languages),this.alternates&&(e.alternate=this.alternates.serialize()),this.children&&(e.children=this.children.serialize()),e}get mediaType(){return this.type!==void 0?Ce.parse({mediaType:this.type}):Ce.BINARY}toURL(e){let t=this.href.replace(/^(\/)/,"");if(t.length===0)return;let o=e||"/";return o.startsWith("/")&&(o="file://"+o),new URL(t,o).href.replace(/^(file:\/\/)/,"")}get templateParameters(){return this.templated?new yt(this.href).parameters:new Set}expandTemplate(e){return new i({href:new yt(this.href).expand(e),templated:!1})}addProperties(e){let t=i.deserialize(this.serialize());return t.properties=t.properties?t.properties?.add(e):new ai(e),t}get locator(){return new H({href:this.href,type:this.type??"",title:this.title})}},te=class i{constructor(e){this.items=e}static deserialize(e){if(e&&Array.isArray(e))return new i(e.map(t=>Ie.deserialize(t)).filter(t=>t!==void 0))}serialize(){return this.items.map(e=>e.serialize())}findWithRel(e){let t=o=>o.rels&&o.rels.has(e);return this.items.find(t)}filterByRel(e){let t=o=>o.rels&&o.rels.has(e);return this.items.filter(t)}findWithHref(e){let t=o=>o.href===e;return this.items.find(t)}findIndexWithHref(e){let t=o=>o.href===e;return this.items.findIndex(t)}findWithMediaType(e){let t=o=>o.mediaType.matches(e);return this.items.find(t)}filterByMediaType(e){let t=o=>o.mediaType.matches(e);return this.items.filter(t)}filterByMediaTypes(e){let t=o=>{for(let r of e)if(o.mediaType.matches(r))return!0;return!1};return this.items.filter(t)}everyIsAudio(){let e=t=>t.mediaType.isAudio;return this.items.length>0&&this.items.every(e)}everyIsBitmap(){let e=t=>t.mediaType.isBitmap;return this.items.length>0&&this.items.every(e)}everyIsHTML(){let e=t=>t.mediaType.isHTML;return this.items.length>0&&this.items.every(e)}everyIsVideo(){let e=t=>t.mediaType.isVideo;return this.items.length>0&&this.items.every(e)}everyMatchesMediaType(e){return Array.isArray(e)?this.items.length>0&&this.items.every(t=>{for(let o of e)return t.mediaType.matches(o);return!1}):this.items.length>0&&this.items.every(t=>t.mediaType.matches(e))}filterLinksHasType(){return this.items.filter(e=>e.type)}};var Re=(i=>(i.EPUB="https://readium.org/webpub-manifest/profiles/epub",i.AUDIOBOOK="https://readium.org/webpub-manifest/profiles/audiobook",i.DIVINA="https://readium.org/webpub-manifest/profiles/divina",i.PDF="https://readium.org/webpub-manifest/profiles/pdf",i))(Re||{});var si=class i{constructor(e,t,o=[],r=[],n=void 0){this._trimmedLinkMap=new Map,this._allItems=e,this.linkMap=t,this._conformsTo=o,this.tocLinks=r,this._tocDepth=n}static build(e,t={}){let o=e.toc?.items??[],r=e.readingOrder.items,{depth:n}=t,a=e.metadata?.conformsTo??[],s=new Map,c=[];for(let l=0;l<r.length;l++){let h=r[l],d=i.bareHref(h.href),u=h.title??i.findTitleInToc(o,d,n),m=i.collectChildrenFromToc(o,d,n,1,s),p={title:u,references:[h.href],children:m.length>0?m:void 0};s.set(p,h),c.push(p)}return new i(c,s,a,o,n)}augment(e){for(let t of this.flatAll){let o=this.linkFor(t);if(!o)continue;let r=e(t,o);r.position!==void 0&&(t.position=r.position),r.scroll!==void 0&&(t.scroll=r.scroll),r.role!==void 0&&(t.role=r.role)}}get depth(){return this._depth}set depth(e){this._depth!==e&&(this._depth=e,this._tocDepth=e,this._items=void 0,this._flat=void 0,this._toc=void 0)}get items(){return this._items||(this._depth!==void 0?(this._trimmedLinkMap=new Map,this._items=i.trimToDepth(this._allItems,this._depth,this.linkMap,this._trimmedLinkMap)):this._items=this._allItems),this._items}locate(e){let t=e.href.split("#")[0],o=_n(e.locations),r=ji(e.locations),n=e.locations.progression;if(o!==void 0){let a=-1/0,s;for(let c of this.flat){if(!this.itemMatchesHref(c,t))continue;let l=this.itemStartTime(c,t);l!==void 0&&l<=o&&l>a&&(a=l,s=c)}if(s)return s}if(r){let a=this.flat.find(s=>this.itemMatchesHref(s,t)?s.references.some(c=>c.split("#")[1]===r):!1);if(a)return a}if(n!==void 0){let a=-1/0,s;for(let l of this.flat){if(!this.itemMatchesHref(l,t))continue;let h=this.itemScrollPosition(l);h!==void 0&&h<=n&&h>a&&(a=h,s=l)}if(s)return s;let c=this.items.find(l=>this.itemMatchesHref(l,t));if(c?.children?.length&&!c.children.some(l=>l.scroll!==void 0)){let l=Math.min(Math.floor(n*c.children.length),c.children.length-1);return c.children[l]}}return this.flat.find(a=>this.itemMatchesHref(a,t))}navigableFrom(e){let t=this.flat.indexOf(e);return{previous:t>0?this.flat[t-1]:void 0,next:t>=0&&t<this.flat.length-1?this.flat[t+1]:void 0}}segmentsForHref(e){let t=e.split("#")[0],o=this.items.find(r=>this.itemMatchesHref(r,t));return o?o.children?.length?o.children:[o]:[]}ancestors(e){return this.ancestorPath(this.items,e)??[]}linkFor(e){return this.linkMap.get(e)??this._trimmedLinkMap.get(e)}get contextualizedToc(){return this._toc||(this._toc=this.tocLinks.length>0?this.buildTocEntries(this.tocLinks,this._tocDepth,1):this._allItems.map(e=>this.entryFor(this.linkFor(e),e))),this._toc}tocEntryFor(e){let t=this.linkFor(e);if(!t)return;let o=this.findTocEntryByLink(this.contextualizedToc,t);return o||this.nearestTocEntryForResource(t.href,e)||this.previousResolvedTocEntry(t)}get flat(){return this._flat||(this._flat=this.flattenItems(this.items)),this._flat}get flatAll(){return this.flattenItems(this._allItems)}static findTitleInToc(e,t,o){let{atStart:r,fragments:n}=i.collectTocCandidates(e,t,o,1);if(r.length>0)return r[0].title;if(n.length===1)return n[0].title}static collectChildrenFromToc(e,t,o,r,n){if(o!==void 0&&r>o)return[];let a=[];for(let s of e){let c=i.bareHref(s.href),l=s.href.startsWith("#");if((c===t||l&&c==="")&&s.title&&!i.isStartOfResource(s.href)){let h=c===""?t+s.href:s.href,d={title:s.title,references:[h]};n.set(d,s),a.push(d)}s.children?.items?.length&&a.push(...i.collectChildrenFromToc(s.children.items,t,o,r+1,n))}return a}static collectTocCandidates(e,t,o,r){if(o!==void 0&&r>o)return{atStart:[],fragments:[]};let n=[],a=[];for(let s of e){let c=i.bareHref(s.href),l=s.href.startsWith("#");if((c===t||l&&c==="")&&s.title&&(i.isStartOfResource(s.href)?n.push(s):a.push(s)),s.children?.items?.length){let h=i.collectTocCandidates(s.children.items,t,o,r+1);n.push(...h.atStart),a.push(...h.fragments)}}return{atStart:n,fragments:a}}static isStartOfResource(e){let t=e.split("#")[1];if(!t)return!0;let o=t.match(/(?:^|&)t=([^&]+)/);return o!==null&&bc(o[1])}get linkToItem(){if(!this._linkToItem){this._linkToItem=new Map;for(let[e,t]of this.linkMap)this._linkToItem.set(t,e)}return this._linkToItem}buildTocEntries(e,t,o){return t!==void 0&&o>t?[]:e.map(r=>{let n=this.linkToItem.get(r)??this.resourceStartItem(r.href),a=r.children?.items?.length?this.buildTocEntries(r.children.items,t,o+1):[];return{...this.entryFor(r,n),children:a.length>0?a:void 0}})}tocLinksFlat(e,t,o){if(t!==void 0&&o>t)return[];let r=[];for(let n of e)r.push(n),n.children?.items?.length&&r.push(...this.tocLinksFlat(n.children.items,t,o+1));return r}entryFor(e,t){let o=this._conformsTo.includes(Re.AUDIOBOOK);return{link:e,position:!o&&t?.position!==void 0?String(t.position):void 0,timestamp:o&&t?.position!==void 0?_c(t.position):void 0}}resourceStartItem(e){let t=i.bareHref(e);return this._allItems.find(o=>this.itemMatchesHref(o,t))}findTocEntryByLink(e,t){for(let o of e){if(o.link===t)return o;if(o.children){let r=this.findTocEntryByLink(o.children,t);if(r)return r}}}nearestTocEntryForResource(e,t){let o=i.bareHref(e),r=this.tocLinksFlat(this.tocLinks,this._tocDepth,1).filter(h=>i.bareHref(h.href)===o).map(h=>({link:h,item:this.linkToItem.get(h)??this.resourceStartItem(h.href)}));if(r.length===0)return;let n=this._conformsTo.includes(Re.AUDIOBOOK),a=n?t.position??0:t.scroll??0,s,c=-1/0;for(let h of r){let d=n?h.item?.position??0:h.item?.scroll??0;d<=a&&d>c&&(c=d,s=h)}let l=s??r[0];return this.findTocEntryByLink(this.contextualizedToc,l.link)}previousResolvedTocEntry(e){let t=this._allItems.findIndex(o=>this.linkFor(o)===e);if(t!==-1)for(let o=t-1;o>=0;o--){let r=this.linkFor(this._allItems[o]);if(!r)continue;let n=i.bareHref(r.href),{atStart:a,fragments:s}=i.collectTocCandidates(this.tocLinks,n,this._tocDepth,1),c=a.filter(u=>i.bareHref(u.href)===n),l=s.filter(u=>i.bareHref(u.href)===n),h=c[0]??(l.length>0?l[l.length-1]:void 0);if(!h)continue;let d=this.findTocEntryByLink(this.contextualizedToc,h);if(d)return d}}static trimToDepth(e,t,o,r){return e.map(n=>{let a={...n,children:t>1&&n.children?.length?i.trimToDepth(n.children,t-1,o,r):void 0},s=o.get(n);return s&&r.set(a,s),a})}flattenItems(e){let t=[];for(let o of e)t.push(o),o.children&&t.push(...this.flattenItems(o.children));return t}itemStartTime(e,t){for(let o of e.references){let r=o.indexOf("#"),n=r>=0?o.slice(0,r):o,a=r>=0?o.slice(r+1):void 0;if((n||t)!==t)continue;if(!a)return;let s=a.match(/(?:^|&)t=([^&]+)/);return s?Mt(s[1]):void 0}}itemMatchesHref(e,t){return e.references.some(o=>{let r=o.split("#")[0];return r===t||r===""})}ancestorPath(e,t){for(let o of e){if(o===t)return[];if(o.children){let r=this.ancestorPath(o.children,t);if(r!==null)return[o,...r]}}return null}itemScrollPosition(e){return e.scroll}static bareHref(e){return e.split("#")[0]}};function vc(i,e){return si.build(i,e)}function wc(i,e){let t=si.build(i,e),o=i.readingOrder.items;if(!(o.length<=1||o.every(a=>a.duration!==void 0)))return t;let r=0,n=new Map(o.map(a=>{let s=r;return r+=a.duration??0,[a.href,s]}));return t.augment((a,s)=>{let c=s.href,l=c.indexOf("#"),h=l>=0?c.slice(0,l):c,d=l>=0?c.slice(l+1):void 0,u=n.get(h)??0,m=d?.match(/(?:^|&)t=([^&]+)/),p=m?Mt(m[1]):void 0;return{position:u+(p??0)}}),t}function xc(i,e){return i.metadata?.conformsTo?.includes(Re.AUDIOBOOK)??!1?wc(i,e):vc(i,e)}var Nt=class i{constructor(e){this.value=e.value,this.scheme=e.scheme}static deserialize(e){if(e){if(typeof e=="string")return new i({value:e});if(typeof e=="object"&&e.value)return new i({value:e.value,scheme:e.scheme})}}serialize(){return this.scheme?{value:this.value,scheme:this.scheme}:this.value}};var vn=class li{constructor(e){this.translations=typeof e=="string"?{[li.UNDEFINED_LANGUAGE]:e}:e}static deserialize(e){if(e&&(typeof e=="string"||e.constructor===Object))return new li(e)}serialize(){return this.translations}getTranslation(e){return this.translations[e||li.UNDEFINED_LANGUAGE]||this.translations[li.UNDEFINED_LANGUAGE]||this.translations[li.LANGUAGE_EN]||(Object.values(this.translations).length===0?"":Object.values(this.translations)[0])}};vn.UNDEFINED_LANGUAGE="und",vn.LANGUAGE_EN="en";var He=vn;var wn=class i{constructor(e){this.name=e.name,this.sortAs=e.sortAs,this.identifier=e.identifier,this.altIdentifiers=e.altIdentifiers,this.roles=e.roles,this.links=e.links,this.position=e.position}static deserialize(e){if(e)return typeof e=="string"?new i({name:He.deserialize(e)}):e.name?new i({name:He.deserialize(e.name),sortAs:He.deserialize(e.sortAs),identifier:e.identifier,altIdentifiers:e.altIdentifier?Array.isArray(e.altIdentifier)?new Set(e.altIdentifier.map(t=>Nt.deserialize(t)).filter(t=>t!==void 0)):new Set([Nt.deserialize(e.altIdentifier)].filter(t=>t!==void 0)):void 0,roles:e.role?new Set(Ne(e.role)):void 0,links:te.deserialize(e.links),position:Ot(e.position)}):void 0}serialize(){let e={name:this.name.serialize()};return this.sortAs!==void 0&&(e.sortAs=this.sortAs.serialize()),this.identifier!==void 0&&(e.identifier=this.identifier),this.altIdentifiers&&(e.altIdentifier=At(this.altIdentifiers).map(t=>t.serialize())),this.roles&&(e.role=At(this.roles)),this.links&&(e.links=this.links.serialize()),this.position!==void 0&&(e.position=this.position),e}},ne=class i{constructor(e){this.items=e}static deserialize(e){if(!e)return;let t=Array.isArray(e)?e:[e];return new i(t.map(o=>wn.deserialize(o)).filter(o=>o!==void 0))}serialize(){return this.items.map(e=>e.serialize())}};var ci=class i{constructor(e){this.items=e&&e.items?e.items:new Map}static deserialize(e){if(!(e&&typeof e=="object"))return;let t=new Map;return Object.entries(e).forEach(([o,r])=>{let n=ne.deserialize(r);n&&n.items.length>0&&t.set(o,n)}),new i({items:t})}serialize(){let e={};return this.items.forEach((t,o)=>e[o]=t.serialize()),e}};var L=(i=>(i.ltr="ltr",i.rtl="rtl",i))(L||{});var xn=class i{constructor(e){this.name=e.name,this.sortAs=e.sortAs,this.code=e.code,this.scheme=e.scheme,this.links=e.links}static deserialize(e){if(e)return typeof e=="string"?new i({name:He.deserialize(e)}):e.name?new i({name:He.deserialize(e.name),sortAs:e.sortAs?He.deserialize(e.sortAs):void 0,code:e.code,scheme:e.scheme,links:te.deserialize(e.links)}):void 0}serialize(){let e={name:this.name.serialize()};return this.sortAs!==void 0&&(e.sortAs=this.sortAs.serialize()),this.code!==void 0&&(e.code=this.code),this.scheme!==void 0&&(e.scheme=this.scheme),this.links&&(e.links=this.links.serialize()),e}},Ao=class i{constructor(e){this.items=e}static deserialize(e){if(!e)return;let t=Array.isArray(e)?e:[e];return new i(t.map(o=>xn.deserialize(o)).filter(o=>o!==void 0))}serialize(){return this.items.map(e=>e.serialize())}};var Rp=(i=>(i.all="all",i.none="none",i))(Rp||{}),Mo=class i{constructor(e){this.reservation=e.reservation,this.policy=e.policy}static deserialize(e){if(e)return new i({reservation:e.reservation,policy:e.policy})}serialize(){let e={};return this.reservation!==void 0&&(e.reservation=this.reservation),this.policy!==void 0&&(e.policy=this.policy),e}};var kc=class kn{constructor(e){this.title=e.title,this.typeUri=e.typeUri,this.conformsTo=e.conformsTo,this.identifier=e.identifier,this.altIdentifier=e.altIdentifier,this.subtitle=e.subtitle,this.sortAs=e.sortAs,this.artists=e.artists,this.authors=e.authors,this.colorists=e.colorists,this.contributors=e.contributors,this.editors=e.editors,this.illustrators=e.illustrators,this.inkers=e.inkers,this.letterers=e.letterers,this.narrators=e.narrators,this.pencilers=e.pencilers,this.translators=e.translators,this.languages=e.languages,this.description=e.description,this.publishers=e.publishers,this.imprints=e.imprints,this.published=e.published,this.modified=e.modified,this.subjects=e.subjects,this.belongsTo=e.belongsTo,this.belongsToCollections=e.belongsToCollections,this.belongsToSeries=e.belongsToSeries,this.belongsToCollections&&this.belongsToCollections.items.length>0&&(this.belongsTo||(this.belongsTo=new ci),this.belongsTo.items.set("collection",this.belongsToCollections)),this.belongsToSeries&&this.belongsToSeries.items.length>0&&(this.belongsTo||(this.belongsTo=new ci),this.belongsTo.items.set("series",this.belongsToSeries)),this.layout=e.layout,this.readingProgression=e.readingProgression,this.duration=e.duration,this.numberOfPages=e.numberOfPages,this.accessibility=e.accessibility,this.tdm=e.tdm,this.otherMetadata=e.otherMetadata}static deserialize(e){if(!(e&&e.title))return;let t=He.deserialize(e.title),o=e["@type"],r=Ne(e.conformsTo),n=e.identifier,a=Nt.deserialize(e.altIdentifier),s=He.deserialize(e.subtitle),c=He.deserialize(e.sortAs),l=ne.deserialize(e.artist),h=ne.deserialize(e.author),d=ne.deserialize(e.colorist),u=ne.deserialize(e.contributor),m=ne.deserialize(e.editor),p=ne.deserialize(e.illustrator),g=ne.deserialize(e.inker),f=ne.deserialize(e.letterer),y=ne.deserialize(e.narrator),w=ne.deserialize(e.penciler),x=ne.deserialize(e.translator),k=Ne(e.language),z=e.description,ce=ne.deserialize(e.publisher),G=ne.deserialize(e.imprint),V=Sn(e.published),To=Sn(e.modified),gn=Ao.deserialize(e.subject),rp=ci.deserialize(e.belongsTo),np=Oo.deserialize(e.accessibility),ap=e.layout,sp=e.readingProgression,lp=rt(e.duration),cp=rt(e.numberOfPages),hp=Mo.deserialize(e.tdm),Fo=Object.assign({},e);return kn.mappedProperties.forEach(dp=>delete Fo[dp]),Object.keys(Fo).length===0&&(Fo=void 0),new kn({title:t,typeUri:o,conformsTo:r,identifier:n,altIdentifier:a,subtitle:s,sortAs:c,artists:l,authors:h,colorists:d,contributors:u,editors:m,illustrators:p,inkers:g,letterers:f,narrators:y,pencilers:w,translators:x,languages:k,description:z,publishers:ce,imprints:G,published:V,modified:To,subjects:gn,belongsTo:rp,layout:ap,readingProgression:sp,duration:lp,numberOfPages:cp,accessibility:np,tdm:hp,otherMetadata:Fo})}serialize(){let e={title:this.title.serialize()};if(this.typeUri!==void 0&&(e["@type"]=this.typeUri),this.conformsTo&&(e.conformsTo=this.conformsTo),this.identifier!==void 0&&(e.identifier=this.identifier),this.altIdentifier&&(e.altIdentifier=this.altIdentifier.serialize()),this.subtitle&&(e.subtitle=this.subtitle.serialize()),this.sortAs&&(e.sortAs=this.sortAs.serialize()),this.editors&&(e.editor=this.editors.serialize()),this.artists&&(e.artist=this.artists.serialize()),this.authors&&(e.author=this.authors.serialize()),this.colorists&&(e.colorist=this.colorists.serialize()),this.contributors&&(e.contributor=this.contributors.serialize()),this.illustrators&&(e.illustrator=this.illustrators.serialize()),this.letterers&&(e.letterer=this.letterers.serialize()),this.narrators&&(e.narrator=this.narrators.serialize()),this.pencilers&&(e.penciler=this.pencilers.serialize()),this.translators&&(e.translator=this.translators.serialize()),this.inkers&&(e.inker=this.inkers.serialize()),this.languages&&(e.language=this.languages),this.description!==void 0&&(e.description=this.description),this.publishers&&(e.publisher=this.publishers.serialize()),this.imprints&&(e.imprint=this.imprints.serialize()),this.published!==void 0&&(e.published=this.published.toISOString()),this.modified!==void 0&&(e.modified=this.modified.toISOString()),this.subjects&&(e.subject=this.subjects.serialize()),this.belongsTo&&(e.belongsTo=this.belongsTo.serialize()),this.layout&&(e.layout=this.layout),this.readingProgression&&(e.readingProgression=this.readingProgression),this.duration!==void 0&&(e.duration=this.duration),this.numberOfPages!==void 0&&(e.numberOfPages=this.numberOfPages),this.accessibility&&(e.accessibility=this.accessibility.serialize()),this.tdm&&(e.tdm=this.tdm.serialize()),this.otherMetadata){let t=this.otherMetadata;Object.keys(t).forEach(o=>e[o]=t[o])}return e}get effectiveLayout(){if(!this.conformsTo)return null;for(let e of this.conformsTo)switch(e){case Re.EPUB:return this.layout||v.reflowable;case Re.DIVINA:return this.layout===v.reflowable?v.fixed:this.layout||v.fixed;case Re.AUDIOBOOK:case Re.PDF:return null}return null}get effectiveReadingProgression(){if(this.readingProgression)return this.readingProgression;if(this.languages?.length!==1)return L.ltr;let e=this.languages[0].toLowerCase();if(e==="zh-hant"||e==="zh-tw")return L.rtl;switch(e.split("-")[0]){case"ar":return L.rtl;case"fa":return L.rtl;case"he":return L.rtl;default:return L.ltr}}};kc.mappedProperties=["title","@type","conformsTo","identifier","altIdentifier","subtitle","sortAs","artist","author","colorist","contributor","editor","illustrator","inker","letterer","narrator","penciler","translator","language","description","publisher","imprint","published","modified","subject","belongsTo","layout","readingProgression","duration","numberOfPages","accessibility","tdm"];var Cc=kc;var Gi=class i{constructor(e){this.metadata=e.metadata,this.links=e.links,this.subcollections=e.subcollections}static deserialize(e){if(!e)return;let t,o,r;if(Array.isArray(e))t=te.deserialize(e);else if(typeof e=="object")t=te.deserialize(e.links),o=new Map,e.metadata&&Object.entries(e.metadata).forEach(([n,a])=>{o?.set(n,a)}),r=i.deserializeCollections(e);else return;if(!(!t||t.items.length===0))return new i({metadata:o?.size?o:void 0,links:t,subcollections:r?.size?r:void 0})}static deserializeCollections(e){if(!e)return;let t=new Map;return Object.entries(e).forEach(([o,r])=>{if(o!=="links"&&o!=="metadata"){let n=i.deserialize(r);if(n){let a=new Array;a.push(n),t.set(o,a)}else if(Array.isArray(r)){let a=r.map(s=>i.deserialize(s)).filter(s=>s!==void 0);t.set(o,a)}}}),t.size?t:void 0}serialize(){let e={};return this.metadata&&(e.metadata={},this.metadata.forEach((t,o)=>e.metadata[o]=t)),this.links.items.length&&(e.links=this.links.serialize()),i.serializeCollection(e,this.subcollections),e}static serializeCollection(e,t){t&&t.size>0&&t.forEach((o,r)=>{o.length===1?e[r]=o[0].serialize():e[r]=o.map(n=>n.serialize())})}};var No=class i{constructor(e){this.context=e.context,this.metadata=e.metadata,this.links=e.links,this.readingOrder=e.readingOrder,this.resources=e.resources,this.toc=e.toc,this.subcollections=e.subcollections}static deserialize(e){if(!e)return;let t=Cc.deserialize(e.metadata);if(!t)return;let o=te.deserialize(e.links),r=te.deserialize(e.readingOrder?e.readingOrder:e.spine);if(!r)return;let n=new Set(["@context","metadata","links","readingOrder","spine","resources","toc"]),a={};return Object.keys(e).forEach(s=>{n.has(s)||(a[s]=e[s])}),new i({context:Ne(e["@context"]),metadata:t,links:o,readingOrder:r,resources:te.deserialize(e.resources),toc:te.deserialize(e.toc),subcollections:Gi.deserializeCollections(a)})}serialize(){let e={};return this.context!==void 0&&(e["@context"]=this.context),e.metadata=this.metadata.serialize(),this.links!==void 0&&(e.links=this.links.serialize()),e.readingOrder=this.readingOrder.serialize(),this.resources&&(e.resources=this.resources.serialize()),this.toc&&(e.toc=this.toc.serialize()),Gi.serializeCollection(e,this.subcollections),e}linkWithRel(e){let t=new Array;t.push(this.readingOrder),this.resources&&t.push(this.resources),this.links&&t.push(this.links);let o;for(let r of t)if(o=r.findWithRel(e),o!==void 0)return o;return o}linksWithRel(e){let t=[];return t.push(this.readingOrder.filterByRel(e)),this.resources&&t.push(this.resources.filterByRel(e)),this.links&&t.push(this.links.filterByRel(e)),t.reduce((o,r)=>o.concat(r),[])}locatorFromLink(e){let t=e.href.split("#")[0],o=this.linkWithHref(t);if(!o)return;let r=o.type;if(!r)return;let n=e.href.includes("#");return new H({href:e.href,type:r,title:o.title??e.title,locations:new ee({progression:n?void 0:0})})}linkWithHref(e){let t=s=>{let c;for(let h of s)if(c=h.findWithHref(e),c!==void 0)return c;let l=new Array;return s.forEach(h=>{let d=[];for(let u of h.items)u.alternates&&d.push(u.alternates),u.children&&d.push(u.children);l.push(...d)}),l.length>0&&(c=t(l)),c},o=[];o.push(this.readingOrder),this.resources&&o.push(this.resources),this.links&&o.push(this.links);let r=t(o);if(r!==void 0)return r;let n=e.split(/[#]/);if(n.length<2)return;let a=n[0];return this.linkWithHref(a)}get baseURL(){let e=this.links?.items.find(t=>t.rels&&t.rels.has("self"));if(e){let t=e.href;if(t){let o=t.lastIndexOf("/"),r=o===-1?void 0:t.substring(o+1);t=t.replace(new RegExp("/?$query$"),""),t=t.replace(new RegExp("//$"),""),r&&(t=t.replace(new RegExp(r+"$"),""))}return t}}setSelfLink(e){this.links||(this.links=new te([])),this.links.items=this.links.items.filter(t=>t.rels===void 0||!t.rels?.has("self")),this.links.items.push(new Ie({href:e,type:Ce.READIUM_WEBPUB_MANIFEST.string,rels:new Set(["self"])}))}};var Io=class i{constructor(e){this.links=e.links,this.guided=e.guided}static deserialize(e){if(e)return new i({links:te.deserialize(e.links),guided:Rn.deserializeArray(e.guided)})}serialize(){let e={};return this.links!==void 0&&(e.links=this.links.serialize()),this.guided!==void 0&&(e.guided=this.guided.map(t=>t.serialize())),e}},Cn=class i{constructor(e){this.plain=e.plain,this.ssml=e.ssml,this.language=e.language}static deserialize(e){if(e!=null){if(typeof e=="string")return new i({plain:e});if(e.plain||e.ssml||e.language)return new i({plain:e.plain,ssml:e.ssml,language:e.language})}}serialize(){let e={};return this.plain!==void 0&&(e.plain=this.plain),this.ssml!==void 0&&(e.ssml=this.ssml),this.language!==void 0&&(e.language=this.language),Object.keys(e).length>0?e:void 0}},Rn=class i{constructor(e){this.audioref=e.audioref,this.children=e.children,this.imgref=e.imgref,this.role=e.role,this.level=e.level!==void 0?Math.min(6,Math.max(1,e.level)):void 0,this.text=e.text,this.textref=e.textref,this.description=e.description}get plainText(){return this.text?.plain}get ssmlText(){return this.text?.ssml}get textLanguage(){return this.text?.language}static deserialize(e){if(e)return new i({audioref:e.audioref,children:i.deserializeArray(e.children),imgref:e.imgref,role:e.role?new Set(Ne(e.role)):void 0,level:typeof e.level=="number"?e.level:void 0,text:Cn.deserialize(e.text),textref:e.textref,description:i.deserialize(e.description)})}static deserializeArray(e){if(Array.isArray(e))return e.map(t=>i.deserialize(t)).filter(t=>t!==void 0)}serialize(){let e={};if(this.audioref!==void 0&&(e.audioref=this.audioref),this.children!==void 0&&(e.children=this.children.map(t=>t.serialize())),this.imgref!==void 0&&(e.imgref=this.imgref),this.role!==void 0&&(e.role=At(this.role)),this.level!==void 0&&(e.level=this.level),this.text!==void 0){let t=this.text.serialize();t!==void 0&&(e.text=t)}return this.textref!==void 0&&(e.textref=this.textref),this.description&&(e.description=this.description.serialize()),e}get audioFile(){return this.audioref?.split("#")[0]}get audioTime(){if(this.audioref?.includes("#"))return this.audioref.split("#",2)[1]}get textFile(){return this.textref?.split("#")[0]}get fragmentId(){if(this.textref?.includes("#"))return this.textref.split("#",2)[1]}get clip(){let e=this.audioFile;if(!e)return;let t=this.audioTime,o={audioResource:e,fragmentId:this.fragmentId};if(!t)return o;let r=this.parseTimer(t);return o.start=r[0],o.end=r[1],o}parseTimer(e){if(!e||!e.startsWith("t="))return[void 0,void 0];let t=e.substring(2).split(",").map(o=>parseFloat(o));return t.length===1?[isNaN(t[0])?void 0:t[0],void 0]:t.length>2?[void 0,void 0]:[isNaN(t[0])?void 0:t[0],isNaN(t[1])?void 0:t[1]]}};var Uo=class{constructor(e){this.fetcher=new zo,e.fetcher&&(this.fetcher=e.fetcher),this.manifest=e.manifest,this.context=e.manifest.context,this.metadata=e.manifest.metadata,this.links=e.manifest.links,this.readingOrder=e.manifest.readingOrder,this.resources=e.manifest.resources,this.toc=e.manifest.toc,this.subcollections=e.manifest.subcollections}get timeline(){return this._timeline||(this._timeline=xc(this)),this._timeline}get baseURL(){return this.manifest.baseURL}linkWithHref(e){return this.manifest.linkWithHref(e)}linksWithRole(e){let t=this.subcollections?.get(e);return t&&t.length>0?t[0].links:void 0}linksWithRel(e){return this.manifest.linksWithRel(e)}linkWithRel(e){return this.manifest.linkWithRel(e)}getCover(){let e=[this.links,this.resources,this.readingOrder].filter(Boolean);for(let t of e){let o=t.items.find(r=>r.rels?.has("cover"));if(o)return o}for(let t of e){let o=t.items.find(r=>r.mediaType.isBitmap||r.mediaType.matches(Ce.SVG));if(o)return o}}async positionsFromManifest(){let e=this.manifest.links?.findWithMediaType("application/vnd.readium.position-list+json");if(e===void 0)return[];let t=await this.get(e).readAsJSON();return t.total?t.positions.map(o=>H.deserialize(o)).filter(o=>o!==void 0):[]}async guideForLink(e){let t=a=>a.alternates?.findWithMediaType("application/guided-navigation+json"),o=t(e);if(!o){let a=this.linkWithHref(e.href);a!==void 0&&(o=t(a))}if(o||(o=this.manifest.links?.findWithMediaType("application/guided-navigation+json")),!o)return;let r=o.href;if(o.templated){let a=new yt(r),s={};a.parameters.has("ref")&&(s.ref=e.href),r=new yt(r).expand(s)}let n=await this.get(new Ie({href:r})).readAsJSON();return Io.deserialize(n)}get(e){return this.fetcher.get(e)}};var Ep=Math.pow(2,32),Rc=()=>Math.round(Math.random()*Ep).toString(36),hi=()=>`${Math.round(performance.now())}-${Rc()}-${Rc()}`;var St=1,Vi=class{constructor(e){this.destination=null,this.registrar=new Map,this.origin="",this.channelId="",this.receiver=this.receive.bind(this),this.preLog=[],this.wnd=e,e.addEventListener("message",this.receiver)}receive(e){if(e.source===null)throw Error("Event source is null");if(typeof e.data!="object")return;let t=e.data;if(!(!("_readium"in t)||!t._readium||t._readium<=0)){if(t.key==="_ping"){if(!this.destination){if(this.destination=e.source,this.origin=e.origin,this.channelId=t._channel,t._readium!==St){t._readium>St?this.send("error",`received comms version ${t._readium} higher than ${St}`):this.send("error",`received comms version ${t._readium} lower than ${St}`),this.destination=null,this.origin="",this.channelId="";return}this.send("_pong",void 0),this.preLog.forEach(o=>this.send("log",o)),this.preLog=[]}return}else if(this.channelId){if(t._channel!==this.channelId||e.origin!==this.origin)return}else return;this.handle(t)}}handle(e){let t=this.registrar.get(e.key);if(!t||t.length===0){e.strict&&this.send("_unhandled",e);return}t.forEach(o=>o.cb(e.data,r=>{this.send("_ack",r,e.id)}))}register(e,t,o){Array.isArray(e)||(e=[e]),e.forEach(r=>{let n=this.registrar.get(r);if(n&&n.length>=0){if(n.find(a=>a.module===t))throw new Error(`Trying to register another callback for combination of event ${r} and module ${t}`);n.push({cb:o,module:t}),this.registrar.set(r,n)}else this.registrar.set(r,[{cb:o,module:t}])})}unregister(e,t){Array.isArray(e)||(e=[e]),e.forEach(o=>{let r=this.registrar.get(o);!r||r.length===0||r.splice(r.findIndex(n=>n.module===t),1)})}unregisterAll(e){this.registrar.forEach((t,o)=>this.registrar.set(o,t.filter(r=>r.module!==e)))}log(...e){this.destination?this.send("log",e):this.preLog.push(e)}get ready(){return!!this.destination}destroy(){this.destination=null,this.channelId="",this.preLog=[],this.registrar.clear(),this.wnd.removeEventListener("message",this.receiver)}send(e,t,o=void 0,r=[]){if(!this.destination)throw Error("Attempted to send comms message before destination has been initialized");let n={_readium:St,_channel:this.channelId,id:o??hi(),key:e,data:t};try{this.destination.postMessage(n,{targetOrigin:this.origin,transfer:r})}catch(a){if(r.length>0)throw a;this.destination.postMessage(n,this.origin,r)}}};var Ee=class{};var Ec="readium-snapper-style",En=class Do extends Ee{constructor(){super(...arguments),this.protected=!1,this.timelineEntries=new Map,this.cachedFragmentIds=[],this.sortedFragmentIds=[],this.cachedFragmentStarts=new Map}static inDomOrder(e,t,o){let r=e.get(t),n=e.get(o);return!r||!n?0:r.compareDocumentPosition(n)&Node.DOCUMENT_POSITION_FOLLOWING?-1:1}updateTimelineEntries(e,t){this.cachedFragmentIds=e,this.timelineEntries.clear(),this.cachedFragmentStarts.clear();for(let o of e){let r=t.document.getElementById(o);r&&this.timelineEntries.set(o,r)}this.sortedFragmentIds=Array.from(this.timelineEntries.keys()).sort((o,r)=>Do.inDomOrder(this.timelineEntries,o,r)),this.refreshFragmentStarts()}refreshFragmentStarts(){for(let[e,t]of this.timelineEntries)this.cachedFragmentStarts.set(e,this.fragmentStart(t))}fragmentStart(e){return 0}currentScrollExtent(){return{pos:0,size:0}}nearestPrecedingTimelineEntry(e){let t;for(let o of this.sortedFragmentIds){let r=this.timelineEntries.get(o),n=r.compareDocumentPosition(e);if(r===e||r.contains(e)||(n&Node.DOCUMENT_POSITION_FOLLOWING)!==0)t=o;else break}return t}fragmentFromGeometry(){for(let t of this.sortedFragmentIds){let o=this.timelineEntries.get(t);if(this.inCenterBand(o))return t}let e;for(let t of this.sortedFragmentIds){let o=this.timelineEntries.get(t);if(this.hasScrolledPast(o))e=t;else break}return e}firstFragmentIfReached(){let e=this.sortedFragmentIds[0];if(e===void 0)return;let t=this.timelineEntries.get(e);return this.hasScrolledPast(t)?e:void 0}currentTimelineFragment(){let{pos:e,size:t}=this.currentScrollExtent(),o=e+t/2,r;for(let n of this.sortedFragmentIds){let a=this.cachedFragmentStarts.get(n);if(a!==void 0)if(a<=o)r=n;else break}return r}sortedVisibleFragmentIds(){let{pos:e,size:t}=this.currentScrollExtent(),o=e+t,r=-1,n=-1;for(let a=0;a<this.sortedFragmentIds.length;a++){let s=this.cachedFragmentStarts.get(this.sortedFragmentIds[a]);s!==void 0&&(s<=e&&(r=a),s<o&&(n=a))}return r<0&&(r=0),n<r?[]:this.sortedFragmentIds.slice(r,n+1)}inCenterBand(e){return!1}buildStyles(){return`
        html, body {
            touch-action: manipulation;
            user-select: ${this.protected?"none":"auto"};
        }`}mount(e,t){let o=e.document.createElement("style");return o.dataset.readium="true",o.id=Ec,o.textContent=this.buildStyles(),e.document.head.appendChild(o),t.register("protect",Do.moduleName,(r,n)=>{this.protected=!0,o.textContent=this.buildStyles(),n(!0)}),t.register("unprotect",Do.moduleName,(r,n)=>{this.protected=!1,o.textContent=this.buildStyles(),n(!0)}),t.log("Snapper Mounted"),!0}unmount(e,t){return e.document.getElementById(Ec)?.remove(),this.timelineEntries.clear(),this.cachedFragmentIds=[],this.sortedFragmentIds=[],this.cachedFragmentStarts.clear(),t.log("Snapper Unmounted"),!0}};En.moduleName="snapper",En.CENTER_TOLERANCE=.01;var he=En;function Pc(i){return(i.document.documentElement.dir||i.document.body.dir).toLowerCase()==="rtl"}function Pn(i){return(i.getComputedStyle(i.document.documentElement).writingMode||i.getComputedStyle(i.document.body).writingMode)==="vertical-lr"}function Pp(i){let e=i.getComputedStyle(i.document.documentElement).writingMode||i.getComputedStyle(i.document.body).writingMode;return e==="vertical-rl"||e==="vertical-lr"}function di(i){let e=Pp(i),t=e&&Pn(i),o=i.innerWidth,r=i.innerHeight,n=i.document.scrollingElement,a=n.scrollLeft,s=n.scrollTop,c=parseInt(i.getComputedStyle(i.document.documentElement).getPropertyValue("column-count")),l=e&&!t?n.scrollWidth-o+a:a,h=s;return{isVertical:e,isVertLR:t,viewportInlineSize:e?r:o,viewportBlockSize:e?o:r,pageInlineSize:e?r:o/(c||1),xDocOffset:l,yDocOffset:h,inlineScrollOffset:e?h:l,blockScrollOffset:e?l:h,inlineStart:d=>e?d.top:d.left,blockStart:d=>e?d.left:d.top,inlineSize:d=>e?d.height:d.width,blockSize:d=>e?d.width:d.height,applyPosition(d,u,m,p,g,f){d.style.position="absolute",e?(d.style.top=`${u*f}px`,d.style.left=`${m*f}px`,d.style.height=`${p*f}px`,d.style.width=`${g*f}px`):(d.style.left=`${u*f}px`,d.style.top=`${m*f}px`,d.style.width=`${p*f}px`,d.style.height=`${g*f}px`)},toRect(d,u,m,p){return e?new DOMRect(u,d,p,m):new DOMRect(d,u,m,p)}}}function Tn(i){return parseInt(i.getComputedStyle(i.document.documentElement).getPropertyValue("column-count"))}function Tp(i){let e=getComputedStyle(i),t=parseFloat(e.paddingTop||"0"),o=parseFloat(e.paddingBottom||"0");return i.clientHeight-t-o}function Fn(i){let e=Tn(i);if(!e)return!1;let t=i.document.querySelectorAll("div[id^='readium-virtual-page']");for(let c of t)c.remove();let o=t.length,r=i.document.scrollingElement.scrollWidth,n=i.visualViewport.width,a=Math.round(r/n*e)%e,s=e===1||a===0?0:e-a;if(s>0)for(let c=0;c<s;c++){let l=i.document.createElement("div");l.setAttribute("id",`readium-virtual-page-${c}`),l.dataset.readium="true",CSS.supports("break-before","column")?l.style.breakBefore="column":(CSS.supports("break-inside","avoid-column")&&(l.style.breakInside="avoid-column"),l.style.height=Tp(i.document.documentElement)+"px"),l.innerHTML="&#8203;",i.document.body.appendChild(l)}return o!==s}function ui(i){let e=i.document.createElement("style");e.appendChild(i.document.createTextNode("*{}")),i.document.body.appendChild(e),i.document.body.removeChild(e)}var Fp=new Set(["backgroundColor","textColor","linkColor","visitedColor","primaryColor","secondaryColor","selectionBackgroundColor","selectionTextColor","blendFilter","darkenFilter","invertFilter","invertGaiji"]),Tc=/--(?:USER|RS)__([\w-]+)/g;function Fc(i,e){let t=i??"",o=e??"",r=new Set;for(let n of t.matchAll(Tc))r.add(n[1]);for(let n of o.matchAll(Tc))r.add(n[1]);for(let n of r)if(!Fp.has(n))return!0;return!1}function Ki(i){let e={},t=i.document.documentElement.style;for(let o in i.document.documentElement.style)Object.hasOwn(t,o)&&!Number.isNaN(Number.parseInt(o))&&(e[t[o]]=t.getPropertyValue(t[o]));return e}function Ho(i,e){let t=Ki(i);Object.keys(t).forEach(o=>{e.hasOwnProperty(o)||It(i,o)}),Object.entries(e).forEach(([o,r])=>{t[o]!==r&&bt(i,o,r)})}function zn(i,e){return i.document.documentElement.style.getPropertyValue(e)}function bt(i,e,t){i.document.documentElement.style.setProperty(e,t)}function It(i,e){i.document.documentElement.style.removeProperty(e)}function zc(i){return i<.5?2*i*i:-1+(4-2*i)*i}function Lc(i){return i.split("").reverse().join("")}function zp(i,e,t){let o=Lc(e);return t.map(r=>{let n=Math.max(0,r.end-e.length-r.errors),a=Lc(i.slice(n,r.end));return{start:Ac(a,o,r.errors).reduce((s,c)=>r.end-c.end<s?r.end-c.end:s,r.end),end:r.end,errors:r.errors}})}function Ln(i){return(i|-i)>>31&1}function Oc(i,e,t,o){let r=i.P[t],n=i.M[t],a=o>>>31,s=e[t]|a,c=s|n,l=(s&r)+r^r|s,h=n|~(l|r),d=r&l,u=Ln(h&i.lastRowMask[t])-Ln(d&i.lastRowMask[t]);return h<<=1,d<<=1,d|=a,h|=Ln(o)-a,r=d|~(c|h),n=h&c,i.P[t]=r,i.M[t]=n,u}function Ac(i,e,t){if(e.length===0)return[];t=Math.min(t,e.length);let o=[],r=32,n=Math.ceil(e.length/r)-1,a={P:new Uint32Array(n+1),M:new Uint32Array(n+1),lastRowMask:new Uint32Array(n+1)};a.lastRowMask.fill(1<<31),a.lastRowMask[n]=1<<(e.length-1)%r;let s=new Uint32Array(n+1),c=new Map,l=[];for(let u=0;u<256;u++)l.push(s);for(let u=0;u<e.length;u+=1){let m=e.charCodeAt(u);if(c.has(m))continue;let p=new Uint32Array(n+1);c.set(m,p),m<l.length&&(l[m]=p);for(let g=0;g<=n;g+=1){p[g]=0;for(let f=0;f<r;f+=1){let y=g*r+f;y>=e.length||e.charCodeAt(y)===m&&(p[g]|=1<<f)}}}let h=Math.max(0,Math.ceil(t/r)-1),d=new Uint32Array(n+1);for(let u=0;u<=h;u+=1)d[u]=(u+1)*r;d[n]=e.length;for(let u=0;u<=h;u+=1)a.P[u]=-1,a.M[u]=0;for(let u=0;u<i.length;u+=1){let m=i.charCodeAt(u),p;m<l.length?p=l[m]:(p=c.get(m),typeof p>"u"&&(p=s));let g=0;for(let f=0;f<=h;f+=1)g=Oc(a,p,f,g),d[f]+=g;if(d[h]-g<=t&&h<n&&(p[h+1]&1||g<0)){h+=1,a.P[h]=-1,a.M[h]=0;let f;if(h===n){let y=e.length%r;f=y===0?r:y}else f=r;d[h]=d[h-1]+f-g+Oc(a,p,h,g)}else for(;h>0&&d[h]>=t+r;)h-=1;h===n&&d[h]<=t&&(d[h]<t&&o.splice(0,o.length),o.push({start:-1,end:u+1,errors:d[h]}),t=d[h])}return o}function Mc(i,e,t){let o=Ac(i,e,t);return zp(i,e,o)}function Ic(i,e,t){let o=0,r=[];for(;o!==-1;)o=i.indexOf(e,o),o!==-1&&(r.push({start:o,end:o+e.length,errors:0}),o+=1);return r.length>0?r:Mc(i,e,t)}function Nc(i,e){return e.length===0||i.length===0?0:1-Ic(i,e,e.length)[0].errors/e.length}function Uc(i,e,t={}){if(e.length===0)return null;let o=Math.min(256,e.length/2),r=Ic(i,e,o);if(r.length===0)return null;let n=s=>{let c=1-s.errors/e.length,l=t.prefix?Nc(i.slice(Math.max(0,s.start-t.prefix.length),s.start),t.prefix):1,h=t.suffix?Nc(i.slice(s.end,s.end+t.suffix.length),t.suffix):1,d=1;return typeof t.hint=="number"&&(d=1-Math.abs(s.start-t.hint)/i.length),(50*c+20*l+20*h+2*d)/92},a=r.map(s=>({start:s.start,end:s.end,score:n(s)}));return a.sort((s,c)=>c.score-s.score),a[0]}function On(i,e,t){let o=t===1?e:e-1;if(i.charAt(o).trim()!=="")return e;let r,n;if(t===2?(r=i.substring(0,e),n=r.trimEnd()):(r=i.substring(e),n=r.trimStart()),!n.length)return-1;let a=r.length-n.length;return t===2?e-a:e+a}function Dc(i,e){let t=i.commonAncestorContainer.ownerDocument.createNodeIterator(i.commonAncestorContainer,NodeFilter.SHOW_TEXT),o=e===1?i.startContainer:i.endContainer,r=e===1?i.endContainer:i.startContainer,n=t.nextNode();for(;n&&n!==o;)n=t.nextNode();e===2&&(n=t.previousNode());let a=-1,s=()=>{if(n=e===1?t.nextNode():t.previousNode(),n){let c=n.textContent,l=e===1?0:c.length;a=On(c,l,e)}};for(;n&&a===-1&&n!==r;)s();if(n&&a>=0)return{node:n,offset:a};throw new RangeError("No text nodes with non-whitespace text found in range")}function Hc(i){if(!i.toString().trim().length)throw new RangeError("Range contains no non-whitespace text");if(i.startContainer.nodeType!==Node.TEXT_NODE)throw new RangeError("Range startContainer is not a text node");if(i.endContainer.nodeType!==Node.TEXT_NODE)throw new RangeError("Range endContainer is not a text node");let e=i.cloneRange(),t=!1,o=!1,r={start:On(i.startContainer.textContent,i.startOffset,1),end:On(i.endContainer.textContent,i.endOffset,2)};if(r.start>=0&&(e.setStart(i.startContainer,r.start),t=!0),r.end>0&&(e.setEnd(i.endContainer,r.end),o=!0),t&&o)return e;if(!t){let{node:n,offset:a}=Dc(e,1);n&&a>=0&&e.setStart(n,a)}if(!o){let{node:n,offset:a}=Dc(e,2);n&&a>0&&e.setEnd(n,a)}return e}function Bc(i){switch(i.nodeType){case Node.ELEMENT_NODE:case Node.TEXT_NODE:return i.textContent?.length??0;default:return 0}}function Wc(i){let e=i.previousSibling,t=0;for(;e;)t+=Bc(e),e=e.previousSibling;return t}function jc(i,...e){let t=e.shift(),o=i.ownerDocument.createNodeIterator(i,NodeFilter.SHOW_TEXT),r=[],n=o.nextNode(),a,s=0;for(;t!==void 0&&n;)a=n,s+a.data.length>t?(r.push({node:a,offset:t-s}),t=e.shift()):(n=o.nextNode(),s+=a.data.length);for(;t!==void 0&&a&&s===t;)r.push({node:a,offset:a.data.length}),t=e.shift();if(t!==void 0)throw new RangeError("Offset exceeds text length");return r}var mi=class i{constructor(e,t){if(t<0)throw new Error("Offset is invalid");this.element=e,this.offset=t}relativeTo(e){if(!e.contains(this.element))throw new Error("Parent is not an ancestor of current element");let t=this.element,o=this.offset;for(;t!==e;)o+=Wc(t),t=t.parentElement;return new i(t,o)}resolve(e={}){try{return jc(this.element,this.offset)[0]}catch(t){if(this.offset===0&&e.direction!==void 0){let o=document.createTreeWalker(this.element.getRootNode(),NodeFilter.SHOW_TEXT);o.currentNode=this.element;let r=e.direction===1,n=r?o.nextNode():o.previousNode();if(!n)throw t;return{node:n,offset:r?0:n.data.length}}else throw t}}static fromCharOffset(e,t){switch(e.nodeType){case Node.TEXT_NODE:return i.fromPoint(e,t);case Node.ELEMENT_NODE:return new i(e,t);default:throw new Error("Node is not an element or text node")}}static fromPoint(e,t){switch(e.nodeType){case Node.TEXT_NODE:{if(t<0||t>e.data.length)throw new Error("Text node offset is out of range");if(!e.parentElement)throw new Error("Text node has no parent");let o=Wc(e)+t;return new i(e.parentElement,o)}case Node.ELEMENT_NODE:{if(t<0||t>e.childNodes.length)throw new Error("Child node offset is out of range");let o=0;for(let r=0;r<t;r++)o+=Bc(e.childNodes[r]);return new i(e,o)}default:throw new Error("Point is not in an element or text node")}}},pi=class i{constructor(e,t){this.start=e,this.end=t}relativeTo(e){return new i(this.start.relativeTo(e),this.end.relativeTo(e))}toRange(){let e,t;this.start.element===this.end.element&&this.start.offset<=this.end.offset?[e,t]=jc(this.start.element,this.start.offset,this.end.offset):(e=this.start.resolve({direction:1}),t=this.end.resolve({direction:2}));let o=new Range;return o.setStart(e.node,e.offset),o.setEnd(t.node,t.offset),o}static fromRange(e){let t=mi.fromPoint(e.startContainer,e.startOffset),o=mi.fromPoint(e.endContainer,e.endOffset);return new i(t,o)}static fromOffsets(e,t,o){return new i(new mi(e,t),new mi(e,o))}static trimmedRange(e){return Hc(i.fromRange(e).toRange())}};var An=class i{constructor(e,t,o){this.root=e,this.start=t,this.end=o}static fromRange(e,t){let o=pi.fromRange(t).relativeTo(e);return new i(e,o.start.offset,o.end.offset)}static fromSelector(e,t){return new i(e,t.start,t.end)}toSelector(){return{type:"TextPositionSelector",start:this.start,end:this.end}}toRange(){return pi.fromOffsets(this.root,this.start,this.end).toRange()}},Wo=class i{constructor(e,t,o={}){this.root=e,this.exact=t,this.context=o}static fromRange(e,t){let o=e.textContent,r=pi.fromRange(t).relativeTo(e),n=r.start.offset,a=r.end.offset,s=32;return new i(e,o.slice(n,a),{prefix:o.slice(Math.max(0,n-s),n),suffix:o.slice(a,Math.min(o.length,a+s))})}static fromSelector(e,t){let{prefix:o,suffix:r}=t;return new i(e,t.exact,{prefix:o,suffix:r})}toSelector(){return{type:"TextQuoteSelector",exact:this.exact,prefix:this.context.prefix,suffix:this.context.suffix}}toRange(e={}){return this.toPositionAnchor(e).toRange()}toPositionAnchor(e={}){let t=this.root.textContent,o=Uc(t,this.exact,{...this.context,hint:e.hint});if(!o)throw new Error("Quote not found");return new An(this.root,o.start,o.end)}};function Lp(i){let e=i.tagName.toUpperCase();return e==="IMG"||e==="VIDEO"||e==="AUDIO"||e==="IFRAME"||e==="OBJECT"||e==="EMBED"||e==="CANVAS"}function Qe(i,e){try{let t=e.locations,o=e.text;if(o&&o.highlight){let r;t&&ft(t)&&(r=i.querySelector(ft(t))),r||(r=i.body);let n=new Wo(r,o.highlight,{prefix:o.before,suffix:o.after});try{return n.toRange()}catch{return console.warn("Quote not found:",n),null}}if(t){let r=null;if(!r&&ft(t)&&(r=i.querySelector(ft(t))),!r&&t.fragments){for(let n of t.fragments)if(r=i.getElementById(n),r)break}if(r){let n=i.createRange();return r.childNodes.length===0||Lp(r)?(n.selectNode(r),n):(n.setStartBefore(r),n.setEndAfter(r),n)}}}catch(t){console.error(t)}return null}function D(i){let e=i.getSelection();e&&e.removeAllRanges()}var Op=["a","area","audio","button","canvas","details","input","label","option","select","submit","textarea","video"],Ap=["dialog","radiogroup","radio","menu","menuitem"];function Yi(i){return Mp(i)?null:Mn(i)?i:i.parentElement?Yi(i.parentElement):null}function Mp(i){return i?i.closest("[inert]")!==null||i.hasAttribute("disabled"):!0}function Mn(i){return i?i.role&&Ap.includes(i.role)?!0:i.tagName.toLowerCase()==="iframe"?!1:i.tabIndex>=0?!0:Op.includes(i.nodeName.toLowerCase())||i.hasAttribute("contenteditable")&&i.getAttribute("contenteditable")?.toLowerCase()!=="false":!1}function _t(i,e){let t=Gc(i,i.document.body,e),o=i._readium_cssSelectorGenerator.getCssSelector(t,{selectors:["tag","id","class","nthchild","nthoftype","attribute"]});return new H({href:"#",type:"application/xhtml+xml",locations:new ee({otherLocations:new Map([["cssSelector",o]])}),text:new me({highlight:t.textContent||void 0})})}function Gc(i,e,t){for(var o=0;o<e.children.length;o++){let r=e.children[o];if(!Up(r)&&Np(i,r,t))return Ip(i,r)?r:Gc(i,r,t)}return e}function Np(i,e,t){if(e===document.body||e===document.documentElement)return!0;if(!document||!document.documentElement||!document.body)return!1;let o=e.getBoundingClientRect();return t?o.bottom>0&&o.top<i.innerHeight:o.right>0&&o.left<i.innerWidth}function Ip(i,e){let t=e.getBoundingClientRect();return t.top>=0&&t.left>=0&&t.bottom<=i.innerHeight&&t.right<=i.innerWidth}function Up(i){let e=getComputedStyle(i);if(e){let t=e.getPropertyValue("display");if(t!="block"&&t!="list-item"||e.getPropertyValue("opacity")==="0")return!0}return!1}var Vc={maxVelocity:200,minVariance:.01,historySize:20,minDirectionChanges:.2,maxConsistentScrolls:15},gi={maxVelocity:200,minVariance:1e-5,historySize:100,minDirectionChanges:.1,maxConsistentScrolls:20},Bo={maxSelectionsPerSecond:500,minVariance:50,historySize:20},Kc={enabled:!0,maxSelectionPercent:.1,minThreshold:100,absoluteMaxChars:5e3,historySize:20};var et=class{constructor(e={}){this.history=[],this.consistentScrollCount=0,this.options={...Vc,...e}}analyze(e,t,o){if(o<=0)return!1;let r=Math.abs(t)/o,n=Date.now();if(this.history.push({timestamp:n,direction:e,velocity:r,distance:Math.abs(t)}),this.history=this.history.filter(p=>n-p.timestamp<2e3).slice(-(this.options.historySize||20)),this.history.length<3)return!1;if(r>this.options.maxVelocity)return this.resetAfterDetection(),!0;let a=this.history.map(p=>p.velocity),s=this.history.map(p=>p.distance),c=a.reduce((p,g)=>p+g,0)/a.length,l=s.reduce((p,g)=>p+g,0)/s.length,h=a.reduce((p,g)=>p+Math.pow(g-c,2),0)/a.length,d=s.reduce((p,g)=>p+Math.pow(g-l,2),0)/s.length;if(h<this.options.minVariance&&d<l*.1){if(this.consistentScrollCount++,this.consistentScrollCount>=(this.options.maxConsistentScrolls||10))return this.resetAfterDetection(),!0}else this.consistentScrollCount=Math.max(0,this.consistentScrollCount-1);let u=0,m=this.history[0].direction;for(let p=1;p<this.history.length;p++)this.history[p].direction!==m&&(u++,m=this.history[p].direction);return u/this.history.length>(this.options.minDirectionChanges||.3)?(this.resetAfterDetection(),!0):!1}resetAfterDetection(){this.history=this.history.slice(-3),this.consistentScrollCount=0}clear(){this.history=[],this.consistentScrollCount=0}};var Yc="readium-column-snapper-style",Dp=200,Xc=class ve extends he{constructor(){super(...arguments),this.isSnapProtectionEnabled=!1,this.patternAnalyzer=null,this.lastTurnTime=0,this.rtl=!1,this.shakeTimeout=0,this.snappingCancelled=!1,this.alreadyScrollLeft=0,this.overscroll=0,this.cachedScrollWidth=0,this.touchState=0,this.startingX=void 0,this.endingX=void 0,this.onTouchStarter=this.onTouchStart.bind(this),this.onTouchEnder=this.onTouchEnd.bind(this),this.onWidthChanger=this.onWidthChange.bind(this),this.onTouchMover=this.onTouchMove.bind(this)}doc(){return this.wnd.document.scrollingElement}scrollOffset(){let e=this.doc().scrollLeft;return e!==0?e:this.alreadyScrollLeft}snapOffset(e){let t=e+(this.rtl?-1:1);return t-t%this.wnd.innerWidth}snapNormOffset(e){let t=e+1;return t-t%this.wnd.innerWidth}normScroll(){let e=this.doc().scrollLeft||this.alreadyScrollLeft;return this.rtl?Math.abs(e):Math.max(0,this.wnd.scrollX>0?this.wnd.scrollX:e)}hasScrolledPast(e){let t=e.getBoundingClientRect();return this.rtl?t.left>=this.wnd.innerWidth:t.right<=0}currentTimelineFragment(){if(this.timelineEntries.size===0)return;let e=this.wnd.innerWidth,t;for(let o of this.sortedFragmentIds){let r=this.timelineEntries.get(o).getBoundingClientRect();if(this.rtl?r.right>0:r.left<e)t=o;else break}return t}sortedVisibleFragmentIds(){let e=this.wnd.innerWidth;return this.sortedFragmentIds.filter(t=>{let o=this.timelineEntries.get(t).getBoundingClientRect();return this.rtl?o.left<e&&o.right>0:o.right>0&&o.left<e})}reportProgress(e){let t=this.cachedScrollWidth,o=this.wnd.innerWidth,r=this.normScroll(),n=Math.max(1,t-o),a=Math.max(0,Math.min(1,r/n)),s=Math.max(0,Math.min(1,(r+o)/t));this.comms.send("progress",{start:a,end:s,fragmentId:e!==void 0?e:this.currentTimelineFragment(),visibleFragmentIds:this.sortedVisibleFragmentIds()})}shake(){if(this.overscroll!==0||this.shakeTimeout!==0)return;let e=this.doc(),t=this.normScroll()<5,o=this.rtl?t?"readium-bounce-r":"readium-bounce-l":"readium-bounce-r";e.classList.add(o);let r=this.scrollOffset();this.shakeTimeout=this.wnd.setTimeout(()=>{e.classList.remove(o),this.shakeTimeout=0,this.doc().scrollLeft=r},150)}takeOverSnap(){this.snappingCancelled=!0,this.clearTouches();let e=this.doc();this.overscroll=e.style.transform?.length>12?parseFloat(e.style.transform.slice(12).split("px")[0]):0}snapCurrentOffset(e=!1,t=!1){let o=this.doc(),r=Tn(this.wnd),n=this.cachedScrollWidth-this.wnd.innerWidth,a=Math.min(Math.max(0,this.normScroll()),n),s=this.dragOffset(),c=this.rtl?-s:s,l=this.wnd.innerWidth/3*(c>0?2:1),h=Math.min(n,Math.max(0,this.snapNormOffset(a+l))),d=this.rtl?-h:h,u=this.rtl?-a:a,m=d>u?"right":"left";if(this.checkSuspiciousSnap(m,Math.abs(d-u)),e&&d!==u){this.snappingCancelled=!1;let p=(w,x,k,z)=>k>z?x:w+(x-w)*zc(k/z),g=Dp*r,f,y=w=>{if(this.snappingCancelled)return;f||(f=w);let x=w-f,k=p(this.overscroll,0,x,g),z=p(u,d,x,g);o.scrollLeft=z,this.overscroll!==0&&(o.style.transform=`translate3d(${-k}px, 0px, 0px)`),x<g?this.wnd.requestAnimationFrame(y):(this.clearTouches(),o.style.removeProperty("transform"),o.scrollLeft=d,t||this.reportProgress())};this.wnd.requestAnimationFrame(y)}else o.style.removeProperty("transform"),this.wnd.requestAnimationFrame(()=>{o.scrollLeft=d,this.clearTouches(),t||this.reportProgress()})}dragOffset(){return(this.startingX??0)-(this.endingX??0)}clearTouches(){this.startingX=void 0,this.endingX=void 0,this.overscroll=0}onTouchStart(e){switch(e.stopPropagation(),this.takeOverSnap(),e.touches.length){case 1:break;case 2:this.onTouchEnd(e);return;default:{this.onTouchEnd(e),this.comms.send("tap_more",e.touches.length);return}}this.startingX=e.touches[0].clientX,this.alreadyScrollLeft=this.doc().scrollLeft,this.touchState=1}onTouchEnd(e){if(this.touchState===2){let t=this.dragOffset(),o=this.normScroll(),r=this.cachedScrollWidth-this.wnd.innerWidth,n=this.rtl?-t:t;this.cachedScrollWidth<=this.wnd.innerWidth?(this.reportProgress(),n>5&&this.comms.send("no_more",void 0),n<-5&&this.comms.send("no_less",void 0)):o<5&&n<5?(this.alreadyScrollLeft=0,this.comms.send("no_less",void 0)):r-o<5&&n>5&&(this.alreadyScrollLeft=this.rtl?-r:r,this.comms.send("no_more",void 0)),this.snapCurrentOffset(!0),this.comms.send("swipe",t)}this.touchState=0}onWidthChange(){this.cachedScrollWidth=this.doc().scrollWidth,this.comms.ready&&this.snapCurrentOffset()}onTouchMove(e){if(this.touchState===0)return;this.touchState===1&&(this.touchState=2,D(this.wnd)),this.endingX=e.touches[0].clientX;let t=this.dragOffset(),o=this.alreadyScrollLeft+t,r=this.rtl?-(this.cachedScrollWidth-this.wnd.innerWidth):0,n=this.rtl?0:this.cachedScrollWidth-this.wnd.innerWidth;o<r?(this.overscroll=o,this.doc().style.transform=`translate3d(${-this.overscroll}px, 0px, 0px)`):o>n?(this.overscroll=o,this.doc().style.transform=`translate3d(${-o}px, 0px, 0px)`):(this.overscroll=0,this.doc().style.removeProperty("transform"),this.doc().scrollLeft=o)}enableSnapProtection(){this.patternAnalyzer||(this.patternAnalyzer=new et({maxVelocity:this.wnd.innerWidth,minVariance:.1,historySize:5,maxConsistentScrolls:3,minDirectionChanges:.3}),this.isSnapProtectionEnabled=!0,this.comms?.log("Snap protection enabled"))}checkSuspiciousSnap(e,t){if(!this.isSnapProtectionEnabled||!this.patternAnalyzer)return;let o=Date.now(),r=o-(this.lastTurnTime||o);this.lastTurnTime=o,this.patternAnalyzer.analyze(e,t,r)&&this.comms?.send("content_protection",{type:"suspicious_snapping",timestamp:Date.now(),event:null})}mount(e,t){if(this.wnd=e,this.comms=t,this.rtl=Pc(e),!super.mount(e,t))return!1;e.navigator.epubReadingSystem&&(e.navigator.epubReadingSystem.layoutStyle="paginated");let o=e.document.createElement("style");o.dataset.readium="true",o.id=Yc,o.textContent=`
        @keyframes readium-bounce-l-animation {
            0%, 100% {transform: translate3d(0, 0, 0);}
            50% {transform: translate3d(-50px, 0, 0);}
        }

        @keyframes readium-bounce-r-animation {
            0%, 100% {transform: translate3d(0, 0, 0);}
            50% {transform: translate3d(50px, 0, 0);}
        }

        .readium-bounce-l {
            animation: readium-bounce-l-animation 150ms ease-out 1;
        }

        .readium-bounce-r {
            animation: readium-bounce-r-animation 150ms ease-out 1;
        }

        html {
            overflow: hidden;
        }

        body {
            -ms-overflow-style: none; /* for Internet Explorer, Edge */
        }

        * {
            scrollbar-width: none; /* for Firefox */
        }

        body::-webkit-scrollbar {
            display: none; /* for Chrome, Safari, and Opera */
        }
        `,e.document.head.appendChild(o),this.resizeObserver=new ResizeObserver(()=>{e.requestAnimationFrame(()=>{e&&Fn(e)}),this.onWidthChange()}),this.resizeObserver.observe(e.document.body),this.mutationObserver=new MutationObserver(a=>{for(let s of a)if(s.target===this.wnd.document.documentElement){let c=s.oldValue,l=s.target.getAttribute("style"),h=/transform\s*:\s*([^;]+)/,d=c?.match(h),u=l?.match(h);(!d&&!u&&Fc(c,l)||d&&!u||d&&u&&d[1]!==u[1])&&(e.requestAnimationFrame(()=>{e&&Fn(e)}),this.onWidthChange())}else e.requestAnimationFrame(()=>this.cachedScrollWidth=this.doc().scrollWidth)}),e.frameElement&&this.mutationObserver.observe(e.frameElement,{attributes:!0,attributeFilter:["style"]}),this.mutationObserver.observe(e.document,{attributes:!0,attributeFilter:["style"]}),this.mutationObserver.observe(e.document.documentElement,{attributes:!0,attributeFilter:["style"]}),t.register("scroll_protection",ve.moduleName,(a,s)=>{this.enableSnapProtection(),s(!0)});let r=a=>{let s=this.doc().scrollLeft;return this.doc().scrollLeft=this.snapOffset(a),s!==this.doc().scrollLeft},n=a=>{let s=this.doc().scrollLeft,c=this.snapNormOffset(Math.max(0,Math.min(this.cachedScrollWidth-e.innerWidth,a)));return this.doc().scrollLeft=-c,s!==this.doc().scrollLeft};return e.addEventListener("orientationchange",this.onWidthChanger),e.addEventListener("resize",this.onWidthChanger),e.requestAnimationFrame(()=>this.cachedScrollWidth=this.doc().scrollWidth),t.register("go_progression",ve.moduleName,(a,s)=>{let c=a;if(c<0||c>1){t.send("error",{message:"go_progression must be given a position from 0.0 to 1.0"}),s(!1);return}this.wnd.requestAnimationFrame(()=>{this.cachedScrollWidth=this.doc().scrollWidth;let l=(this.cachedScrollWidth-e.innerWidth)*c;this.rtl?this.doc().scrollLeft=-this.snapNormOffset(l):this.doc().scrollLeft=this.snapOffset(l),this.reportProgress(),D(this.wnd),s(!0)})}),t.register("go_id",ve.moduleName,(a,s)=>{let c=e.document.getElementById(a);if(!c){s(!1);return}this.wnd.requestAnimationFrame(()=>{this.rtl?this.doc().scrollLeft=-this.snapNormOffset(c.getBoundingClientRect().left+e.scrollX):this.doc().scrollLeft=this.snapOffset(c.getBoundingClientRect().left+e.scrollX);let l=a;this.reportProgress(this.timelineEntries.has(l)?l:this.nearestPrecedingTimelineEntry(c)),D(this.wnd),s(!0)})}),t.register("go_text",ve.moduleName,(a,s)=>{let c;Array.isArray(a)&&(a.length>1&&(c=a[1]),a=a[0]);let l=me.deserialize(a),h=Qe(this.wnd.document,new H({href:e.location.href,type:"text/html",text:l,locations:c?new ee({otherLocations:new Map([["cssSelector",c]])}):void 0}));if(!h){s(!1);return}this.wnd.requestAnimationFrame(()=>{this.rtl?this.doc().scrollLeft=-this.snapNormOffset(h.getBoundingClientRect().left+e.scrollX):this.doc().scrollLeft=this.snapOffset(h.getBoundingClientRect().left+e.scrollX),this.reportProgress(this.nearestPrecedingTimelineEntry(h.startContainer)),D(this.wnd),s(!0)})}),t.register("go_end",ve.moduleName,(a,s)=>{this.wnd.requestAnimationFrame(()=>{this.cachedScrollWidth=this.doc().scrollWidth;let c;if(this.rtl?c=-this.snapNormOffset(this.cachedScrollWidth-e.innerWidth):c=this.snapOffset(this.cachedScrollWidth),this.doc().scrollLeft===c)return s(!1);this.doc().scrollLeft=c,this.reportProgress(this.sortedFragmentIds[this.sortedFragmentIds.length-1]),D(this.wnd),s(!0)})}),t.register("go_start",ve.moduleName,(a,s)=>{this.wnd.requestAnimationFrame(()=>{if(this.doc().scrollLeft===0)return s(!1);this.doc().scrollLeft=0,this.reportProgress(this.firstFragmentIfReached()),D(this.wnd),s(!0)})}),t.register("go_prev",ve.moduleName,(a,s)=>{this.wnd.requestAnimationFrame(()=>{this.cachedScrollWidth=this.doc().scrollWidth;let c;this.rtl?c=n(this.normScroll()-e.innerWidth):c=r(e.scrollX-e.innerWidth),this.reportProgress(),c&&(D(this.wnd),this.checkSuspiciousSnap("left",this.wnd.innerWidth)),s(c)})}),t.register("go_next",ve.moduleName,(a,s)=>{this.wnd.requestAnimationFrame(()=>{this.cachedScrollWidth=this.doc().scrollWidth;let c;this.rtl?c=n(this.normScroll()+e.innerWidth):c=r(e.scrollX+e.innerWidth),this.reportProgress(),c&&(D(this.wnd),this.checkSuspiciousSnap("right",this.wnd.innerWidth)),s(c)})}),t.register("unfocus",ve.moduleName,(a,s)=>{this.snappingCancelled=!0,D(this.wnd),s(!0)}),t.register("shake",ve.moduleName,(a,s)=>{this.shake(),s(!0)}),t.register("focus",ve.moduleName,(a,s)=>{this.wnd.requestAnimationFrame(()=>{this.cachedScrollWidth=this.doc().scrollWidth,this.snapCurrentOffset(!1,!0),this.reportProgress(),s(!0)})}),t.register("first_visible_locator",ve.moduleName,(a,s)=>{let c=_t(e,!1);this.comms.send("first_visible_locator",c.serialize()),s(!0)}),t.register("timeline_entries",ve.moduleName,(a,s)=>{this.updateTimelineEntries(Array.isArray(a)?a:[],e),e.requestAnimationFrame(()=>this.reportProgress()),s(!0)}),e.addEventListener("touchstart",this.onTouchStarter,{passive:!0}),e.addEventListener("touchend",this.onTouchEnder,{passive:!0}),e.addEventListener("touchmove",this.onTouchMover,{passive:!0}),e.document.addEventListener("touchstart",()=>{}),t.log("ColumnSnapper Mounted"),!0}unmount(e,t){return this.snappingCancelled=!0,t.unregisterAll(ve.moduleName),this.resizeObserver.disconnect(),this.mutationObserver.disconnect(),this.patternAnalyzer&&(this.patternAnalyzer.clear(),this.patternAnalyzer=null,this.isSnapProtectionEnabled=!1),e.removeEventListener("touchstart",this.onTouchStarter),e.removeEventListener("touchend",this.onTouchEnder),e.removeEventListener("touchmove",this.onTouchMover),e.removeEventListener("orientationchange",this.onWidthChanger),e.removeEventListener("resize",this.onWidthChanger),e.document.getElementById(Yc)?.remove(),this.timelineEntries.clear(),t.log("ColumnSnapper Unmounted"),super.unmount(e,t)}};Xc.moduleName="column_snapper";var $c=Xc;var qc="readium-scroll-snapper-style",Jc=class Pe extends he{constructor(){super(...arguments),this.patternAnalyzer=null,this.lastScrollTime=0,this.isScrollProtectionEnabled=!1,this.initialScrollHandled=!1,this.isScrolling=!1,this.lastScrollTop=0,this.isResizing=!1,this.resizeDebounce=null,this.handleScroll=e=>{if(this.comms.ready&&!this.isResizing){if(!this.initialScrollHandled){this.lastScrollTop=this.doc().scrollTop,this.initialScrollHandled=!0,this.reportProgress();return}this.isScrolling||(this.isScrolling=!0,this.wnd.requestAnimationFrame(()=>{this.reportProgress();let t=this.doc().scrollTop,o=t-this.lastScrollTop;if(this.lastScrollTop=t,this.isScrollProtectionEnabled&&Math.abs(o)>5){let r=Date.now(),n=r-(this.lastScrollTime||r);if(this.patternAnalyzer&&this.patternAnalyzer.analyze(o>0?"down":"up",Math.abs(o),n)){let a=e.target&&"tagName"in e.target?{tagName:e.target.tagName}:null;this.comms?.send("content_protection",{type:"suspicious_scrolling",timestamp:Date.now(),scrollDelta:o,scrollDirection:o>0?"down":"up",targetElement:a})}this.lastScrollTime=r}this.comms.send("scroll",o),this.isScrolling=!1}))}}}doc(){return this.wnd.document.scrollingElement}hasScrolledPast(e){let t=this.wnd.innerHeight/2;return e.getBoundingClientRect().top<=t+this.wnd.innerHeight*he.CENTER_TOLERANCE}inCenterBand(e){let t=e.getBoundingClientRect(),o=this.wnd.innerHeight/2,r=this.wnd.innerHeight*he.CENTER_TOLERANCE,n=o-r,a=o+r;return t.height===0?t.top<=a&&t.top>=n:Math.max(0,Math.min(t.bottom,a)-Math.max(t.top,n))/t.height>=he.CENTER_TOLERANCE}fragmentStart(e){return e.getBoundingClientRect().top+this.doc().scrollTop}currentScrollExtent(){return{pos:this.doc().scrollTop,size:this.wnd.innerHeight}}reportProgress(e){if(!this.comms.ready)return;let t=Math.ceil(this.doc().scrollTop),o=this.doc().scrollHeight,r=this.wnd.innerHeight,n=Math.max(0,Math.min(1,t/o)),a=Math.max(0,Math.min(1,(t+r)/o));this.comms.send("progress",{start:n,end:a,fragmentId:e!==void 0?e:this.currentTimelineFragment(),visibleFragmentIds:this.sortedVisibleFragmentIds()})}enableScrollProtection(){this.patternAnalyzer||(this.patternAnalyzer=new et(gi),this.isScrollProtectionEnabled=!0,this.comms?.log("Scroll protection enabled"))}mount(e,t){this.wnd=e,this.comms=t,this.initialScrollHandled=!1,this.lastScrollTop=0,this.isResizing=!1,this.resizeDebounce&&(this.wnd.clearTimeout(this.resizeDebounce),this.resizeDebounce=null),e.navigator.epubReadingSystem&&(e.navigator.epubReadingSystem.layoutStyle="scrolling");let o=e.document.createElement("style");return o.dataset.readium="true",o.id=qc,o.textContent=`
        * {
            scrollbar-width: none; /* for Firefox */
        }

        body::-webkit-scrollbar {
            display: none; /* for Chrome, Safari, and Opera */
        }
        `,e.document.head.appendChild(o),this.resizeObserver=new ResizeObserver(()=>{this.resizeDebounce&&this.wnd.clearTimeout(this.resizeDebounce),this.isResizing=!0,this.resizeDebounce=this.wnd.setTimeout(()=>{this.isResizing=!1,this.resizeDebounce=null,this.refreshFragmentStarts(),this.reportProgress()},50)}),this.resizeObserver.observe(e.document.body),e.addEventListener("scroll",this.handleScroll,{passive:!0}),t.register("force_webkit_recalc",Pe.moduleName,()=>{ui(this.wnd);let r=this.doc().scrollTop;r>1?this.doc().scrollTop=r-1:this.doc().scrollTop=r+1,this.doc().scrollTop=r}),t.register("go_progression",Pe.moduleName,(r,n)=>{let a=r;if(a<0||a>1){t.send("error",{message:"go_progression must be given a position from 0.0 to 1.0"}),n(!1);return}this.wnd.requestAnimationFrame(()=>{this.doc().scrollTop=this.doc().offsetHeight*a,this.reportProgress(this.fragmentFromGeometry()),D(this.wnd),n(!0)})}),t.register("go_id",Pe.moduleName,(r,n)=>{let a=e.document.getElementById(r);if(!a){n(!1);return}this.wnd.requestAnimationFrame(()=>{this.doc().scrollTop=a.getBoundingClientRect().top+e.scrollY-e.innerHeight/2;let s=r;this.reportProgress(this.timelineEntries.has(s)?s:this.nearestPrecedingTimelineEntry(a)),D(this.wnd),n(!0)})}),t.register("go_text",Pe.moduleName,(r,n)=>{let a;Array.isArray(r)&&(r.length>1&&(a=r[1]),r=r[0]);let s=me.deserialize(r),c=Qe(this.wnd.document,new H({href:e.location.href,type:"text/html",text:s,locations:a?new ee({otherLocations:new Map([["cssSelector",a]])}):void 0}));if(!c){n(!1);return}this.wnd.requestAnimationFrame(()=>{this.doc().scrollTop=c.getBoundingClientRect().top+e.scrollY-e.innerHeight/2,this.reportProgress(this.nearestPrecedingTimelineEntry(c.startContainer)),D(this.wnd),n(!0)})}),t.register("go_start",Pe.moduleName,(r,n)=>{if(this.doc().scrollTop===0)return n(!1);this.doc().scrollTop=0,this.reportProgress(this.firstFragmentIfReached()),n(!0)}),t.register("go_end",Pe.moduleName,(r,n)=>{if(this.doc().scrollTop===this.doc().scrollHeight-this.doc().offsetHeight)return n(!1);this.doc().scrollTop=this.doc().scrollHeight-this.doc().offsetHeight,this.reportProgress(this.sortedFragmentIds[this.sortedFragmentIds.length-1]),n(!0)}),t.register("unfocus",Pe.moduleName,(r,n)=>{D(this.wnd),n(!0)}),t.register("scroll_protection",Pe.moduleName,(r,n)=>{this.enableScrollProtection(),n(!0)}),t.register(["go_next","go_prev"],Pe.moduleName,(r,n)=>n(!1)),t.register("focus",Pe.moduleName,(r,n)=>{this.reportProgress(),n(!0)}),t.register("first_visible_locator",Pe.moduleName,(r,n)=>{let a=_t(e,!0);this.comms.send("first_visible_locator",a.serialize()),n(!0)}),t.register("timeline_entries",Pe.moduleName,(r,n)=>{this.updateTimelineEntries(Array.isArray(r)?r:[],e),n(!0)}),t.log("ScrollSnapper Mounted"),!0}unmount(e,t){return t.unregisterAll(Pe.moduleName),this.resizeObserver.disconnect(),this.handleScroll&&e.removeEventListener("scroll",this.handleScroll),e.document.getElementById(qc)?.remove(),this.timelineEntries.clear(),this.cachedFragmentIds=[],this.sortedFragmentIds=[],this.cachedFragmentStarts.clear(),this.patternAnalyzer&&(this.patternAnalyzer.clear(),this.patternAnalyzer=null,this.isScrollProtectionEnabled=!1),t.log("ScrollSnapper Unmounted"),!0}};Jc.moduleName="scroll_snapper";var Zc=Jc;var Qc=class Te extends he{constructor(){super(...arguments),this.patternAnalyzer=null,this.lastScrollTime=0,this.isScrollProtectionEnabled=!1,this.initialScrollHandled=!1,this.isScrolling=!1,this.lastScrollTop=0,this.isResizing=!1,this.resizeDebounce=null,this.handleScroll=e=>{if(this.comms.ready&&!this.isResizing){if(!this.initialScrollHandled){this.lastScrollTop=this.doc().scrollTop,this.initialScrollHandled=!0,this.reportProgress();return}this.isScrolling||(this.isScrolling=!0,this.wnd.requestAnimationFrame(()=>{this.reportProgress();let t=this.doc().scrollTop,o=t-this.lastScrollTop;if(this.lastScrollTop=t,this.isScrollProtectionEnabled&&Math.abs(o)>5){let r=Date.now(),n=r-(this.lastScrollTime||r);if(this.patternAnalyzer&&this.patternAnalyzer.analyze(o>0?"down":"up",Math.abs(o),n)){let a=e.target&&"tagName"in e.target?{tagName:e.target.tagName}:null;this.comms?.send("content_protection",{type:"suspicious_scrolling",timestamp:Date.now(),scrollDelta:o,scrollDirection:o>0?"down":"up",targetElement:a})}this.lastScrollTime=r}this.comms.send("scroll",o),this.isScrolling=!1}))}}}doc(){return this.wnd.document.scrollingElement}hasScrolledPast(e){let t=this.wnd.innerHeight/2;return e.getBoundingClientRect().top<=t+this.wnd.innerHeight*he.CENTER_TOLERANCE}inCenterBand(e){let t=e.getBoundingClientRect(),o=this.wnd.innerHeight/2,r=this.wnd.innerHeight*he.CENTER_TOLERANCE,n=o-r,a=o+r;return t.height===0?t.top<=a&&t.top>=n:Math.max(0,Math.min(t.bottom,a)-Math.max(t.top,n))/t.height>=he.CENTER_TOLERANCE}fragmentStart(e){return e.getBoundingClientRect().top+this.doc().scrollTop}currentScrollExtent(){return{pos:this.doc().scrollTop,size:this.wnd.innerHeight}}reportProgress(e){if(!this.comms.ready)return;let t=Math.ceil(this.doc().scrollTop),o=this.doc().scrollHeight,r=this.wnd.innerHeight,n=Math.max(0,Math.min(1,t/o)),a=Math.max(0,Math.min(1,(t+r)/o));this.comms.send("progress",{start:n,end:a,fragmentId:e!==void 0?e:this.currentTimelineFragment(),visibleFragmentIds:this.sortedVisibleFragmentIds()})}enableScrollProtection(){this.patternAnalyzer||(this.patternAnalyzer=new et(gi),this.isScrollProtectionEnabled=!0,this.comms?.log("Scroll protection enabled"))}mount(e,t){return this.wnd=e,this.comms=t,this.initialScrollHandled=!1,this.lastScrollTop=0,this.isResizing=!1,this.resizeDebounce&&(this.wnd.clearTimeout(this.resizeDebounce),this.resizeDebounce=null),this.resizeObserver=new ResizeObserver(()=>{this.resizeDebounce&&this.wnd.clearTimeout(this.resizeDebounce),this.isResizing=!0,this.resizeDebounce=this.wnd.setTimeout(()=>{this.isResizing=!1,this.resizeDebounce=null,this.refreshFragmentStarts(),this.reportProgress()},50)}),this.resizeObserver.observe(e.document.body),e.addEventListener("scroll",this.handleScroll,{passive:!0}),t.register("force_webkit_recalc",Te.moduleName,()=>{ui(this.wnd);let o=this.doc().scrollTop;o>1?this.doc().scrollTop=o-1:this.doc().scrollTop=o+1,this.doc().scrollTop=o}),t.register("go_progression",Te.moduleName,(o,r)=>{let n=o;if(n<0||n>1){t.send("error",{message:"go_progression must be given a position from 0.0 to 1.0"}),r(!1);return}this.wnd.requestAnimationFrame(()=>{this.doc().scrollTop=this.doc().offsetHeight*n,this.reportProgress(this.fragmentFromGeometry()),D(this.wnd),r(!0)})}),t.register("go_id",Te.moduleName,(o,r)=>{let n=e.document.getElementById(o);if(!n){r(!1);return}this.wnd.requestAnimationFrame(()=>{this.doc().scrollTop=n.getBoundingClientRect().top+e.scrollY-e.innerHeight/2;let a=o;this.reportProgress(this.timelineEntries.has(a)?a:this.nearestPrecedingTimelineEntry(n)),D(this.wnd),r(!0)})}),t.register("go_text",Te.moduleName,(o,r)=>{let n;Array.isArray(o)&&(o.length>1&&(n=o[1]),o=o[0]);let a=me.deserialize(o),s=Qe(this.wnd.document,new H({href:e.location.href,type:"text/html",text:a,locations:n?new ee({otherLocations:new Map([["cssSelector",n]])}):void 0}));if(!s){r(!1);return}this.wnd.requestAnimationFrame(()=>{this.doc().scrollTop=s.getBoundingClientRect().top+e.scrollY-e.innerHeight/2,this.reportProgress(this.nearestPrecedingTimelineEntry(s.startContainer)),D(this.wnd),r(!0)})}),t.register("go_start",Te.moduleName,(o,r)=>{if(this.doc().scrollTop===0)return r(!1);this.doc().scrollTop=0,this.reportProgress(this.firstFragmentIfReached()),r(!0)}),t.register("go_end",Te.moduleName,(o,r)=>{if(this.doc().scrollTop===this.doc().scrollHeight-this.doc().offsetHeight)return r(!1);this.doc().scrollTop=this.doc().scrollHeight-this.doc().offsetHeight,this.reportProgress(this.sortedFragmentIds[this.sortedFragmentIds.length-1]),r(!0)}),t.register("unfocus",Te.moduleName,(o,r)=>{D(this.wnd),r(!0)}),t.register("scroll_protection",Te.moduleName,(o,r)=>{this.enableScrollProtection(),r(!0)}),t.register(["go_next","go_prev"],Te.moduleName,(o,r)=>r(!1)),t.register("focus",Te.moduleName,(o,r)=>{this.reportProgress(),r(!0)}),t.register("first_visible_locator",Te.moduleName,(o,r)=>{let n=_t(e,!0);t.send("first_visible_locator",n.serialize()),r(!0)}),t.register("timeline_entries",Te.moduleName,(o,r)=>{this.updateTimelineEntries(Array.isArray(o)?o:[],e),r(!0)}),t.log("WebPubSnapper Mounted"),!0}unmount(e,t){return t.unregisterAll(Te.moduleName),this.resizeObserver.disconnect(),this.handleScroll&&e.removeEventListener("scroll",this.handleScroll),this.patternAnalyzer&&(this.patternAnalyzer.clear(),this.patternAnalyzer=null,this.isScrollProtectionEnabled=!1),this.timelineEntries.clear(),this.cachedFragmentIds=[],this.sortedFragmentIds=[],this.cachedFragmentStarts.clear(),t.log("WebPubSnapper Unmounted"),!0}};Qc.moduleName="webpub_snapper";var eh=Qc;var jo=class{constructor(e,t){this.window=e,this.copyHistory=[],this.lastSelectionLength=0,this.lastSelectionTime=0,this.options=t}cleanupOldHistory(e){this.copyHistory=this.copyHistory.filter(t=>e-t.timestamp<1e4),this.copyHistory.length>this.options.historySize&&(this.copyHistory=this.copyHistory.slice(-this.options.historySize))}isSuspiciousPattern(e){return this.copyHistory.length<3?!1:this.copyHistory.filter(t=>e-t.timestamp<2e3).length>=3?!0:this.copyHistory.slice().sort((t,o)=>t.timestamp-o.timestamp).every((t,o,r)=>o===0?!0:t.length>r[o-1].length*1.5)}shouldAllowCopy(e){if(!this.options.enabled)return!0;let t=this.window.getSelection();if(!t)return!0;let o=t.toString().length,r=this.window.document.body.innerText.length,n=Date.now();if(this.cleanupOldHistory(n),o<this.options.minThreshold)return this.copyHistory.push({timestamp:n,length:o,wasBlocked:!1}),!0;let a=n-this.lastSelectionTime<100&&o>this.lastSelectionLength*1.5,s=Math.min(r*this.options.maxSelectionPercent,this.options.absoluteMaxChars),c=this.isSuspiciousPattern(n),l=o>s||a||c;return this.copyHistory.push({timestamp:n,length:o,wasBlocked:l}),l?(e?.preventDefault(),!1):(this.lastSelectionLength=o,this.lastSelectionTime=n,!0)}destroy(){this.lastSelectionLength=0,this.lastSelectionTime=0,this.copyHistory=[],this.options.enabled=!1}};var Go=class{constructor(e=Bo){this.options=e,this.events=[],this.selectionStartTime=0,this.lastSelectionTime=0,this.lastSelectionPosition=0,this.selectionPatterns=[],this.lastSelectedText=""}analyze(e){if(!e)return this.clear(),!1;let t=e.toString();if(t.length===0)return this.clear(),!1;if(e.type!=="Range"||!e.rangeCount)return!1;let o=Date.now();if(t.length<=50||t===this.lastSelectedText)return!1;if(this.selectionStartTime===0)return this.selectionStartTime=o,this.lastSelectedText=t,!1;if(o-this.selectionStartTime<500)return!1;o-this.lastSelectionTime>1e3&&(this.lastSelectionTime=o),this.selectionStartTime===0&&(this.selectionStartTime=o),this.lastSelectedText=t;let r=this.analyzeSelectionPattern(e,o);return this.cleanup(o),r}analyzeSelectionPattern(e,t){if(!e.rangeCount)return!1;let o=e.getRangeAt(0),r=o.toString(),n=(t-this.selectionStartTime)/1e3;if(r.length/Math.max(1,n)>this.options.maxSelectionsPerSecond)return!0;let a=o.startOffset,s=Math.abs(a-this.lastSelectionPosition);return this.selectionPatterns.push(s),this.selectionPatterns.length>this.options.historySize&&(this.selectionPatterns.shift(),this.calculateVariance(this.selectionPatterns)<this.options.minVariance)?!0:(this.lastSelectionPosition=a,!1)}calculateVariance(e){if(e.length===0)return 0;let t=e.reduce((o,r)=>o+r,0)/e.length;return e.reduce((o,r)=>o+Math.pow(r-t,2),0)/e.length}cleanup(e){this.events=this.events.filter(t=>e-t.timestamp<=1e3)}clear(){this.events=[],this.selectionStartTime=0,this.lastSelectionTime=0,this.lastSelectionPosition=0,this.selectionPatterns=[],this.lastSelectedText=""}};var Ut=class{match(e,t){for(let o of t)if(this.matchesCombo(e,o))return!0;return!1}matchesCombo(e,t){return e.keyCode===t.keyCode&&this.matchesModifier(e.ctrlKey,t.ctrl)&&this.matchesModifier(e.shiftKey,t.shift)&&this.matchesModifier(e.altKey,t.alt)&&this.matchesModifier(e.metaKey,t.meta)}matchesModifier(e,t){return t===void 0?!e:e===t}createKeyHandler(e,t){return o=>{this.match(o,e)&&(o.preventDefault(),o.stopPropagation(),t(o))}}createActivityEvent(e,t,o,r){let n,a;if(r){let s=r.getSelection(),c=s?.toString()||"",l=(c&&s?.rangeCount?s.getRangeAt(0)?.getClientRects():null)?.[0];l&&c&&(n={text:c,x:l.x,y:l.y,width:l.width,height:l.height});let h=r.document.activeElement;h&&h!==r.document.body&&(a=Yi(h)?.outerHTML)}return{type:t,timestamp:Date.now(),key:e.key,code:e.code,keyCode:e.keyCode,ctrlKey:e.ctrlKey,altKey:e.altKey,shiftKey:e.shiftKey,metaKey:e.metaKey,targetFrameSrc:o,selectedText:n,interactiveElement:a}}createKeyboardHandlers(e,t,o,r){let n=[];return t.forEach(a=>{n.push(...a.keyCombos.map(s=>({...s,handler:c=>{let l=a.type,h=this.createActivityEvent(c,l,e,r);o(h)}})))}),n}createUnifiedHandler(e,t,o,r){let n=this.createKeyboardHandlers(e,t,o,r);return a=>{if(a.isTrusted){for(let s of n)if(this.match(a,[s])){let c=s.suppressOnInteractiveElement;if(c){let l=(r?.document??document).activeElement;if(Array.isArray(c)?c.some(h=>l?.matches(h)):Mn(l))return}a.preventDefault(),a.stopPropagation(),s.handler(a);return}}}}};var th=class Xi extends Ee{constructor(){super(...arguments),this.configApplied=!1,this.cleanupCallbacks=[],this.pointerMoved=!1,this.isContextMenuEnabled=!1,this.isDragAndDropEnabled=!1,this.isSelectionMonitoringEnabled=!1,this.isBulkCopyProtectionEnabled=!1,this.selectionAnalyzer=null,this.currentSelection=null,this.bulkCopyProtector=null,this.keyManager=new Ut,this.keyDownHandler=null,this.preventBulkCopy=e=>{if(!this.isBulkCopyProtectionEnabled||!this.bulkCopyProtector)return!0;if(!this.bulkCopyProtector.shouldAllowCopy(e)){e.preventDefault();let t=this.wnd.getSelection(),o=t?.toString()||"",r=(o?t?.getRangeAt(0)?.getClientRects():null)?.[0],n={type:"bulk_copy",timestamp:Date.now(),clipboardTypes:e.clipboardData?.types?[...e.clipboardData.types]:[],selectedText:r?{text:o,x:r.x,y:r.y,width:r.width,height:r.height}:void 0,selectionLength:o.length,targetFrameSrc:this.wnd.location.href};return this.comms?.send("content_protection",n),!1}return!0},this.handleSelection=e=>{if(!this.isSelectionMonitoringEnabled||!this.wnd||!this.selectionAnalyzer)return;let t=this.wnd.getSelection();if(t){if(this.currentSelection=t.toString(),this.selectionAnalyzer.analyze(t)&&this.currentSelection){let o=this.wnd.getSelection(),r=o?.toString()||"",n=(r&&o?.rangeCount?o.getRangeAt(0)?.getClientRects():null)?.[0],a={type:"suspicious_selection",timestamp:Date.now(),selectionLength:r.length,selectedText:{text:r,x:n?.x??0,y:n?.y??0,width:n?.width??0,height:n?.height??0},eventType:e?.type||"selectionchange",targetFrameSrc:this.wnd.location.href};this.comms?.send("content_protection",a)}}else this.currentSelection=null},this.onDragOver=e=>{this.isDragAndDropEnabled&&(e.preventDefault(),e.stopPropagation())},this.onDragStart=e=>{if(this.isDragAndDropEnabled){e.preventDefault();let t={type:"drag_detected",timestamp:Date.now(),dataTransferTypes:e.dataTransfer?.types?[...e.dataTransfer.types]:[],targetFrameSrc:this.wnd.location.href};return this.comms?.send("content_protection",t),!1}else return!0},this.onDrop=e=>{if(this.isDragAndDropEnabled){e.preventDefault();let t=e.dataTransfer,o={type:"drop_detected",timestamp:Date.now(),dataTransferTypes:t?.types?[...t.types]:[],fileCount:t?.files?.length||0,targetFrameSrc:this.wnd.location.href};return this.comms?.send("content_protection",o),!1}else return!0},this.onContext=e=>{if(this.isContextMenuEnabled){e.preventDefault();let t=this.wnd.getSelection(),o=t?.toString()||"",r=(o&&t?.rangeCount?t.getRangeAt(0)?.getClientRects():null)?.[0],n={timestamp:Date.now(),clientX:e.clientX,clientY:e.clientY,...r&&{selectedText:{text:o,x:r.x,y:r.y,width:r.width,height:r.height}},targetFrameSrc:this.wnd.location.href};this.comms?.send("context_menu",n)}},this.onPointerUp=this.onPointUp.bind(this),this.onPointerMove=this.onPointMove.bind(this),this.onPointerDown=this.onPointDown.bind(this),this.onClicker=this.onClick.bind(this)}addContextMenuPrevention(){this.isContextMenuEnabled||!this.wnd||(this.wnd.document.addEventListener("contextmenu",this.onContext),this.isContextMenuEnabled=!0)}removeContextMenuPrevention(){!this.isContextMenuEnabled||!this.wnd||(this.wnd.document.removeEventListener("contextmenu",this.onContext),this.isContextMenuEnabled=!1)}addDragAndDropPrevention(){this.isDragAndDropEnabled||!this.wnd||(this.wnd.document.addEventListener("dragstart",this.onDragStart),this.wnd.document.addEventListener("dragover",this.onDragOver),this.wnd.document.addEventListener("drop",this.onDrop),this.isDragAndDropEnabled=!0)}removeDragAndDropPrevention(){!this.isDragAndDropEnabled||!this.wnd||(this.wnd.document.removeEventListener("dragstart",this.onDragStart),this.wnd.document.removeEventListener("dragover",this.onDragOver),this.wnd.document.removeEventListener("drop",this.onDrop),this.isDragAndDropEnabled=!1)}enableKeyboardPeripherals(e=[]){this.disableKeyboardPeripherals();let t=o=>{this.comms?.send("keyboard_peripherals",o)};this.keyDownHandler=this.keyManager.createUnifiedHandler(this.wnd.location.href,e,t,this.wnd),this.wnd&&this.wnd.document.addEventListener("keydown",this.keyDownHandler,{capture:!0})}disableKeyboardPeripherals(){this.wnd&&this.keyDownHandler&&(this.wnd.document.removeEventListener("keydown",this.keyDownHandler,{capture:!0}),this.keyDownHandler=null)}addBulkCopyProtection(e={}){if(this.isBulkCopyProtectionEnabled||!this.wnd)return;let t=Kc,o=e?{...t,...e}:t;this.bulkCopyProtector=new jo(this.wnd,o),this.wnd.document.addEventListener("copy",this.preventBulkCopy,!0),this.wnd.document.addEventListener("cut",this.preventBulkCopy,!0),this.isBulkCopyProtectionEnabled=!0}removeBulkCopyProtection(){!this.isBulkCopyProtectionEnabled||!this.wnd||(this.wnd.document.removeEventListener("copy",this.preventBulkCopy,!0),this.wnd.document.removeEventListener("cut",this.preventBulkCopy,!0),this.bulkCopyProtector?.destroy(),this.bulkCopyProtector=null,this.isBulkCopyProtectionEnabled=!1)}addSelectionMonitoring(e){if(this.isSelectionMonitoringEnabled||!this.wnd)return;let t=e||Bo;this.selectionAnalyzer=new Go(t),this.wnd.document.addEventListener("selectionchange",this.handleSelection),this.isSelectionMonitoringEnabled=!0}removeSelectionMonitoring(){!this.isSelectionMonitoringEnabled||!this.wnd||(this.wnd.document.removeEventListener("selectionchange",this.handleSelection),this.selectionAnalyzer?.clear(),this.selectionAnalyzer=null,this.isSelectionMonitoringEnabled=!1)}onPointUp(e){let t=this.wnd.getSelection();if(t&&t.toString()?.length>0){let r=t.getRangeAt(0)?.getClientRects();if(!r||r.length===0)return;let n=r[0],a={text:t.toString(),x:n.x,y:n.y,width:n.width,height:n.height,targetFrameSrc:this.wnd?.location?.href};this.comms.send("text_selected",a)}if(this.pointerMoved){this.pointerMoved=!1;return}if(!t?.isCollapsed||!e.isPrimary)return;let o=this.wnd.devicePixelRatio;e.preventDefault(),this.comms.send(e.pointerType==="touch"?"tap":"click",{defaultPrevented:e.defaultPrevented,x:e.clientX*o,y:e.clientY*o,targetFrameSrc:this.wnd.location.href,targetElement:e.target.outerHTML,interactiveElement:Yi(e.target)?.outerHTML,cssSelector:this.wnd._readium_cssSelectorGenerator.getCssSelector(e.target)}),this.pointerMoved=!1}onPointMove(e){if(e.movementY!==void 0&&e.movementX!==void 0){(Math.abs(e.movementX)>1||Math.abs(e.movementY)>1)&&(this.pointerMoved=!0);return}this.pointerMoved=!0}onPointDown(){this.pointerMoved=!1}onClick(e){if(e.preventDefault(),!e.isTrusted){let t=new PointerEvent("pointerup",{isPrimary:!0,pointerType:"mouse",clientX:e.clientX,clientY:e.clientY});Object.defineProperty(t,"target",{writable:!1,value:e.target}),Object.defineProperty(t,"defaultPrevented",{writable:!1,value:e.defaultPrevented}),this.onPointUp(t)}}registerProtectionHandlers(){this.comms?.register("peripherals_protection",Xi.moduleName,(e,t)=>{let o=e;if(!this.configApplied){if(this.configApplied=!0,o.monitorSelection){let r=typeof o.monitorSelection=="boolean"?void 0:o.monitorSelection;this.addSelectionMonitoring(r),this.comms?.log("Selection monitoring enabled")}typeof o.protectCopy=="object"?(this.addBulkCopyProtection({enabled:!0,...o.protectCopy}),this.comms?.log("Copy protection enabled (limited)")):o.protectCopy===!0&&(this.addBulkCopyProtection({enabled:!0,maxSelectionPercent:0,minThreshold:0,absoluteMaxChars:0}),this.comms?.log("Copy protection enabled")),o.disableContextMenu&&(this.addContextMenuPrevention(),this.comms?.log("Context menu protection enabled")),o.disableDragAndDrop&&(this.addDragAndDropPrevention(),this.comms?.log("Drag and drop protection enabled"))}t(!0)}),this.comms?.register("unfocus",Xi.moduleName,(e,t)=>{this.disableKeyboardPeripherals(),t(!0)}),this.comms?.register("keyboard_peripherals",Xi.moduleName,(e,t)=>{let o=e;o&&o.length>0&&(this.enableKeyboardPeripherals(o),this.comms?.log(`Keyboard peripherals enabled: ${o.map(r=>r.type).join(", ")}`)),t(!0)})}mount(e,t){return this.wnd=e,this.comms=t,this.registerProtectionHandlers(),e.document.addEventListener("pointerdown",this.onPointerDown),e.document.addEventListener("pointerup",this.onPointerUp),e.document.addEventListener("pointermove",this.onPointerMove),e.document.addEventListener("click",this.onClicker),t.log("Peripherals Mounted"),!0}unmount(e,t){return this.removeBulkCopyProtection(),this.removeSelectionMonitoring(),this.removeContextMenuPrevention(),this.removeDragAndDropPrevention(),this.disableKeyboardPeripherals(),this.cleanupCallbacks.forEach(o=>o()),this.cleanupCallbacks=[],e.document.removeEventListener("pointerdown",this.onPointerDown),e.document.removeEventListener("pointerup",this.onPointerUp),e.document.removeEventListener("pointermove",this.onPointerMove),e.document.removeEventListener("click",this.onClicker),t.unregisterAll(Xi.moduleName),this.configApplied=!1,t.log("Peripherals Unmounted"),!0}};th.moduleName="peripherals";var ih=th;var oh=class Nn extends Ee{constructor(){super(...arguments),this.mediaPlayingCount=0,this.allAnimations=new Set}wndOnErr(e){this.comms?.send("error",{message:e.message,filename:e.filename,lineno:e.lineno,colno:e.colno})}unblock(e){for(e._readium_blockEvents=!1;e._readium_blockedEvents?.length>0;){let t=e._readium_blockedEvents.shift();switch(t[0]){case 0:Reflect.apply(t[1],t[2],t[3]);break;case 1:let o=t[1],r=t[2];e.removeEventListener(o.type,e._readium_eventBlocker,!0);let n=new Event(o.type,{bubbles:o.bubbles,cancelable:o.cancelable});r?r.dispatchEvent(n):e.dispatchEvent(n);break}}}onMediaPlayEvent(){this.mediaPlayingCount++,this.comms?.send("media_play",this.mediaPlayingCount)}onMediaPauseEvent(){this.mediaPlayingCount>0&&this.mediaPlayingCount--,this.comms?.send("media_pause",this.mediaPlayingCount)}pauseAllMedia(e){let t=e.document.querySelectorAll("audio,video");for(let o=0;o<t.length;o++)t[o].pause()}mount(e,t){this.comms=t,e.addEventListener("error",this.wndOnErr,!1),Reflect.defineProperty(e.navigator,"epubReadingSystem",{value:{name:"readium-ts-toolkit",version:"2.6.3",hasFeature:(r,n="")=>{switch(r){case"dom-manipulation":return!0;case"layout-changes":return!0;case"touch-events":return!0;case"mouse-events":return!0;case"keyboard-events":return!0;case"spine-scripting":return!0;case"embedded-web-content":return!0;default:return!1}}},writable:!1}),"getAnimations"in e.document&&e.document.getAnimations().forEach(r=>{r.cancel(),this.allAnimations.add(r)}),t.register("activate",Nn.moduleName,(r,n)=>{this.allAnimations.forEach(a=>{a.cancel(),a.play()}),n(!0)}),t.register("unfocus",Nn.moduleName,(r,n)=>{this.pauseAllMedia(e),this.allAnimations.forEach(a=>a.pause()),n(!0)});let o=e.document.querySelectorAll("audio,video");for(let r=0;r<o.length;r++){let n=o[r];n.addEventListener("play",this.onMediaPlayEvent,{passive:!0}),n.addEventListener("pause",this.onMediaPauseEvent,{passive:!0})}return t.log("Setup Mounted"),!0}unmount(e,t){return e.removeEventListener("error",this.wndOnErr),e.removeEventListener("play",this.onMediaPlayEvent),e.removeEventListener("pause",this.onMediaPauseEvent),this.allAnimations.forEach(o=>o.cancel()),this.allAnimations.clear(),t.log("Setup Unmounted"),!0}};oh.moduleName="setup";var Vo=oh;var rh="readium-viewport",nh=class Dt extends Vo{onViewportWidthChanged(e){let t=e.target;bt(t,"--RS__viewportWidth",`${t.innerWidth}px`)}mount(e,t){if(!super.mount(e,t))return!1;let o=e.document.createElement("meta");return o.dataset.readium="true",o.setAttribute("name","viewport"),o.setAttribute("id",rh),o.setAttribute("content","width=device-width, initial-scale=1.0, maximum-scale=1.0, user-scalable=no, shrink-to-fit=no"),e.document.head.appendChild(o),e.addEventListener("orientationchange",this.onViewportWidthChanged),e.addEventListener("resize",this.onViewportWidthChanged),this.onViewportWidthChanged({target:e}),t.register("get_properties",Dt.moduleName,(r,n)=>{Ki(e),n(!0)}),t.register("update_properties",Dt.moduleName,(r,n)=>{r["--RS__viewportWidth"]=`${e.innerWidth}px`,Ho(e,r),n(!0)}),t.register("set_property",Dt.moduleName,(r,n)=>{let a=r;bt(e,a[0],a[1]),n(!0)}),t.register("remove_property",Dt.moduleName,(r,n)=>{It(e,r),n(!0)}),t.register("activate",Dt.moduleName,(r,n)=>{this.unblock(e),n(!0)}),t.log("ReflowableSetup Mounted"),!0}unmount(e,t){return t.unregisterAll(Dt.moduleName),e.document.head.querySelector(`#${rh}`)?.remove(),e.removeEventListener("orientationchange",this.onViewportWidthChanged),t.log("ReflowableSetup Unmounted"),super.unmount(e,t)}};nh.moduleName="reflowable_setup";var ah=nh;var sh="readium-fixed-style",lh=class vt extends Vo{mount(e,t){if(!super.mount(e,t))return!1;e.navigator.epubReadingSystem&&(e.navigator.epubReadingSystem.layoutStyle="paginated");let o=e.document.createElement("style");return o.id=sh,o.dataset.readium="true",o.textContent=`
        html, body {
            text-size-adjust: none;
            -ms-text-size-adjust: none;
            -webkit-text-size-adjust: none;
            -moz-text-size-adjust: none;

            /* Fight Safari pinches */
            touch-action: none !important;
            min-height: 100%;

            /*cursor: var() TODO*/
        }`,e.document.head.appendChild(o),t.register("set_property",vt.moduleName,(r,n)=>{let a=r;bt(e,a[0],a[1]),n(!0)}),t.register("remove_property",vt.moduleName,(r,n)=>{It(e,r),n(!0)}),t.register("first_visible_locator",vt.moduleName,(r,n)=>n(!1)),t.register("unfocus",vt.moduleName,(r,n)=>{D(e),n(!0)}),t.register(["focus","go_next","go_prev","go_id","go_end","go_start","go_text","go_progression"],vt.moduleName,(r,n)=>n(!0)),t.register("activate",vt.moduleName,(r,n)=>{this.unblock(e),n(!0)}),t.log("FixedSetup Mounted"),!0}unmount(e,t){return t.unregisterAll(vt.moduleName),e.document.getElementById(sh)?.remove(),t.log("FixedSetup Unmounted"),super.unmount(e,t)}};lh.moduleName="fixed_setup";var ch=lh;function dh(i,e){let t=e.map(c=>c.toUpperCase()),o=[],r=i.commonAncestorContainer,n=r.nodeType===Node.TEXT_NODE?r.parentNode:r,a=i.startContainer.ownerDocument.createTreeWalker(n,NodeFilter.SHOW_TEXT),s=a.nextNode();for(;s;){if(i.intersectsNode(s)){let c=s.parentNode,l=!1;for(;c;){if(c.nodeType===Node.ELEMENT_NODE&&t.includes(c.tagName.toUpperCase())){l=!0;break}c=c.parentNode}if(!l){let h=i.cloneRange();h.selectNode(s),h.compareBoundaryPoints(Range.START_TO_START,i)<0&&h.setStart(i.startContainer,i.startOffset),h.compareBoundaryPoints(Range.END_TO_END,i)>0&&h.setEnd(i.endContainer,i.endOffset);for(let d of h.getClientRects())o.push({left:d.left,right:d.right,top:d.top,bottom:d.bottom,width:d.width,height:d.height})}}s=a.nextNode()}return o}function $i(i,e,t=!1,o=0){let r;if(Array.isArray(i))r=i;else{let l=i.getClientRects();l.length||i.commonAncestorContainer.nodeType===Node.ELEMENT_NODE&&(l=i.commonAncestorContainer.getClientRects()),r=[];for(let h of l)r.push({bottom:h.bottom,height:h.height,left:h.left,right:h.right,top:h.top,width:h.width})}if(o)for(let l of r)l.left-=o,l.top-=o,l.right+=o,l.bottom+=o,l.width+=o*2,l.height+=o*2;let n=uh(r,1,e,t),a=Wp(n,1),s=mh(a),c=4;for(let l=s.length-1;l>=0;l--){let h=s[l];if(!(h.width*h.height>c))if(s.length>1)s.splice(l,1);else break}return s}function uh(i,e,t,o=!1){for(let r=0;r<i.length;r++)for(let n=r+1;n<i.length;n++){let a=i[r],s=i[n];if(a===s)continue;let c=We(a.top,s.top,e)&&We(a.bottom,s.bottom,e),l=We(a.left,s.left,e)&&We(a.right,s.right,e);if((l&&!t&&!o||c&&!l)&&ph(a,s,e)){let h=i.filter(u=>u!==a&&u!==s),d=Hp(a,s);return h.push(d),uh(h,e,t,o)}}return i}function Hp(i,e){let t=Math.min(i.left,e.left),o=Math.max(i.right,e.right),r=Math.min(i.top,e.top),n=Math.max(i.bottom,e.bottom);return{bottom:n,height:n-r,left:t,right:o,top:r,width:o-t}}function Wp(i,e){let t=new Set(i);for(let o of i){if(!(o.width>1&&o.height>1)){t.delete(o);continue}for(let r of i)if(o!==r&&t.has(r)&&Bp(r,o,e)){t.delete(o);break}}return Array.from(t)}function Bp(i,e,t){return nt(i,e.left,e.top,t)&&nt(i,e.right,e.top,t)&&nt(i,e.left,e.bottom,t)&&nt(i,e.right,e.bottom,t)}function nt(i,e,t,o){return(i.left<e||We(i.left,e,o))&&(i.right>e||We(i.right,e,o))&&(i.top<t||We(i.top,t,o))&&(i.bottom>t||We(i.bottom,t,o))}function mh(i){for(let e=0;e<i.length;e++)for(let t=e+1;t<i.length;t++){let o=i[e],r=i[t];if(o!==r&&ph(o,r,-1)){let n=[],a,s=hh(o,r);if(s.length===1)n=s,a=o;else{let l=hh(r,o);s.length<l.length?(n=s,a=o):(n=l,a=r)}let c=i.filter(l=>l!==a);return Array.prototype.push.apply(c,n),mh(c)}}return i}function hh(i,e){let t=jp(e,i);if(t.height===0||t.width===0)return[i];let o=[];{let r={bottom:i.bottom,height:0,left:i.left,right:t.left,top:i.top,width:0};r.width=r.right-r.left,r.height=r.bottom-r.top,r.height!==0&&r.width!==0&&o.push(r)}{let r={bottom:t.top,height:0,left:t.left,right:t.right,top:i.top,width:0};r.width=r.right-r.left,r.height=r.bottom-r.top,r.height!==0&&r.width!==0&&o.push(r)}{let r={bottom:i.bottom,height:0,left:t.left,right:t.right,top:t.bottom,width:0};r.width=r.right-r.left,r.height=r.bottom-r.top,r.height!==0&&r.width!==0&&o.push(r)}{let r={bottom:i.bottom,height:0,left:t.right,right:i.right,top:i.top,width:0};r.width=r.right-r.left,r.height=r.bottom-r.top,r.height!==0&&r.width!==0&&o.push(r)}return o}function jp(i,e){let t=Math.max(i.left,e.left),o=Math.min(i.right,e.right),r=Math.max(i.top,e.top),n=Math.min(i.bottom,e.bottom);return{bottom:n,height:Math.max(0,n-r),left:t,right:o,top:r,width:Math.max(0,o-t)}}function ph(i,e,t){return(i.left<e.right||t>=0&&We(i.left,e.right,t))&&(e.left<i.right||t>=0&&We(e.left,i.right,t))&&(i.top<e.bottom||t>=0&&We(i.top,e.bottom,t))&&(e.top<i.bottom||t>=0&&We(e.top,i.bottom,t))}function We(i,e,t){return Math.abs(i-e)<=t}var Ko=null,In=null,qi=0,yi={r:255,g:255,b:255,a:1},fi=new Map,Gp=()=>{if(!Ko)if(typeof OffscreenCanvas<"u")Ko=new OffscreenCanvas(5,5),In=Ko.getContext("2d",{willReadFrequently:!0,desynchronized:!0});else{let i=document.createElement("canvas");i.width=5,i.height=5,Ko=i,In=i.getContext("2d",{willReadFrequently:!0,desynchronized:!0})}return In},Vp=i=>{if(!i)return!0;let e=i.trim().toLowerCase();return e.startsWith("var(")||["transparent","currentcolor","inherit","initial","revert","unset","revert-layer"].includes(e)?!0:["linear-gradient","radial-gradient","conic-gradient","repeating-linear-gradient","repeating-radial-gradient","repeating-conic-gradient"].some(t=>e.includes(t))},Yo=(i,e)=>{console.warn(`[Decorator] Could not parse color: "${i}". ${e} Falling back to ${JSON.stringify(yi)} to compute contrast. Please use a CSS color value that can be computed to RGB(A).`)},at=(i,e=null)=>{let t=e?`${i}|${e}`:i,o=fi.get(t);if(o!==void 0)return o??yi;if(Vp(i))return Yo(i,"Unsupported color format or special value."),fi.set(t,null),yi;let r=Gp();if(!r)return Yo(i,"Could not get canvas context."),fi.set(t,null),yi;try{qi===0&&r.clearRect(0,0,5,5);let n=qi%5,a=Math.floor(qi/5);r.clearRect(n,a,1,1),e&&(r.fillStyle=e,r.fillRect(n,a,1,1)),r.fillStyle=i,r.fillRect(n,a,1,1);let s=r.getImageData(n,a,1,1);qi=(qi+1)%25;let[c,l,h,d]=s.data;if(d===0)return Yo(i,"Fully transparent color."),fi.set(t,null),yi;let u={r:c,g:l,b:h,a:d/255};return fi.set(t,u),u}catch(n){return Yo(i,`Error: ${n instanceof Error?n.message:String(n)}`),fi.set(t,null),yi}},Un=i=>{let e=i/255;return e<=.03928?e/12.92:Math.pow((e+.055)/1.055,2.4)},Xo=i=>{let e=Un(i.r),t=Un(i.g),o=Un(i.b);return .2126*e+.7152*t+.0722*o},Ji=(i,e)=>{let t=typeof i=="string"?at(i):i,o=typeof e=="string"?at(e):e,r=Xo(t),n=Xo(o),a=Math.max(r,n),s=Math.min(r,n);return(a+.05)/(s+.05)},$o=(i,e=null)=>{let t=at(i,e),o=Ji(t,{r:255,g:255,b:255,a:1}),r=Ji(t,{r:0,g:0,b:0,a:1});return o>r};var qo=(i,e=null)=>$o(i,e)?"white":"black",Kp=i=>{let e=i.a!==void 0?i.a:1;return`rgba(${Math.round(i.r)}, ${Math.round(i.g)}, ${Math.round(i.b)}, ${e})`},Yp=(i,e)=>({r:Math.min(255,i.r+(255-i.r)*e),g:Math.min(255,i.g+(255-i.g)*e),b:Math.min(255,i.b+(255-i.b)*e),a:i.a??1}),Xp=(i,e)=>({r:Math.max(0,i.r*(1-e)),g:Math.max(0,i.g*(1-e)),b:Math.max(0,i.b*(1-e)),a:i.a??1}),wt=(i,e=null,t=3)=>{let o=at(i),r=e?at(e):{r:255,g:255,b:255,a:1},n=Ji(o,r);if(n>=t)return i;let a=Xo(r)<.5,s={...o,a:o.a??1},c=20,l=.1;for(let h=0;h<c&&(a?s=Yp(s,l):s=Xp(s,l),n=Ji(s,r),!(n>=t));h++);return Kp(s)};var gh=()=>typeof navigator>"u"?"":navigator.userAgent||"",fh=()=>typeof navigator>"u"?void 0:navigator.userAgentData||void 0,Jo=class{constructor(){let e=fh(),t=gh(),o=n=>(typeof n=="string"||typeof n=="number")&&n?String(n).replace(/_/g,".").split(".").map(a=>parseInt(a)||0):[],r=(n="")=>{if(!n)return[];let a=new RegExp("^.*"+n+"[ :\\/]?(\\d+([\\._]\\d+)*).*$");return a.test(t)?o(t.replace(a,"$1")):[]};this.OS=(n=>(/(macOS|Mac OS X)/.test(t)?(/\(iP(hone|od touch);/.test(t)&&(n.iOS=r("CPU (?:iPhone )?OS ")),/\(iPad;/.test(t)?n.iOS=n.iPadOS=r("CPU (?:iPhone )?OS "):/(macOS|Mac OS X) \d/.test(t)&&(document.ontouchend!==void 0?n.iOS=n.iPadOS=r():n.macOS=r("(?:macOS|Mac OS X) "))):/Windows( NT)? \d/.test(t)?n.Windows=(a=>a[0]!==6||!a[1]?a:a[1]===1?[7]:a[1]===2?[8]:[8,1])(r("Windows(?: NT)?")):/Android \d/.test(t)?n.Android=r("Android"):/CrOS/.test(t)?n.ChromeOS=r():/X11;/.test(t)&&(n.Linux=r()),n))({}),e&&e.getHighEntropyValues(["architecture","model","platform","platformVersion","uaFullVersion"]).then(n=>(a=>{let s=n.platform,c=n.platformVersion;if(!(!s||!c)){if(/^i(OS|P(hone|od touch))$/.test(s))a.iOS=o(c);else if(/^iPad(OS)?$/.test(s))a.iOS=a.iPadOS=o(c);else if(/^(macOS|(Mac )?OS X|Mac(Intel)?)$/.test(s))document.ontouchend!==void 0?a.iOS=a.iPadOS=o():a.macOS=o(c);else if(/^(Microsoft )?Windows$/.test(s))a.Windows=o(c);else if(/^(Google )?Android$/.test(s))a.Android=o(c);else if(/^((Google )?Chrome OS|CrOS)$/.test(s))a.ChromeOS=o(c);else if(/^(Linux|Ubuntu|X11)$/.test(s))a.Linux=o(c);else return;Object.keys(this.OS).forEach(l=>delete this.OS[l]),Object.assign(this.OS,a)}})({})),this.UA=(n=>{let a=!1;if(e&&Array.isArray(e.brands)){let s=e.brands.reduce((c,l)=>(c[l.brand]=[l.version*1],c),{});s["Google Chrome"]?(a=!0,n.Blink=n.Chromium=s.Chromium||[],n.Chrome=s["Google Chrome"]):s["Microsoft Edge"]?(a=!0,n.Blink=n.Chromium=s.Chromium||[],n.Edge=s["Microsoft Edge"]):s.Opera&&(a=!0,n.Blink=n.Chromium=s.Chromium||[],n.Opera=s.Opera)}return a||(/ Gecko\/\d/.test(t)?(n.Gecko=r("rv"),/ Waterfox\/\d/.test(t)?n.Waterfox=r("Waterfox"):/ Firefox\/\d/.test(t)&&(n.Firefox=r("Firefox"))):/ Edge\/\d/.test(t)?(n.EdgeHTML=r("Edge"),n.Edge=n.EdgeHTML):/ Chrom(ium|e)\/\d/.test(t)?(n.Blink=n.Chromium=(s=>s[0]?s:r("Chrome"))(r("Chromium")),/ EdgA?\/\d/.test(t)?n.Edge=(s=>s[0]?s:r("Edg"))(r("EdgA")):/ OPR\/\d/.test(t)?n.Opera=r("OPR"):/ Vivaldi\/\d/.test(t)?n.Vivaldi=r("Vivaldi"):/ Silk\/\d/.test(t)?n.Silk=r("Silk"):/ UCBrowser\/\d/.test(t)?n.UCBrowser=r("UCBrowser"):/ Phoebe\/\d/.test(t)?n.Phoebe=r("Phoebe"):n.Chrome=(s=>s[0]?s:n.Chromium)(r("Chrome"))):/ AppleWebKit\/\d/.test(t)?(n.WebKit=r("AppleWebKit"),/ CriOS \d/.test(t)?n.Chrome=r("CriOS"):/ FxiOS \d/.test(t)?n.Firefox=r("FxiOS"):/ EdgiOS\/\d/.test(t)?n.Edge=r("EdgiOS"):/ Version\/\d/.test(t)&&(n.Safari=r("Version"))):/ Trident\/\d/.test(t)&&(n.Trident=r("Trident"),n.InternetExplorer=(s=>s[0]?s:r("MSIE"))(r("rv")))),/[\[; ]FB(AN|_IAB)\//.test(t)&&(n.Facebook=r("FBAV")),/ Line\/\d/.test(t)&&(n.LINE=r("Line")),n})({}),this.Env={get:()=>[this.OS,this.UA].reduce((n,a)=>{for(let s in a)a[s]&&n.push(s);return n},[])}}},Dn=class extends Jo{get iOSRequest(){let e=fh(),t=gh();if(this.OS.iOS&&!this.OS.iPadOS)return"mobile";if(this.OS.iPadOS)return/\(iPad;/.test(t)||e&&/^iPad(OS)?$/.test(e.platform)?"mobile":"desktop"}},Hn=new Jo,$p=new Dn;var yh=["div","span","p","br","hr","b","i","em","strong","s","u","mark","small","sub","sup","abbr","cite","code","data","dfn","kbd","q","samp","time","var","blockquote","pre","svg","g","path","circle","ellipse","rect","line","polygon","polyline","text","tspan","defs","use"],qp=/^on/i,Jp=new Set(["href","src","action","formaction","xlink:href"]),Zp=/^\s*(javascript|data):/i;function Sh(i,e){let t=i.document.createElement("div");if("Sanitizer"in i&&typeof t.setHTML=="function")try{let r=new i.Sanitizer({allowElements:yh});return t.setHTML(e,{sanitizer:r}),t.firstElementChild}catch{}let o=i.document.implementation.createHTMLDocument("");for(o.body.innerHTML=e,Qp(o.body,new Set(yh));o.body.firstChild;)t.appendChild(i.document.adoptNode(o.body.firstChild));return t.firstElementChild}function Qp(i,e){let t=Array.from(i.querySelectorAll("*")).reverse();for(let o of t){if(!e.has(o.localName)){o.replaceWith(...Array.from(o.childNodes));continue}for(let{name:r,value:n}of Array.from(o.attributes))(qp.test(r)||Jp.has(r)&&Zp.test(n))&&o.removeAttribute(r)}}function Wn(i){switch(i){case F.Mask:return"rgba(255, 255, 255, 0.5)";case F.Highlight:case F.HighlightUnderline:return"#FFFF00";default:return"#FF0000"}}var F={Highlight:"highlight",HighlightUnderline:"highlightUnderline",Underline:"underline",Strikethrough:"strikethrough",Outline:"outline",TextColor:"textColor",Mask:"mask",Template:"template"},_h=(i=>(i.Wrap="wrap",i.Viewport="viewport",i.Bounds="bounds",i.Page="page",i))(_h||{}),vh=(i=>(i.Boxes="boxes",i.Bounds="bounds",i))(vh||{}),eg=()=>"Highlight"in window,bh=["IMG","IMAGE","AUDIO","VIDEO","SVG"],Bn=class{constructor(e,t,o,r){this.wnd=e,this.comms=t,this.id=o,this.name=r,this.items=[],this.lastItemId=0,this.container=void 0,this._activatable=!1,this._hoverable=!1,this.hoveredItem=void 0,this.experimentalHighlights=!1,this._tintSubKeys=new Map,this._subKeyCounter=0,this.maskSvg=void 0,this.shadowHost=void 0,this.shadowRoot=void 0,this.currentRender=0,eg()&&(this.experimentalHighlights=!0,this.notTextFlag=new Map),this.activationHandler=this.handleActivation.bind(this),this.wnd.document.addEventListener("pointerup",this.activationHandler),this.hoverHandler=this.handleHover.bind(this),this.wnd.document.addEventListener("pointermove",this.hoverHandler)}get activatable(){return this._activatable}set activatable(e){this._activatable=e}get hoverable(){return this._hoverable}set hoverable(e){if(this._hoverable=e,!e&&this.hoveredItem){let t=this.hoveredItem.range.getBoundingClientRect(),o=this.wnd.devicePixelRatio;this.comms.send("decoration_pointer_leave",{decorationId:this.hoveredItem.decoration.id,group:this.name,rect:{top:t.top*o,left:t.left*o,width:t.width*o,height:t.height*o}}),this.hoveredItem=void 0}}add(e){let t=`${this.id}-${this.lastItemId++}`,o=Qe(this.wnd.document,e.locator);if(!o){this.comms.log("Can't locate DOM range for decoration",e);return}let r=o.commonAncestorContainer;if(r.nodeType!==Node.TEXT_NODE&&this.experimentalHighlights&&(bh.includes(r.nodeName.toUpperCase())&&this.notTextFlag?.set(t,!0),o.cloneContents().querySelector(bh.join(", ").toLowerCase())&&this.notTextFlag?.set(t,!0),(r.textContent?.trim()||"").length===0&&this.notTextFlag?.set(t,!0)),this.experimentalHighlights&&!this.notTextFlag?.has(t)){let a=s=>{for(;s&&s.nodeType===Node.ELEMENT_NODE;){if(s.namespaceURI?.includes("svg"))return!0;s=s.parentNode}return!1};(a(o.startContainer)||a(o.endContainer))&&this.notTextFlag?.set(t,!0)}if(this.experimentalHighlights){let{type:a}=e.style,{layout:s,width:c,expand:l}=e.style;a!==F.TextColor&&(a===F.Outline||a===F.Template||a===F.Mask||s!==void 0&&s!=="boxes"||c!==void 0&&c!=="wrap"||l)&&this.notTextFlag?.set(t,!0)}let n={decoration:e,id:t,range:o,hitRects:[],clickableElements:void 0,container:void 0};this.items.push(n),this.layout(n),n.hitRects=this.clientRectsToDocCoords($i(n.range,!1,!1,(n.decoration.style.expand??0)+this.hitGap())),this.renderLayout([n])}remove(e){let t=this.items.findIndex(n=>n.decoration.id===e);if(t<0)return;let o=this.items[t],r=o.decoration.style?.type===F.Mask;if(this.items.splice(t,1),o.clickableElements=void 0,o.container&&(o.container.remove(),o.container=void 0),this.experimentalHighlights&&!this.notTextFlag?.has(o.id)&&o.highlightSubKey){let n=this.wnd.CSS.highlights;n.get(o.highlightSubKey)?.delete(o.range),this.items.some(s=>s.highlightSubKey===o.highlightSubKey)||n.delete(o.highlightSubKey);let a=this.wnd.document.getElementById(`${this.id}-style`);a&&this._rebuildHighlightStylesheet(a)}this.notTextFlag?.delete(o.id),this.hoveredItem===o&&(this.hoveredItem=void 0),r&&this.updateSharedMask()}update(e){this.remove(e.id),this.add(e)}clear(){this.clearContainer(),this.items.length=0,this.notTextFlag?.clear(),this.hoveredItem=void 0,this.maskSvg&&(this.maskSvg.remove(),this.maskSvg=void 0),this.shadowHost&&(this.shadowHost.remove(),this.shadowHost=void 0,this.shadowRoot=void 0)}destroy(){this.clear(),this.wnd.document.removeEventListener("pointerup",this.activationHandler),this.wnd.document.removeEventListener("pointermove",this.hoverHandler)}clientRectsToDocCoords(e){let t=di(this.wnd),o=t.xDocOffset,r=t.yDocOffset;return o===0&&r===0?e:e.map(n=>({left:n.left+o,top:n.top+r,right:n.right+o,bottom:n.bottom+r,width:n.width,height:n.height}))}pointerToDocCoords(e){let t=di(this.wnd);return{docX:e.clientX+t.xDocOffset,docY:e.clientY+t.yDocOffset}}effectiveZoom(){if(!Hn.UA.Blink)return 1;let e=parseFloat(this.wnd.getComputedStyle(this.wnd.document.documentElement).zoom),t=parseFloat(this.wnd.getComputedStyle(this.wnd.document.body).zoom);return(e||1)*(t||1)}hitGap(){return 2*this.effectiveZoom()}handleActivation(e){if(!this._activatable)return;let{docX:t,docY:o}=this.pointerToDocCoords(e),r=this.wnd.devicePixelRatio;for(let n of this.items){let a;if(n.decoration.style.type===F.Template)for(let s of n.clickableElements??[]){let c=s.getBoundingClientRect();if(nt(c,e.clientX,e.clientY,0)){a=c;break}}else for(let s of n.hitRects)if(nt(s,t,o,0)){a=n.range.getBoundingClientRect();break}if(a){this.comms.send("decoration_activated",{decorationId:n.decoration.id,group:this.name,rect:{top:a.top*r,left:a.left*r,width:a.width*r,height:a.height*r},point:{x:e.clientX*r,y:e.clientY*r}});return}}}handleHover(e){if(!this._hoverable)return;let{docX:t,docY:o}=this.pointerToDocCoords(e),r=this.wnd.devicePixelRatio,n,a;for(let s of this.items){if(s.decoration.style.type===F.Template)for(let c of s.clickableElements??[]){let l=c.getBoundingClientRect();if(nt(l,e.clientX,e.clientY,0)){n=s,a=l;break}}else for(let c of s.hitRects)if(nt(c,t,o,0)){n=s,a=s.range.getBoundingClientRect();break}if(n)break}if(n!==this.hoveredItem){if(this.hoveredItem){let s=this.hoveredItem.range.commonAncestorContainer.isConnected?this.hoveredItem.range.getBoundingClientRect():null;this.comms.send("decoration_pointer_leave",{decorationId:this.hoveredItem.decoration.id,group:this.name,rect:s?{top:s.top*r,left:s.left*r,width:s.width*r,height:s.height*r}:void 0,point:{x:e.clientX*r,y:e.clientY*r}})}this.hoveredItem=n,n&&a&&this.comms.send("decoration_pointer_enter",{decorationId:n.decoration.id,group:this.name,rect:{top:a.top*r,left:a.left*r,width:a.width*r,height:a.height*r},point:{x:e.clientX*r,y:e.clientY*r}})}}requestLayout(){this.wnd.cancelAnimationFrame(this.currentRender),this.clearContainer(),this.wnd.document.fonts.ready.then(()=>{this.currentRender=this.wnd.requestAnimationFrame(()=>{this.items.forEach(e=>{this.layout(e),e.hitRects=this.clientRectsToDocCoords($i(e.range,!1,!1,(e.decoration.style.expand??0)+this.hitGap()))}),this.renderLayout(this.items),this.updateSharedMask()})})}experimentalLayout(e){let t=this.requireContainer(!0),o=this.wnd.CSS.highlights,r=e.decoration.style,n=r.type??F.Highlight,a=r.tint??Wn(n),s=r.width,c=r.layout,l=this._getSubKey(n,a);e.highlightSubKey&&(o.get(e.highlightSubKey)?.delete(e.range),e.highlightSubKey!==l&&!this.items.some(g=>g!==e&&g.highlightSubKey===e.highlightSubKey)&&o.delete(e.highlightSubKey)),e.highlightSubKey=l;let h;o.has(l)?h=o.get(l):(h=new this.wnd.Highlight,o.set(l,h));let d=(g,f)=>this.wnd.document.caretPositionFromPoint?.(g,f)??null;if(n===F.TextColor&&(c==="bounds"||s==="bounds"||s==="page")){let g=di(this.wnd);if(g.isVertical)console.warn("Vertical writing detected: caretPositionFromPoint has known bugs, falling back to original range"),h.add(e.range);else{let f=e.range.getBoundingClientRect(),y,w;s==="page"?(y=Math.floor(g.inlineStart(f)/g.pageInlineSize)*g.pageInlineSize,w=g.pageInlineSize):(y=g.inlineStart(f),w=g.inlineSize(f));let x=d(y,g.blockStart(f)+1),k=d(y+w,g.blockStart(f)+g.blockSize(f)-1);if(x&&k){let z=this.wnd.document.createRange();z.setStart(x.offsetNode,x.offset),z.setEnd(k.offsetNode,k.offset),h.add(z),e.range=z}else h.add(e.range)}}else h.add(e.range);let u=this.getBackgroundColor(),m=r.enforceContrast!==!1?wt(a,u):a,p;switch(n){case F.Underline:p=`::highlight(${l}) {
                    text-decoration: underline;
                    text-decoration-color: ${m};
                    text-decoration-thickness: 0.1em;
                }`;break;case F.Strikethrough:p=`::highlight(${l}) {
                    text-decoration: line-through;
                    text-decoration-color: ${m};
                    text-decoration-thickness: 0.1em;
                }`;break;case F.Outline:p=`::highlight(${l}) {
                    outline: 2px solid ${m};
                    outline-offset: 1px;
                }`;break;case F.TextColor:p=`::highlight(${l}) {
                    color: ${m};
                }`;break;case F.HighlightUnderline:{let{r:g,g:f,b:y}=at(m),w=`rgba(${g}, ${f}, ${y}, 0.3)`;p=`::highlight(${l}) {
                    color: ${qo(m,u)};
                    background-color: ${w};
                    text-decoration: underline;
                    text-decoration-color: ${m};
                    text-decoration-thickness: 0.1em;
                }`;break}case F.Highlight:default:p=`::highlight(${l}) {
                    color: ${qo(m,u)};
                    background-color: ${m};
                }`}e.highlightCSS=p,this._rebuildHighlightStylesheet(t)}_getSubKey(e,t){let o=`${e}::${t}`,r=this._tintSubKeys.get(o);return r||(r=`${this.id}--${this._subKeyCounter++}`,this._tintSubKeys.set(o,r)),r}_rebuildHighlightStylesheet(e){let t=new Set,o=[];for(let r of this.items)r.highlightSubKey&&r.highlightCSS&&!t.has(r.highlightSubKey)&&(t.add(r.highlightSubKey),o.push(r.highlightCSS));e.innerHTML=o.join(`
`)}layout(e){if(this.experimentalHighlights&&!this.notTextFlag?.has(e.id))return this.experimentalLayout(e);let t=this.wnd.document.createElement("div");t.setAttribute("id",e.id),t.dataset.highlightId=e.decoration.id,t.style.setProperty("pointer-events","none");let o=di(this.wnd),r=1/this.effectiveZoom(),n=e.decoration.style.expand??0,a=(d,u,m,p=0)=>{let g=e.decoration?.style?.width,f=u;switch(g){case"viewport":{let y=Math.floor(o.inlineStart(f)/o.viewportInlineSize)*o.viewportInlineSize;o.applyPosition(d,y+o.inlineScrollOffset+p,o.blockStart(f)+o.blockScrollOffset,o.viewportInlineSize-2*p,o.blockSize(f),r);break}case"page":{let y=Math.floor(o.inlineStart(f)/o.pageInlineSize)*o.pageInlineSize;o.applyPosition(d,y+o.inlineScrollOffset+p,o.blockStart(f)+o.blockScrollOffset,o.pageInlineSize-2*p,o.blockSize(f),r);break}case"bounds":{o.applyPosition(d,o.inlineStart(m)+o.inlineScrollOffset,o.blockStart(f)+o.blockScrollOffset,o.inlineSize(m),o.blockSize(f),r);break}default:o.applyPosition(d,o.inlineStart(f)+o.inlineScrollOffset,o.blockStart(f)+o.blockScrollOffset,o.inlineSize(f),o.blockSize(f),r)}},s=e.range.getBoundingClientRect(),c=e.decoration.style,l=(()=>{if(c.type!==F.Outline)return 0;let d=c.width;return d==="page"||d==="viewport"?3:0})(),h;if(c.type===F.Template){c.stylesheet&&this.injectCustomStylesheet(c.stylesheet);let d=Sh(this.wnd,c.element);if(!d){e.container=t,e.clickableElements=[];return}d.style.setProperty("pointer-events","none"),h=d}else{let d=c,u=d.type??F.Highlight,m=d.tint??Wn(u);if(u===F.TextColor){e.container=t,e.clickableElements=[];return}if(u===F.Mask){e.container=t,e.clickableElements=[],this.updateSharedMask();return}let p=this.getCurrentDarkMode(),g=this.getBackgroundColor(),f=d.enforceContrast!==!1,y=(()=>{switch(u){case F.Underline:{let x=f?wt(m,g):m,k=d.layout==="bounds",[z,ce]=o.isVertical?["border-right","border-left"]:["border-bottom","border-top"];return[k?`${ce}: 0.1em solid ${x} !important`:null,`${z}: 0.1em solid ${x} !important`,"background-color: transparent !important","box-sizing: border-box !important"].filter(Boolean).join("; ")}case F.Strikethrough:{let x=f?wt(m,g):m;return d.layout==="bounds"?[`background: repeating-linear-gradient(-45deg, transparent, transparent 19px, ${x} 19px, ${x} 20px) !important`,"background-color: transparent !important","box-sizing: border-box !important"].join("; "):[`background-color: ${x} !important`,"box-sizing: border-box !important"].join("; ")}case F.Outline:return[`outline: 2px solid ${f?wt(m,g):m} !important`,"outline-offset: 1px !important","background-color: transparent !important","box-sizing: border-box !important"].join("; ");case F.HighlightUnderline:{let x=f?wt(m,g):m,{r:k,g:z,b:ce}=at(x),G=`rgba(${k}, ${z}, ${ce}, 0.3)`,V=d.layout==="bounds",[To,gn]=o.isVertical?["border-right","border-left"]:["border-bottom","border-top"];return[`background-color: ${G} !important`,V?`${gn}: 0.1em solid ${x} !important`:null,`${To}: 0.1em solid ${x} !important`,"box-sizing: border-box !important"].filter(Boolean).join("; ")}case F.Highlight:default:return[`background-color: ${f?wt(m,g):m} !important`,`mix-blend-mode: ${p?"exclusion":"multiply"} !important`,"opacity: 1 !important","box-sizing: border-box !important"].join("; ")}})(),w=this.wnd.document.createElement("template");w.innerHTML=`<div data-readium="true" class="readium-${u}" style="${y}"></div>`.trim(),h=w.content.firstElementChild}if(e.decoration?.style?.layout==="bounds"){let d=h.cloneNode(!0);d.style.setProperty("pointer-events","none");let u=n?{left:s.left-n,right:s.right+n,top:s.top-n,bottom:s.bottom+n,width:s.width+n*2,height:s.height+n*2}:s;a(d,u,s,l),t.append(d)}else{let d=c.type,u=d===F.Underline||d===F.Strikethrough,m=d===F.Strikethrough,p=u?dh(e.range,["rt","rp"]):e.range,g=$i(p,!0,o.isVertical,u?0:n);g=g.sort((f,y)=>o.isVertical?(o.isVertLR?1:-1)*(f.left-y.left):f.top-y.top);for(let f of g){let y=h.cloneNode(!0);y.style.setProperty("pointer-events","none");let w=f;if(m){let x=o.blockSize(f)*.1,k=o.blockStart(f)+o.blockSize(f)/2-x/2;w=o.isVertical?{left:k,right:k+x,top:f.top,bottom:f.bottom,width:x,height:f.height}:{top:k,bottom:k+x,left:f.left,right:f.right,height:x,width:f.width}}n&&u&&(w=o.isVertical?{...w,top:w.top-n,bottom:w.bottom+n,height:w.height+n*2}:{...w,left:w.left-n,right:w.right+n,width:w.width+n*2}),a(y,w,s,l),t.append(y)}}e.container=t,e.clickableElements=Array.from(t.querySelectorAll("[data-activable='1']")),e.clickableElements.length||(e.clickableElements=Array.from(t.children))}renderLayout(e){this.wnd.cancelAnimationFrame(this.currentRender),this.currentRender=this.wnd.requestAnimationFrame(()=>{e=e.filter(t=>!this.experimentalHighlights||!!this.notTextFlag?.has(t.id)),!(!e||e.length===0)&&this.requireContainer().append(...e.map(t=>t.container).filter(t=>!!t))})}requireContainer(e=!1){if(e){let t;return this.wnd.document.getElementById(`${this.id}-style`)?t=this.wnd.document.getElementById(`${this.id}-style`):(t=this.wnd.document.createElement("style"),t.dataset.readium="true",t.id=`${this.id}-style`,this.wnd.document.head.appendChild(t)),t}return this.container||(this.shadowRoot||(this.shadowHost=this.wnd.document.createElement("div"),this.shadowHost.style.cssText="position:absolute;top:0;left:0;width:100%;height:100%;pointer-events:none",this.wnd.document.body.appendChild(this.shadowHost),this.shadowRoot=this.shadowHost.attachShadow({mode:"open"})),this.container=this.wnd.document.createElement("div"),this.container.setAttribute("id",this.id),this.container.dataset.group=this.name,this.container.dataset.readium="true",this.container.style.setProperty("pointer-events","none"),this.container.style.display="contents",this.shadowRoot.appendChild(this.container)),this.container}getCurrentDarkMode(){return zn(this.wnd,"--USER__appearance")==="readium-night-on"||$o(this.getBackgroundColor())}getBackgroundColor(){return zn(this.wnd,"--USER__backgroundColor")||this.wnd.getComputedStyle(this.wnd.document.documentElement).getPropertyValue("background-color")}updateSharedMask(){let e=this.items.filter(u=>u.decoration.style?.type===F.Mask);if(e.length===0){this.maskSvg&&(this.maskSvg.remove(),this.maskSvg=void 0),this.shadowRoot&&(this.shadowRoot.innerHTML="");return}let t=di(this.wnd),o=1/this.effectiveZoom(),r=this.wnd.document.documentElement,n=r.scrollWidth,a=r.scrollHeight,s=[];for(let u of e){let m=u.decoration.style,p=m.layout??"boxes",g=m.width??"wrap",f=m.expand??0,y=u.range.getBoundingClientRect(),w=p==="bounds"?[f?{left:y.left-f,top:y.top-f,right:y.right+f,bottom:y.bottom+f,width:y.width+f*2,height:y.height+f*2}:y]:$i(u.range,!1,!1,f);for(let x of w){let k;switch(g){case"viewport":{let z=Math.floor(t.inlineStart(x)/t.viewportInlineSize)*t.viewportInlineSize;k=t.toRect(z,t.blockStart(x),t.viewportInlineSize,t.blockSize(x));break}case"page":{let z=Math.floor(t.inlineStart(x)/t.pageInlineSize)*t.pageInlineSize;k=t.toRect(z,t.blockStart(x),t.pageInlineSize,t.blockSize(x));break}case"bounds":{k=t.toRect(t.inlineStart(y),t.blockStart(x),t.inlineSize(y),t.blockSize(x));break}default:k=t.toRect(t.inlineStart(x),t.blockStart(x),t.inlineSize(x),t.blockSize(x))}s.push(k)}}let c=[`M0 0 H${n} V${a} H0 Z`,...s.map(u=>{let m=(u.left+t.xDocOffset)*o,p=(u.top+t.yDocOffset)*o,g=(u.right+t.xDocOffset)*o,f=(u.bottom+t.yDocOffset)*o;return`M${m} ${p} H${g} V${f} H${m} Z`})].join(" "),l="http://www.w3.org/2000/svg";if(!this.maskSvg){this.shadowRoot||(this.shadowHost=this.wnd.document.createElement("div"),this.shadowHost.style.cssText="position:absolute;top:0;left:0;width:100%;height:100%;pointer-events:none",this.wnd.document.body.appendChild(this.shadowHost),this.shadowRoot=this.shadowHost.attachShadow({mode:"open"})),this.maskSvg=this.wnd.document.createElementNS(l,"svg"),this.maskSvg.style.cssText=`position:absolute;top:0;left:0;width:${n}px;height:${a}px;pointer-events:none;z-index:9999`,this.maskSvg.dataset.readium="true";let u=this.wnd.document.createElementNS(l,"defs"),m=this.wnd.document.createElementNS(l,"clipPath"),p=`${this.id}-mask-clip`;m.setAttribute("id",p),m.setAttribute("clipPathUnits","userSpaceOnUse");let g=this.wnd.document.createElementNS(l,"path");g.setAttribute("clip-rule","evenodd"),m.appendChild(g),u.appendChild(m),this.maskSvg.appendChild(u);let f=this.wnd.document.createElementNS(l,"rect");f.setAttribute("id",`${this.id}-mask-rect`),f.setAttribute("clip-path",`url(#${p})`),f.style.pointerEvents="none",this.maskSvg.appendChild(f),this.shadowRoot.appendChild(this.maskSvg)}this.maskSvg.style.width=`${n}px`,this.maskSvg.style.height=`${a}px`;let h=this.maskSvg.querySelector("path");h&&h.setAttribute("d",c);let d=this.maskSvg.querySelector("rect");if(d){let u=e[0].decoration.style.tint,m=u??this.getBackgroundColor()??Wn(F.Mask),p=u?"1":"0.5";d.setAttribute("x","0"),d.setAttribute("y","0"),d.setAttribute("width",String(n)),d.setAttribute("height",String(a)),d.setAttribute("fill",m),d.setAttribute("fill-opacity",p)}}injectCustomStylesheet(e){let t=`${this.id}-custom-style`,o=this.wnd.document.getElementById(t);o||(o=this.wnd.document.createElement("style"),o.id=t,o.dataset.readium="true",this.wnd.document.head.appendChild(o)),o.innerHTML=e}clearContainer(){if(this.experimentalHighlights){let e=this.wnd.CSS.highlights;for(let t of this._tintSubKeys.values())e.delete(t);this._tintSubKeys.clear(),this._subKeyCounter=0}this.wnd.document.getElementById(`${this.id}-custom-style`)?.remove(),this.container&&(this.container.remove(),this.container=void 0)}},wh=class Zi extends Ee{constructor(){super(...arguments),this.resizeFrame=0,this.lastGroupId=0,this.groups=new Map,this.handleResizer=this.handleResize.bind(this)}cleanup(){this.groups.forEach(e=>e.destroy()),this.groups.clear()}updateHighlightStyles(){this.groups.forEach(e=>{e.requestLayout()})}handleResize(){this.wnd.clearTimeout(this.resizeFrame),this.resizeFrame=this.wnd.setTimeout(()=>{this.groups.forEach(e=>{e.experimentalHighlights||e.requestLayout()})},50)}mount(e,t){return this.wnd=e,t.register("decorate",Zi.moduleName,(o,r)=>{let n=o;(n.action==="add"||n.action==="update")&&n.decoration.locator&&(n.decoration.locator=H.deserialize(n.decoration.locator)),this.groups.has(n.group)||this.groups.set(n.group,new Bn(e,t,`readium-decoration-${this.lastGroupId++}`,n.group));let a=this.groups.get(n.group);switch(n.action){case"add":a?.add(n.decoration);break;case"remove":a?.remove(n.decoration.id);break;case"clear":a?.clear();break;case"update":a?.update(n.decoration);break}r(!0)}),t.register("decoration_activatable",Zi.moduleName,(o,r)=>{let n=o,a=this.groups.get(n.group);a&&(a.activatable=n.activatable),r(!0)}),t.register("decoration_hoverable",Zi.moduleName,(o,r)=>{let n=o,a=this.groups.get(n.group);a&&(a.hoverable=n.hoverable),r(!0)}),this.resizeObserver=new ResizeObserver(()=>e.requestAnimationFrame(()=>this.handleResize())),this.resizeObserver.observe(e.document.documentElement),e.addEventListener("orientationchange",this.handleResizer),e.addEventListener("resize",this.handleResizer),this.styleObserver=new MutationObserver(o=>{o.some(r=>r.type==="attributes"&&r.attributeName==="style"&&r.oldValue!==r.target.getAttribute("style"))&&this.updateHighlightStyles()}),this.styleObserver.observe(e.document.documentElement,{attributes:!0,attributeFilter:["style"],attributeOldValue:!0}),t.log("Decorator Mounted"),!0}unmount(e,t){return e.removeEventListener("orientationchange",this.handleResizer),e.removeEventListener("resize",this.handleResizer),t.unregisterAll(Zi.moduleName),this.resizeObserver.disconnect(),this.styleObserver.disconnect(),this.cleanup(),t.log("Decorator Unmounted"),!0}};wh.moduleName="decorator";var jn=wh;var xh=class Ht extends Ee{wndOnErr(e){this.comms?.send("error",{message:e.message,filename:e.filename,lineno:e.lineno,colno:e.colno})}mount(e,t){return this.comms=t,e.addEventListener("error",this.wndOnErr,!1),t.register("get_properties",Ht.moduleName,(o,r)=>{Ki(e),r(!0)}),t.register("update_properties",Ht.moduleName,(o,r)=>{Ho(e,o),r(!0)}),t.register("set_property",Ht.moduleName,(o,r)=>{let n=o;bt(e,n[0],n[1]),r(!0)}),t.register("remove_property",Ht.moduleName,(o,r)=>{It(e,o),r(!0)}),t.register("activate",Ht.moduleName,(o,r)=>{r(!0)}),t.log("WebPubSetup Mounted"),!0}unmount(e,t){return t.unregisterAll(Ht.moduleName),e.removeEventListener("error",this.wndOnErr),t.log("WebPubSetup Unmounted"),!0}};xh.moduleName="webpub_setup";var kh=xh;var Ch=class Gn extends Ee{constructor(){super(...arguments),this.styleElement=null,this.beforePrintHandler=null,this.configApplied=!1}setupPrintProtection(e,t){if(!t.disable)return;let o=e.document.createElement("style");o.textContent=`
            @media print {
                body * {
                    display: none !important;
                }
                body::after {
                    content: "${t.watermark||"Printing has been disabled"}";
                    font-size: 200%;
                    display: block;
                    text-align: center;
                    margin-top: 50vh;
                    transform: translateY(-50%);
                }
            }
        `,e.document.head.appendChild(o),this.styleElement=o,this.beforePrintHandler=r=>(r.preventDefault(),!1),e.addEventListener("beforeprint",this.beforePrintHandler)}registerPrintHandlers(){this.comms?.register("print_protection",Gn.moduleName,e=>{let t=e;return this.configApplied||(this.configApplied=!0,this.setupPrintProtection(this.wnd,t),this.comms?.log("Print protection configuration applied")),!0})}mount(e,t){return this.wnd=e,this.comms=t,this.registerPrintHandlers(),!0}unmount(e,t){return this.beforePrintHandler&&(e.removeEventListener("beforeprint",this.beforePrintHandler),this.beforePrintHandler=null),this.styleElement?.parentNode&&(this.styleElement.parentNode.removeChild(this.styleElement),this.styleElement=null),this.comms?.unregisterAll(Gn.moduleName),this.configApplied=!1,!0}};Ch.moduleName="print_protection";var Rh=Ch;var Eh="readium-cjk-vertical-snapper-style",Ph=class Fe extends he{constructor(){super(...arguments),this.patternAnalyzer=null,this.lastScrollTime=0,this.isScrollProtectionEnabled=!1,this.initialScrollHandled=!1,this.isScrolling=!1,this.lastScrollLeft=0,this.isResizing=!1,this.resizeDebounce=null,this.verticalLR=!1,this.handleScroll=e=>{if(this.comms.ready&&!this.isResizing){if(!this.initialScrollHandled){this.lastScrollLeft=Math.abs(this.doc().scrollLeft),this.initialScrollHandled=!0,this.reportProgress();return}this.isScrolling||(this.isScrolling=!0,this.wnd.requestAnimationFrame(()=>{this.reportProgress();let t=Math.abs(this.doc().scrollLeft),o=t-this.lastScrollLeft;if(this.lastScrollLeft=t,this.isScrollProtectionEnabled&&Math.abs(o)>5){let r=Date.now(),n=r-(this.lastScrollTime||r);if(this.patternAnalyzer&&this.patternAnalyzer.analyze(o>0?"down":"up",Math.abs(o),n)){let a=e.target&&"tagName"in e.target?{tagName:e.target.tagName}:null;this.comms?.send("content_protection",{type:"suspicious_scrolling",timestamp:Date.now(),scrollDelta:o,scrollDirection:o>0?"left":"right",targetElement:a})}this.lastScrollTime=r}this.comms.send("scroll",o),this.isScrolling=!1}))}}}doc(){return this.wnd.document.scrollingElement}scrollable(){return Math.max(0,this.doc().scrollWidth-this.wnd.innerWidth)}hasScrolledPast(e){let t=e.getBoundingClientRect(),o=this.wnd.innerWidth/2,r=this.wnd.innerWidth*he.CENTER_TOLERANCE;return this.verticalLR?t.right<=o+r:t.left>=o-r}inCenterBand(e){let t=e.getBoundingClientRect(),o=this.wnd.innerWidth/2,r=this.wnd.innerWidth*he.CENTER_TOLERANCE,n=o-r,a=o+r;return t.width===0?t.left<=a&&t.left>=n:Math.max(0,Math.min(t.right,a)-Math.max(t.left,n))/t.width>=he.CENTER_TOLERANCE}fragmentStart(e){return e.getBoundingClientRect().left+Math.abs(this.doc().scrollLeft)}currentScrollExtent(){return{pos:Math.abs(this.doc().scrollLeft),size:this.wnd.innerWidth}}reportProgress(e){if(!this.comms.ready)return;let t=this.doc().scrollWidth,o=this.wnd.innerWidth,r=Math.max(1,t-o),n=Math.abs(this.doc().scrollLeft),a=Math.max(0,Math.min(1,n/r)),s=Math.max(0,Math.min(1,(n+o)/t));this.comms.send("progress",{start:a,end:s,fragmentId:e!==void 0?e:this.currentTimelineFragment(),visibleFragmentIds:this.sortedVisibleFragmentIds()})}enableScrollProtection(){this.patternAnalyzer||(this.patternAnalyzer=new et(gi),this.isScrollProtectionEnabled=!0,this.comms?.log("Scroll protection enabled"))}mount(e,t){this.wnd=e,this.comms=t,this.initialScrollHandled=!1,this.lastScrollLeft=0,this.isResizing=!1,this.verticalLR=Pn(e),this.resizeDebounce&&(this.wnd.clearTimeout(this.resizeDebounce),this.resizeDebounce=null),e.navigator.epubReadingSystem&&(e.navigator.epubReadingSystem.layoutStyle="scrolling");let o=e.document.createElement("style");return o.dataset.readium="true",o.id=Eh,o.textContent=`
        * {
            scrollbar-width: none;
        }
        body::-webkit-scrollbar {
            display: none;
        }
        html {
            overflow-x: auto !important;
            overflow-y: hidden !important;
        }
        `,e.document.head.appendChild(o),this.resizeObserver=new ResizeObserver(()=>{this.resizeDebounce&&this.wnd.clearTimeout(this.resizeDebounce),this.isResizing=!0,this.resizeDebounce=this.wnd.setTimeout(()=>{this.isResizing=!1,this.resizeDebounce=null,this.refreshFragmentStarts(),this.reportProgress()},50)}),this.resizeObserver.observe(e.document.body),e.addEventListener("scroll",this.handleScroll,{passive:!0}),t.register("force_webkit_recalc",Fe.moduleName,()=>{ui(this.wnd);let r=this.doc().scrollLeft;this.verticalLR?this.doc().scrollLeft=r>1?r-1:r+1:this.doc().scrollLeft=r<-1?r+1:r-1,this.doc().scrollLeft=r}),t.register("go_progression",Fe.moduleName,(r,n)=>{let a=r;if(a<0||a>1){t.send("error",{message:"go_progression must be given a position from 0.0 to 1.0"}),n(!1);return}this.wnd.requestAnimationFrame(()=>{let s=this.scrollable()*a;this.doc().scrollLeft=this.verticalLR?s:-s,this.reportProgress(this.fragmentFromGeometry()),D(this.wnd),n(!0)})}),t.register("go_id",Fe.moduleName,(r,n)=>{let a=e.document.getElementById(r);if(!a){n(!1);return}this.wnd.requestAnimationFrame(()=>{this.doc().scrollLeft+=a.getBoundingClientRect().left-e.innerWidth/2;let s=r;this.reportProgress(this.timelineEntries.has(s)?s:this.nearestPrecedingTimelineEntry(a)),D(this.wnd),n(!0)})}),t.register("go_text",Fe.moduleName,(r,n)=>{let a;Array.isArray(r)&&(r.length>1&&(a=r[1]),r=r[0]);let s=me.deserialize(r),c=Qe(this.wnd.document,new H({href:e.location.href,type:"text/html",text:s,locations:a?new ee({otherLocations:new Map([["cssSelector",a]])}):void 0}));if(!c){n(!1);return}this.wnd.requestAnimationFrame(()=>{this.doc().scrollLeft+=c.getBoundingClientRect().left-e.innerWidth/2,this.reportProgress(this.nearestPrecedingTimelineEntry(c.startContainer)),D(this.wnd),n(!0)})}),t.register("go_start",Fe.moduleName,(r,n)=>{if(this.doc().scrollLeft===0)return n(!1);this.doc().scrollLeft=0,this.reportProgress(this.firstFragmentIfReached()),n(!0)}),t.register("go_end",Fe.moduleName,(r,n)=>{if(Math.abs(this.doc().scrollLeft)===this.scrollable())return n(!1);this.doc().scrollLeft=this.verticalLR?this.scrollable():-this.scrollable(),this.reportProgress(this.sortedFragmentIds[this.sortedFragmentIds.length-1]),n(!0)}),t.register(["go_next","go_prev"],Fe.moduleName,(r,n)=>n(!1)),t.register("unfocus",Fe.moduleName,(r,n)=>{D(this.wnd),n(!0)}),t.register("scroll_protection",Fe.moduleName,(r,n)=>{this.enableScrollProtection(),n(!0)}),t.register("focus",Fe.moduleName,(r,n)=>{this.reportProgress(),n(!0)}),t.register("first_visible_locator",Fe.moduleName,(r,n)=>{let a=_t(e,!0);this.comms.send("first_visible_locator",a.serialize()),n(!0)}),t.register("timeline_entries",Fe.moduleName,(r,n)=>{this.updateTimelineEntries(Array.isArray(r)?r:[],e),n(!0)}),t.log("CJKVerticalSnapper Mounted"),!0}unmount(e,t){return t.unregisterAll(Fe.moduleName),this.resizeObserver.disconnect(),this.handleScroll&&e.removeEventListener("scroll",this.handleScroll),e.document.getElementById(Eh)?.remove(),this.patternAnalyzer&&(this.patternAnalyzer.clear(),this.patternAnalyzer=null,this.isScrollProtectionEnabled=!1),this.timelineEntries.clear(),this.cachedFragmentIds=[],this.sortedFragmentIds=[],this.cachedFragmentStarts.clear(),t.log("CJKVerticalSnapper Unmounted"),!0}};Ph.moduleName="cjk_vertical_snapper";var Th=Ph;var Vn=["fixed_setup","decorator","peripherals","print_protection"],Kn=["reflowable_setup","decorator","peripherals","column_snapper","scroll_snapper","cjk_vertical_snapper","print_protection"];var Si=new Map([ch,ah,kh,eh,ih,jn,$c,Zc,Th,Rh].map(i=>[i.moduleName,i]));var st=class{constructor(e=window,t=[]){this.loadedModules=[],this.wnd=e,this.comms=new Vi(e);let o=[...new Set(t)];if(o.length){if(typeof e>"u")throw Error("Loader is not in a web browser");e.parent!==e&&this.comms.log("Loader is probably in a frame"),this.loadedModules=o.map(r=>{let n=this.loadModule(r);if(n)return n.mount(this.wnd,this.comms),n}).filter(r=>r!==void 0)}}loadModule(e){let t=Si.get(e);return t===void 0?(this.comms.log(`Module "${name}" does not exist in the library`),t):new t}addModule(e){let t=this.loadModule(e);return!t||!t.mount(this.wnd,this.comms)?!1:(this.loadedModules.push(t),!0)}removeModule(e){let t=Si.get(e);if(t===void 0)return this.comms.log(`Module "${e}" does not exist in the library`),!1;let o=this.loadedModules.findIndex(r=>r instanceof t);return o<0?!1:(this.loadedModules[o].unmount(this.wnd,this.comms),this.loadedModules.splice(o,1),!0)}destroy(){this.comms.destroy(),this.loadedModules.forEach(e=>e.unmount(this.wnd,this.comms)),this.loadedModules=[]}};var Yn={type:"developer_tools",keyCombos:[{keyCode:73,meta:!0,alt:!0},{keyCode:73,ctrl:!0,shift:!0},{keyCode:74,meta:!0,alt:!0},{keyCode:74,ctrl:!0,shift:!0},{keyCode:85,meta:!0,alt:!0},{keyCode:67,meta:!0,alt:!0},{keyCode:67,meta:!0,shift:!0},{keyCode:67,ctrl:!0,shift:!0},{keyCode:65,meta:!0,alt:!0},{keyCode:84,meta:!0,shift:!0,alt:!0},{keyCode:67,shift:!0,alt:!0},{keyCode:123},{keyCode:123,shift:!0},{keyCode:123,ctrl:!0,shift:!0},{keyCode:123,meta:!0,alt:!0}]},Xn={type:"select_all",keyCombos:[{keyCode:65,meta:!0},{keyCode:65,ctrl:!0}]},$n={type:"print",keyCombos:[{keyCode:80,meta:!0},{keyCode:80,ctrl:!0},{keyCode:80,meta:!0,shift:!0},{keyCode:80,ctrl:!0,shift:!0},{keyCode:80,meta:!0,alt:!0},{keyCode:80,ctrl:!0,alt:!0}]},qn={type:"save",keyCombos:[{keyCode:83,meta:!0},{keyCode:83,ctrl:!0}]};var Jn=class{mergeKeyboardPeripherals(e,t=[]){let o=[],r=t.filter(n=>!["developer_tools","select_all","print","save"].includes(n.type));e.disableSelectAll&&o.push(Xn),e.disableSave&&o.push(qn),e.monitorDevTools&&o.push(Yn),e.protectPrinting?.disable&&o.push($n);for(let n of r){let a=n.keyCombos.filter(s=>!o.some(c=>c.keyCombos.some(l=>s.keyCode===l.keyCode&&s.ctrl===l.ctrl&&s.shift===l.shift&&s.alt===l.alt&&s.meta===l.meta)));a.length>0&&o.push({...n,keyCombos:a})}return o}},Zo=class extends Jn{goLeft(e=!1,t){this.readingProgression===L.ltr?this.goBackward(e,t):this.readingProgression===L.rtl&&this.goForward(e,t)}goRight(e=!1,t){this.readingProgression===L.ltr?this.goForward(e,t):this.readingProgression===L.rtl&&this.goBackward(e,t)}};var zh=new Set(Object.values(F));function Zn(i,e){return i===F.TextColor?typeof window<"u"&&"Highlight"in window:zh.has(i)?!0:!!e?.[i]}function Qi(i,e){let{style:t}=i;if(t.type===F.Template){let o=t;return{...i,style:{...o,element:Fh(o,i)}}}if(t.type&&e?.[t.type]){let o=e[t.type];return{...i,style:{type:F.Template,layout:o.layout,width:o.width,stylesheet:o.stylesheet,element:Fh(o,i)}}}return i}function Fh(i,e){return typeof i.element=="function"?i.element(e):i.element}function tg(i,e){if(i.type!==e.type)return!1;if(i.type===F.Template){let r=i,n=e;return r.layout===n.layout&&r.width===n.width&&r.stylesheet===n.stylesheet}let t=i,o=e;return t.tint===o.tint&&t.layout===o.layout&&t.width===o.width&&(t.enforceContrast??!0)===(o.enforceContrast??!0)&&(t.expand??0)===(o.expand??0)}function Qn(i,e){return i.locator.href===e.locator.href&&JSON.stringify(i.locator.locations?.serialize?.()??i.locator.locations)===JSON.stringify(e.locator.locations?.serialize?.()??e.locator.locations)&&JSON.stringify(i.locator.text??null)===JSON.stringify(e.locator.text??null)&&tg(i.style,e.style)&&JSON.stringify(i.extras??null)===JSON.stringify(e.extras??null)}var ig=1e4,Wt=class{constructor(e,t){this.registry=new Map,this._ready=!1,this.listenerBuffer=[],this.handler=this.handle.bind(this),this.wnd=e,this.origin=t;try{this.channelId=window.crypto.randomUUID()}catch{this.channelId=hi()}this.gc=setInterval(()=>{this.registry.forEach((o,r)=>{performance.now()-o.time>ig&&(console.warn(r,"event for",o.key,"was never handled!"),this.registry.delete(r))})},5e3),window.addEventListener("message",this.handler),this.send("_ping",void 0)}set listener(e){this.listenerBuffer.length>0&&this.listenerBuffer.forEach(t=>e(t[0],t[1])),this.listenerBuffer=[],this._listener=e}clearListener(){typeof this._listener=="function"&&(this._listener=void 0)}halt(){this._ready=!1,window.removeEventListener("message",this.handler),clearInterval(this.gc),this._listener=void 0,this.registry.clear()}resume(){window.addEventListener("message",this.handler),this._ready=!0}handle(e){let t=e.data;if(!t._readium){console.warn("Ignoring",t);return}if(t._channel===this.channelId)switch(t.key){case"_ack":{if(!t.id)return;let o=this.registry.get(t.id);if(!o)return;this.registry.delete(t.id),o.cb(!!t.data);return}case"_pong":this._ready=!0;default:{if(!this.ready)return;typeof this._listener=="function"?this._listener(t.key,t.data):this.listenerBuffer.push([t.key,t.data])}}}get ready(){return this._ready}send(e,t,o,r=!1,n=[]){let a=hi();return o&&this.registry.set(a,{cb:o,time:performance.now(),key:e}),this.wnd.postMessage({_readium:St,_channel:this.channelId,id:a,data:t,key:e,strict:r},"/",n),a}};var Lh=()=>typeof navigator>"u"?"":navigator.userAgent||"",Oh=()=>typeof navigator>"u"?void 0:navigator.userAgentData||void 0,Qo=class{constructor(){let e=Oh(),t=Lh(),o=n=>(typeof n=="string"||typeof n=="number")&&n?String(n).replace(/_/g,".").split(".").map(a=>parseInt(a)||0):[],r=(n="")=>{if(!n)return[];let a=new RegExp("^.*"+n+"[ :\\/]?(\\d+([\\._]\\d+)*).*$");return a.test(t)?o(t.replace(a,"$1")):[]};this.OS=(n=>(/(macOS|Mac OS X)/.test(t)?(/\(iP(hone|od touch);/.test(t)&&(n.iOS=r("CPU (?:iPhone )?OS ")),/\(iPad;/.test(t)?n.iOS=n.iPadOS=r("CPU (?:iPhone )?OS "):/(macOS|Mac OS X) \d/.test(t)&&(document.ontouchend!==void 0?n.iOS=n.iPadOS=r():n.macOS=r("(?:macOS|Mac OS X) "))):/Windows( NT)? \d/.test(t)?n.Windows=(a=>a[0]!==6||!a[1]?a:a[1]===1?[7]:a[1]===2?[8]:[8,1])(r("Windows(?: NT)?")):/Android \d/.test(t)?n.Android=r("Android"):/CrOS/.test(t)?n.ChromeOS=r():/X11;/.test(t)&&(n.Linux=r()),n))({}),e&&e.getHighEntropyValues(["architecture","model","platform","platformVersion","uaFullVersion"]).then(n=>(a=>{let s=n.platform,c=n.platformVersion;if(!(!s||!c)){if(/^i(OS|P(hone|od touch))$/.test(s))a.iOS=o(c);else if(/^iPad(OS)?$/.test(s))a.iOS=a.iPadOS=o(c);else if(/^(macOS|(Mac )?OS X|Mac(Intel)?)$/.test(s))document.ontouchend!==void 0?a.iOS=a.iPadOS=o():a.macOS=o(c);else if(/^(Microsoft )?Windows$/.test(s))a.Windows=o(c);else if(/^(Google )?Android$/.test(s))a.Android=o(c);else if(/^((Google )?Chrome OS|CrOS)$/.test(s))a.ChromeOS=o(c);else if(/^(Linux|Ubuntu|X11)$/.test(s))a.Linux=o(c);else return;Object.keys(this.OS).forEach(l=>delete this.OS[l]),Object.assign(this.OS,a)}})({})),this.UA=(n=>{let a=!1;if(e&&Array.isArray(e.brands)){let s=e.brands.reduce((c,l)=>(c[l.brand]=[l.version*1],c),{});s["Google Chrome"]?(a=!0,n.Blink=n.Chromium=s.Chromium||[],n.Chrome=s["Google Chrome"]):s["Microsoft Edge"]?(a=!0,n.Blink=n.Chromium=s.Chromium||[],n.Edge=s["Microsoft Edge"]):s.Opera&&(a=!0,n.Blink=n.Chromium=s.Chromium||[],n.Opera=s.Opera)}return a||(/ Gecko\/\d/.test(t)?(n.Gecko=r("rv"),/ Waterfox\/\d/.test(t)?n.Waterfox=r("Waterfox"):/ Firefox\/\d/.test(t)&&(n.Firefox=r("Firefox"))):/ Edge\/\d/.test(t)?(n.EdgeHTML=r("Edge"),n.Edge=n.EdgeHTML):/ Chrom(ium|e)\/\d/.test(t)?(n.Blink=n.Chromium=(s=>s[0]?s:r("Chrome"))(r("Chromium")),/ EdgA?\/\d/.test(t)?n.Edge=(s=>s[0]?s:r("Edg"))(r("EdgA")):/ OPR\/\d/.test(t)?n.Opera=r("OPR"):/ Vivaldi\/\d/.test(t)?n.Vivaldi=r("Vivaldi"):/ Silk\/\d/.test(t)?n.Silk=r("Silk"):/ UCBrowser\/\d/.test(t)?n.UCBrowser=r("UCBrowser"):/ Phoebe\/\d/.test(t)?n.Phoebe=r("Phoebe"):n.Chrome=(s=>s[0]?s:n.Chromium)(r("Chrome"))):/ AppleWebKit\/\d/.test(t)?(n.WebKit=r("AppleWebKit"),/ CriOS \d/.test(t)?n.Chrome=r("CriOS"):/ FxiOS \d/.test(t)?n.Firefox=r("FxiOS"):/ EdgiOS\/\d/.test(t)?n.Edge=r("EdgiOS"):/ Version\/\d/.test(t)&&(n.Safari=r("Version"))):/ Trident\/\d/.test(t)&&(n.Trident=r("Trident"),n.InternetExplorer=(s=>s[0]?s:r("MSIE"))(r("rv")))),/[\[; ]FB(AN|_IAB)\//.test(t)&&(n.Facebook=r("FBAV")),/ Line\/\d/.test(t)&&(n.LINE=r("Line")),n})({}),this.Env={get:()=>[this.OS,this.UA].reduce((n,a)=>{for(let s in a)a[s]&&n.push(s);return n},[])}}},ea=class extends Qo{get iOSRequest(){let e=Oh(),t=Lh();if(this.OS.iOS&&!this.OS.iPadOS)return"mobile";if(this.OS.iPadOS)return/\(iPad;/.test(t)||e&&/^iPad(OS)?$/.test(e.platform)?"mobile":"desktop"}},Be=new Qo,je=new ea;var bi=class{constructor(e,t){this.config=e,this.onUpdate=t,this.unsubs=[],this.conditionValues=new Map}setup(){let e=!0;this.config.forEach(t=>t.keyCombos.forEach(o=>{if(o.condition){let r=o.condition.subscribe(n=>{this.conditionValues.set(o,n),e||this.onUpdate(this.buildSerializable())});this.unsubs.push(r)}})),e=!1,this.onUpdate(this.buildSerializable())}buildSerializable(){return this.config.map(e=>({...e,keyCombos:e.keyCombos.filter(t=>!t.condition||this.conditionValues.get(t)===!0).map(({condition:t,...o})=>o)})).filter(e=>e.keyCombos.length>0)}destroy(){this.unsubs.forEach(e=>e()),this.unsubs=[],this.conditionValues.clear()}};var ta,Ah;function Mh(){if(Ah)return ta;Ah=1;function i(r){if(typeof r!="string")throw new TypeError("Path must be a string. Received "+JSON.stringify(r))}function e(r,n){for(var a="",s=0,c=-1,l=0,h,d=0;d<=r.length;++d){if(d<r.length)h=r.charCodeAt(d);else{if(h===47)break;h=47}if(h===47){if(!(c===d-1||l===1))if(c!==d-1&&l===2){if(a.length<2||s!==2||a.charCodeAt(a.length-1)!==46||a.charCodeAt(a.length-2)!==46){if(a.length>2){var u=a.lastIndexOf("/");if(u!==a.length-1){u===-1?(a="",s=0):(a=a.slice(0,u),s=a.length-1-a.lastIndexOf("/")),c=d,l=0;continue}}else if(a.length===2||a.length===1){a="",s=0,c=d,l=0;continue}}n&&(a.length>0?a+="/..":a="..",s=2)}else a.length>0?a+="/"+r.slice(c+1,d):a=r.slice(c+1,d),s=d-c-1;c=d,l=0}else h===46&&l!==-1?++l:l=-1}return a}function t(r,n){var a=n.dir||n.root,s=n.base||(n.name||"")+(n.ext||"");return a?a===n.root?a+s:a+r+s:s}var o={resolve:function(){for(var r="",n=!1,a,s=arguments.length-1;s>=-1&&!n;s--){var c;s>=0?c=arguments[s]:(a===void 0&&(a=process.cwd()),c=a),i(c),c.length!==0&&(r=c+"/"+r,n=c.charCodeAt(0)===47)}return r=e(r,!n),n?r.length>0?"/"+r:"/":r.length>0?r:"."},normalize:function(r){if(i(r),r.length===0)return".";var n=r.charCodeAt(0)===47,a=r.charCodeAt(r.length-1)===47;return r=e(r,!n),r.length===0&&!n&&(r="."),r.length>0&&a&&(r+="/"),n?"/"+r:r},isAbsolute:function(r){return i(r),r.length>0&&r.charCodeAt(0)===47},join:function(){if(arguments.length===0)return".";for(var r,n=0;n<arguments.length;++n){var a=arguments[n];i(a),a.length>0&&(r===void 0?r=a:r+="/"+a)}return r===void 0?".":o.normalize(r)},relative:function(r,n){if(i(r),i(n),r===n||(r=o.resolve(r),n=o.resolve(n),r===n))return"";for(var a=1;a<r.length&&r.charCodeAt(a)===47;++a);for(var s=r.length,c=s-a,l=1;l<n.length&&n.charCodeAt(l)===47;++l);for(var h=n.length,d=h-l,u=c<d?c:d,m=-1,p=0;p<=u;++p){if(p===u){if(d>u){if(n.charCodeAt(l+p)===47)return n.slice(l+p+1);if(p===0)return n.slice(l+p)}else c>u&&(r.charCodeAt(a+p)===47?m=p:p===0&&(m=0));break}var g=r.charCodeAt(a+p),f=n.charCodeAt(l+p);if(g!==f)break;g===47&&(m=p)}var y="";for(p=a+m+1;p<=s;++p)(p===s||r.charCodeAt(p)===47)&&(y.length===0?y+="..":y+="/..");return y.length>0?y+n.slice(l+m):(l+=m,n.charCodeAt(l)===47&&++l,n.slice(l))},_makeLong:function(r){return r},dirname:function(r){if(i(r),r.length===0)return".";for(var n=r.charCodeAt(0),a=n===47,s=-1,c=!0,l=r.length-1;l>=1;--l)if(n=r.charCodeAt(l),n===47){if(!c){s=l;break}}else c=!1;return s===-1?a?"/":".":a&&s===1?"//":r.slice(0,s)},basename:function(r,n){if(n!==void 0&&typeof n!="string")throw new TypeError('"ext" argument must be a string');i(r);var a=0,s=-1,c=!0,l;if(n!==void 0&&n.length>0&&n.length<=r.length){if(n.length===r.length&&n===r)return"";var h=n.length-1,d=-1;for(l=r.length-1;l>=0;--l){var u=r.charCodeAt(l);if(u===47){if(!c){a=l+1;break}}else d===-1&&(c=!1,d=l+1),h>=0&&(u===n.charCodeAt(h)?--h===-1&&(s=l):(h=-1,s=d))}return a===s?s=d:s===-1&&(s=r.length),r.slice(a,s)}else{for(l=r.length-1;l>=0;--l)if(r.charCodeAt(l)===47){if(!c){a=l+1;break}}else s===-1&&(c=!1,s=l+1);return s===-1?"":r.slice(a,s)}},extname:function(r){i(r);for(var n=-1,a=0,s=-1,c=!0,l=0,h=r.length-1;h>=0;--h){var d=r.charCodeAt(h);if(d===47){if(!c){a=h+1;break}continue}s===-1&&(c=!1,s=h+1),d===46?n===-1?n=h:l!==1&&(l=1):n!==-1&&(l=-1)}return n===-1||s===-1||l===0||l===1&&n===s-1&&n===a+1?"":r.slice(n,s)},format:function(r){if(r===null||typeof r!="object")throw new TypeError('The "pathObject" argument must be of type Object. Received type '+typeof r);return t("/",r)},parse:function(r){i(r);var n={root:"",dir:"",base:"",ext:"",name:""};if(r.length===0)return n;var a=r.charCodeAt(0),s=a===47,c;s?(n.root="/",c=1):c=0;for(var l=-1,h=0,d=-1,u=!0,m=r.length-1,p=0;m>=c;--m){if(a=r.charCodeAt(m),a===47){if(!u){h=m+1;break}continue}d===-1&&(u=!1,d=m+1),a===46?l===-1?l=m:p!==1&&(p=1):l!==-1&&(p=-1)}return l===-1||d===-1||p===0||p===1&&l===d-1&&l===h+1?d!==-1&&(h===0&&s?n.base=n.name=r.slice(1,d):n.base=n.name=r.slice(h,d)):(h===0&&s?(n.name=r.slice(1,l),n.base=r.slice(1,d)):(n.name=r.slice(h,l),n.base=r.slice(h,d)),n.ext=r.slice(l,d)),h>0?n.dir=r.slice(0,h-1):s&&(n.dir="/"),n},sep:"/",delimiter:":",win32:null,posix:null};return o.posix=o,ta=o,ta}var ia=Mh();function xt(i){let e=i.languages?.[0]?.toLowerCase(),t=i.readingProgression;if(e){if(e.startsWith("zh")||e.startsWith("ja")||e.startsWith("ko"))return t===L.rtl?"cjk-vertical":"cjk-horizontal";if(e.startsWith("mn-mong"))return"mongolian-vertical";if(e.startsWith("ar")||e.startsWith("fa")||e.startsWith("he"))return"rtl"}return"ltr"}var og={description:"Attempts to filter out paragraphs that are implicitly headings or part of headers",scope:"RS",value:"readium-experimentalHeaderFiltering-on"},rg={description:"Attempts to filter out elements that are sized using viewport units and should not be scaled directly e.g. tables, images, iframes, etc.",scope:"RS",value:"readium-experimentalZoom-on"},Nh={experimentalHeaderFiltering:og,experimentalZoom:rg};var er=Nh;var kt=(i=>(i.start="start",i.left="left",i.right="right",i.justify="justify",i))(kt||{}),Bt={range:[0,100],step:1},jt={range:[.7,4],step:.05},Gt={range:[100,1e3],step:100},Vt={range:[50,250],step:10},oa={range:[0,1],step:.125},ra={range:[1,2.5],step:.1},Kt={range:[20,100],step:1},na={range:[0,3],step:.25},aa={range:[0,3],step:.25},sa={range:[0,2],step:.125};var eo=class{constructor(){}toFlag(e){return`readium-${e}-on`}toUnitless(e){return e.toString()}toPercentage(e,t=!1){return t||e>0&&e<=1?`${Math.round(e*100)}%`:`${e}%`}toVw(e){let t=Math.round(e*100);return`${Math.min(t,100)}vw`}toVh(e){let t=Math.round(e*100);return`${Math.min(t,100)}vh`}toPx(e){return`${e}px`}toRem(e){return`${e}rem`}};function Ih(i,e){return i==null||e==null||i<=e?i:void 0}function Uh(i,e){return i==null||e==null||i>=e?i:void 0}function pe(i){return typeof i=="string"?i:i===null?null:void 0}function X(i){return typeof i=="boolean"||i==null?i:void 0}function tr(i,e){if(i!==void 0)return i===null?null:e[i]!==void 0?i:void 0}function Ct(i){return typeof i=="boolean"||typeof i=="number"&&i>=0?i:i===null?null:void 0}function M(i){if(i!==void 0)return i===null?null:i<0?void 0:i}function Rt(i,e){if(i===void 0)return;if(i===null)return null;let t=Math.min(...e),o=Math.max(...e);return i>=t&&i<=o?i:void 0}function ir(i,e){return i===void 0?e:i}function Dh(i){if(i!==void 0)return i===null?null:i.filter(e=>e in er)}var ie=class{constructor({initialValue:e=null,effectiveValue:t,isEffective:o,onChange:r}){this._value=e,this._effectiveValue=t,this._isEffective=o,this._onChange=r}set value(e){this._value=e,this._onChange(this._value)}get value(){return this._value}get effectiveValue(){return this._effectiveValue}get isEffective(){return this._isEffective}clear(){this._value=null}},ze=class extends ie{set value(e){this._value=e,this._onChange(this._value)}get value(){return this._value}get effectiveValue(){return this._effectiveValue}get isEffective(){return this._isEffective}clear(){this._value=null}toggle(){this._value=!this._value,this._onChange(this._value)}},or=class extends ie{constructor({initialValue:e=null,effectiveValue:t,isEffective:o,onChange:r,supportedValues:n}){super({initialValue:e,effectiveValue:t,isEffective:o,onChange:r}),this._supportedValues=n}set value(e){if(e&&!this._supportedValues.includes(e))throw new Error(`Value '${String(e)}' is not in the supported values for this preference.`);this._value=e,this._onChange(this._value)}get value(){return this._value}get effectiveValue(){return this._effectiveValue}get isEffective(){return this._isEffective}get supportedValues(){return this._supportedValues}clear(){this._value=null}},de=class extends ie{constructor({initialValue:e=null,effectiveValue:t,isEffective:o,onChange:r,supportedRange:n,step:a}){super({initialValue:e,effectiveValue:t,isEffective:o,onChange:r}),this._supportedRange=n,this._step=a,this._decimals=this._step.toString().includes(".")?this._step.toString().split(".")[1].length:0}set value(e){if(e&&(e<this._supportedRange[0]||e>this._supportedRange[1]))throw new Error(`Value '${String(e)}' is out of the supported range for this preference.`);this._value=e,this._onChange(this._value)}get value(){return this._value}get effectiveValue(){return this._effectiveValue}get isEffective(){return this._isEffective}get supportedRange(){return this._supportedRange}get step(){return this._step}increment(){this._value&&this._value<this._supportedRange[1]&&(this._value=Math.min(Math.round((this._value+this._step)*10**this._decimals)/10**this._decimals,this._supportedRange[1]),this._onChange(this._value))}decrement(){this._value&&this._value>this._supportedRange[0]&&(this._value=Math.max(Math.round((this._value-this._step)*10**this._decimals)/10**this._decimals,this._supportedRange[0]),this._onChange(this._value))}format(e){return e.toString()}clear(){this._value=null}};var la="";function ng(i){la=i}var Wh=i=>{if("blob"in i&&i.blob.type)return i.blob.type;if(i.as==="script")return"text/javascript";if(i.as==="link"&&"url"in i){let e=i.url.toLowerCase();if(e.endsWith(".css"))return"text/css";if([".js",".mjs",".cjs"].some(t=>e.endsWith(t)))return"text/javascript"}},Bh=(i,e)=>{e.attributes&&Object.entries(e.attributes).forEach(([t,o])=>{t==="type"||t==="rel"||t==="href"||t==="src"||o!=null&&(typeof o=="boolean"?o&&i.setAttribute(t,""):i.setAttribute(t,o))})},ag=(i,e,t)=>{let o=i.createElement("script");o.setAttribute("nonce",la),o.dataset.readium="true",e.id&&(o.id=e.id);let r=e.type||Wh(e);return r&&(o.type=r),Bh(o,e),o.src=t,o},Hh=(i,e,t)=>{let o=i.createElement("link");o.dataset.readium="true",e.id&&(o.id=e.id),e.rel&&(o.rel=e.rel);let r=e.type||Wh(e);return r&&(o.type=r),Bh(o,e),o.href=t,o},rr=class{constructor(e){this.blobStore=new Map,this.createdBlobUrls=new Set,this.allowedDomains=[],this.injectableIdCounter=0,this.allowedDomains=(e.allowedDomains||[]).map(t=>{try{return new URL(t),t}catch{throw new Error(`Invalid allowed domain: "${t}". Must be a valid URL (e.g., "https://fonts.googleapis.com").`)}}),this.rules=e.rules.map(t=>{let o={...t};return t.prepend&&(o.prepend=t.prepend.map(r=>({...r,id:r.id||`injectable-${this.injectableIdCounter++}`})).reverse()),t.append&&(o.append=t.append.map(r=>({...r,id:r.id||`injectable-${this.injectableIdCounter++}`}))),o})}dispose(){for(let e of this.createdBlobUrls)try{URL.revokeObjectURL(e)}catch(t){console.warn("Failed to revoke blob URL:",e,t)}this.createdBlobUrls.clear()}getAllowedDomains(){return[...this.allowedDomains]}async injectForDocument(e,t){for(let o of this.rules)this.matchesRule(o,t)&&await this.applyRule(e,o)}matchesRule(e,t){let o=t.href;return e.resources.some(r=>r instanceof RegExp?r.test(o):o===r)}async getOrCreateBlobUrl(e){let t=e.id;if(this.blobStore.has(t)){let o=this.blobStore.get(t);return o.refCount++,o.url}if("blob"in e){let o=URL.createObjectURL(e.blob);return this.blobStore.set(t,{url:o,refCount:1}),this.createdBlobUrls.add(o),o}throw new Error("Resource must have a blob property")}async releaseBlobUrl(e){if(!this.createdBlobUrls.has(e))return;let t=Array.from(this.blobStore.values()).find(o=>o.url===e);if(t&&(t.refCount--,t.refCount<=0)){URL.revokeObjectURL(e),this.createdBlobUrls.delete(e);for(let[o,r]of this.blobStore.entries())if(r.url===e){this.blobStore.delete(o);break}}}async getResourceUrl(e,t){if("url"in e){let o=new URL(e.url,t.baseURI).toString();if(!this.isValidUrl(o,t))throw new Error(`Invalid URL: Only HTTPS, data:, blob:, or localhost HTTP URLs are allowed. Got: ${e.url}`);return o}else return this.getOrCreateBlobUrl(e)}createPreloadLink(e,t,o){if(t.as!=="link"||t.rel!=="preload")return;let r={...t,rel:"preload",attributes:{...t.attributes,as:t.as}},n=Hh(e,r,o);e.head.appendChild(n)}createElement(e,t,o){if(t.as==="script")return ag(e,t,o);if(t.as==="link")return Hh(e,t,o);throw new Error(`Unsupported element type: ${t.as}`)}async applyRule(e,t){let o=[],r=t.prepend?t.prepend.filter(a=>!a.condition||a.condition(e)):[],n=t.append?t.append.filter(a=>!a.condition||a.condition(e)):[];try{for(let a of r)await this.processInjectable(a,e,o,"prepend");for(let a of n)await this.processInjectable(a,e,o,"append")}catch(a){for(let{element:s,url:c}of o)try{s.remove(),await this.releaseBlobUrl(c)}catch(l){console.error("Error during cleanup:",l)}throw a}}async processInjectable(e,t,o,r){let n=e.target==="body"?t.body:t.head;if(!n)return;let a=null;try{if(a=await this.getResourceUrl(e,t),e.rel==="preload"&&"url"in e)this.createPreloadLink(t,e,a);else{let s=this.createElement(t,e,a);o.push({element:s,url:a}),r==="prepend"?n.prepend(s):n.append(s)}}catch(s){throw console.error("Failed to process resource:",s),a&&"blob"in e&&await this.releaseBlobUrl(a),s}}isValidUrl(e,t){try{let o=new URL(e,t.baseURI);if(o.protocol==="data:"||o.protocol==="blob:"&&this.createdBlobUrls.has(e))return!0;if(this.allowedDomains.length>0){let r=o.origin;return this.allowedDomains.some(n=>{let a=new URL(n).origin;return r===a})}return!1}catch{return!1}}};var nr=i=>i.replace(/\/\/.*/g,"").replace(/\/\*[\s\S]*?\*\//g,"").replace(/\n/g,"").replace(/\s+/g," "),to=i=>i.replace(/\/\*(?:(?!\*\/)[\s\S])*\*\/|[\r\n\t]+/g,"").replace(/ {2,}/g," ");var jh='!function(t,e){"object"==typeof exports&&"object"==typeof module?module.exports=e():"function"==typeof define&&define.amd?define([],e):"object"==typeof exports?exports._readium_cssSelectorGenerator=e():t._readium_cssSelectorGenerator=e()}(self,(()=>(()=>{"use strict";var t={d:(e,n)=>{for(var o in n)t.o(n,o)&&!t.o(e,o)&&Object.defineProperty(e,o,{enumerable:!0,get:n[o]})},o:(t,e)=>Object.prototype.hasOwnProperty.call(t,e),r:t=>{"undefined"!=typeof Symbol&&Symbol.toStringTag&&Object.defineProperty(t,Symbol.toStringTag,{value:"Module"}),Object.defineProperty(t,"__esModule",{value:!0})}},e={};function n(t){return"object"==typeof t&&null!==t&&t.nodeType===Node.ELEMENT_NODE}t.r(e),t.d(e,{_readium_cssSelectorGenerator:()=>Z,default:()=>tt,getCssSelector:()=>X});const o={NONE:"",DESCENDANT:" ",CHILD:" > "},r={id:"id",class:"class",tag:"tag",attribute:"attribute",nthchild:"nthchild",nthoftype:"nthoftype"},i="_readium_cssSelectorGenerator";function c(t="unknown problem",...e){console.warn(`${i}: ${t}`,...e)}const s={selectors:[r.id,r.class,r.tag,r.attribute],includeTag:!1,whitelist:[],blacklist:[],combineWithinSelector:!0,combineBetweenSelectors:!0,root:null,maxCombinations:Number.POSITIVE_INFINITY,maxCandidates:Number.POSITIVE_INFINITY,useScope:!1};function u(t){return t instanceof RegExp}function l(t){return["string","function"].includes(typeof t)||u(t)}function a(t){return Array.isArray(t)?t.filter(l):[]}function f(t){const e=[Node.DOCUMENT_NODE,Node.DOCUMENT_FRAGMENT_NODE,Node.ELEMENT_NODE];return function(t){return t instanceof Node}(t)&&e.includes(t.nodeType)}function d(t,e){if(f(t))return t.contains(e)||c("element root mismatch","Provided root does not contain the element. This will most likely result in producing a fallback selector using element\'s real root node. If you plan to use the selector using provided root (e.g. `root.querySelector`), it will not work as intended."),t;const n=e.getRootNode({composed:!1});return f(n)?(n!==document&&c("shadow root inferred","You did not provide a root and the element is a child of Shadow DOM. This will produce a selector using ShadowRoot as a root. If you plan to use the selector using document as a root (e.g. `document.querySelector`), it will not work as intended."),n):S(e)}function m(t){return"number"==typeof t?t:Number.POSITIVE_INFINITY}function p(t=[]){const[e=[],...n]=t;return 0===n.length?e:n.reduce(((t,e)=>t.filter((t=>e.includes(t)))),e)}function g(t){const e=t.map((t=>{if(u(t))return e=>t.test(e);if("function"==typeof t)return e=>{const n=t(e);return"boolean"!=typeof n?(c("pattern matcher function invalid","Provided pattern matching function does not return boolean. It\'s result will be ignored.",t),!1):n};if("string"==typeof t){const e=new RegExp("^"+t.replace(/[|\\\\{}()[\\]^$+?.]/g,"\\\\$&").replace(/\\*/g,".+")+"$");return t=>e.test(t)}return c("pattern matcher invalid","Pattern matching only accepts strings, regular expressions and/or functions. This item is invalid and will be ignored.",t),()=>!1}));return t=>e.some((e=>e(t)))}function h(t,e,n){const o=Array.from(d(n,t[0]).querySelectorAll(e));return o.length===t.length&&t.every((t=>o.includes(t)))}function y(t,e){e=null!=e?e:S(t);const o=[];let r=t;for(;n(r)&&r!==e;)o.push(r),r=r.parentElement;return o}function b(t,e){return p(t.map((t=>y(t,e))))}function S(t){return t.ownerDocument.querySelector(":root")}const N=", ",v=new RegExp(["^$","\\\\s"].join("|")),E=new RegExp(["^$"].join("|")),x=[r.nthoftype,r.tag,r.id,r.class,r.attribute,r.nthchild],w=g(["class","id","ng-*"]);function I({name:t}){return`[${t}]`}function T({name:t,value:e}){return`[${t}=\'${e}\']`}function O({nodeName:t,nodeValue:e}){return{name:F(t),value:F(null!=e?e:void 0)}}function C(t){const e=Array.from(t.attributes).filter((e=>function({nodeName:t,nodeValue:e},n){const o=n.tagName.toLowerCase();return!(["input","option"].includes(o)&&"value"===t||"src"===t&&(null==e?void 0:e.startsWith("data:"))||w(t))}(e,t))).map(O);return[...e.map(I),...e.map(T)]}function j(t){var e;return(null!==(e=t.getAttribute("class"))&&void 0!==e?e:"").trim().split(/\\s+/).filter((t=>!E.test(t))).map((t=>`.${F(t)}`))}function A(t){var e;const n=null!==(e=t.getAttribute("id"))&&void 0!==e?e:"",o=`#${F(n)}`,r=t.getRootNode({composed:!1});return!v.test(n)&&h([t],o,r)?[o]:[]}function R(t){var e;const n=null===(e=t.parentElement)||void 0===e?void 0:e.children;if(n)for(let e=0;e<n.length;e++)if(n[e]===t)return[`:nth-child(${String(e+1)})`];return[]}function $(t){return[F(t.tagName.toLowerCase())]}function D(t){const e=[...new Set((n=t.map($),[].concat(...n)))];var n;return 0===e.length||e.length>1?[]:[e[0]]}function k(t){const e=D([t])[0],n=t.parentElement;if(n){const o=Array.from(n.children).filter((t=>t.tagName.toLowerCase()===e)),r=o.indexOf(t);if(r>-1)return[`${e}:nth-of-type(${String(r+1)})`]}return[]}function*P(t=[],{maxResults:e=Number.POSITIVE_INFINITY}={}){let n=0,o=L(1);for(;o.length<=t.length&&n<e;){n+=1;const e=o.map((e=>t[e]));yield e,o=_(o,t.length-1)}}function _(t=[],e=0){const n=t.length;if(0===n)return[];const o=[...t];o[n-1]+=1;for(let t=n-1;t>=0;t--)if(o[t]>e){if(0===t)return L(n+1);o[t-1]++,o[t]=o[t-1]+1}return o[n-1]>e?L(n+1):o}function L(t=1){return Array.from(Array(t).keys())}const M=":".charCodeAt(0).toString(16).toUpperCase(),V=/[ !"#$%&\'()\\[\\]{|}<>*+,./;=?@^`~\\\\]/;function F(t=""){return CSS?CSS.escape(t):function(t=""){return t.split("").map((t=>":"===t?`\\\\${M} `:V.test(t)?`\\\\${t}`:escape(t).replace(/%/g,"\\\\"))).join("")}(t)}const Y={tag:D,id:function(t){return 0===t.length||t.length>1?[]:A(t[0])},class:function(t){return p(t.map(j))},attribute:function(t){return p(t.map(C))},nthchild:function(t){return p(t.map(R))},nthoftype:function(t){return p(t.map(k))}},G={tag:$,id:A,class:j,attribute:C,nthchild:R,nthoftype:k};function W(t){return t.includes(r.tag)||t.includes(r.nthoftype)?[...t]:[...t,r.tag]}function*q(t,e){const n={};for(const o of t){const t=e[o];t&&t.length>0&&(n[o]=t)}for(const t of function*(t={}){const e=Object.entries(t);if(0===e.length)return;const n=[{index:e.length-1,partial:{}}];for(;n.length>0;){const t=n.pop();if(!t)break;const{index:o,partial:r}=t;if(o<0){yield r;continue}const[i,c]=e[o];for(let t=c.length-1;t>=0;t--)n.push({index:o-1,partial:Object.assign(Object.assign({},r),{[i]:c[t]})})}}(n))yield B(t)}function B(t={}){const e=[...x];return t[r.tag]&&t[r.nthoftype]&&e.splice(e.indexOf(r.tag),1),e.map((e=>{return(o=t)[n=e]?o[n].join(""):"";var n,o})).join("")}function H(t,e){return[...t.map((t=>e+o.DESCENDANT+t)),...t.map((t=>e+o.CHILD+t))]}function*U(t,e,n="",o){const r=function*(t,e){const n=new Set,o=function(t,e){const{blacklist:n,whitelist:o,combineWithinSelector:r,maxCombinations:i}=e,c=g(n),s=g(o);return function(t){const{selectors:e,includeTag:n}=t,o=[...e];return n&&!o.includes("tag")&&o.push("tag"),o}(e).reduce(((e,n)=>{const o=function(t,e){return(0,Y[e])(t)}(t,n),u=function(t=[],e,n){return t.filter((t=>n(t)||!e(t)))}(o,c,s),l=function(t=[],e){return t.sort(((t,n)=>{const o=e(t),r=e(n);return o&&!r?-1:!o&&r?1:0}))}(u,s);return e[n]=r?Array.from(P(l,{maxResults:i})):l.map((t=>[t])),e}),{})}(t,e);for(const t of function*(t,e){for(const n of function(t){const{selectors:e,combineBetweenSelectors:n,includeTag:o,maxCandidates:r}=t,i=n?function(t=[],{maxResults:e=Number.POSITIVE_INFINITY}={}){return Array.from(P(t,{maxResults:e}))}(e,{maxResults:r}):e.map((t=>[t]));return o?i.map(W):i}(e))yield*q(n,t)}(o,e))n.has(t)||(n.add(t),yield t)}(t,o);for(const o of function*(t,e){if(""===e)yield*t;else for(const n of t)yield*H([n],e)}(r,n))h(t,o,e)&&(yield o)}function*z(t,e,n="",o){if(0===t.length)return null;const r=[t.length>1?t:[],...b(t,e).map((t=>[t]))];for(const t of r)for(const r of U(t,e,n,o))yield{foundElements:t,selector:r}}function J(t){return{value:t,include:!1}}function K({selectors:t,operator:e}){let n=[...x];t[r.tag]&&t[r.nthoftype]&&(n=n.filter((t=>t!==r.tag)));let o="";return n.forEach((e=>{var n;(null!==(n=t[e])&&void 0!==n?n:[]).forEach((({value:t,include:e})=>{e&&(o+=t)}))})),e+o}function Q(t,e){return t.map((t=>function(t,e){return[e?":scope":":root",...y(t,e).reverse().map((t=>{var e;const n=function(t,e,n=o.NONE){const r={};return e.forEach((e=>{Reflect.set(r,e,function(t,e){return G[e](t)}(t,e).map(J))})),{element:t,operator:n,selectors:r}}(t,[r.nthchild],o.CHILD);return(null!==(e=n.selectors.nthchild)&&void 0!==e?e:[]).forEach((t=>{t.include=!0})),n})).map(K)].join("")}(t,e))).join(N)}function X(t,e={}){return Z(t,Object.assign(Object.assign({},e),{maxResults:1})).next().value}function*Z(t,e={}){var o;const i=function(t){(t instanceof NodeList||t instanceof HTMLCollection)&&(t=Array.from(t));const e=(Array.isArray(t)?t:[t]).filter(n);return[...new Set(e)]}(t),c=function(t,e={}){const n=Object.assign(Object.assign({},s),e);return{selectors:(o=n.selectors,Array.isArray(o)?o.filter((t=>{return e=r,n=t,Object.values(e).includes(n);var e,n})):[]),whitelist:a(n.whitelist),blacklist:a(n.blacklist),root:d(n.root,t),combineWithinSelector:!!n.combineWithinSelector,combineBetweenSelectors:!!n.combineBetweenSelectors,includeTag:!!n.includeTag,maxCombinations:m(n.maxCombinations),maxCandidates:m(n.maxCandidates),useScope:!!n.useScope,maxResults:m(n.maxResults)};var o}(i[0],e),u=null!==(o=c.root)&&void 0!==o?o:S(i[0]);let l=0;for(const t of function*({elements:t,root:e,rootSelector:n="",options:o}){let r=e,i=n,c=!0;for(;c;){let n=!1;for(const c of z(t,r,i,o)){const{foundElements:o,selector:s}=c;if(n=!0,!h(t,s,e)){r=o[0],i=s;break}yield s}n||(c=!1)}}({elements:i,options:c,root:u,rootSelector:""}))if(yield t,l++,l>=c.maxResults)return;i.length>1&&(yield i.map((t=>X(t,c))).join(N),l++,l>=c.maxResults)||(yield Q(i,c.useScope?u:void 0))}const tt=X;return e})()));';var Gh=`(function() {
    if(window.onload) window.onload = new Proxy(window.onload, {
        apply: function(target, receiver, args) {
            if(!window._readium_blockEvents) {
                Reflect.apply(target, receiver, args);
                return;
            }
            _readium_blockedEvents.push([
                0, target, receiver, args
            ]);
        }
    });
})();
`;var ar=class{constructor(e){if(this.detectedTools=new Set,!e.onDetected)throw new Error("onDetected callback is required");this.options=e,this.setupDetection()}isAutomationToolPresent(){let e=window;return e.domAutomation||e.domAutomationController?"Selenium":navigator.webdriver===!0?"Puppeteer/Playwright":e.__webdriver_evaluate||e.__selenium_evaluate?"Chrome Automation":e.callPhantom||e._phantom?"PhantomJS":e.__nightmare?"Nightmare":e.$testCafe?"TestCafe":null}setupDetection(){let e=this.isAutomationToolPresent();if(e){this.handleDetected(e);return}this.observer=new MutationObserver(()=>{let t=this.isAutomationToolPresent();t&&!this.detectedTools.has(t)&&this.handleDetected(t)}),this.observer.observe(document.documentElement,{childList:!0,subtree:!0,attributes:!0}),window.addEventListener("unload",()=>this.destroy())}handleDetected(e){this.detectedTools.add(e),this.options.onDetected?.(e)}destroy(){this.observer?.disconnect(),this.observer=void 0,this.detectedTools.clear()}};var sg=0;function lg(){return++sg}var cg=`
onmessage = function(event) {
  var action = event.data;
  var startTime = performance.now()

  console[action.type](...action.payload);
  postMessage({
    id: action.id,
    time: performance.now() - startTime
  })
}
`,Vh=class{constructor(e,t){this.callbacks=new Map,this.worker=e,this.blobUrl=t,this.worker.onmessage=o=>{let r=o.data,n=r.id,a=this.callbacks.get(r.id);a&&(a({time:r.time}),this.callbacks.delete(n))},this.log=(...o)=>this.send("log",...o),this.table=(...o)=>this.send("table",...o),this.clear=(...o)=>this.send("clear",...o)}async send(e,...t){let o=lg();return new Promise((r,n)=>{this.callbacks.set(o,r),this.worker.postMessage({id:o,type:e,payload:t}),setTimeout(()=>{n(new Error("timeout")),this.callbacks.delete(o)},2e3)})}destroy(){this.worker.terminate(),URL.revokeObjectURL(this.blobUrl)}};Vh.workerScript=cg;var ca=Vh;function ha(i){return typeof window<"u"&&console?console[i]:(...e)=>{}}var Kh=ha("log"),da=ha("table"),Yh=ha("clear");async function ua(){if(typeof navigator<"u"&&navigator.brave&&navigator.brave.isBrave)try{return await Promise.race([navigator.brave.isBrave(),new Promise(i=>setTimeout(()=>i(!1),1e3))])}catch{return!0}return!1}function Xh(i){return i.excludes.some(e=>e())?!1:i.includes.some(e=>e())}var sr=class{constructor(e={}){if(this.isOpen=!1,this.checkCount=0,this.maxChecks=10,this.maxPrintTime=0,this.largeObjectArray=null,this.options={onDetected:e.onDetected||(()=>{}),onClosed:e.onClosed||(()=>{}),interval:e.interval||1e3,enableDebuggerDetection:e.enableDebuggerDetection||!1},!Be.UA.Firefox)try{let t=new Blob([ca.workerScript],{type:"application/javascript"}),o=URL.createObjectURL(t),r=new Worker(o);this.workerConsole=new ca(r,o)}catch(t){console.warn("Failed to create Web Worker for DevTools detection:",t)}this.startDetection()}createLargeObjectArray(){let e={};for(let o=0;o<500;o++)e[`${o}`]=`${o}`;let t=[];for(let o=0;o<50;o++)t.push(e);return t}getLargeObjectArray(){return this.largeObjectArray===null&&(this.largeObjectArray=this.createLargeObjectArray()),this.largeObjectArray}async calcTablePrintTime(){let e=this.getLargeObjectArray();if(this.workerConsole)try{return(await this.workerConsole.table(e)).time}catch{let t=performance.now();return da(e),performance.now()-t}else{let t=performance.now();return da(e),performance.now()-t}}async calcLogPrintTime(){let e=this.getLargeObjectArray();if(this.workerConsole)return(await this.workerConsole.log(e)).time;{let t=performance.now();return Kh(e),performance.now()-t}}isPerformanceDetectionEnabled(){return Xh({includes:[()=>!!Be.UA.Chrome,()=>!!Be.UA.Chromium,()=>!!Be.UA.Safari,()=>!!Be.UA.Firefox],excludes:[]})}isDebuggerDetectionEnabled(){return this.options.enableDebuggerDetection}async checkPerformanceBased(){if(!this.isPerformanceDetectionEnabled())return!1;let e=await this.calcTablePrintTime(),t=Math.max(await this.calcLogPrintTime(),await this.calcLogPrintTime());return this.maxPrintTime=Math.max(this.maxPrintTime,t),this.workerConsole?await this.workerConsole.clear():Yh(),e===0?!1:this.maxPrintTime===0?!!await ua():e>this.maxPrintTime*10}async checkDebuggerBased(){if(!this.isDebuggerDetectionEnabled()||await ua())return!1;let e=performance.now();try{(()=>{}).constructor("debugger")()}catch{debugger}return performance.now()-e>100}async detectDevTools(){return await this.checkPerformanceBased()?!0:this.options.enableDebuggerDetection&&this.checkCount>=this.maxChecks?await this.checkDebuggerBased():!1}startDetection(){this.intervalId=window.setInterval(async()=>{this.checkCount++;let e=await this.detectDevTools();e!==this.isOpen&&(this.isOpen=e,e?this.options.onDetected():this.options.onClosed()),this.checkCount>this.maxChecks*2&&(this.checkCount=0)},this.options.interval),window.addEventListener("beforeunload",()=>this.destroy())}isDevToolsOpen(){return this.isOpen}async checkNow(){let e=this.isOpen;return this.isOpen=await this.detectDevTools(),this.isOpen!==e&&(this.isOpen?this.options.onDetected():this.options.onClosed()),this.isOpen}destroy(){this.intervalId&&(clearInterval(this.intervalId),this.intervalId=void 0),this.workerConsole&&(this.workerConsole.destroy(),this.workerConsole=void 0),this.isOpen=!1,this.checkCount=0}};var lr=class{constructor(e){if(this.detected=!1,!e.onDetected)throw new Error("onDetected callback is required");this.options=e,this.setupDetection()}isIframed(){try{return window.self!==window.top?{isEmbedded:!0,isCrossOrigin:!window.top.location.href}:{isEmbedded:!1,isCrossOrigin:!1}}catch{return{isEmbedded:!0,isCrossOrigin:!0}}}setupDetection(){let{isEmbedded:e,isCrossOrigin:t}=this.isIframed();if(e){this.handleDetected(t);return}this.observer=new MutationObserver(()=>{let{isEmbedded:o,isCrossOrigin:r}=this.isIframed();o&&!this.detected&&(this.handleDetected(r),this.observer?.disconnect())}),this.observer.observe(document.documentElement,{childList:!0,subtree:!0,attributes:!0}),window.addEventListener("unload",()=>this.destroy())}handleDetected(e){this.detected=!0,this.options.onDetected?.(e)}destroy(){this.observer?.disconnect(),this.observer=void 0,this.detected=!1}};var cr=class{constructor(e={}){this.styleElement=null,this.beforePrintHandler=null,this.onPrintAttempt=e.onPrintAttempt,e.disable&&this.setupPrintProtection(e.watermark)}setupPrintProtection(e){let t=document.createElement("style");t.textContent=`
            @media print {
                body * {
                    display: none !important;
                }
                body::after {
                    content: "${e||"Printing has been disabled"}";
                    font-size: 200%;
                    display: block;
                    text-align: center;
                    margin-top: 50vh;
                    transform: translateY(-50%);
                }
            }
        `,document.head.appendChild(t),this.styleElement=t,this.beforePrintHandler=o=>(o.preventDefault(),this.onPrintAttempt?.(),!1),window.addEventListener("beforeprint",this.beforePrintHandler)}destroy(){this.beforePrintHandler&&(window.removeEventListener("beforeprint",this.beforePrintHandler),this.beforePrintHandler=null),this.styleElement?.parentNode&&(this.styleElement.parentNode.removeChild(this.styleElement),this.styleElement=null)}};var hr=class{constructor(e={}){this.onContextMenuBlocked=e.onContextMenuBlocked,this.contextMenuHandler=this.handleContextMenu.bind(this),document.addEventListener("contextmenu",this.contextMenuHandler,!0),window.addEventListener("unload",()=>this.destroy())}handleContextMenu(e){e.preventDefault(),e.stopPropagation();let t={type:"context_menu",timestamp:Date.now(),clientX:e.clientX,clientY:e.clientY,targetFrameSrc:""};return this.onContextMenuBlocked&&this.onContextMenuBlocked(t),!1}destroy(){this.contextMenuHandler&&(document.removeEventListener("contextmenu",this.contextMenuHandler,!0),this.contextMenuHandler=void 0)}};var ur="readium:navigator:suspiciousActivity",dr=class{dispatchSuspiciousActivity(e,t){let o=new CustomEvent(ur,{detail:{type:e,timestamp:Date.now(),...t}});window.dispatchEvent(o)}constructor(e={}){e.monitorDevTools&&(this.devToolsDetector=new sr({onDetected:()=>{this.dispatchSuspiciousActivity("developer_tools",{targetFrameSrc:"",key:"",code:"",keyCode:-1,ctrlKey:!1,altKey:!1,shiftKey:!1,metaKey:!1})}})),e.checkAutomation&&(this.automationDetector=new ar({onDetected:t=>{this.dispatchSuspiciousActivity("automation_detected",{tool:t})}})),e.checkIFrameEmbedding&&(this.iframeEmbeddingDetector=new lr({onDetected:t=>{this.dispatchSuspiciousActivity("iframe_embedding_detected",{isCrossOrigin:t})}})),e.protectPrinting?.disable&&(this.printProtector=new cr({...e.protectPrinting,onPrintAttempt:()=>{this.dispatchSuspiciousActivity("print",{})}})),e.disableContextMenu&&(this.contextMenuProtector=new hr({onContextMenuBlocked:t=>{this.dispatchSuspiciousActivity("context_menu",t)}}))}destroy(){this.automationDetector?.destroy(),this.devToolsDetector?.destroy(),this.iframeEmbeddingDetector?.destroy(),this.printProtector?.destroy(),this.contextMenuProtector?.destroy()}};var pr="readium:navigator:keyboardPeripheral",mr=class{constructor(e={}){this.keyManager=new Ut,this.setupKeyboardPeripherals(e.keyboardPeripherals||[])}setupKeyboardPeripherals(e){if(e.length>0){let t=o=>{let r=new CustomEvent(pr,{detail:o});window.dispatchEvent(r)};this.keydownHandler=this.keyManager.createUnifiedHandler("",e,t),this.keydownHandler&&document.addEventListener("keydown",this.keydownHandler,!0)}window.addEventListener("unload",()=>this.destroy())}destroy(){this.keydownHandler&&(document.removeEventListener("keydown",this.keydownHandler,!0),this.keydownHandler=void 0)}};var hg=i=>{let e=i.join(" ");return["upgrade-insecure-requests",`default-src ${e} blob:`,"connect-src 'none'",`script-src ${e} blob: 'unsafe-inline'`,`style-src ${e} blob: 'unsafe-inline'`,`img-src ${e} blob: data:`,`font-src ${e} blob: data:`,`object-src ${e} blob:`,`child-src ${e}`,"form-action 'none'"].join("; ")},_i=class{constructor(e,t,o,r){this.injector=null,this.pub=e,this.item=o,this.burl=o.toURL(t)||"",this.cssProperties=r.cssProperties,this.injector=r.injector??null}async build(e=!1){if(this.item.mediaType.isHTML)return await this.buildHtmlFrame(e);if(this.item.mediaType.isBitmap||this.item.mediaType.equals(Ce.SVG))return this.buildImageFrame();throw Error("Unsupported frame mediatype "+this.item.mediaType.string)}async buildHtmlFrame(e=!1){let t=await this.pub.get(this.item).readAsString();if(!t)throw new Error(`Failed reading item ${this.item.href}`);let o=new DOMParser().parseFromString(t,this.item.mediaType.string),r=o.querySelector("parsererror");if(r){let n=r.querySelector("div");throw new Error(`Failed parsing item ${this.item.href}: ${n?.textContent||r.textContent}`)}return this.injector&&await this.injector.injectForDocument(o,this.item),this.finalizeDOM(o,this.pub.baseURL,this.burl,this.item.mediaType,e,this.cssProperties)}buildImageFrame(){let e=document.implementation.createHTMLDocument(this.item.title||this.item.href),t=document.createElement("img");return t.src=this.burl||"",t.alt=this.item.title||"",t.decoding="async",e.body.appendChild(t),this.finalizeDOM(e,this.pub.baseURL,this.burl,this.item.mediaType,!0)}setProperties(e,t){for(let o in e){let r=e[o];r&&t.documentElement.style.setProperty(o,r)}}finalizeDOM(e,t,o,r,n=!1,a){if(!e)return"";let s=this.injector?.getAllowedDomains?.()||[],c=[...new Set([...t?[t]:[],...s])].filter(Boolean);if(a&&!n&&this.setProperties(a,e),e.body.querySelectorAll("img").forEach(h=>{h.setAttribute("fetchpriority","high")}),r.isHTML&&this.pub.metadata.languages?.[0]){let h=this.pub.metadata.languages[0];if(r===Ce.XHTML){let d=e.documentElement.lang||e.documentElement.getAttribute("xml:lang"),u=e.body.lang||e.body.getAttribute("xml:lang");u&&!d?(e.documentElement.lang=u,e.documentElement.setAttribute("xml:lang",u),e.body.removeAttribute("xml:lang"),e.body.removeAttribute("lang")):d||(e.documentElement.lang=h,e.documentElement.setAttribute("xml:lang",h))}else r===Ce.HTML&&!e.documentElement.lang&&(e.documentElement.lang=h)}if(xt(this.pub.metadata)==="rtl"&&!e.documentElement.dir&&!e.body.dir&&(e.documentElement.dir=L.rtl),o!==void 0){let h=e.createElement("base");h.href=o,h.dataset.readium="true",e.head.firstChild.before(h)}let l=e.createElement("meta");return l.httpEquiv="Content-Security-Policy",l.content=hg(c),l.dataset.readium="true",e.head.firstChild.before(l),URL.createObjectURL(new Blob([new XMLSerializer().serializeToString(e)],{type:r.isHTML?r.string:"application/xhtml+xml"}))}};var vi=class{constructor(e,t={},o=[],r=[]){this.timelineFragmentIds=r,this.hidden=!0,this.destroyed=!1,this.currModules=[],this.frame=document.createElement("iframe"),this.frame.sandbox.value="allow-same-origin allow-scripts",this.frame.classList.add("readium-navigator-iframe"),this.frame.style.visibility="hidden",this.frame.style.setProperty("aria-hidden","true"),this.frame.style.opacity="0",this.frame.style.position="absolute",this.frame.style.pointerEvents="none",this.frame.style.transition="visibility 0s, opacity 0.1s linear",this.source=e,this.contentProtectionConfig={...t},this.keyboardPeripheralsConfig=[...o]}async load(e){return new Promise((t,o)=>{if(this.loader){let r=this.frame.contentWindow;if([...this.currModules].sort().join("|")===[...e].sort().join("|")){try{t(r)}catch{}return}this.comms?.halt(),this.loader.destroy(),this.loader=new st(r,e),this.currModules=e,this.comms=void 0;try{t(r)}catch{}return}this.frame.onload=()=>{let r=this.frame.contentWindow;this.loader=new st(r,e),this.currModules=e;try{t(r)}catch{}},this.frame.onerror=r=>{try{o(r)}catch{}},this.frame.contentWindow.location.replace(this.source)})}applyContentProtection(){this.comms||this.comms.resume(),this.comms.send("peripherals_protection",this.contentProtectionConfig),this.keyboardPeripheralsConfig&&this.keyboardPeripheralsConfig.length>0&&(this.conditionBridge?.destroy(),this.conditionBridge=new bi(this.keyboardPeripheralsConfig,e=>{e.length>0&&this.comms.send("keyboard_peripherals",e)}),this.conditionBridge.setup()),this.contentProtectionConfig.monitorScrollingExperimental&&this.comms.send("scroll_protection",{}),this.contentProtectionConfig.protectPrinting?.disable&&this.comms.send("print_protection",this.contentProtectionConfig.protectPrinting)}async destroy(){this.conditionBridge?.destroy(),await this.hide(),this.loader?.destroy(),this.frame.remove(),this.destroyed=!0}async hide(){if(!this.destroyed){if(this.frame.style.visibility="hidden",this.frame.style.setProperty("aria-hidden","true"),this.frame.style.opacity="0",this.frame.style.pointerEvents="none",this.hidden=!0,this.frame.blur(),this.frame.parentElement)return this.comms===void 0||!this.comms.ready?void 0:new Promise((e,t)=>{this.comms?.send("unfocus",void 0,o=>{this.comms?.halt(),e()})});this.comms?.halt()}}async show(e){if(this.destroyed)throw Error("Trying to show frame when it doesn't exist");if(!this.frame.parentElement)throw Error("Trying to show frame that is not attached to the DOM");return this.comms?this.comms.resume():this.comms=new Wt(this.frame.contentWindow,this.source),new Promise((t,o)=>{this.comms?.send("activate",void 0,()=>{this.comms?.send("focus",void 0,()=>{this.applyContentProtection(),this.timelineFragmentIds.length>0&&this.comms?.send("timeline_entries",this.timelineFragmentIds);let r=()=>{this.frame.style.removeProperty("visibility"),this.frame.style.removeProperty("aria-hidden"),this.frame.style.removeProperty("opacity"),this.frame.style.removeProperty("pointer-events"),this.hidden=!1,Be.UA.WebKit&&this.comms?.send("force_webkit_recalc",void 0),t()};e!==void 0?this.comms?.send("go_progression",e,r):r()})})})}setCSSProperties(e){this.destroyed||!this.frame.contentWindow||(this.hidden&&(this.comms?this.comms?.resume():this.comms=new Wt(this.frame.contentWindow,this.source)),this.comms?.send("update_properties",e),this.hidden&&this.comms?.halt())}get iframe(){if(this.destroyed)throw Error("Trying to use frame when it doesn't exist");return this.frame}get realSize(){if(this.destroyed)throw Error("Trying to use frame client rect when it doesn't exist");return this.frame.getBoundingClientRect()}get isDestroyed(){return this.destroyed}get window(){if(this.destroyed||!this.frame.contentWindow)throw Error("Trying to use frame window when it doesn't exist");return this.frame.contentWindow}get atLeft(){return this.window.scrollX<5}get atRight(){return this.window.scrollX>this.window.document.scrollingElement.scrollWidth-this.window.innerWidth-5}get msg(){return this.comms}get ldr(){return this.loader}};var $h=5,qh=3,gr=class{constructor(e,t,o,r,n,a,s){this.pool=new Map,this.blobs=new Map,this.inprogress=new Map,this.pendingUpdates=new Map,this.injector=null,this.container=e,this.positions=t,this.currentCssProperties=o,this.injector=r??null,this.contentProtectionConfig=n||{},this.keyboardPeripheralsConfig=a||[],this.getFragmentIds=s??(()=>[])}async destroy(){let e=this.inprogress.values(),t=e.next(),o=[];for(;t.value;)o.push(t.value),t=e.next();o.length>0&&await Promise.allSettled(o),this.inprogress.clear();let r=this.pool.values(),n=r.next();for(;n.value;)await n.value.destroy(),n=r.next();this.pool.clear(),this.blobs.forEach(a=>{this.injector?.releaseBlobUrl?.(a),URL.revokeObjectURL(a)}),this.injector?.dispose(),this.container.childNodes.forEach(a=>{(a.nodeType===Node.ELEMENT_NODE||a.nodeType===Node.TEXT_NODE)&&a.remove()})}async update(e,t,o,r=!1){let n=this.positions.findIndex(c=>c.locations.position===t.locations.position);if(n<0)throw Error(`Locator not found in position list: ${t.locations.position} > ${this.positions.reduce((c,l)=>l.locations.position||0>c?l.locations.position||0:c,0)}`);let a=this.positions[n].href;this.inprogress.has(a)&&await this.inprogress.get(a);let s=new Promise(async(c,l)=>{let h=[],d=[];this.positions.forEach((p,g)=>{(g>n+$h||g<n-$h)&&(h.includes(p.href)||h.push(p.href)),g<n+qh&&g>n-qh&&(d.includes(p.href)||d.push(p.href))}),h.forEach(async p=>{d.includes(p)||this.pool.has(p)&&(await this.pool.get(p)?.destroy(),this.pool.delete(p),this.pendingUpdates.has(p)&&this.pendingUpdates.set(p,{inPool:!1}))}),this.currentBaseURL!==void 0&&e.baseURL!==this.currentBaseURL&&(this.blobs.forEach(p=>{this.injector?.releaseBlobUrl?.(p),URL.revokeObjectURL(p)}),this.blobs.clear()),this.currentBaseURL=e.baseURL;let u=async p=>{if(r&&(this.blobs.forEach(y=>{this.injector?.releaseBlobUrl?.(y),URL.revokeObjectURL(y)}),this.blobs.clear(),this.pendingUpdates.clear()),this.pendingUpdates.has(p)&&this.pendingUpdates.get(p)?.inPool===!1){let y=this.blobs.get(p);y&&(this.injector?.releaseBlobUrl?.(y),URL.revokeObjectURL(y),this.blobs.delete(p),this.pendingUpdates.delete(p))}if(this.pool.has(p)){let y=this.pool.get(p);if(!this.blobs.has(p))await y.destroy(),this.pool.delete(p),this.pendingUpdates.delete(p);else{await y.load(o);return}}let g=e.readingOrder.findWithHref(p);if(!g)return;if(!this.blobs.has(p)){let y=await new _i(e,this.currentBaseURL||"",g,{cssProperties:this.currentCssProperties,injector:this.injector}).build();this.blobs.set(p,y)}let f=new vi(this.blobs.get(p),this.contentProtectionConfig,this.keyboardPeripheralsConfig,this.getFragmentIds(p));p!==a&&await f.hide(),this.container.appendChild(f.iframe),await f.load(o),this.pool.set(p,f)};try{await Promise.all(d.map(p=>u(p)))}catch(p){l(p)}let m=this.pool.get(a);if((m?.source!==this._currentFrame?.source||r)&&(await this._currentFrame?.hide(),m&&await m.load(o),m&&await m.show(t.locations.progression),this._currentFrame=m,m)){let p=this.container.ownerDocument.activeElement;p&&p.tagName==="IFRAME"&&p!==m.iframe&&m.iframe.focus({preventScroll:!0})}c()});this.inprogress.set(a,s),await s,this.inprogress.delete(a)}setCSSProperties(e){if(!((t,o)=>{let r=Object.keys(t),n=Object.keys(o);if(r.length!==n.length)return!1;for(let a of r)if(t[a]!==o[a])return!1;return!0})(this.currentCssProperties||{},e)){this.currentCssProperties=e,this.pool.forEach(t=>{t.setCSSProperties(e)});for(let t of this.blobs.keys())this.pendingUpdates.set(t,{inPool:this.pool.has(t)})}}get currentFrames(){return[this._currentFrame]}get currentBounds(){let e={x:0,y:0,width:0,height:0,top:0,right:0,bottom:0,left:0,toJSON(){return this}};return this.currentFrames.forEach(t=>{if(!t)return;let o=t.realSize;e.x=Math.min(e.x,o.x),e.y=Math.min(e.y,o.y),e.width+=o.width,e.height=Math.max(e.height,o.height),e.top=Math.min(e.top,o.top),e.right=Math.min(e.right,o.right),e.bottom=Math.min(e.bottom,o.bottom),e.left=Math.min(e.left,o.left)}),e}};var fr=class{constructor(e,t,o,r={},n=[]){this.currModules=[],this.cachedPage=void 0,this.peripherals=e,this.debugHref=o,this.contentProtectionConfig={...r},this.keyboardPeripheralsConfig=[...n],this.frame=document.createElement("iframe"),this.frame.sandbox.value="allow-same-origin allow-scripts",this.frame.classList.add("readium-navigator-iframe"),this.frame.classList.add("blank"),this.frame.scrolling="no",this.frame.style.visibility="hidden",this.frame.style.setProperty("aria-hidden","true"),this.frame.style.display="none",this.frame.style.position="absolute",this.frame.style.pointerEvents="none",this.frame.style.transformOrigin="0 0",this.frame.style.transform="scale(1)",this.frame.style.background="#fff",this.frame.style.touchAction="none",this.frame.dataset.originalHref=o,this.source="about:blank",this.wrapper=document.createElement("div"),this.wrapper.style.position="relative",this.wrapper.style.float=this.wrapper.style.cssFloat=t===L.rtl?"right":"left",this.wrapper.appendChild(this.frame)}async load(e,t){return this.source===t&&this.loadPromise&&[...this.currModules].sort().join("|")===[...e].sort().join("|")?this.loadPromise:(this.loaded&&this.source!==t&&this.window.stop(),this.source=t,this.loadPromise=new Promise((o,r)=>{if(this.loader&&this.loaded){let n=this.frame.contentWindow;if([...this.currModules].sort().join("|")===[...e].sort().join("|")){try{o(n),this.loadPromise=void 0}catch{}return}this.comms?.halt(),this.loader.destroy(),this.loader=new st(n,e),this.currModules=e,this.comms=void 0;try{o(n),this.loadPromise=void 0}catch{}return}this.frame.addEventListener("load",()=>{let n=this.frame.contentWindow;this.loader=new st(n,e),this.currModules=e,this.peripherals.observe(this.wrapper),this.peripherals.observe(n);try{o(n)}catch{}},{once:!0}),this.frame.addEventListener("error",n=>{try{r(n.error),this.loadPromise=void 0}catch{}},{once:!0}),this.frame.style.removeProperty("display"),this.frame.contentWindow.location.replace(this.source)}),this.loadPromise)}loadPageSize(){let e=this.frame.contentWindow,t=e.document.head.querySelector("meta[name=viewport]");if(t){let o=/(\w+) *= *([^\s,]+)/g,r,n=0,a=0;for(;r=o.exec(t.content);)r[1]==="width"?n=Number.parseFloat(r[2]):r[1]==="height"&&(a=Number.parseFloat(r[2]));if(n>0&&a>0)return{width:n,height:a}}return{width:e.document.body.scrollWidth,height:e.document.body.scrollHeight}}update(e){if(!this.loaded)return;let t=this.loadPageSize();this.frame.style.height=`${t.height}px`,this.frame.style.width=`${t.width}px`;let o=Math.min(this.wrapper.clientWidth/t.width,this.wrapper.clientHeight/t.height);this.frame.style.transform=`scale(${o})`;let r=this.frame.getBoundingClientRect(),n=this.wrapper.clientHeight-r.height;if(this.frame.style.top=`${n/2}px`,e===_e.left){let a=this.wrapper.clientWidth-r.width;this.frame.style.left=`${a}px`}else if(e===_e.center){let a=this.wrapper.clientWidth-r.width;this.frame.style.left=`${a/2}px`}else this.frame.style.left="0px";this.frame.style.removeProperty("visibility"),this.frame.style.removeProperty("aria-hidden"),this.frame.style.removeProperty("pointer-events"),this.frame.classList.remove("blank"),this.frame.classList.add("loaded")}async destroy(){this.conditionBridge?.destroy(),await this.unfocus(),this.loader?.destroy(),this.wrapper.remove()}async unload(){if(this.loaded)return this.deselect(),this.frame.style.visibility="hidden",this.frame.style.setProperty("aria-hidden","true"),this.frame.style.pointerEvents="none",this.frame.classList.add("blank"),this.frame.classList.remove("loaded"),this.comms?.halt(),this.loader?.destroy(),this.comms=void 0,this.frame.blur(),new Promise((e,t)=>{this.frame.addEventListener("load",()=>{try{this.showPromise=void 0,e()}catch{}},{once:!0}),this.frame.addEventListener("error",o=>{try{this.showPromise=void 0,t(o.error)}catch{}},{once:!0}),this.source="about:blank",this.frame.contentWindow.location.replace("about:blank"),this.frame.style.display="none"})}deselect(){this.frame.contentWindow?.getSelection()?.removeAllRanges()}async unfocus(){if(this.frame.parentElement)return this.comms===void 0?void 0:(this.frame.blur(),new Promise((e,t)=>{this.comms?.send("unfocus",void 0,o=>{this.comms?.halt(),this.showPromise=void 0,e()})}));this.comms?.halt()}applyContentProtection(){this.comms||this.comms.resume(),this.comms.send("peripherals_protection",this.contentProtectionConfig),this.keyboardPeripheralsConfig&&this.keyboardPeripheralsConfig.length>0&&(this.conditionBridge?.destroy(),this.conditionBridge=new bi(this.keyboardPeripheralsConfig,e=>{e.length>0&&this.comms.send("keyboard_peripherals",e)}),this.conditionBridge.setup()),this.contentProtectionConfig.protectPrinting?.disable&&this.comms.send("print_protection",this.contentProtectionConfig.protectPrinting)}async show(e){if(!this.frame.parentElement){console.warn("Trying to show frame that is not attached to the DOM");return}if(!this.loaded){this.showPromise=void 0;return}return this.showPromise?(this.cachedPage!==e&&(this.update(e),this.cachedPage=e),this.showPromise):(this.cachedPage=e,this.comms?this.comms.resume():this.comms=new Wt(this.frame.contentWindow,this.source),this.showPromise=new Promise((t,o)=>{this.comms.send("focus",void 0,r=>{this.update(this.cachedPage),this.applyContentProtection(),t()})}),this.showPromise)}async activate(){return new Promise((e,t)=>{if(!this.comms)return e();this.comms?.send("activate",void 0,()=>{e()})})}get element(){return this.wrapper}get iframe(){return this.frame}get realSize(){return this.frame.getBoundingClientRect()}get loaded(){return this.frame.contentWindow&&this.frame.contentWindow.location.href!=="about:blank"}set width(e){let t=`${e}%`;this.wrapper.style.width!==t&&(this.wrapper.style.width=t)}set height(e){let t=`${e}px`;this.wrapper.style.height!==t&&(this.wrapper.style.height=t)}get window(){if(!this.frame.contentWindow)throw Error("Trying to use frame window when it doesn't exist");return this.frame.contentWindow}get atLeft(){return this.window.scrollX<5}get atRight(){return this.window.scrollX>this.window.document.scrollingElement.scrollWidth-this.window.innerWidth-5}get msg(){return this.comms}get ldr(){return this.loader}};var dg=(i=>(i[i.Left=0]="Left",i[i.Center=1]="Center",i[i.Right=2]="Right",i))(dg||{}),ug=(i=>(i[i.Top=0]="Top",i[i.Middle=1]="Middle",i[i.Bottom=2]="Bottom",i))(ug||{}),yr=class{constructor(){this.outerWidth=0,this.outerHeight=0,this.HTML=document.documentElement,this.Head=document.head,this.Body=document.body}refreshOuterPixels(e){Be.OS.iOS||(this.outerHeight=window.outerHeight-window.innerHeight,Be.OS.Android&&Be.UA.Chrome&&window.screen.height>window.innerHeight&&(this.outerHeight=(window.screen.height-window.innerHeight)/1.5),this.outerWidth=window.outerWidth-window.innerWidth)}getBibiEventCoord(e,t=0){let o={X:0,Y:0};return/^touch/.test(e.type)?(o.X=e.touches[t].screenX,o.Y=e.touches[t].screenY):(o.X=e.screenX,o.Y=e.screenY),(e.target.ownerDocument?.documentElement||e.target.documentElement)===this.HTML&&(o.X-=this.HTML.scrollLeft+this.Body.scrollLeft,o.Y-=this.HTML.scrollTop+this.Body.scrollTop),o.X-=this.outerWidth,o.Y-=this.outerHeight,o}getTouchDistance(e){if(e.touches.length!==2)return 0;let t=e.touches[0].screenX-this.outerWidth,o=e.touches[0].screenY-this.outerHeight,r=e.touches[1].screenX-this.outerWidth,n=e.touches[1].screenY-this.outerHeight;return Math.sqrt(Math.pow(r-t,2)+Math.pow(n-o,2))}getTouchCenter(e){if(e.touches.length!==2)return null;let t=this.HTML.scrollLeft+this.Body.scrollLeft,o=this.HTML.scrollTop+this.Body.scrollTop,r=e.touches[0].screenX-this.outerWidth-t,n=e.touches[0].screenY-this.outerHeight-o,a=e.touches[1].screenX-this.outerWidth-t,s=e.touches[1].screenY-this.outerHeight-o;return{X:(r+a)/2,Y:(n+s)/2}}getBibiEvent(e){if(!e)return{Coord:null,Division:null,Ratio:null,Target:null};let t=this.getBibiEventCoord(e),o=.3,r={X:t.X/window.innerWidth,Y:t.Y/window.innerHeight},n,a,s,c;s=n=o,c=a=1-o;let l={X:null,Y:null};return r.X<s?l.X=0:c<r.X?l.X=2:l.X=1,r.Y<n?l.Y=0:a<r.Y?l.Y=2:l.Y=1,{Target:e.target,Coord:t,Ratio:r,Division:l}}};var Sr=class{constructor(){this._DOM={show:!1,pinchTarget:document.createElement("div"),touch1:document.createElement("div"),touch2:document.createElement("div"),center:document.createElement("div"),stats:document.createElement("div")},this._DOM.show=!0,this._DOM.pinchTarget.style.zIndex=this._DOM.stats.style.zIndex=this._DOM.center.style.zIndex=this._DOM.touch1.style.zIndex=this._DOM.touch2.style.zIndex="100000",this._DOM.pinchTarget.style.position=this._DOM.stats.style.position=this._DOM.center.style.position=this._DOM.touch1.style.position=this._DOM.touch2.style.position="absolute",this._DOM.pinchTarget.style.borderRadius=this._DOM.center.style.borderRadius=this._DOM.touch1.style.borderRadius=this._DOM.touch2.style.borderRadius="50%",this._DOM.pinchTarget.style.pointerEvents=this._DOM.stats.style.pointerEvents=this._DOM.center.style.pointerEvents=this._DOM.touch1.style.pointerEvents=this._DOM.touch2.style.pointerEvents="none",this._DOM.pinchTarget.style.display=this._DOM.center.style.display=this._DOM.touch1.style.display=this._DOM.touch2.style.display="none",this._DOM.pinchTarget.style.paddingTop=this._DOM.center.style.paddingTop="10px",this._DOM.pinchTarget.style.width=this._DOM.pinchTarget.style.height=this._DOM.center.style.width=this._DOM.center.style.height="10px",this._DOM.pinchTarget.style.backgroundColor="green",this._DOM.center.style.backgroundColor="red",this._DOM.touch1.style.backgroundColor=this._DOM.touch2.style.backgroundColor="blue",this._DOM.touch1.style.height=this._DOM.touch2.style.height="20px",this._DOM.touch1.style.width=this._DOM.touch2.style.width="20px",this._DOM.touch1.style.paddingTop=this._DOM.touch2.style.paddingTop="20px",this._DOM.touch1.textContent="1",this._DOM.touch2.textContent="2",this._DOM.stats.style.padding="20px",this._DOM.stats.style.backgroundColor="rgba(0,0,0,0.5)",this._DOM.stats.style.color="white",this._DOM.stats.textContent="[stats]",document.body.appendChild(this._DOM.stats),document.body.appendChild(this._DOM.center),document.body.appendChild(this._DOM.touch1),document.body.appendChild(this._DOM.touch2),document.body.appendChild(this._DOM.pinchTarget)}get show(){return this.DOM.show}get DOM(){return this._DOM}};var Jh=6,ma=1.02,Zh=50,br=class{constructor(e,t=!1){this.dragState=0,this.minimumMoved=!1,this.pan={startX:0,endX:0,startY:0,overscrollX:0,overscrollY:0,letItGo:!1,preventClick:!1,translateX:0,translateY:0,touchID:0},this.pinch={startDistance:0,startScale:0,target:{X:0,Y:0},touchN:0,startTranslate:{X:0,Y:0}},this._scale=1,this.scaleDebouncer=0,this.frameBounds=null,this.debugger=null,this.btouchstartHandler=this.touchstartHandler.bind(this),this.btouchendHandler=this.touchendHandler.bind(this),this.btouchmoveHandler=this.touchmoveHandler.bind(this),this.bdblclickHandler=this.dblclickHandler.bind(this),this.bmousedownHandler=this.mousedownHandler.bind(this),this.bmouseupHandler=this.mouseupHandler.bind(this),this.bmousemoveHandler=this.mousemoveHandler.bind(this),this.moveFrame=0,this.manager=e,this.coordinator=new yr,this.attachEvents(),t&&(this.debugger=new Sr)}get scale(){return this._scale}set scale(e){isNaN(e)&&(e=1),window.clearTimeout(this.scaleDebouncer),this.scaleDebouncer=window.setTimeout(()=>{this.dragState===0&&this.scale<ma&&(this.pan.translateX=0,this.pan.translateY=0,this.clearPan(),this.manager.updateBookStyle()),this.manager.listener("zoom",e)},100),this._scale=e}attachEvents(){this.observe(this.manager.spineElement),this.pan={startX:0,startY:0,endX:0,overscrollX:0,overscrollY:0,letItGo:!1,preventClick:!1,translateX:0,translateY:0,touchID:0},this.pinch={startDistance:0,startScale:0,target:{X:0,Y:0},startTranslate:{X:0,Y:0},touchN:0}}clearPan(){this.pan.letItGo=!1,this.pan.touchID=0,this.pan.endX=0,this.pan.overscrollX=0,this.pan.overscrollY=0}clearPinch(){this.pinch={startDistance:0,startScale:this.pinch.startScale,target:{X:0,Y:0},touchN:0,startTranslate:{X:0,Y:0}}}observe(e){e.addEventListener("touchstart",this.btouchstartHandler),e.addEventListener("touchend",this.btouchendHandler),e.addEventListener("touchmove",this.btouchmoveHandler,{passive:!0}),e.addEventListener("dblclick",this.bdblclickHandler,{passive:!0}),e.addEventListener("mousedown",this.bmousedownHandler),e.addEventListener("mouseup",this.bmouseupHandler),e.addEventListener("mousemove",this.bmousemoveHandler)}clickHandler(e){}touchstartHandler(e){if(["TEXTAREA","OPTION","INPUT","SELECT"].indexOf(e.target.nodeName)!==-1)return;switch(e.stopPropagation(),this.frameBounds=this.manager.currentBounds,this.coordinator.refreshOuterPixels(this.frameBounds),e.touches.length){case 3:return;case 2:{e.preventDefault(),this.pinch.startDistance=this.coordinator.getTouchDistance(e);let o=this.startTouch(e);this.pan.startX=o.X,this.pan.startY=o.Y,this.dragState=2,this.manager.updateBookStyle(!0),this.isScaled?(this.pinch.target.X-=this.pan.translateX*(this.pinch.startScale/this.scale),this.pinch.target.Y-=this.pan.translateY*(this.pinch.startScale/this.scale),this.pinch.target={X:0,Y:0},this.pinch.startScale=1/this.scale):(this.pinch.target={X:0,Y:0},this.pinch.startScale=this.scale),this.pinch.startTranslate={X:this.pan.translateX,Y:this.pan.translateY},this.debugger?.show&&(this.debugger.DOM.touch2.style.display="",this.debugger.DOM.center.style.display="",this.debugger.DOM.pinchTarget.style.display="");return}case 1:this.pan.touchID=e.touches[0].identifier,this.debugger?.show&&(this.debugger.DOM.touch1.style.display="");default:this.dragState<1&&(this.dragState=1),this.manager.updateBookStyle(!0)}this.manager.updateSpineStyle(!1);let t=this.startTouch(e);this.pan.startX=t.X,this.pan.startY=t.Y}startTouch(e){let t=this.coordinator.getTouchCenter(e)||this.coordinator.getBibiEventCoord(e);return{X:t.X-this.manager.width/2-this.pan.translateX*this.scale+this.manager.width/2,Y:t.Y-this.manager.height/2-this.pan.translateY*this.scale+this.manager.height/2}}touchendHandler(e){if(e.stopPropagation(),!e.touches||e.touches.length===0)this.pan.endX&&!this.isScaled?(this.pinch.touchN&&(this.pan.endX=this.pan.startX),this.updateAfterDrag()):!this.pinch.touchN&&Math.abs(this.pan.overscrollX)>Zh&&Math.abs(this.pan.overscrollY)<Zh/2&&(this.pan.startX=0,this.pan.endX=-this.pan.overscrollX,this.updateAfterDrag()),this.dragState=0,this.minimumMoved=!1,this.clearPinch(),this.debugger?.show&&(this.debugger.DOM.center.style.display="none",this.debugger.DOM.touch1.style.display="none",this.debugger.DOM.touch2.style.display="none");else if(e.touches.length===1){this.dragState=1,e.touches[0].identifier!==this.pan.touchID&&(this.pan.touchID=e.touches[0].identifier),this.debugger?.show&&(this.debugger.DOM.center.style.display="none",this.debugger.DOM.touch2.style.display="none",this.debugger.DOM.pinchTarget.style.display="none");let t=this.startTouch(e);this.pan.startX=t.X,this.pan.startY=t.Y}window.setTimeout(()=>{this.manager.updateBookStyle(!0),this.dragState===0&&(this.scale<ma&&(this.pan.translateX=0,this.pan.translateY=0),this.clearPan()),this.manager.updateBookStyle(!0)},50)}touchmoveHandler(e){e.stopPropagation();let t=this.coordinator.getBibiEventCoord(e);Math.abs(this.pan.startY-t.Y)+Math.abs(this.pan.startX-t.X)>5&&(this.minimumMoved||(this.manager.deselect(),this.minimumMoved=!0),this.dragState<1&&(this.dragState=1));let o=this.coordinator?.getTouchDistance(e),r=!1,n=this.scale;if(this.dragState===2&&o){if(this.pinch.touchN++,this.pinch.touchN<4)return;let a=o/this.pinch.startDistance*this.scale;a>=Jh&&(a=Jh),a<=ma&&(a=1),this.scale=a,this.pinch.startDistance=o,r=!0}if(this.pan.letItGo===!1&&(this.pan.letItGo=Math.abs(this.pan.startY-t.Y)<Math.abs(this.pan.startX-t.X)),this.debugger?.show&&(this.debugger.DOM.touch1.style.top=`${t.Y-10}px`,this.debugger.DOM.touch1.style.left=`${t.X-10}px`,this.debugger.DOM.touch1.innerText=`${t.X.toFixed(2)},${t.Y.toFixed(2)}`),this.dragState>0&&this.isScaled||this.dragState>1){if(this.dragState===1){let c={X:t.X-this.manager.width/2,Y:t.Y-this.manager.height/2};this.pan.translateX=(c.X-(this.pan.startX-this.manager.width/2))*1/this.scale,this.pan.translateY=(c.Y-(this.pan.startY-this.manager.height/2))*1/this.scale}else if(this.dragState===2){let c=this.coordinator.getTouchCenter(e);if(this.debugger?.show){this.debugger.DOM.center.style.top=`${c.Y-5}px`,this.debugger.DOM.center.style.left=`${c.X-5}px`,this.debugger.DOM.center.innerText=`${c.X.toFixed(2)},${c.Y.toFixed(2)}`;let m=this.coordinator.getBibiEventCoord(e,1);this.debugger.DOM.touch2.style.top=`${m.Y-10}px`,this.debugger.DOM.touch2.style.left=`${m.X-10}px`,this.debugger.DOM.touch2.innerText=`${m.X.toFixed(2)},${m.Y.toFixed(2)}`}c.X-=this.manager.width/2,c.Y-=this.manager.height/2;let l=-c.X/n;l+=c.X/this.scale,this.pinch.target.X+=l,c.X+=this.pinch.target.X*this.scale/this.pinch.startScale;let h=-c.Y/n;h+=c.Y/this.scale,this.pinch.target.Y+=h,c.Y+=this.pinch.target.Y*this.scale/this.pinch.startScale;let d=(c.X-(this.pan.startX-this.manager.width/2))*1/this.scale,u=(c.Y-(this.pan.startY-this.manager.height/2))*1/this.scale;this.pan.translateX=d,this.pan.translateY=u,this.debugger?.show&&(this.debugger.DOM.pinchTarget.style.left=`${this.pinch.target.X*this.scale/this.pinch.startScale-5+this.manager.width/2}px`,this.debugger.DOM.pinchTarget.style.top=`${this.pinch.target.Y*this.scale/this.pinch.startScale-5+this.manager.height/2}px`,this.debugger.DOM.pinchTarget.innerText=`${(this.pinch.target.X*this.scale/this.pinch.startScale).toFixed(2)},${(this.pinch.target.Y*this.scale/this.pinch.startScale).toFixed(2)}`)}let a=this.frameBounds.width/6,s=this.frameBounds.height/6;this.pan.translateX<-a&&(this.pan.overscrollX=-(a+this.pan.translateX),this.pan.translateX=-a),this.pan.translateY<-s&&(this.pan.overscrollY=-(s+this.pan.translateY),this.pan.translateY=-s),this.pan.translateX>a&&(this.pan.overscrollX=a-this.pan.translateX,this.pan.translateX=a),this.pan.translateY>s&&(this.pan.overscrollY=s-this.pan.translateY,this.pan.translateY=s),r=!0,this.debugger?.show&&(this.debugger.DOM.stats.innerText=`TX: ${this.pan.translateX.toFixed(2)}
TY: ${this.pan.translateY.toFixed(2)}
Zoom: ${this.scale.toFixed(2)}
Overscroll: ${this.pan.overscrollX.toFixed(2)},${this.pan.overscrollY.toFixed(2)}`)}if(r){this.manager.updateBookStyle();return}if(this.dragState>0&&this.pan.letItGo){this.pan.endX=t.X;let a=this.manager.currentSlide*(this.manager.width/this.manager.perPage),s=this.pan.endX-this.pan.startX,c=this.manager.rtl?a+s:a-s;cancelAnimationFrame(this.moveFrame),this.moveFrame=requestAnimationFrame(()=>{requestAnimationFrame(()=>{this.manager.spineElement.style.transform=`translate3d(${(this.manager.rtl?1:-1)*c}px, 0, 0)`})})}}dblclickHandler(e){clearTimeout(this.dtimer),this.pdblclick=!0,this.dtimer=window.setTimeout(()=>this.pdblclick=!1,200),!this.disableDblClick&&this.isScaled&&(this.scale=1)}get isScaled(){return this.scale>1}addTouch(e){e.touches=[{pageX:e.pageX,pageY:e.pageY}]}mousedownHandler(e){this.isScaled&&(this.addTouch(e),this.touchstartHandler(e))}mouseupHandler(e){this.isScaled&&this.touchendHandler(e)}mousemoveHandler(e){this.isScaled&&e.buttons>0&&(e.preventDefault(),this.addTouch(e),this.touchmoveHandler(e))}updateAfterDrag(){let e=(this.manager.rtl?-1:1)*(this.pan.endX-this.pan.startX),t=Math.abs(e);e>0&&t>this.manager.threshold&&this.manager.slength>this.manager.perPage?this.manager.listener("no_less",void 0):e<0&&t>this.manager.threshold&&this.manager.slength>this.manager.perPage&&this.manager.listener("no_more",void 0),this.manager.slideToCurrent(!0,!0)}};var vr=(i=>(i.auto="auto",i.landscape="landscape",i.portrait="portrait",i))(vr||{}),wr=(i=>(i.auto="auto",i.both="both",i.none="none",i.landscape="landscape",i))(wr||{}),_r=class{constructor(e){this.shift=!0,this.spreads=[],this.nLandscape=0,this.index(e),this.testShift(e),console.log(`Indexed ${this.spreads.length} spreads for ${e.readingOrder.items.length} items`)}index(e,t=!1){this.nLandscape=0,e.readingOrder.items.forEach((o,r)=>{t||(e.readingOrder.items[r]=o.addProperties({number:r+1,isImage:o.type?.indexOf("image/")===0}));let n=o.properties?.otherProperties.orientation==="landscape";(!o.properties?.page||t)&&(o.properties=o.properties?.add({page:n?"center":((this.shift?0:1)+r-this.nLandscape)%2?e.metadata.readingProgression===L.rtl?"right":"left":e.metadata.readingProgression===L.rtl?"left":"right"})),(n||o.properties?.otherProperties.addBlank)&&this.nLandscape++}),t&&(this.spreads=[]),this.buildSpreads(e.readingOrder)}testShift(e){let t=!1;this.spreads.forEach((o,r)=>{if(o.length>1)return;let n=o[0],a=n.properties?.otherProperties.orientation;r===0&&(a==="landscape"||a!=="portrait"&&((n.width||0)>(n.height||0)||n.properties?.otherProperties.spread==="both"))&&(this.shift=!1),t&&n.properties?.page===_e.center&&this.spreads[r-1][0].addProperties({addBlank:!0}),a==="portrait"&&n.properties?.page!=="center"&&n.properties?.otherProperties.number>1?t=!0:t=!1}),this.shift||this.index(e,!0)}buildSpreads(e){let t=[];e.items.forEach((o,r)=>{!r&&this.shift?this.spreads.push([o]):o.properties?.page===_e.center?(t.length>0&&this.spreads.push(t),this.spreads.push([o]),t=[]):t.length>=2?(this.spreads.push(t),t=[o]):t.push(o)}),t.length>0&&this.spreads.push(t)}currentSpread(e,t){return this.spreads[Math.min(Math.floor(e/t),this.spreads.length-1)]}findByLink(e){return this.spreads.find(t=>t.includes(e))||void 0}};var Qh=8,ed=5,mg=300,pg=15e3,gg=250,fg=150,yg=500,xr=class{constructor(e,t,o,r,n,a){if(this.pool=new Map,this.blobs=new Map,this.inprogress=new Map,this.delayedShow=new Map,this.delayedTimeout=new Map,this.previousFrames=[],this.injector=null,this.width=0,this.height=0,this.transform="",this.currentSlide=0,this.spread=!0,this.orientationInternal=-1,this.container=e,this.positions=t,this.pub=o,this.injector=r??null,this.contentProtectionConfig=n||{},this.keyboardPeripheralsConfig=a||[],this.spreadPresentation=o.metadata.otherMetadata?.spread||wr.auto,this.pub.metadata.effectiveReadingProgression!==L.rtl&&this.pub.metadata.effectiveReadingProgression!==L.ltr)throw Error("Unsupported reading progression for EPUB");this.spreader=new _r(this.pub),this.containerHeightCached=e.clientHeight,this.bookElement=document.createElement("div"),this.bookElement.ariaLabel="Book",this.bookElement.tabIndex=-1,this.updateBookStyle(!0),this.spineElement=document.createElement("div"),this.spineElement.ariaLabel="Spine",this.bookElement.appendChild(this.spineElement),this.container.appendChild(this.bookElement),this.updateSpineStyle(!0),this.peripherals=new br(this),this.pub.readingOrder.items.forEach(s=>{let c=new fr(this.peripherals,this.pub.metadata.effectiveReadingProgression,s.href,this.contentProtectionConfig,this.keyboardPeripheralsConfig);this.spineElement.appendChild(c.element),this.pool.set(s.href,c),c.width=100/this.length*(s.properties?.otherProperties.orientation===vr.landscape||s.properties?.otherProperties.addBlank?this.perPage:1),c.height=this.height})}set listener(e){this._listener=e}get listener(){return this._listener}get doNotDisturb(){return this.peripherals.pan.touchID>0}resizeHandler(e=!0,t=!0){this.currentSlide+this.perPage>this.length&&(this.currentSlide=this.length<=this.perPage?0:this.length-1),this.containerHeightCached=this.container.clientHeight,this.orientationInternal=-1,this.updateSpineStyle(!0),e&&(this.currentSlide=this.reAlign(),this.slideToCurrent(!t,t)),clearTimeout(this.resizeTimeout),this.resizeTimeout=window.setTimeout(()=>{this.pool.forEach((o,r)=>{let n=this.pub.readingOrder.items.findIndex(c=>c.href===r),a=this.pub.readingOrder.items[n];if(o.width=100/this.length*(a.properties?.otherProperties.orientation===vr.landscape||a.properties?.otherProperties.addBlank?this.perPage:1),o.height=this.height,!o.loaded)return;let s=this.spreader.findByLink(a);o.update(this.spreadPosition(s,a))})},gg)}updateDimensions(){this.width=this.bookElement.clientWidth,this.height=this.bookElement.clientHeight}get rtl(){return this.pub.metadata.effectiveReadingProgression===L.rtl}get single(){return!this.spread||this.portrait}get perPage(){return this.spread&&!this.portrait?2:1}get threshold(){return 50}get portrait(){return this.spreadPresentation===wr.none?!0:(this.orientationInternal===-1&&(this.orientationInternal=this.containerHeightCached>this.container.clientWidth?1:0),this.orientationInternal===1)}updateSpineStyle(e,t=!0){let o="0";this.updateDimensions(),this.perPage>1&&(o=`${this.width/2}px`);let r={transition:e?`all ${t?fg:yg}ms ease-out`:"all 0ms ease-out",marginRight:this.rtl?o:"0",marginLeft:this.rtl?"0":o,width:`${this.width/this.perPage*this.length}px`,transform:this.transform,contain:"content"};Object.assign(this.spineElement.style,r)}updateBookStyle(e=!1){if(e){let t={overflow:"hidden",direction:this.pub.metadata.effectiveReadingProgression,cursor:"",height:"100%",width:"100%",position:"relative",outline:"none",transition:this.peripherals?.dragState?"none":"transform .15s ease-in-out",touchAction:"none"};Object.assign(this.bookElement.style,t)}this.bookElement.style.transform=`scale(${this.peripherals?.scale||1})`+(this.peripherals?` translate3d(${this.peripherals.pan.translateX}px, ${this.peripherals.pan.translateY}px, 0px)`:"")}goTo(e){if(this.slength<=this.perPage)return;e=this.reAlign(e);let t=this.currentSlide;this.currentSlide=Math.min(Math.max(e,0),this.length-1),t!==this.currentSlide&&this.slideToCurrent(!1)}onChange(){this.peripherals.scale=1,this.updateBookStyle()}get offset(){return(this.rtl?1:-1)*this.currentSlide*(this.width/this.perPage)}get length(){if(this.single)return this.slength;let e=this.slength+this.nLandscape;return this.shift&&e%2===0?e+1:e}get slength(){return this.pub.readingOrder.items.length||0}get shift(){return this.spreader.shift}get nLandscape(){return this.spreader.nLandscape}setPerPage(e){e===null?this.spread=!0:e===1?this.spread=!1:this.spread=!0,requestAnimationFrame(()=>this.resizeHandler(!0))}slideToCurrent(e,t=!0){if(this.updateDimensions(),e)requestAnimationFrame(()=>{requestAnimationFrame(()=>{let o=`translate3d(${this.offset}px, 0, 0)`;this.spineElement.style.transform!==o&&(this.transform=o,this.updateSpineStyle(!0,t),this.deselect())})});else{let o=`translate3d(${this.offset}px, 0, 0)`;if(this.spineElement.style.transform===o)return;this.transform=o,this.updateSpineStyle(!1),this.deselect()}}bounce(e=!1){requestAnimationFrame(()=>{this.transform=`translate3d(${this.offset+50*(e?1:-1)}px, 0, 0)`,this.updateSpineStyle(!0,!0),setTimeout(()=>{this.transform=`translate3d(${this.offset}px, 0, 0)`,this.updateSpineStyle(!0,!0)},100)})}next(e=1){if(this.slength<=this.perPage)return!1;let t=this.currentSlide;return this.currentSlide=Math.min(this.currentSlide+e,this.length-1),this.perPage>1&&this.currentSlide%2&&this.currentSlide--,this.currentSlide===t&&(this.currentSlide+1,this.length),t!==this.currentSlide?(this.slideToCurrent(!0),this.onChange(),!0):(this.bounce(this.rtl),!1)}prev(e=1){if(this.slength<=this.perPage)return!1;let t=this.currentSlide;return this.currentSlide=Math.max(this.currentSlide-e,0),this.perPage>1&&this.currentSlide%2&&this.currentSlide++,t!==this.currentSlide?(this.slideToCurrent(!0),this.onChange(),!0):(this.bounce(!this.rtl),!1)}get ownerWindow(){return this.container.ownerDocument.defaultView||window}async destroy(){let e=this.inprogress.values(),t=e.next(),o=[];for(;t.value;)o.push(t.value),t=e.next();o.length>0&&await Promise.allSettled(o),this.inprogress.clear();let r=this.pool.values(),n=r.next();for(;n.value;)await n.value.destroy(),n=r.next();this.pool.clear(),this.blobs.forEach(a=>URL.revokeObjectURL(a)),this.injector?.dispose(),this.container.childNodes.forEach(a=>{(a.nodeType===Node.ELEMENT_NODE||a.nodeType===Node.TEXT_NODE)&&a.remove()})}makeSpread(e){return this.perPage<2?[this.pub.readingOrder.items[e]]:this.spreader.currentSpread(e,this.perPage)}reAlign(e=this.currentSlide){return e%2&&!this.single&&e++,e}spreadPosition(e,t){return this.perPage<2?_e.center:e.length<2?_e.center:t.href===e[0].href?this.rtl?_e.right:_e.left:this.rtl?_e.left:_e.right}async waitForItem(e){if(this.inprogress.has(e)&&await this.inprogress.get(e),this.delayedShow.has(e)){let t=this.delayedTimeout.get(e);t>0?clearTimeout(t):await this.delayedShow.get(e),this.delayedTimeout.set(e,0),this.delayedShow.delete(e)}}async cancelShowing(e){if(this.delayedShow.has(e)){let t=this.delayedTimeout.get(e);t>0&&clearTimeout(t),this.delayedShow.delete(e)}}async update(e,t,o,r=!1){let n=this.pub.readingOrder.items.findIndex(c=>c.href===t.href);if(n<0)throw Error("Href not found in reading order");this.currentSlide!==n&&(this.currentSlide=this.reAlign(n),this.slideToCurrent(!0));let a=this.makeSpread(this.currentSlide);this.perPage>1&&n++;for(let c of a)await this.waitForItem(c.href);let s=new Promise(async(c,l)=>{let h=[],d=[];this.positions.forEach((g,f)=>{(f>n+Qh||f<n-Qh)&&(h.includes(g.href)||h.push(g.href)),f<n+ed&&f>n-ed&&(d.includes(g.href)||d.push(g.href))}),h.forEach(async g=>{d.includes(g)||this.pool.has(g)&&(this.cancelShowing(g),await this.pool.get(g)?.unload())}),this.currentBaseURL!==void 0&&e.baseURL!==this.currentBaseURL&&(this.blobs.forEach(g=>URL.revokeObjectURL(g)),this.blobs.clear()),this.currentBaseURL=e.baseURL;let u=async g=>{let f=e.readingOrder.findIndexWithHref(g),y=e.readingOrder.items[f];if(y){if(!this.blobs.has(g)){let w=await new _i(e,this.currentBaseURL||"",y,{injector:this.injector}).build(!0);this.blobs.set(g,w)}this.delayedShow.has(g)||this.delayedShow.set(g,new Promise((w,x)=>{let k=!1,z=window.setTimeout(async()=>{this.delayedTimeout.set(g,0);let ce=this.makeSpread(this.reAlign(f)),G=this.spreadPosition(ce,y),V=this.pool.get(g);await V.load(o,this.blobs.get(g)),this.peripherals.isScaled||await V.show(G),this.delayedShow.delete(g),k=!0,w()},mg);setTimeout(()=>{!k&&this.delayedShow.has(g)&&x(`Offscreen load timeout: ${g}`)},pg),this.delayedTimeout.set(g,z)}))}};try{await Promise.all(d.map(g=>u(g)))}catch(g){l(g)}let m=[];for(let g of a){let f=this.pool.get(g.href),y=this.blobs.get(g.href);y&&(this.cancelShowing(g.href),await f.load(o,y),await f.show(this.spreadPosition(a,g)),this.previousFrames.push(f),await f.activate(),m.push(f))}for(;this.previousFrames.length>0;){let g=this.previousFrames.shift();g&&!m.includes(g)&&await g.unfocus()}this.previousFrames=m;let p=this.container.ownerDocument.activeElement;p&&p.tagName==="IFRAME"&&!m.some(g=>g.iframe===p)&&m[0]?.iframe.focus({preventScroll:!0}),c()});for(let c of a)this.inprogress.set(c.href,s);await s;for(let c of a)this.inprogress.delete(c.href)}get currentFrames(){if(this.perPage<2){let e=this.pub.readingOrder.items[this.currentSlide];return[this.pool.get(e.href)]}return this.spreader.currentSpread(this.currentSlide,this.perPage).map(e=>this.pool.get(e.href))}get currentBounds(){let e={x:0,y:0,width:0,height:0,top:0,right:0,bottom:0,left:0,toJSON(){return this}};return this.currentFrames.forEach(t=>{if(!t)return;let o=t.realSize;e.x=Math.min(e.x,o.x),e.y=Math.min(e.y,o.y),e.width+=o.width,e.height=Math.max(e.height,o.height),e.top=Math.min(e.top,o.top),e.right=Math.min(e.right,o.right),e.bottom=Math.min(e.bottom,o.bottom),e.left=Math.min(e.left,o.left)}),e}get viewport(){let e={readingOrder:[],progressions:new Map,positions:null};return(this.perPage<2?[this.pub.readingOrder.items[this.currentSlide]]:this.spreader.currentSpread(this.currentSlide,this.perPage)).forEach(t=>{e.readingOrder.push(t.href),e.progressions.set(t.href,{start:0,end:1})}),e.positions=this.getCurrentNumbers(),e}getCurrentNumbers(){if(this.perPage<2)return[this.pub.readingOrder.items[this.currentSlide].properties?.otherProperties.number];let e=this.spreader.currentSpread(this.currentSlide,this.perPage);return e.length>1?[e[0].properties?.otherProperties.number,e[e.length-1].properties?.otherProperties.number]:[e[0].properties?.otherProperties.number]}deselect(){this.currentFrames?.forEach(e=>e?.deselect())}};var Yt=class i{constructor(e={}){this.backgroundColor=pe(e.backgroundColor),this.blendFilter=X(e.blendFilter),this.constraint=M(e.constraint),this.columnCount=M(e.columnCount),this.darkenFilter=Ct(e.darkenFilter),this.deprecatedFontSize=X(e.deprecatedFontSize),this.fontFamily=pe(e.fontFamily),this.fontSize=Rt(e.fontSize,jt.range),this.fontSizeNormalize=X(e.fontSizeNormalize),this.fontOpticalSizing=X(e.fontOpticalSizing),this.fontWeight=Rt(e.fontWeight,Gt.range),this.fontWidth=Rt(e.fontWidth,Vt.range),this.hyphens=X(e.hyphens),this.invertFilter=Ct(e.invertFilter),this.invertGaijiFilter=Ct(e.invertGaijiFilter),this.iOSPatch=X(e.iOSPatch),this.iPadOSPatch=X(e.iPadOSPatch),this.letterSpacing=M(e.letterSpacing),this.ligatures=X(e.ligatures),this.lineHeight=M(e.lineHeight),this.linkColor=pe(e.linkColor),this.noRuby=X(e.noRuby),this.pageGutter=M(e.pageGutter),this.paragraphIndent=M(e.paragraphIndent),this.paragraphSpacing=M(e.paragraphSpacing),this.scroll=X(e.scroll),this.scrollPaddingTop=M(e.scrollPaddingTop),this.scrollPaddingBottom=M(e.scrollPaddingBottom),this.scrollPaddingLeft=M(e.scrollPaddingLeft),this.scrollPaddingRight=M(e.scrollPaddingRight),this.selectionBackgroundColor=pe(e.selectionBackgroundColor),this.selectionTextColor=pe(e.selectionTextColor),this.textAlign=tr(e.textAlign,kt),this.textColor=pe(e.textColor),this.textNormalization=X(e.textNormalization),this.visitedColor=pe(e.visitedColor),this.wordSpacing=M(e.wordSpacing),this.optimalLineLength=M(e.optimalLineLength),this.maximalLineLength=M(e.maximalLineLength),this.minimalLineLength=M(e.minimalLineLength)}static serialize(e){let{...t}=e;return JSON.stringify(t)}static deserialize(e){try{let t=JSON.parse(e);return new i(t)}catch(t){return console.error("Failed to deserialize preferences:",t),null}}merging(e){let t={...this};for(let o of Object.keys(e))e[o]!==void 0&&(o!=="maximalLineLength"||e[o]===null||e[o]>=(e.optimalLineLength??t.optimalLineLength??65))&&(o!=="minimalLineLength"||e[o]===null||e[o]<=(e.optimalLineLength??t.optimalLineLength??65))&&(t[o]=e[o]);return new i(t)}};var kr=class{constructor(e){this.backgroundColor=pe(e.backgroundColor)||null,this.blendFilter=X(e.blendFilter)??!1,this.constraint=M(e.constraint)||0,this.columnCount=M(e.columnCount)||null,this.darkenFilter=Ct(e.darkenFilter)??!1,this.deprecatedFontSize=X(e.deprecatedFontSize),(this.deprecatedFontSize===!1||this.deprecatedFontSize===null)&&(this.deprecatedFontSize=!CSS.supports("zoom","1")),this.fontFamily=pe(e.fontFamily)||null,this.fontSize=Rt(e.fontSize,jt.range)||1,this.fontSizeNormalize=X(e.fontSizeNormalize)??!1,this.fontOpticalSizing=X(e.fontOpticalSizing)??null,this.fontWeight=Rt(e.fontWeight,Gt.range)||null,this.fontWidth=Rt(e.fontWidth,Vt.range)||null,this.hyphens=X(e.hyphens)??null,this.invertFilter=Ct(e.invertFilter)??!1,this.invertGaijiFilter=Ct(e.invertGaijiFilter)??!1,this.iOSPatch=e.iOSPatch===!1?!1:(je.OS.iOS||je.OS.iPadOS)&&je.iOSRequest==="mobile",this.iPadOSPatch=e.iPadOSPatch===!1?!1:je.OS.iPadOS&&je.iOSRequest==="desktop",this.letterSpacing=M(e.letterSpacing)||null,this.ligatures=X(e.ligatures)??null,this.lineHeight=M(e.lineHeight)||null,this.linkColor=pe(e.linkColor)||null,this.noRuby=X(e.noRuby)??!1,this.pageGutter=ir(M(e.pageGutter),20),this.paragraphIndent=M(e.paragraphIndent)??null,this.paragraphSpacing=M(e.paragraphSpacing)??null,this.scroll=X(e.scroll)??!1,this.scrollPaddingTop=M(e.scrollPaddingTop)??null,this.scrollPaddingBottom=M(e.scrollPaddingBottom)??null,this.scrollPaddingLeft=M(e.scrollPaddingLeft)??null,this.scrollPaddingRight=M(e.scrollPaddingRight)??null,this.selectionBackgroundColor=pe(e.selectionBackgroundColor)||null,this.selectionTextColor=pe(e.selectionTextColor)||null,this.textAlign=tr(e.textAlign,kt)||null,this.textColor=pe(e.textColor)||null,this.textNormalization=X(e.textNormalization)??!1,this.visitedColor=pe(e.visitedColor)||null,this.wordSpacing=M(e.wordSpacing)||null,this.optimalLineLength=M(e.optimalLineLength)||65,this.maximalLineLength=ir(Uh(e.maximalLineLength,this.optimalLineLength),80),this.minimalLineLength=ir(Ih(e.minimalLineLength,this.optimalLineLength),40),this.experiments=Dh(e.experiments)||null}};var Sg="#FFFFFF",bg="#121212",_g="#0000EE",vg="#551A8B",wg="#b4d8fe",xg="inherit",Xt={backgroundColor:Sg,textColor:bg,linkColor:_g,visitedColor:vg,selectionBackgroundColor:wg,selectionTextColor:xg};var io=class{constructor(e,t,o){this.preferences=new Yt({...e}),this.settings=t,this.metadata=o,this.layout=this.metadata?.effectiveLayout||v.reflowable}clear(){this.preferences=new Yt({backgroundColor:null,blendFilter:null,columnCount:null,constraint:null,darkenFilter:null,deprecatedFontSize:null,fontFamily:null,fontSize:null,fontSizeNormalize:null,fontOpticalSizing:null,fontWeight:null,fontWidth:null,hyphens:null,invertFilter:null,invertGaijiFilter:null,iOSPatch:null,iPadOSPatch:null,letterSpacing:null,ligatures:null,lineHeight:null,linkColor:null,maximalLineLength:null,minimalLineLength:null,noRuby:null,optimalLineLength:null,pageGutter:null,paragraphIndent:null,paragraphSpacing:null,scroll:null,scrollPaddingTop:null,scrollPaddingBottom:null,scrollPaddingLeft:null,scrollPaddingRight:null,selectionBackgroundColor:null,selectionTextColor:null,textAlign:null,textColor:null,textNormalization:null,visitedColor:null,wordSpacing:null})}updatePreference(e,t){this.preferences[e]=t}get backgroundColor(){return new ie({initialValue:this.preferences.backgroundColor,effectiveValue:this.settings.backgroundColor||Xt.backgroundColor,isEffective:this.preferences.backgroundColor!==null,onChange:e=>{this.updatePreference("backgroundColor",e??null)}})}get blendFilter(){return new ze({initialValue:this.preferences.blendFilter,effectiveValue:this.settings.blendFilter||!1,isEffective:this.preferences.blendFilter!==null,onChange:e=>{this.updatePreference("blendFilter",e??null)}})}get columnCount(){return new ie({initialValue:this.preferences.columnCount,effectiveValue:this.settings.columnCount||null,isEffective:this.layout!==v.fixed&&!this.settings.scroll,onChange:e=>{this.updatePreference("columnCount",e??null)}})}get constraint(){return new ie({initialValue:this.preferences.constraint,effectiveValue:this.preferences.constraint||0,isEffective:!0,onChange:e=>{this.updatePreference("constraint",e??null)}})}get darkenFilter(){return new de({initialValue:typeof this.preferences.darkenFilter=="boolean"?100:this.preferences.darkenFilter,effectiveValue:typeof this.settings.darkenFilter=="boolean"?100:this.settings.darkenFilter||0,isEffective:this.settings.darkenFilter!==null,onChange:e=>{this.updatePreference("darkenFilter",e??null)},supportedRange:Bt.range,step:Bt.step})}get deprecatedFontSize(){return new ze({initialValue:this.preferences.deprecatedFontSize,effectiveValue:CSS.supports("zoom","1")?this.settings.deprecatedFontSize||!1:!0,isEffective:this.layout!==v.fixed,onChange:e=>{this.updatePreference("deprecatedFontSize",e??null)}})}get fontFamily(){return new ie({initialValue:this.preferences.fontFamily,effectiveValue:this.settings.fontFamily||null,isEffective:this.layout!==v.fixed,onChange:e=>{this.updatePreference("fontFamily",e??null)}})}get fontSize(){return new de({initialValue:this.preferences.fontSize,effectiveValue:this.settings.fontSize||1,isEffective:this.layout!==v.fixed,onChange:e=>{this.updatePreference("fontSize",e??null)},supportedRange:jt.range,step:jt.step})}get fontSizeNormalize(){return new ze({initialValue:this.preferences.fontSizeNormalize,effectiveValue:this.settings.fontSizeNormalize||!1,isEffective:this.layout!==v.fixed&&this.preferences.fontSizeNormalize!==null,onChange:e=>{this.updatePreference("fontSizeNormalize",e??null)}})}get fontOpticalSizing(){return new ze({initialValue:this.preferences.fontOpticalSizing,effectiveValue:this.settings.fontOpticalSizing||!0,isEffective:this.layout!==v.fixed&&this.preferences.fontOpticalSizing!==null,onChange:e=>{this.updatePreference("fontOpticalSizing",e??null)}})}get fontWeight(){return new de({initialValue:this.preferences.fontWeight,effectiveValue:this.settings.fontWeight||400,isEffective:this.layout!==v.fixed&&this.preferences.fontWeight!==null,onChange:e=>{this.updatePreference("fontWeight",e??null)},supportedRange:Gt.range,step:Gt.step})}get fontWidth(){return new de({initialValue:this.preferences.fontWidth,effectiveValue:this.settings.fontWidth||100,isEffective:this.layout!==v.fixed&&this.preferences.fontWidth!==null,onChange:e=>{this.updatePreference("fontWidth",e??null)},supportedRange:Vt.range,step:Vt.step})}get hyphens(){return new ze({initialValue:this.preferences.hyphens,effectiveValue:this.settings.hyphens||!1,isEffective:this.layout!==v.fixed&&this.metadata?.effectiveReadingProgression===L.ltr&&this.preferences.hyphens!==null,onChange:e=>{this.updatePreference("hyphens",e??null)}})}get invertFilter(){return new de({initialValue:typeof this.preferences.invertFilter=="boolean"?100:this.preferences.invertFilter,effectiveValue:typeof this.settings.invertFilter=="boolean"?100:this.settings.invertFilter||0,isEffective:this.settings.invertFilter!==null,onChange:e=>{this.updatePreference("invertFilter",e??null)},supportedRange:Bt.range,step:Bt.step})}get invertGaijiFilter(){return new de({initialValue:typeof this.preferences.invertGaijiFilter=="boolean"?100:this.preferences.invertGaijiFilter,effectiveValue:typeof this.settings.invertGaijiFilter=="boolean"?100:this.settings.invertGaijiFilter||0,isEffective:this.preferences.invertGaijiFilter!==null,onChange:e=>{this.updatePreference("invertGaijiFilter",e??null)},supportedRange:Bt.range,step:Bt.step})}get iOSPatch(){return new ze({initialValue:this.preferences.iOSPatch,effectiveValue:this.settings.iOSPatch||!1,isEffective:this.layout!==v.fixed,onChange:e=>{this.updatePreference("iOSPatch",e??null)}})}get iPadOSPatch(){return new ze({initialValue:this.preferences.iPadOSPatch,effectiveValue:this.settings.iPadOSPatch||!1,isEffective:this.layout!==v.fixed,onChange:e=>{this.updatePreference("iPadOSPatch",e??null)}})}get letterSpacing(){return new de({initialValue:this.preferences.letterSpacing,effectiveValue:this.settings.letterSpacing||0,isEffective:this.layout!==v.fixed&&this.preferences.letterSpacing!==null,onChange:e=>{this.updatePreference("letterSpacing",e??null)},supportedRange:oa.range,step:oa.step})}get ligatures(){return new ze({initialValue:this.preferences.ligatures,effectiveValue:this.settings.ligatures||!0,isEffective:(()=>{if(this.preferences.ligatures===null||this.layout===v.fixed)return!1;let e=this.metadata?.languages?.[0]?.toLowerCase();return!(e&&["zh","ja","ko","mn-mong"].some(t=>e.startsWith(t)))})(),onChange:e=>{this.updatePreference("ligatures",e??null)}})}get lineHeight(){return new de({initialValue:this.preferences.lineHeight,effectiveValue:this.settings.lineHeight,isEffective:this.layout!==v.fixed&&this.preferences.lineHeight!==null,onChange:e=>{this.updatePreference("lineHeight",e??null)},supportedRange:ra.range,step:ra.step})}get linkColor(){return new ie({initialValue:this.preferences.linkColor,effectiveValue:this.settings.linkColor||Xt.linkColor,isEffective:this.layout!==v.fixed&&this.preferences.linkColor!==null,onChange:e=>{this.updatePreference("linkColor",e??null)}})}get maximalLineLength(){return new de({initialValue:this.preferences.maximalLineLength,effectiveValue:this.settings.maximalLineLength,isEffective:this.layout!==v.fixed,onChange:e=>{this.updatePreference("maximalLineLength",e)},supportedRange:Kt.range,step:Kt.step})}get minimalLineLength(){return new de({initialValue:this.preferences.minimalLineLength,effectiveValue:this.settings.minimalLineLength,isEffective:this.layout!==v.fixed,onChange:e=>{this.updatePreference("minimalLineLength",e)},supportedRange:Kt.range,step:Kt.step})}get noRuby(){return new ze({initialValue:this.preferences.noRuby,effectiveValue:this.settings.noRuby||!1,isEffective:this.layout!==v.fixed&&this.metadata?.languages?.includes("ja")||!1,onChange:e=>{this.updatePreference("noRuby",e??null)}})}get optimalLineLength(){return new de({initialValue:this.preferences.optimalLineLength,effectiveValue:this.settings.optimalLineLength,isEffective:this.layout!==v.fixed,onChange:e=>{this.updatePreference("optimalLineLength",e)},supportedRange:Kt.range,step:Kt.step})}get pageGutter(){return new ie({initialValue:this.preferences.pageGutter,effectiveValue:this.settings.pageGutter,isEffective:this.layout!==v.fixed&&!this.settings.scroll,onChange:e=>{this.updatePreference("pageGutter",e??null)}})}get paragraphIndent(){return new de({initialValue:this.preferences.paragraphIndent,effectiveValue:this.settings.paragraphIndent||0,isEffective:this.layout!==v.fixed&&this.preferences.paragraphIndent!==null,onChange:e=>{this.updatePreference("paragraphIndent",e??null)},supportedRange:na.range,step:na.step})}get paragraphSpacing(){return new de({initialValue:this.preferences.paragraphSpacing,effectiveValue:this.settings.paragraphSpacing||0,isEffective:this.layout!==v.fixed&&this.preferences.paragraphSpacing!==null,onChange:e=>{this.updatePreference("paragraphSpacing",e??null)},supportedRange:aa.range,step:aa.step})}get scroll(){return new ze({initialValue:this.preferences.scroll,effectiveValue:this.settings.scroll||!1,isEffective:this.layout!==v.fixed,onChange:e=>{this.updatePreference("scroll",e??null)}})}get scrollPaddingTop(){return new ie({initialValue:this.preferences.scrollPaddingTop,effectiveValue:this.settings.scrollPaddingTop||0,isEffective:this.layout!==v.fixed&&!!this.settings.scroll&&this.preferences.scrollPaddingTop!==null,onChange:e=>{this.updatePreference("scrollPaddingTop",e??null)}})}get scrollPaddingBottom(){return new ie({initialValue:this.preferences.scrollPaddingBottom,effectiveValue:this.settings.scrollPaddingBottom||0,isEffective:this.layout!==v.fixed&&!!this.settings.scroll&&this.preferences.scrollPaddingBottom!==null,onChange:e=>{this.updatePreference("scrollPaddingBottom",e??null)}})}get scrollPaddingLeft(){return new ie({initialValue:this.preferences.scrollPaddingLeft,effectiveValue:this.settings.scrollPaddingLeft||0,isEffective:this.layout!==v.fixed&&!!this.settings.scroll&&this.preferences.scrollPaddingLeft!==null,onChange:e=>{this.updatePreference("scrollPaddingLeft",e??null)}})}get scrollPaddingRight(){return new ie({initialValue:this.preferences.scrollPaddingRight,effectiveValue:this.settings.scrollPaddingRight||0,isEffective:this.layout!==v.fixed&&!!this.settings.scroll&&this.preferences.scrollPaddingRight!==null,onChange:e=>{this.updatePreference("scrollPaddingRight",e??null)}})}get selectionBackgroundColor(){return new ie({initialValue:this.preferences.selectionBackgroundColor,effectiveValue:this.settings.selectionBackgroundColor||Xt.selectionBackgroundColor,isEffective:this.layout!==v.fixed&&this.preferences.selectionBackgroundColor!==null,onChange:e=>{this.updatePreference("selectionBackgroundColor",e??null)}})}get selectionTextColor(){return new ie({initialValue:this.preferences.selectionTextColor,effectiveValue:this.settings.selectionTextColor||Xt.selectionTextColor,isEffective:this.layout!==v.fixed&&this.preferences.selectionTextColor!==null,onChange:e=>{this.updatePreference("selectionTextColor",e??null)}})}get textAlign(){return new or({initialValue:this.preferences.textAlign,effectiveValue:this.settings.textAlign||kt.start,isEffective:this.layout!==v.fixed&&this.preferences.textAlign!==null,onChange:e=>{this.updatePreference("textAlign",e??null)},supportedValues:Object.values(kt)})}get textColor(){return new ie({initialValue:this.preferences.textColor,effectiveValue:this.settings.textColor||Xt.textColor,isEffective:this.layout!==v.fixed&&this.preferences.textColor!==null,onChange:e=>{this.updatePreference("textColor",e??null)}})}get textNormalization(){return new ze({initialValue:this.preferences.textNormalization,effectiveValue:this.settings.textNormalization||!1,isEffective:this.layout!==v.fixed,onChange:e=>{this.updatePreference("textNormalization",e??null)}})}get visitedColor(){return new ie({initialValue:this.preferences.visitedColor,effectiveValue:this.settings.visitedColor||Xt.visitedColor,isEffective:this.layout!==v.fixed&&this.preferences.visitedColor!==null,onChange:e=>{this.updatePreference("visitedColor",e??null)}})}get wordSpacing(){return new de({initialValue:this.preferences.wordSpacing,effectiveValue:this.settings.wordSpacing||0,isEffective:this.layout!==v.fixed&&this.preferences.wordSpacing!==null,onChange:e=>{this.updatePreference("wordSpacing",e??null)},supportedRange:sa.range,step:sa.step})}};function $t(i){let e=getComputedStyle(i),t=parseFloat(e.paddingLeft||"0"),o=parseFloat(e.paddingRight||"0");return i.clientWidth-t-o}function td(i){let e=getComputedStyle(i),t=parseFloat(e.paddingTop||"0"),o=parseFloat(e.paddingBottom||"0");return i.clientHeight-t-o}var wi=class extends eo{constructor(e){super(),this.a11yNormalize=e.a11yNormalize??null,this.backgroundColor=e.backgroundColor??null,this.blendFilter=e.blendFilter??null,this.bodyHyphens=e.bodyHyphens??null,this.colCount=e.colCount??null,this.darkenFilter=e.darkenFilter??null,this.deprecatedFontSize=e.deprecatedFontSize??null,this.fontFamily=e.fontFamily??null,this.fontOpticalSizing=e.fontOpticalSizing??null,this.fontSize=e.fontSize??null,this.fontSizeNormalize=e.fontSizeNormalize??null,this.fontWeight=e.fontWeight??null,this.fontWidth=e.fontWidth??null,this.invertFilter=e.invertFilter??null,this.invertGaijiFilter=e.invertGaijiFilter??null,this.iOSPatch=e.iOSPatch??null,this.iPadOSPatch=e.iPadOSPatch??null,this.letterSpacing=e.letterSpacing??null,this.ligatures=e.ligatures??null,this.lineHeight=e.lineHeight??null,this.lineLength=e.lineLength??null,this.linkColor=e.linkColor??null,this.noRuby=e.noRuby??null,this.paraIndent=e.paraIndent??null,this.paraSpacing=e.paraSpacing??null,this.selectionBackgroundColor=e.selectionBackgroundColor??null,this.selectionTextColor=e.selectionTextColor??null,this.textAlign=e.textAlign??null,this.textColor=e.textColor??null,this.view=e.view??null,this.visitedColor=e.visitedColor??null,this.wordSpacing=e.wordSpacing??null}toCSSProperties(){let e={};return this.a11yNormalize&&(e["--USER__a11yNormalize"]=this.toFlag("a11y")),this.backgroundColor&&(e["--USER__backgroundColor"]=this.backgroundColor),this.blendFilter&&(e["--USER__blendFilter"]=this.toFlag("blend")),this.bodyHyphens&&(e["--USER__bodyHyphens"]=this.bodyHyphens),this.colCount&&(e["--USER__colCount"]=this.toUnitless(this.colCount)),this.darkenFilter===!0?e["--USER__darkenFilter"]=this.toFlag("darken"):typeof this.darkenFilter=="number"&&(e["--USER__darkenFilter"]=this.toPercentage(this.darkenFilter)),this.deprecatedFontSize&&(e["--USER__fontSizeImplementation"]=this.toFlag("deprecatedFontSize")),this.fontFamily&&(e["--USER__fontFamily"]=this.fontFamily),this.fontOpticalSizing!=null&&(e["--USER__fontOpticalSizing"]=this.fontOpticalSizing),this.fontSize!=null&&(e["--USER__fontSize"]=this.toPercentage(this.fontSize,!0)),this.fontSizeNormalize&&(e["--USER__fontSizeNormalize"]=this.toFlag("normalize")),this.fontWeight!=null&&(e["--USER__fontWeight"]=this.toUnitless(this.fontWeight)),this.fontWidth!=null&&(e["--USER__fontWidth"]=typeof this.fontWidth=="string"?this.fontWidth:this.toUnitless(this.fontWidth)),this.invertFilter===!0?e["--USER__invertFilter"]=this.toFlag("invert"):typeof this.invertFilter=="number"&&(e["--USER__invertFilter"]=this.toPercentage(this.invertFilter)),this.invertGaijiFilter===!0?e["--USER__invertGaiji"]=this.toFlag("invertGaiji"):typeof this.invertGaijiFilter=="number"&&(e["--USER__invertGaiji"]=this.toPercentage(this.invertGaijiFilter)),this.iOSPatch&&(e["--USER__iOSPatch"]=this.toFlag("iOSPatch")),this.iPadOSPatch&&(e["--USER__iPadOSPatch"]=this.toFlag("iPadOSPatch")),this.letterSpacing!=null&&(e["--USER__letterSpacing"]=this.toRem(this.letterSpacing)),this.ligatures&&(e["--USER__ligatures"]=this.ligatures),this.lineHeight!=null&&(e["--USER__lineHeight"]=this.toUnitless(this.lineHeight)),this.lineLength!=null&&(e["--USER__lineLength"]=this.toPx(this.lineLength)),this.linkColor&&(e["--USER__linkColor"]=this.linkColor),this.noRuby&&(e["--USER__noRuby"]=this.toFlag("noRuby")),this.paraIndent!=null&&(e["--USER__paraIndent"]=this.toRem(this.paraIndent)),this.paraSpacing!=null&&(e["--USER__paraSpacing"]=this.toRem(this.paraSpacing)),this.selectionBackgroundColor&&(e["--USER__selectionBackgroundColor"]=this.selectionBackgroundColor),this.selectionTextColor&&(e["--USER__selectionTextColor"]=this.selectionTextColor),this.textAlign&&(e["--USER__textAlign"]=this.textAlign),this.textColor&&(e["--USER__textColor"]=this.textColor),this.view&&(e["--USER__view"]=this.toFlag(this.view)),this.visitedColor&&(e["--USER__visitedColor"]=this.visitedColor),this.wordSpacing!=null&&(e["--USER__wordSpacing"]=this.toRem(this.wordSpacing)),e}},Cr=class extends eo{constructor(e){super(),this.backgroundColor=e.backgroundColor??null,this.baseFontFamily=e.baseFontFamily??null,this.baseFontSize=e.baseFontSize??null,this.baseLineHeight=e.baseLineHeight??null,this.boxSizingMedia=e.boxSizingMedia??null,this.boxSizingTable=e.boxSizingTable??null,this.colWidth=e.colWidth??null,this.colCount=e.colCount??null,this.colGap=e.colGap??null,this.codeFontFamily=e.codeFontFamily??null,this.compFontFamily=e.compFontFamily??null,this.defaultLineLength=e.defaultLineLength??null,this.flowSpacing=e.flowSpacing??null,this.humanistTf=e.humanistTf??null,this.linkColor=e.linkColor??null,this.maxMediaWidth=e.maxMediaWidth??null,this.maxMediaHeight=e.maxMediaHeight??null,this.modernTf=e.modernTf??null,this.monospaceTf=e.monospaceTf??null,this.noOverflow=e.noOverflow??null,this.noVerticalPagination=e.noVerticalPagination??null,this.oldStyleTf=e.oldStyleTf??null,this.pageGutter=e.pageGutter??null,this.paraIndent=e.paraIndent??null,this.paraSpacing=e.paraSpacing??null,this.primaryColor=e.primaryColor??null,this.scrollPaddingBottom=e.scrollPaddingBottom??null,this.scrollPaddingLeft=e.scrollPaddingLeft??null,this.scrollPaddingRight=e.scrollPaddingRight??null,this.scrollPaddingTop=e.scrollPaddingTop??null,this.sansSerifJa=e.sansSerifJa??null,this.sansSerifJaV=e.sansSerifJaV??null,this.sansTf=e.sansTf??null,this.secondaryColor=e.secondaryColor??null,this.selectionBackgroundColor=e.selectionBackgroundColor??null,this.selectionTextColor=e.selectionTextColor??null,this.serifJa=e.serifJa??null,this.serifJaV=e.serifJaV??null,this.textColor=e.textColor??null,this.typeScale=e.typeScale??null,this.visitedColor=e.visitedColor??null,this.experiments=e.experiments??null}toCSSProperties(){let e={};return this.backgroundColor&&(e["--RS__backgroundColor"]=this.backgroundColor),this.baseFontFamily&&(e["--RS__baseFontFamily"]=this.baseFontFamily),this.baseFontSize!=null&&(e["--RS__baseFontSize"]=this.toRem(this.baseFontSize)),this.baseLineHeight!=null&&(e["--RS__baseLineHeight"]=this.toUnitless(this.baseLineHeight)),this.boxSizingMedia&&(e["--RS__boxSizingMedia"]=this.boxSizingMedia),this.boxSizingTable&&(e["--RS__boxSizingTable"]=this.boxSizingTable),this.colWidth!=null&&(e["--RS__colWidth"]=this.colWidth),this.colCount!=null&&(e["--RS__colCount"]=this.toUnitless(this.colCount)),this.colGap!=null&&(e["--RS__colGap"]=this.toPx(this.colGap)),this.codeFontFamily&&(e["--RS__codeFontFamily"]=this.codeFontFamily),this.compFontFamily&&(e["--RS__compFontFamily"]=this.compFontFamily),this.defaultLineLength!=null&&(e["--RS__defaultLineLength"]=this.toPx(this.defaultLineLength)),this.flowSpacing!=null&&(e["--RS__flowSpacing"]=this.toRem(this.flowSpacing)),this.humanistTf&&(e["--RS__humanistTf"]=this.humanistTf),this.linkColor&&(e["--RS__linkColor"]=this.linkColor),this.maxMediaWidth&&(e["--RS__maxMediaWidth"]=this.toVw(this.maxMediaWidth)),this.maxMediaHeight&&(e["--RS__maxMediaHeight"]=this.toVh(this.maxMediaHeight)),this.modernTf&&(e["--RS__modernTf"]=this.modernTf),this.monospaceTf&&(e["--RS__monospaceTf"]=this.monospaceTf),this.noOverflow&&(e["--RS__disableOverflow"]=this.toFlag("noOverflow")),this.noVerticalPagination&&(e["--RS__disablePagination"]=this.toFlag("noVerticalPagination")),this.oldStyleTf&&(e["--RS__oldStyleTf"]=this.oldStyleTf),this.pageGutter!=null&&(e["--RS__pageGutter"]=this.toPx(this.pageGutter)),this.paraIndent!=null&&(e["--RS__paraIndent"]=this.toRem(this.paraIndent)),this.paraSpacing!=null&&(e["--RS__paraSpacing"]=this.toRem(this.paraSpacing)),this.primaryColor&&(e["--RS__primaryColor"]=this.primaryColor),this.sansSerifJa&&(e["--RS__sans-serif-ja"]=this.sansSerifJa),this.sansSerifJaV&&(e["--RS__sans-serif-ja-v"]=this.sansSerifJaV),this.sansTf&&(e["--RS__sansTf"]=this.sansTf),this.scrollPaddingBottom!=null&&(e["--RS__scrollPaddingBottom"]=this.toPx(this.scrollPaddingBottom)),this.scrollPaddingLeft!=null&&(e["--RS__scrollPaddingLeft"]=this.toPx(this.scrollPaddingLeft)),this.scrollPaddingRight!=null&&(e["--RS__scrollPaddingRight"]=this.toPx(this.scrollPaddingRight)),this.scrollPaddingTop!=null&&(e["--RS__scrollPaddingTop"]=this.toPx(this.scrollPaddingTop)),this.secondaryColor&&(e["--RS__secondaryColor"]=this.secondaryColor),this.selectionBackgroundColor&&(e["--RS__selectionBackgroundColor"]=this.selectionBackgroundColor),this.selectionTextColor&&(e["--RS__selectionTextColor"]=this.selectionTextColor),this.serifJa&&(e["--RS__serif-ja"]=this.serifJa),this.serifJaV&&(e["--RS__serif-ja-v"]=this.serifJaV),this.textColor&&(e["--RS__textColor"]=this.textColor),this.typeScale&&(e["--RS__typeScale"]=this.toUnitless(this.typeScale)),this.visitedColor&&(e["--RS__visitedColor"]=this.visitedColor),this.experiments&&this.experiments.forEach(t=>{e["--RS__"+t]=er[t].value}),e}};var Rr=class{constructor(e){this.rsProperties=e.rsProperties,this.userProperties=e.userProperties,this.lineLengths=e.lineLengths,this.container=e.container,this.containerParent=e.container.parentElement||document.documentElement,this.constraint=e.constraint,this.isCJKVertical=e.isCJKVertical??!1,this.cachedColCount=e.userProperties.colCount,this.effectiveContainerWidth=$t(this.containerParent)}update(e){this.cachedColCount=e.columnCount,e.constraint!==this.constraint&&(this.constraint=e.constraint),e.pageGutter!==this.rsProperties.pageGutter&&(this.rsProperties.pageGutter=e.pageGutter),e.scrollPaddingBottom!==this.rsProperties.scrollPaddingBottom&&(this.rsProperties.scrollPaddingBottom=e.scrollPaddingBottom),e.scrollPaddingLeft!==this.rsProperties.scrollPaddingLeft&&(this.rsProperties.scrollPaddingLeft=e.scrollPaddingLeft),e.scrollPaddingRight!==this.rsProperties.scrollPaddingRight&&(this.rsProperties.scrollPaddingRight=e.scrollPaddingRight),e.scrollPaddingTop!==this.rsProperties.scrollPaddingTop&&(this.rsProperties.scrollPaddingTop=e.scrollPaddingTop),e.experiments!==this.rsProperties.experiments&&(this.rsProperties.experiments=e.experiments),this.lineLengths.update({fontFace:e.fontFamily,letterSpacing:e.letterSpacing,padding:e.scroll?(e.scrollPaddingLeft||0)+(e.scrollPaddingRight||0):(e.pageGutter||0)*2,wordSpacing:e.wordSpacing,optimalChars:e.optimalLineLength,minChars:e.minimalLineLength,maxChars:e.maximalLineLength});let t=this.updateLayout(e.fontSize,e.deprecatedFontSize||e.iOSPatch,e.scroll,e.columnCount);t?.effectiveContainerWidth&&(this.effectiveContainerWidth=t?.effectiveContainerWidth);let o={a11yNormalize:e.textNormalization,backgroundColor:e.backgroundColor,blendFilter:e.blendFilter,bodyHyphens:typeof e.hyphens!="boolean"?null:e.hyphens?"auto":"none",colCount:t?.colCount,darkenFilter:e.darkenFilter,deprecatedFontSize:e.deprecatedFontSize,fontFamily:e.fontFamily,fontOpticalSizing:typeof e.fontOpticalSizing!="boolean"?null:e.fontOpticalSizing?"auto":"none",fontSize:e.fontSize,fontSizeNormalize:e.fontSizeNormalize,fontWeight:e.fontWeight,fontWidth:e.fontWidth,invertFilter:e.invertFilter,invertGaijiFilter:e.invertGaijiFilter,iOSPatch:e.iOSPatch,iPadOSPatch:e.iPadOSPatch,letterSpacing:e.letterSpacing,ligatures:typeof e.ligatures!="boolean"?null:e.ligatures?"common-ligatures":"none",lineHeight:e.lineHeight,lineLength:t?.effectiveLineLength,linkColor:e.linkColor,noRuby:e.noRuby,paraIndent:e.paragraphIndent,paraSpacing:e.paragraphSpacing,selectionBackgroundColor:e.selectionBackgroundColor,selectionTextColor:e.selectionTextColor,textAlign:e.textAlign,textColor:e.textColor,view:typeof e.scroll!="boolean"?null:e.scroll?"scroll":"paged",visitedColor:e.visitedColor,wordSpacing:e.wordSpacing};this.userProperties=new wi(o)}updateLayout(e,t,o,r){return this.isCJKVertical?this.computeCJKVerticalLength(e,t):o??this.userProperties.view==="scroll"?this.computeScrollLength(e,t):this.paginate(e,t,r)}getCompensatedMetrics(e,t){let o=e||this.userProperties.fontSize||1,r=o<1?1/o:t?o:1;return{zoomFactor:o,zoomCompensation:r,optimal:Math.round(this.lineLengths.optimalLineLength)*o,minimal:this.lineLengths.minimalLineLength!==null?Math.round(this.lineLengths.minimalLineLength*o):null,maximal:this.lineLengths.maximalLineLength!==null?Math.round(this.lineLengths.maximalLineLength*o):null}}paginate(e,t,o){let r=Math.round($t(this.containerParent)-this.constraint),n=this.getCompensatedMetrics(e,t),{zoomCompensation:a,optimal:s,minimal:c,maximal:l}=n,h=()=>r>=s&&l!==null?Math.min(Math.round(l*a),r):r,d=1,u=r;if(o===void 0)return{colCount:void 0,effectiveContainerWidth:u,effectiveLineLength:Math.round(u/d*a)};if(o===null)if(r>=s&&l!==null){d=Math.floor(r/s);let m=Math.round(d*(l*a));u=Math.min(m,r)}else u=h();else if(o>1){let m=Math.round(o*(c!==null?c:s));if(r>=m)if(d=o,l===null)u=r;else{let p=Math.round(d*(l*a));u=Math.min(p,r)}else if(c!==null&&r<Math.round(o*c))if(d=Math.floor(r/c),d<=1)d=1,u=h();else{let p=Math.round(d*(s*a));u=Math.min(p,r)}else{d=o;let p=Math.round(d*(s*a));u=Math.min(p,r)}}else d=1,u=h();return{colCount:d,effectiveContainerWidth:u,effectiveLineLength:Math.round(u/d/(e&&e>=1?e:1)*a)}}computeCJKVerticalLength(e,t){let o=Math.round($t(this.containerParent)-this.constraint),r=Math.round(td(this.containerParent)),n=this.getCompensatedMetrics(e,t),a=n.maximal!==null?Math.min(Math.round(n.maximal*n.zoomCompensation),r):r;return{colCount:void 0,effectiveContainerWidth:o,effectiveLineLength:a}}computeScrollLength(e,t){let o=Math.round($t(this.containerParent)-this.constraint),r=this.getCompensatedMetrics(e&&(e<1||t)?e:1,t),n=r.zoomCompensation,a=r.optimal,s=r.maximal,c,l=o,h=Math.round(a*n);if(s===null)h=o;else{let d=Math.min(Math.round(s*n),o);h=t?d:Math.round(d*n)}return{colCount:c,effectiveContainerWidth:l,effectiveLineLength:h}}setContainerWidth(){this.container.style.width=`${this.effectiveContainerWidth}px`}resizeHandler(){let e=this.updateLayout(this.userProperties.fontSize,this.userProperties.deprecatedFontSize||this.userProperties.iOSPatch,this.userProperties.view==="scroll",this.cachedColCount);this.userProperties.colCount=e.colCount,this.userProperties.lineLength=e.effectiveLineLength,this.effectiveContainerWidth=e.effectiveContainerWidth,this.container.style.width=`${this.effectiveContainerWidth}px`}};var kg="modulepreload",Cg=function(i){return"/"+i},id={},Le=function(i,e,t){let o=Promise.resolve();if(e&&e.length>0){let n=function(c){return Promise.all(c.map(l=>Promise.resolve(l).then(h=>({status:"fulfilled",value:h}),h=>({status:"rejected",reason:h}))))};document.getElementsByTagName("link");let a=document.querySelector("meta[property=csp-nonce]"),s=a?.nonce||a?.getAttribute("nonce");o=n(e.map(c=>{if(c=Cg(c),c in id)return;id[c]=!0;let l=c.endsWith(".css"),h=l?'[rel="stylesheet"]':"";if(document.querySelector(`link[href="${c}"]${h}`))return;let d=document.createElement("link");if(d.rel=l?"stylesheet":kg,l||(d.as="script"),d.crossOrigin="",d.href=c,s&&d.setAttribute("nonce",s),document.head.appendChild(d),l)return new Promise((u,m)=>{d.addEventListener("load",u),d.addEventListener("error",()=>m(new Error(`Unable to preload CSS for ${c}`)))})}))}function r(n){let a=new Event("vite:preloadError",{cancelable:!0});if(a.payload=n,window.dispatchEvent(a),!a.defaultPrevented)throw n}return o.then(n=>{for(let a of n||[])a.status==="rejected"&&r(a.reason);return i().catch(r)})};var od=`// Note: we aren't blocking some of the events right now to try and be as nonintrusive as possible.
// For a more comprehensive implementation, see https://github.com/hackademix/noscript/blob/3a83c0e4a506f175e38b0342dad50cdca3eae836/src/content/syncFetchPolicy.js#L142
// The snippet of code at the beginning of this source is an attempt at defence against JS using persistent storage
(function() {
    const noop = () => {}, emptyObj = {}, emptyPromise = () => Promise.resolve(void 0), fakeStorage = {
        getItem: noop, 
        setItem: noop, 
        removeItem: noop, 
        clear: noop, 
        key: noop, 
        length: 0
    };
    
    ["localStorage", "sessionStorage"].forEach((e) => Object.defineProperty(window, e, {
        get: () => fakeStorage,
        configurable: !0
    }));
    
    Object.defineProperty(document, "cookie", {
        get: () => "",
        set: noop,
        configurable: !0
    });
    
    Object.defineProperty(window, "indexedDB", {
        get: () => {},
        configurable: !0
    });
    
    Object.defineProperty(window, "caches", {
        get: () => emptyObj,
        configurable: !0
    });
    
    Object.defineProperty(navigator, "storage", {
        get: () => ({
            persist: emptyPromise,
            persisted: emptyPromise,
            estimate: () => Promise.resolve({quota: 0, usage: 0})
        }),
        configurable: !0
    });
    
    Object.defineProperty(navigator, "serviceWorker", {
        get: () => ({
            register: emptyPromise,
            getRegistration: emptyPromise,
            ready: emptyPromise()
        }),
        configurable: !0
    });

    window._readium_blockedEvents = [];
    window._readium_blockEvents = true;
    window._readium_eventBlocker = (e) => {
        if(!window._readium_blockEvents) return;
        e.preventDefault();
        e.stopImmediatePropagation();
        _readium_blockedEvents.push([
            1, e, e.currentTarget || e.target
        ]);
    };
    window.addEventListener("DOMContentLoaded", window._readium_eventBlocker, true);
    window.addEventListener("load", window._readium_eventBlocker, true);
})();
`;async function Fd(i,e){let t=i.effectiveLayout===v.fixed,o=e.filter(s=>s.mediaType.isHTML).map(s=>s.href),r=o.length>0?o:[/\.xhtml$/,/\.html$/],n=[{id:"css-selector-generator",as:"script",target:"head",blob:new Blob([nr(jh)],{type:"text/javascript"})},{id:"execution-prevention",as:"script",target:"head",blob:new Blob([nr(od)],{type:"text/javascript"}),condition:s=>!!(s.querySelector("script")||s.querySelector("body[onload]:not(body[onload=''])"))}],a=[{id:"onload-proxy",as:"script",target:"head",blob:new Blob([nr(Gh)],{type:"text/javascript"}),condition:s=>!!(s.querySelector("script")||s.querySelector("body[onload]:not(body[onload=''])"))}];if(!t){let s=xt(i),c,l,h;switch(s){case"rtl":{let[d,u,m]=await Promise.all([Le(()=>Promise.resolve().then(()=>(nd(),rd)),[]),Le(()=>Promise.resolve().then(()=>(sd(),ad)),[]),Le(()=>Promise.resolve().then(()=>(cd(),ld)),[])]);c=d.default,l=u.default,h=m.default;break}case"cjk-horizontal":{let[d,u,m]=await Promise.all([Le(()=>Promise.resolve().then(()=>(dd(),hd)),[]),Le(()=>Promise.resolve().then(()=>(md(),ud)),[]),Le(()=>Promise.resolve().then(()=>(gd(),pd)),[])]);c=d.default,l=u.default,h=m.default;break}case"cjk-vertical":case"mongolian-vertical":{let[d,u,m]=await Promise.all([Le(()=>Promise.resolve().then(()=>(yd(),fd)),[]),Le(()=>Promise.resolve().then(()=>(bd(),Sd)),[]),Le(()=>Promise.resolve().then(()=>(vd(),_d)),[])]);c=d.default,l=u.default,h=m.default;break}default:{let[d,u,m]=await Promise.all([Le(()=>Promise.resolve().then(()=>(xd(),wd)),[]),Le(()=>Promise.resolve().then(()=>(Cd(),kd)),[]),Le(()=>Promise.resolve().then(()=>(Ed(),Rd)),[])]);c=d.default,l=u.default,h=m.default;break}}if(n.unshift({id:"readium-css-before",as:"link",target:"head",blob:new Blob([to(c)],{type:"text/css"}),rel:"stylesheet"}),a.unshift({id:"readium-css-default",as:"link",target:"head",blob:new Blob([to(l)],{type:"text/css"}),rel:"stylesheet",condition:d=>!(d.querySelector("link[rel='stylesheet']")||d.querySelector("style")||d.querySelector("[style]:not([style=''])"))},{id:"readium-css-after",as:"link",target:"head",blob:new Blob([to(h)],{type:"text/css"}),rel:"stylesheet"}),(s==="cjk-horizontal"||s==="cjk-vertical")&&(i.description==="ebpaj-guide-1.0"||i.otherMetadata?.["ebpaj:guide-version"]!==void 0)){let{default:d}=await Le(async()=>{let{default:u}=await Promise.resolve().then(()=>(Td(),Pd));return{default:u}},[]);a.push({id:"readium-css-ebpaj",as:"link",target:"head",blob:new Blob([to(d)],{type:"text/css"}),rel:"stylesheet"})}}return[{resources:r,prepend:n,append:a}]}var oo=class{constructor(e,t){this.backgroundColor=e.backgroundColor||t.backgroundColor||null,this.blendFilter=typeof e.blendFilter=="boolean"?e.blendFilter:t.blendFilter??null,this.columnCount=e.columnCount!==void 0?e.columnCount:t.columnCount!==void 0?t.columnCount:null,this.constraint=e.constraint||t.constraint,this.darkenFilter=typeof e.darkenFilter=="boolean"?e.darkenFilter:t.darkenFilter??null,this.deprecatedFontSize=typeof e.deprecatedFontSize=="boolean"?e.deprecatedFontSize:t.deprecatedFontSize??null,this.fontFamily=e.fontFamily||t.fontFamily||null,this.fontSize=e.fontSize!==void 0?e.fontSize:t.fontSize!==void 0?t.fontSize:null,this.fontSizeNormalize=typeof e.fontSizeNormalize=="boolean"?e.fontSizeNormalize:t.fontSizeNormalize??null,this.fontOpticalSizing=typeof e.fontOpticalSizing=="boolean"?e.fontOpticalSizing:t.fontOpticalSizing??null,this.fontWeight=e.fontWeight!==void 0?e.fontWeight:t.fontWeight!==void 0?t.fontWeight:null,this.fontWidth=e.fontWidth!==void 0?e.fontWidth:t.fontWidth!==void 0?t.fontWidth:null,this.hyphens=typeof e.hyphens=="boolean"?e.hyphens:t.hyphens??null,this.invertFilter=typeof e.invertFilter=="boolean"?e.invertFilter:t.invertFilter??null,this.invertGaijiFilter=typeof e.invertGaijiFilter=="boolean"?e.invertGaijiFilter:t.invertGaijiFilter??null,this.iOSPatch=this.deprecatedFontSize||e.iOSPatch===!1?!1:e.iOSPatch===!0?(je.OS.iOS||je.OS.iPadOS)&&je.iOSRequest==="mobile":t.iOSPatch,this.iPadOSPatch=this.deprecatedFontSize||e.iPadOSPatch===!1?!1:e.iPadOSPatch===!0?je.OS.iPadOS&&je.iOSRequest==="desktop":t.iPadOSPatch,this.letterSpacing=e.letterSpacing!==void 0?e.letterSpacing:t.letterSpacing!==void 0?t.letterSpacing:null,this.ligatures=typeof e.ligatures=="boolean"?e.ligatures:t.ligatures??null,this.lineHeight=e.lineHeight!==void 0?e.lineHeight:t.lineHeight!==void 0?t.lineHeight:null,this.linkColor=e.linkColor||t.linkColor||null,this.maximalLineLength=e.maximalLineLength===null?null:e.maximalLineLength||t.maximalLineLength||null,this.minimalLineLength=e.minimalLineLength===null?null:e.minimalLineLength||t.minimalLineLength||null,this.noRuby=typeof e.noRuby=="boolean"?e.noRuby:t.noRuby??null,this.optimalLineLength=e.optimalLineLength||t.optimalLineLength,this.pageGutter=e.pageGutter!==void 0?e.pageGutter:t.pageGutter!==void 0?t.pageGutter:null,this.paragraphIndent=e.paragraphIndent!==void 0?e.paragraphIndent:t.paragraphIndent!==void 0?t.paragraphIndent:null,this.paragraphSpacing=e.paragraphSpacing!==void 0?e.paragraphSpacing:t.paragraphSpacing!==void 0?t.paragraphSpacing:null,this.scroll=typeof e.scroll=="boolean"?e.scroll:t.scroll??null,this.scrollPaddingTop=e.scrollPaddingTop!==void 0?e.scrollPaddingTop:t.scrollPaddingTop!==void 0?t.scrollPaddingTop:null,this.scrollPaddingBottom=e.scrollPaddingBottom!==void 0?e.scrollPaddingBottom:t.scrollPaddingBottom!==void 0?t.scrollPaddingBottom:null,this.scrollPaddingLeft=e.scrollPaddingLeft!==void 0?e.scrollPaddingLeft:t.scrollPaddingLeft!==void 0?t.scrollPaddingLeft:null,this.scrollPaddingRight=e.scrollPaddingRight!==void 0?e.scrollPaddingRight:t.scrollPaddingRight!==void 0?t.scrollPaddingRight:null,this.selectionBackgroundColor=e.selectionBackgroundColor||t.selectionBackgroundColor||null,this.selectionTextColor=e.selectionTextColor||t.selectionTextColor||null,this.textAlign=e.textAlign||t.textAlign||null,this.textColor=e.textColor||t.textColor||null,this.textNormalization=typeof e.textNormalization=="boolean"?e.textNormalization:t.textNormalization??null,this.visitedColor=e.visitedColor||t.visitedColor||null,this.wordSpacing=e.wordSpacing!==void 0?e.wordSpacing:t.wordSpacing!==void 0?t.wordSpacing:null,this.experiments=t.experiments||null}};var Dg="'Iowan Old Style', Sitka, 'Sitka Text', Palatino, 'Book Antiqua', 'URW Palladio L', P052, serif",zd={oldStyleTf:Dg};var Hg=16,Ld=zd.oldStyleTf,Er=class i{constructor(e){this._optimalLineLength=null,this._canvas=document.createElement("canvas"),this._optimalChars=e.optimalChars,this._minChars=e.minChars,this._maxChars=e.maxChars,this._baseFontSize=e.baseFontSize||Hg,this._fontFace=e.fontFace||Ld,this._sample=e.sample||null,this._padding=e.padding??0,this._letterSpacing=e.letterSpacing?Math.round(e.letterSpacing*this._baseFontSize):0,this._wordSpacing=e.wordSpacing?Math.round(e.wordSpacing*this._baseFontSize):0,this._isCJK=e.isCJK||!1,this._getRelative=e.getRelative||!1,this._minDivider=this._minChars&&this._minChars<this._optimalChars?this._optimalChars/this._minChars:this._minChars===null?null:1,this._maxMultiplier=this._maxChars&&this._maxChars>this._optimalChars?this._maxChars/this._optimalChars:this._maxChars===null?null:1,this._approximatedWordSpaces=i.approximateWordSpaces(this._optimalChars,this._sample)}updateMultipliers(){this._minDivider=this._minChars&&this._minChars<this._optimalChars?this._optimalChars/this._minChars:this._minChars===null?null:1,this._maxMultiplier=this._maxChars&&this._maxChars>this._optimalChars?this._maxChars/this._optimalChars:this._maxChars===null?null:1}update(e){e.optimalChars&&(this._optimalChars=e.optimalChars),e.minChars!==void 0&&(this._minChars=e.minChars),e.maxChars!==void 0&&(this._maxChars=e.maxChars),e.baseFontSize&&(this._baseFontSize=e.baseFontSize),e.fontFace!==void 0&&(this._fontFace=e.fontFace||Ld),e.letterSpacing&&(this._letterSpacing=e.letterSpacing),e.wordSpacing&&(this._wordSpacing=e.wordSpacing),e.isCJK!=null&&(this._isCJK=e.isCJK),e.padding!==void 0&&(this._padding=e.padding??0),e.getRelative&&(this._getRelative=e.getRelative),e.sample&&(this._sample=e.sample,this._approximatedWordSpaces=i.approximateWordSpaces(this._optimalChars,this._sample)),this.updateMultipliers(),this._optimalLineLength=this.getOptimalLineLength()}get baseFontSize(){return this._baseFontSize}get minimalLineLength(){return this._optimalLineLength||(this._optimalLineLength=this.getOptimalLineLength()),this._minDivider!==null?Math.round(this._optimalLineLength/this._minDivider+this._padding)/(this._getRelative?this._baseFontSize:1):null}get maximalLineLength(){return this._optimalLineLength||(this._optimalLineLength=this.getOptimalLineLength()),this._maxMultiplier!==null?Math.round(this._optimalLineLength*this._maxMultiplier+this._padding)/(this._getRelative?this._baseFontSize:1):null}get optimalLineLength(){return this._optimalLineLength||(this._optimalLineLength=this.getOptimalLineLength()),Math.round(this._optimalLineLength+this._padding)/(this._getRelative?this._baseFontSize:1)}get all(){return this._optimalLineLength||(this._optimalLineLength=this.getOptimalLineLength()),{min:this.minimalLineLength,max:this.maximalLineLength,optimal:this.optimalLineLength,baseFontSize:this._baseFontSize}}static approximateWordSpaces(e,t){let o=0;if(t&&t.length>=e){let r=t.match(/([\s]+)/gi);o=(r?r.length:0)*(e/t.length)}return o}getLineLengthFallback(){let e=this._letterSpacing*(this._optimalChars-1),t=this._wordSpacing*this._approximatedWordSpaces;return this._optimalChars*(this._baseFontSize*.5)+e+t}getOptimalLineLength(){if(this._fontFace){if(typeof this._fontFace=="string")return this.measureText(this._fontFace);{let e=new FontFace(this._fontFace.name,`url(${this._fontFace.url})`);e.load().then(()=>(document.fonts.add(e),this.measureText(e.family)),t=>{})}}return this.getLineLengthFallback()}measureText(e){let t=this._canvas.getContext("2d");if(t&&e){let o=this._isCJK?"\u6C34".repeat(this._optimalChars):"0".repeat(this._optimalChars);if(t.font=`${this._baseFontSize}px ${e}`,this._sample&&this._sample.length>=this._optimalChars&&(o=this._sample.slice(0,this._optimalChars)),Object.hasOwn(t,"letterSpacing")&&Object.hasOwn(t,"wordSpacing"))return t.letterSpacing=this._letterSpacing.toString()+"px",t.wordSpacing=this._wordSpacing.toString()+"px",t.measureText(o).width;{let r=this._letterSpacing*(this._optimalChars-1),n=this._wordSpacing*i.approximateWordSpaces(this._optimalChars,this._sample);return t.measureText(o).width+r+n}}else return this.getLineLengthFallback()}};var Wg=i=>({frameLoaded:i.frameLoaded||(()=>{}),positionChanged:i.positionChanged||(()=>{}),timelineItemChanged:i.timelineItemChanged||(()=>{}),tap:i.tap||(()=>!1),click:i.click||(()=>!1),zoom:i.zoom||(()=>{}),miscPointer:i.miscPointer||(()=>{}),scroll:i.scroll||(()=>{}),customEvent:i.customEvent||(()=>{}),handleLocator:i.handleLocator||(()=>!1),textSelected:i.textSelected||(()=>{}),contentProtection:i.contentProtection||(()=>{}),contextMenu:i.contextMenu||(()=>{}),peripheral:i.peripheral||(()=>{})});function Bg(i,e){return i.length===e.length&&i.every((t,o)=>t===e[o])}var Pr=class i extends Zo{constructor(e,t,o,r=[],n=void 0,a={preferences:{},defaults:{}}){super(),this._visibleFragmentIds=[],this._notifiedVisibleFragmentIds=[],this._timelineAugmented=!1,this._preferencesEditor=null,this._injector=null,this._isNavigating=!1,this._navigatorProtector=null,this._keyboardPeripheralsManager=null,this._suspiciousActivityListener=null,this._keyboardPeripheralListener=null,this._decorations=new Map,this._decorationObservers=new Map,this._decorationHoveredDecorations=new Map,this._decorationActivationState=new Map,this._decorationHoverState=new Map,this._decorationActivationConsumed=!1,this.reflowViewport={readingOrder:[],progressions:new Map,positions:null},this.pub=t,this.container=e,this.listeners=Wg(o),this.currentLocation=n,r.length&&(this.positions=r),this._preferences=new Yt(a.preferences),this._defaults=new kr(a.defaults),this._settings=new oo(this._preferences,this._defaults);let s=xt(t.metadata),c=s==="cjk-horizontal",l=s==="cjk-vertical",h=l||s==="mongolian-vertical",d=c||l;this._css=new Rr({rsProperties:new Cr({noVerticalPagination:h||void 0}),userProperties:new wi({}),lineLengths:new Er({optimalChars:this._settings.optimalLineLength,minChars:this._settings.minimalLineLength,maxChars:this._settings.maximalLineLength,padding:this._settings.scroll?(this._settings.scrollPaddingLeft||0)+(this._settings.scrollPaddingRight||0):(this._settings.pageGutter||0)*2,fontFace:this._settings.fontFamily,letterSpacing:this._settings.letterSpacing,wordSpacing:this._settings.wordSpacing,isCJK:d}),container:e,constraint:this._settings.constraint,isCJKVertical:h}),this._layout=i.determineLayout(t,!!this._settings.scroll),this.currentProgression=t.metadata.effectiveReadingProgression,this._injectablesConfig=a.injectables||{rules:[],allowedDomains:[]},this._readiumRulesPromise=Fd(t.metadata,t.readingOrder.items),this._contentProtection=a.contentProtection||{},this._decoratorConfig=a.decoratorConfig||{},this._keyboardPeripherals=this.mergeKeyboardPeripherals(this._contentProtection,a.keyboardPeripherals||[]),(this._contentProtection.disableContextMenu||this._contentProtection.checkAutomation||this._contentProtection.checkIFrameEmbedding||this._contentProtection.monitorDevTools||this._contentProtection.protectPrinting?.disable)&&(this._navigatorProtector=new dr(this._contentProtection),this._suspiciousActivityListener=u=>{let{type:m,...p}=u.detail;m==="context_menu"?this.listeners.contextMenu(p):this.listeners.contentProtection(m,p)},window.addEventListener(ur,this._suspiciousActivityListener)),this._keyboardPeripherals.length>0&&(this._keyboardPeripheralsManager=new mr({keyboardPeripherals:this._keyboardPeripherals}),this._keyboardPeripheralListener=u=>{let m=u.detail;this.listeners.peripheral(m)},window.addEventListener(pr,this._keyboardPeripheralListener)),this.resizeObserver=new ResizeObserver(()=>this.ownerWindow.requestAnimationFrame(async()=>await this.resizeHandler())),this.resizeObserver.observe(this.container.parentElement||document.documentElement)}static determineLayout(e,t){let o=e.metadata.effectiveLayout;if(o===v.fixed)return v.fixed;if(e.metadata.otherMetadata&&"http://openmangaformat.org/schema/1.0#version"in e.metadata.otherMetadata)return v.fixed;if(e.metadata?.conformsTo?.includes(Re.DIVINA))return v.fixed;if(o===v.scrolled)return v.scrolled;let r=xt(e.metadata);return r==="cjk-vertical"||r==="mongolian-vertical"?v.scrolled:o===v.reflowable&&t?v.scrolled:v.reflowable}async load(){if(this.positions?.length||(this.positions=await this.pub.positionsFromManifest()),!this._injector){let e=await this._readiumRulesPromise;this._injector=new rr({rules:[...e,...this._injectablesConfig.rules],allowedDomains:this._injectablesConfig.allowedDomains})}if(this._layout===v.fixed)this.framePool=new xr(this.container,this.positions,this.pub,this._injector,this._contentProtection,this._keyboardPeripherals),this.framePool.listener=(e,t)=>{this.eventListener(e,t)};else{await this.updateCSS(!1);let e=this.compileCSSProperties(this._css);this.framePool=new gr(this.container,this.positions,e,this._injector,this._contentProtection,this._keyboardPeripherals,t=>this.pub.timeline.segmentsForHref(t).flatMap(o=>o.references).map(o=>{let r=o.indexOf("#");return r>=0?o.slice(r+1):""}).filter(Boolean))}this.currentLocation===void 0&&(this.currentLocation=this.positions[0]),await this.resizeHandler(),await this.apply()}get settings(){if(this._layout===v.fixed)return Object.freeze({...this._settings});{let e=this._css.userProperties.colCount||this._css.rsProperties.colCount||this._settings.columnCount;return Object.freeze({...this._settings,columnCount:e})}}get preferencesEditor(){return this._preferencesEditor===null&&(this._preferencesEditor=new io(this._preferences,this.settings,this.pub.metadata)),this._preferencesEditor}async submitPreferences(e){this._preferences=this._preferences.merging(e),await this.applyPreferences()}async applyPreferences(){let e=this._settings;this._settings=new oo(this._preferences,this._defaults),this._preferencesEditor!==null&&(this._preferencesEditor=new io(this._preferences,this.settings,this.pub.metadata)),this._layout===v.fixed?this.handleFXLPrefs(e,this._settings):await this.updateCSS(!0)}handleFXLPrefs(e,t){e.columnCount!==t.columnCount&&this.framePool.setPerPage(t.columnCount)}async updateCSS(e){this._css.update(this._settings),e&&await this.commitCSS(this._css)}compileCSSProperties(e){let t={};for(let[o,r]of Object.entries(e.rsProperties.toCSSProperties()))t[o]=r;for(let[o,r]of Object.entries(e.userProperties.toCSSProperties()))t[o]=r;return t}async commitCSS(e){if(!this.framePool)return;let t=this.compileCSSProperties(e);this.framePool.setCSSProperties(t),this._css.userProperties.view==="paged"&&this._layout===v.scrolled?await this.setLayout(v.reflowable):this._css.userProperties.view==="scroll"&&this._layout===v.reflowable&&await this.setLayout(v.scrolled),this._css.setContainerWidth()}async resizeHandler(){let e=this.container.parentElement||document.documentElement;if(this._layout===v.fixed){if(this.container.style.width=`${$t(e)-this._settings.constraint}px`,!this.framePool)return;this.framePool.resizeHandler()}else{let t=this._css.userProperties.colCount,o=this._css.userProperties.lineLength;this._css.resizeHandler(),(this._css.userProperties.view!=="scroll"&&t!==this._css.userProperties.colCount||o!==this._css.userProperties.lineLength)&&await this.commitCSS(this._css)}}get layout(){return this._layout}get ownerWindow(){return this.container.ownerDocument.defaultView||window}get _cframes(){return(this.framePool?.currentFrames??[]).filter(e=>!(e instanceof vi&&e.isDestroyed))}get pool(){return this.framePool}eventListener(e,t,o){switch(e){case"_pong":if(this.listeners.frameLoaded(this._cframes[0].iframe.contentWindow),this.listeners.positionChanged(this.currentLocation),this._notifyTimelineChange(this.currentLocation),o){let l=this._cframes.filter(d=>!!d).indexOf(o),h=l>=0?this.viewport.readingOrder[l]:void 0;h&&this._reapplyDecorationsToFrame(o,h)}else this._reapplyDecorationsToCurrentFrames();break;case"first_visible_locator":let r=H.deserialize(t);if(!r)break;this.currentLocation=new H({href:this.currentLocation.href,type:this.currentLocation.type,title:this.currentLocation.title,locations:r?.locations,text:r?.text}),this.listeners.positionChanged(this.currentLocation),this._notifyTimelineChange(this.currentLocation);break;case"text_selected":{let l=t;if(o){let h=this._cframes.filter(u=>!!u).indexOf(o),d=h>=0?this.viewport.readingOrder[h]:void 0;if(d){let u=this.pub.readingOrder.findWithHref(d);l.locator=new H({href:d,type:u.type||"application/xhtml+xml",text:new me({highlight:l.text})})}}this.listeners.textSelected(l);break}case"decoration_activated":{this._handleDecorationActivated(t)&&(this._decorationActivationConsumed=!0);break}case"decoration_pointer_enter":this._handleDecorationPointerEnter(t);break;case"decoration_pointer_leave":this._handleDecorationPointerLeave(t);break;case"click":case"tap":if(this._decorationActivationConsumed){this._decorationActivationConsumed=!1;break}let n=t;if(n.interactiveElement){let l=new DOMParser().parseFromString(n.interactiveElement,"text/html").body.children[0];if(l.nodeType===l.ELEMENT_NODE&&l.nodeName==="A"&&l.hasAttribute("href")){let h=l.attributes.getNamedItem("href")?.value;if(h.startsWith("#"))this.go(this.currentLocation.copyWithLocations({fragments:[h.substring(1)]}),!1,()=>{});else if(h.startsWith("http://")||h.startsWith("https://")||h.startsWith("mailto:")||h.startsWith("tel:"))this.listeners.handleLocator(new Ie({href:h}).locator);else try{this.goLink(new Ie({href:ia.join(ia.dirname(this.currentLocation.href),h)}),!1,()=>{})}catch(d){console.warn(`Couldn't go to link for ${h}: ${d}`),this.listeners.handleLocator(new Ie({href:h}).locator)}}else console.log("Clicked on",l)}else{if(this._layout===v.fixed&&this.framePool.doNotDisturb&&(n.doNotDisturb=!0),this._layout===v.fixed&&(this.currentProgression===L.rtl||this.currentProgression===L.ltr)&&this.framePool.currentFrames.length>1){let h=this.framePool.currentFrames;n.targetFrameSrc===h[this.currentProgression===L.rtl?0:1]?.source&&(n.x+=(h[this.currentProgression===L.rtl?1:0]?.iframe.contentWindow?.innerWidth??0)*window.devicePixelRatio)}if(e==="click"?this.listeners.click(n):this.listeners.tap(n))break;let l=(this._cframes.length===2?this._cframes[0].window.innerWidth+this._cframes[1].window.innerWidth:this._cframes[0].window.innerWidth)*window.devicePixelRatio/4;n.x>=l&&n.x<=l*3&&this.listeners.miscPointer(1),n.x<l?this.goLeft(!1,()=>{}):n.x>l*3&&this.goRight(!1,()=>{})}break;case"tap_more":this.listeners.miscPointer(t);break;case"no_more":this.changeResource(1);break;case"no_less":this.changeResource(-1);break;case"swipe":break;case"scroll":this.listeners.scroll(t);break;case"zoom":this.listeners.zoom(t);break;case"progress":this.syncLocation(t);break;case"content_protection":let a=t;this.listeners.contentProtection(a.type,a);break;case"context_menu":this.listeners.contextMenu(t);break;case"keyboard_peripherals":let s=t,c={...s,interactiveElement:void 0};s.interactiveElement&&(c.interactiveElement=new DOMParser().parseFromString(s.interactiveElement,"text/html").body.children[0]),this.listeners.peripheral(c);break;case"log":console.log(this._cframes[0]?.source?.split("/")[3],...t);break;default:this.listeners.customEvent(e,t);break}}determineModules(){let e=Array.from(Si.keys());if(this._layout===v.fixed)return e.filter(r=>Vn.includes(r));e=e.filter(r=>Kn.includes(r));let t=xt(this.pub.metadata);if(t==="cjk-vertical"||t==="mongolian-vertical")return e.filter(r=>r!=="column_snapper"&&r!=="scroll_snapper");let o=e;return this._layout===v.scrolled?e=o.filter(r=>r!=="column_snapper"&&r!=="cjk_vertical_snapper"):e=o.filter(r=>r!=="scroll_snapper"&&r!=="cjk_vertical_snapper"),e}attachListener(){let e=this._cframes.filter(t=>!!t);if(e.length===0)throw Error("no cframe to attach listener to");e.forEach(t=>{t.msg&&(t.msg.listener=(o,r)=>{this.eventListener(o,r,t)})}),this._reapplyDecorationsToCurrentFrames()}async apply(){if(await this.framePool.update(this.pub,this.currentLocator,this.determineModules()),this.attachListener(),this.pub.readingOrder.findIndexWithHref(this.currentLocation.href)<0)throw Error("Link for "+this.currentLocation.href+" not found!")}supportsDecorationStyle(e){return Zn(e,this._decoratorConfig.decorationTemplates)}registerDecorationObserver(e,t){this._decorationObservers.has(e)||this._decorationObservers.set(e,new Set),this._decorationObservers.get(e).add(t),t.onDecorationActivated&&(this._decorationActivationState.set(e,!0),this._sendDecorationActivationToFrames(e,!0)),(t.onDecorationPointerEnter||t.onDecorationPointerLeave)&&(this._decorationHoverState.set(e,!0),this._sendDecorationHoverToFrames(e,!0))}unregisterDecorationObserver(e){this._decorationObservers.forEach((t,o)=>{if(!t.has(e))return;t.delete(e);let r=[...t].some(a=>a.onDecorationActivated);this._decorationActivationState.has(o)&&!r&&(this._decorationActivationState.delete(o),this._sendDecorationActivationToFrames(o,!1));let n=[...t].some(a=>a.onDecorationPointerEnter||a.onDecorationPointerLeave);this._decorationHoverState.has(o)&&!n&&(this._decorationHoverState.delete(o),this._sendDecorationHoverToFrames(o,!1))})}_sendDecorationActivationToFrames(e,t){this._cframes.filter(o=>!!o).forEach(o=>{o.msg&&o.msg.send("decoration_activatable",{group:e,activatable:t})})}_sendDecorationHoverToFrames(e,t){this._cframes.filter(o=>!!o).forEach(o=>{o.msg&&o.msg.send("decoration_hoverable",{group:e,hoverable:t})})}applyDecorations(e,t){let o=this._decorations.get(t)??[],r=new Map(o.map(d=>[d.id,d])),n=new Map(e.map(d=>[d.id,d])),a=[],s=[],c=[];for(let[d,u]of r)n.has(d)?Qn(u,n.get(d))||c.push(n.get(d)):a.push(d);for(let[d,u]of n)r.has(d)||s.push(u);this._decorations.set(t,e),this._sendDecorationOps(t,a,s,c,o);let l=this._decorationActivationState.get(t);l!==void 0&&this._sendDecorationActivationToFrames(t,l);let h=this._decorationHoverState.get(t);h!==void 0&&this._sendDecorationHoverToFrames(t,h)}_sendDecorationOps(e,t,o,r,n){let a=this._cframes.filter(l=>!!l),s=new Map(n.map(l=>[l.id,l])),c=this.viewport.readingOrder;a.forEach((l,h)=>{if(!l.msg)return;let d=c[h];if(d){for(let u of t){let m=s.get(u);!m||m.locator.href!==d||l.msg.send("decorate",{group:e,action:"remove",decoration:{id:u}})}for(let u of o)u.locator.href===d&&l.msg.send("decorate",{group:e,action:"add",decoration:Qi(u,this._decoratorConfig.decorationTemplates)});for(let u of r)u.locator.href===d&&l.msg.send("decorate",{group:e,action:"update",decoration:Qi(u,this._decoratorConfig.decorationTemplates)})}})}_reapplyDecorationsToFrame(e,t){if(e.msg){for(let[o,r]of this._decorations){let n=r.filter(a=>a.locator.href===t);if(n.length!==0){e.msg.send("decorate",{group:o,action:"clear"});for(let a of n)e.msg.send("decorate",{group:o,action:"add",decoration:Qi(a,this._decoratorConfig.decorationTemplates)})}}for(let[o,r]of this._decorationActivationState)e.msg.send("decoration_activatable",{group:o,activatable:r});for(let[o,r]of this._decorationHoverState)e.msg.send("decoration_hoverable",{group:o,hoverable:r})}}_reapplyDecorationsToCurrentFrames(){let e=this._cframes.filter(o=>!!o),t=this.viewport.readingOrder;e.forEach((o,r)=>{let n=t[r];n&&this._reapplyDecorationsToFrame(o,n)})}_handleDecorationActivated(e){let t=this._decorationObservers.get(e.group);if(!t||t.size===0)return!1;let o=(this._decorations.get(e.group)??[]).find(a=>a.id===e.decorationId);if(!o)return!1;let r={decoration:o,group:e.group,rect:e.rect,point:e.point},n=!1;for(let a of t)a.onDecorationActivated?.(r)&&(n=!0);return n}_handleDecorationPointerEnter(e){let t=this._decorationObservers.get(e.group);if(!t||t.size===0)return;let o=(this._decorations.get(e.group)??[]).find(n=>n.id===e.decorationId);if(!o)return;this._decorationHoveredDecorations.set(e.group,o);let r={decoration:o,group:e.group,rect:e.rect,point:e.point};for(let n of t)n.onDecorationPointerEnter?.(r)}_handleDecorationPointerLeave(e){let t=this._decorationObservers.get(e.group);if(!t||t.size===0)return;let o=(this._decorations.get(e.group)??[]).find(n=>n.id===e.decorationId)??this._decorationHoveredDecorations.get(e.group);if(this._decorationHoveredDecorations.delete(e.group),!o)return;let r={decoration:o,group:e.group,rect:e.rect,point:e.point};for(let n of t)n.onDecorationPointerLeave?.(r)}async destroy(){this._suspiciousActivityListener&&window.removeEventListener(ur,this._suspiciousActivityListener),this._keyboardPeripheralListener&&window.removeEventListener(pr,this._keyboardPeripheralListener),this._navigatorProtector?.destroy(),this._keyboardPeripheralsManager?.destroy(),await this.framePool?.destroy(),this._decorations.clear(),this._decorationObservers.clear(),this._decorationHoveredDecorations.clear(),this._decorationActivationState.clear(),this._decorationHoverState.clear()}async changeResource(e){if(e===0)return!1;if(this._layout===v.fixed){let r=this.framePool,n=r.viewport.positions[0];if(e===1){if(!r.next(r.perPage))return!1}else if(e===-1){if(!r.prev(r.perPage))return!1}else throw Error("Invalid relative value for FXL");let a=r.viewport.positions[0];if(n>a){for(let s=this.positions.length-1;s>=0;s--)if(this.positions[s].href===this.pub.readingOrder.items[a-1].href){this.currentLocation=this.positions[s].copyWithLocations({progression:.999999999999});break}}else if(n<a){for(let s=0;s<this.positions.length;s++)if(this.positions[s].href===this.pub.readingOrder.items[a-1].href){this.currentLocation=this.positions[s];break}}return await this.apply(),this.listeners.positionChanged(this.currentLocation),this._notifyTimelineChange(this.currentLocation),!0}let t=this.pub.readingOrder.findIndexWithHref(this.currentLocation.href),o=Math.max(0,Math.min(this.pub.readingOrder.items.length-1,t+e));if(o===t)return this._cframes[0]?.msg?.send("shake",void 0,async r=>{}),!1;if(t>o){for(let r=this.positions.length-1;r>=0;r--)if(this.positions[r].href===this.pub.readingOrder.items[o].href){this.currentLocation=this.positions[r].copyWithLocations({progression:.999999999999});break}}else for(let r=0;r<this.positions.length;r++)if(this.positions[r].href===this.pub.readingOrder.items[o].href){this.currentLocation=this.positions[r];break}return await this.apply(),!0}findLastPositionInProgressionRange(e,t){let o=e.findLastIndex(r=>{let n=r.locations.progression;return!!(n&&n>t.start&&n<=t.end)});return o!==-1?e[o]:void 0}findNearestPositions(e){let t=this.positions.filter(a=>a.href===this.currentLocation.href),o=this.currentLocation,r,n=t.findLastIndex(a=>(a.locations.progression??0)<=e.start);if(n!==-1){o=t[n];let a=t.slice(n+1);r=this.findLastPositionInProgressionRange(a,e)}return{first:o,last:r}}updateViewport(e){this.reflowViewport.readingOrder=[],this.reflowViewport.progressions.clear(),this.reflowViewport.positions=null,this.currentLocation&&(this.reflowViewport.readingOrder.push(this.currentLocation.href),this.reflowViewport.progressions.set(this.currentLocation.href,e),this.currentLocation.locations?.position!==void 0&&(this.reflowViewport.positions=[this.currentLocation.locations.position],this.lastLocationInView?.locations?.position!==void 0&&this.reflowViewport.positions.push(this.lastLocationInView.locations.position)))}async syncLocation(e){let t=e,o=this.findNearestPositions(t);this.currentLocation=o.first.copyWithLocations({progression:t.start}),this.lastLocationInView=o.last,this.updateViewport(t),this.listeners.positionChanged(this.currentLocation);let r=t.fragmentId?this.currentLocation.copyWithLocations({fragments:[`#${t.fragmentId}`]}):this.currentLocation;this._visibleFragmentIds=t.visibleFragmentIds??[],this._notifyTimelineChange(r),await this.framePool.update(this.pub,this.currentLocation,this.determineModules())}goBackward(e,t){if(this._isNavigating){t(!1);return}this._isNavigating=!0,this._layout===v.fixed?this.changeResource(-1).then(o=>{this._isNavigating=!1,t(o)}):this._cframes[0]?.msg?.send("go_prev",void 0,async o=>{if(o)this._isNavigating=!1,t(!0);else{let r=await this.changeResource(-1);this._isNavigating=!1,t(r)}})}goForward(e,t){if(this._isNavigating){t(!1);return}this._isNavigating=!0,this._layout===v.fixed?this.changeResource(1).then(o=>{this._isNavigating=!1,t(o)}):this._cframes[0]?.msg?.send("go_next",void 0,async o=>{if(o)this._isNavigating=!1,t(!0);else{let r=await this.changeResource(1);this._isNavigating=!1,t(r)}})}get currentLocator(){return this.currentLocation}get viewport(){return this._layout===v.fixed?this.framePool?this.framePool.viewport:{readingOrder:[],progressions:new Map,positions:null}:this.reflowViewport}get isScrollStart(){let e=this.viewport.readingOrder[0];return this.viewport.progressions.get(e)?.start===0}get isScrollEnd(){let e=this.viewport.readingOrder[this.viewport.readingOrder.length-1];return this.viewport.progressions.get(e)?.end===1}get canGoBackward(){let e=this.pub.readingOrder.items[0]?.href;return!(this.viewport.progressions.has(e)&&this.viewport.progressions.get(e)?.start===0)}get canGoForward(){let e=this.pub.readingOrder.items[this.pub.readingOrder.items.length-1]?.href;return!(this.viewport.progressions.has(e)&&this.viewport.progressions.get(e)?.end===1)}get readingProgression(){return this.currentProgression}async setLayout(e){this._layout!==e&&(this._layout=e,await this.framePool.update(this.pub,this.currentLocator,this.determineModules(),!0),this.attachListener())}get publication(){return this.pub}get timeline(){let e=this.pub.timeline;if(!this._timelineAugmented&&this.positions?.length){let t=this.positions,o=new Set;e.augment((r,n)=>{if(r.children?.length)return{};let a=n.href.indexOf("#"),s=a>=0?n.href.slice(0,a):n.href,c=a>=0?n.href.slice(a+1):void 0,l=t.filter(u=>u.href===s);if(!l.length)return{};let h=c?l.find(u=>u.locations.fragments[0]===c):void 0,d;return h?d=h:o.has(s)?d=void 0:(o.add(s),d=l.reduce((u,m)=>(m.locations.position??1/0)<(u.locations.position??1/0)?m:u)),d?{position:d.locations.position,scroll:h?.locations.progression}:{}}),this._timelineAugmented=!0}return this._wrappedTimeline||(this._wrappedTimeline=new Proxy(e,{get:(t,o)=>{if(o!=="navigableFrom"){let r=Reflect.get(t,o,t);return typeof r=="function"?r.bind(t):r}return r=>{let n=this._visibleFragmentIds,a=(n.length?t.locate(this.currentLocation.copyWithLocations({fragments:[`#${n[0]}`]})):void 0)??r,s=(n.length?t.locate(this.currentLocation.copyWithLocations({fragments:[`#${n[n.length-1]}`]})):void 0)??r;if(this.isScrollStart){let h=t.ancestors(a).find(d=>d.references.includes(this.currentLocation.href));h&&(a=h)}let c=t.navigableFrom(a).previous,l=t.navigableFrom(s).next;return{previous:c,next:l}}}})),this._wrappedTimeline}async loadLocator(e,t){let o=!1,r=ft(e.locations);if(e.text?.highlight?o=await new Promise((s,c)=>{this._cframes[0].msg.send("go_text",r?[e.text?.serialize(),r]:e.text?.serialize(),l=>s(l))}):r&&(o=await new Promise((s,c)=>{this._cframes[0].msg.send("go_text",["",r],l=>s(l))})),o){t(o);return}let n=ji(e.locations);if(n&&(o=await new Promise((s,c)=>{this._cframes[0].msg.send("go_id",n,l=>s(l))})),o){t(o);return}let a=e?.locations?.progression;a&&a>0?o=await new Promise((s,c)=>{this._cframes[0].msg.send("go_progression",a,l=>s(l))}):o=!0,t(o)}go(e,t,o){let r=e.href.split("#")[0],n=this.pub.readingOrder.findWithHref(r);if(!n)return o(this.listeners.handleLocator(e));if(this._isNavigating){o(!1);return}this._isNavigating=!0,this.currentLocation=this.positions.find(a=>a.href===n.href),this.apply().then(()=>this.loadLocator(e,a=>{this._isNavigating=!1,o(a)})).then(()=>{this.attachListener()})}goLink(e,t,o){return this.go(e.locator,t,o)}_notifyTimelineChange(e){let t=this.timeline.locate(e),o=!Bg(this._visibleFragmentIds,this._notifiedVisibleFragmentIds);(t!==this._currentTimelineItem||o)&&(this._currentTimelineItem=t,this._notifiedVisibleFragmentIds=this._visibleFragmentIds,this.listeners.timelineItemChanged(t))}};var op={};_(op,{Lexer:()=>ti,List:()=>ue,OffsetToLocation:()=>Ti,TokenStream:()=>Fi,clone:()=>pn,createLexer:()=>mw,createSyntax:()=>an,definitionSyntax:()=>Ja,find:()=>gw,findAll:()=>yw,findLast:()=>fw,fork:()=>_w,fromPlainObject:()=>bw,generate:()=>dw,ident:()=>pc,isCustomProperty:()=>go,keyword:()=>po,lexer:()=>uw,parse:()=>hw,property:()=>Yr,string:()=>hn,toPlainObject:()=>Sw,tokenNames:()=>Et,tokenTypes:()=>Tr,tokenize:()=>cw,url:()=>un,vendorPrefix:()=>Of,version:()=>aw,walk:()=>pw});var Tr={};_(Tr,{AtKeyword:()=>$,BadString:()=>xi,BadUrl:()=>ge,CDC:()=>fe,CDO:()=>qt,Colon:()=>ae,Comma:()=>ye,Comment:()=>J,Delim:()=>T,Dimension:()=>P,EOF:()=>lt,Function:()=>E,Hash:()=>N,Ident:()=>S,LeftCurlyBracket:()=>oe,LeftParenthesis:()=>O,LeftSquareBracket:()=>we,Number:()=>C,Percentage:()=>W,RightCurlyBracket:()=>xe,RightParenthesis:()=>R,RightSquareBracket:()=>Oe,Semicolon:()=>se,String:()=>Ue,Url:()=>re,WhiteSpace:()=>I});var lt=0,S=1,E=2,$=3,N=4,Ue=5,xi=6,re=7,ge=8,T=9,C=10,W=11,P=12,I=13,qt=14,fe=15,ae=16,se=17,ye=18,we=19,Oe=20,O=21,R=22,oe=23,xe=24,J=25;function Z(i){return i>=48&&i<=57}function Ae(i){return Z(i)||i>=65&&i<=70||i>=97&&i<=102}function zr(i){return i>=65&&i<=90}function jg(i){return i>=97&&i<=122}function Gg(i){return zr(i)||jg(i)}function Vg(i){return i>=128}function Fr(i){return Gg(i)||Vg(i)||i===95}function ki(i){return Fr(i)||Z(i)||i===45}function Kg(i){return i>=0&&i<=8||i===11||i>=14&&i<=31||i===127}function ro(i){return i===10||i===13||i===12}function tt(i){return ro(i)||i===32||i===9}function Se(i,e){return!(i!==92||ro(e)||e===0)}function Ci(i,e,t){return i===45?Fr(e)||e===45||Se(e,t):Fr(i)?!0:i===92?Se(i,e):!1}function Lr(i,e,t){return i===43||i===45?Z(e)?2:e===46&&Z(t)?3:0:i===46?Z(e)?2:0:Z(i)?1:0}function Or(i){return i===65279||i===65534?1:0}var pa=new Array(128),Yg=128,no=130,ga=131,Ar=132,fa=133;for(let i=0;i<pa.length;i++)pa[i]=tt(i)&&no||Z(i)&&ga||Fr(i)&&Ar||Kg(i)&&fa||i||Yg;function Mr(i){return i<128?pa[i]:Ar}function Ri(i,e){return e<i.length?i.charCodeAt(e):0}function Nr(i,e,t){return t===13&&Ri(i,e+1)===10?2:1}function ct(i,e,t){let o=i.charCodeAt(e);return zr(o)&&(o=o|32),o===t}function ht(i,e,t,o){if(t-e!==o.length||e<0||t>i.length)return!1;for(let r=e;r<t;r++){let n=o.charCodeAt(r-e),a=i.charCodeAt(r);if(zr(a)&&(a=a|32),a!==n)return!1}return!0}function Od(i,e){for(;e>=0&&tt(i.charCodeAt(e));e--);return e+1}function ao(i,e){for(;e<i.length&&tt(i.charCodeAt(e));e++);return e}function ya(i,e){for(;e<i.length&&Z(i.charCodeAt(e));e++);return e}function Ke(i,e){if(e+=2,Ae(Ri(i,e-1))){for(let o=Math.min(i.length,e+5);e<o&&Ae(Ri(i,e));e++);let t=Ri(i,e);tt(t)&&(e+=Nr(i,e,t))}return e}function so(i,e){for(;e<i.length;e++){let t=i.charCodeAt(e);if(!ki(t)){if(Se(t,Ri(i,e+1))){e=Ke(i,e)-1;continue}break}}return e}function Jt(i,e){let t=i.charCodeAt(e);if((t===43||t===45)&&(t=i.charCodeAt(e+=1)),Z(t)&&(e=ya(i,e+1),t=i.charCodeAt(e)),t===46&&Z(i.charCodeAt(e+1))&&(e+=2,e=ya(i,e)),ct(i,e,101)){let o=0;t=i.charCodeAt(e+1),(t===45||t===43)&&(o=1,t=i.charCodeAt(e+2)),Z(t)&&(e=ya(i,e+1+o+1))}return e}function Ir(i,e){for(;e<i.length;e++){let t=i.charCodeAt(e);if(t===41){e++;break}Se(t,Ri(i,e+1))&&(e=Ke(i,e))}return e}function Ei(i){if(i.length===1&&!Ae(i.charCodeAt(0)))return i[0];let e=parseInt(i,16);return(e===0||e>=55296&&e<=57343||e>1114111)&&(e=65533),String.fromCodePoint(e)}var Et=["EOF-token","ident-token","function-token","at-keyword-token","hash-token","string-token","bad-string-token","url-token","bad-url-token","delim-token","number-token","percentage-token","dimension-token","whitespace-token","CDO-token","CDC-token","colon-token","semicolon-token","comma-token","[-token","]-token","(-token",")-token","{-token","}-token","comment-token"];function Pi(i=null,e){return i===null||i.length<e?new Uint32Array(Math.max(e+1024,16384)):i}var Ad=10,Xg=12,Md=13;function Nd(i){let e=i.source,t=e.length,o=e.length>0?Or(e.charCodeAt(0)):0,r=Pi(i.lines,t),n=Pi(i.columns,t),a=i.startLine,s=i.startColumn;for(let c=o;c<t;c++){let l=e.charCodeAt(c);r[c]=a,n[c]=s++,(l===Ad||l===Md||l===Xg)&&(l===Md&&c+1<t&&e.charCodeAt(c+1)===Ad&&(c++,r[c]=a,n[c]=s),a++,s=1)}r[t]=a,n[t]=s,i.lines=r,i.columns=n,i.computed=!0}var Ti=class{constructor(e,t,o,r){this.setSource(e,t,o,r),this.lines=null,this.columns=null}setSource(e="",t=0,o=1,r=1){this.source=e,this.startOffset=t,this.startLine=o,this.startColumn=r,this.computed=!1}getLocation(e,t){return this.computed||Nd(this),{source:t,offset:this.startOffset+e,line:this.lines[e],column:this.columns[e]}}getLocationRange(e,t,o){return this.computed||Nd(this),{source:o,start:{offset:this.startOffset+e,line:this.lines[e],column:this.columns[e]},end:{offset:this.startOffset+t,line:this.lines[t],column:this.columns[t]}}}};var Ye=16777215,Xe=24,lo=1,Ur=2,Pt=new Uint8Array(32);Pt[2]=22;Pt[21]=22;Pt[19]=20;Pt[23]=24;var $e=new Uint8Array(32);$e[2]=lo;$e[21]=lo;$e[19]=lo;$e[23]=lo;$e[22]=Ur;$e[20]=Ur;$e[24]=Ur;function Id(i,e,t){return i<e?e:i>t?t:i}var Fi=class{constructor(e,t){this.setSource(e,t)}reset(){this.eof=!1,this.tokenIndex=-1,this.tokenType=0,this.tokenStart=this.firstCharOffset,this.tokenEnd=this.firstCharOffset}setSource(e="",t=()=>{}){e=String(e||"");let o=e.length,r=Pi(this.offsetAndType,e.length+1),n=Pi(this.balance,e.length+1),a=0,s=-1,c=0,l=e.length;this.offsetAndType=null,this.balance=null,n.fill(0),t(e,(h,d,u)=>{let m=a++;if(r[m]=h<<Xe|u,s===-1&&(s=d),n[m]=l,h===c){let p=n[l];n[l]=m,l=p,c=Pt[r[p]>>Xe]}else this.isBlockOpenerTokenType(h)&&(l=m,c=Pt[h])}),r[a]=0<<Xe|o,n[a]=a;for(let h=0;h<a;h++){let d=n[h];if(d<=h){let u=n[d];u!==h&&(n[h]=u)}else d>a&&(n[h]=a)}this.source=e,this.firstCharOffset=s===-1?0:s,this.tokenCount=a,this.offsetAndType=r,this.balance=n,this.reset(),this.next()}lookupType(e){return e+=this.tokenIndex,e<this.tokenCount?this.offsetAndType[e]>>Xe:0}lookupTypeNonSC(e){for(let t=this.tokenIndex;t<this.tokenCount;t++){let o=this.offsetAndType[t]>>Xe;if(o!==13&&o!==25&&e--===0)return o}return 0}lookupOffset(e){return e+=this.tokenIndex,e<this.tokenCount?this.offsetAndType[e-1]&Ye:this.source.length}lookupOffsetNonSC(e){for(let t=this.tokenIndex;t<this.tokenCount;t++){let o=this.offsetAndType[t]>>Xe;if(o!==13&&o!==25&&e--===0)return t-this.tokenIndex}return 0}lookupValue(e,t){return e+=this.tokenIndex,e<this.tokenCount?ht(this.source,this.offsetAndType[e-1]&Ye,this.offsetAndType[e]&Ye,t):!1}getTokenStart(e){return e===this.tokenIndex?this.tokenStart:e>0?e<this.tokenCount?this.offsetAndType[e-1]&Ye:this.offsetAndType[this.tokenCount]&Ye:this.firstCharOffset}getTokenEnd(e){return e===this.tokenIndex?this.tokenEnd:this.offsetAndType[Id(e,0,this.tokenCount)]&Ye}getTokenType(e){return e===this.tokenIndex?this.tokenType:this.offsetAndType[Id(e,0,this.tokenCount)]>>Xe}substrToCursor(e){return this.source.substring(e,this.tokenStart)}isBlockOpenerTokenType(e){return $e[e]===lo}isBlockCloserTokenType(e){return $e[e]===Ur}getBlockTokenPairIndex(e){let t=this.getTokenType(e);if($e[t]===1){let o=this.balance[e],r=this.getTokenType(o);return Pt[t]===r?o:-1}else if($e[t]===2){let o=this.balance[e],r=this.getTokenType(o);return Pt[r]===t?o:-1}return-1}isBalanceEdge(e){return this.balance[this.tokenIndex]<e}isDelim(e,t){return t?this.lookupType(t)===9&&this.source.charCodeAt(this.lookupOffset(t))===e:this.tokenType===9&&this.source.charCodeAt(this.tokenStart)===e}skip(e){let t=this.tokenIndex+e;t<this.tokenCount?(this.tokenIndex=t,this.tokenStart=this.offsetAndType[t-1]&Ye,t=this.offsetAndType[t],this.tokenType=t>>Xe,this.tokenEnd=t&Ye):(this.tokenIndex=this.tokenCount,this.next())}next(){let e=this.tokenIndex+1;e<this.tokenCount?(this.tokenIndex=e,this.tokenStart=this.tokenEnd,e=this.offsetAndType[e],this.tokenType=e>>Xe,this.tokenEnd=e&Ye):(this.eof=!0,this.tokenIndex=this.tokenCount,this.tokenType=0,this.tokenStart=this.tokenEnd=this.source.length)}skipSC(){for(;this.tokenType===13||this.tokenType===25;)this.next()}skipUntilBalanced(e,t){let o=e,r=0,n=0;e:for(;o<this.tokenCount;o++){if(r=this.balance[o],r<e)break e;switch(n=o>0?this.offsetAndType[o-1]&Ye:this.firstCharOffset,t(this.source.charCodeAt(n))){case 1:break e;case 2:o++;break e;default:this.isBlockOpenerTokenType(this.offsetAndType[o]>>Xe)&&(o=r)}}this.skip(o-this.tokenIndex)}forEachToken(e){for(let t=0,o=this.firstCharOffset;t<this.tokenCount;t++){let r=o,n=this.offsetAndType[t],a=n&Ye,s=n>>Xe;o=a,e(s,r,a,t)}}dump(){let e=new Array(this.tokenCount);return this.forEachToken((t,o,r,n)=>{e[n]={idx:n,type:Et[t],chunk:this.source.substring(o,r),balance:this.balance[n]}}),e}};function Tt(i,e){function t(d){return d<s?i.charCodeAt(d):0}function o(){if(l=Jt(i,l),Ci(t(l),t(l+1),t(l+2))){h=12,l=so(i,l);return}if(t(l)===37){h=11,l++;return}h=10}function r(){let d=l;if(l=so(i,l),ht(i,d,l,"url")&&t(l)===40){if(l=ao(i,l+1),t(l)===34||t(l)===39){h=2,l=d+4;return}a();return}if(t(l)===40){h=2,l++;return}h=1}function n(d){for(d||(d=t(l++)),h=5;l<i.length;l++){let u=i.charCodeAt(l);switch(Mr(u)){case d:l++;return;case no:if(ro(u)){l+=Nr(i,l,u),h=6;return}break;case 92:if(l===i.length-1)break;let m=t(l+1);ro(m)?l+=Nr(i,l+1,m):Se(u,m)&&(l=Ke(i,l)-1);break}}}function a(){for(h=7,l=ao(i,l);l<i.length;l++){let d=i.charCodeAt(l);switch(Mr(d)){case 41:l++;return;case no:if(l=ao(i,l),t(l)===41||l>=i.length){l<i.length&&l++;return}l=Ir(i,l),h=8;return;case 34:case 39:case 40:case fa:l=Ir(i,l),h=8;return;case 92:if(Se(d,t(l+1))){l=Ke(i,l)-1;break}l=Ir(i,l),h=8;return}}}i=String(i||"");let s=i.length,c=Or(t(0)),l=c,h;for(;l<s;){let d=i.charCodeAt(l);switch(Mr(d)){case no:h=13,l=ao(i,l+1);break;case 34:n();break;case 35:ki(t(l+1))||Se(t(l+1),t(l+2))?(h=4,l=so(i,l+1)):(h=9,l++);break;case 39:n();break;case 40:h=21,l++;break;case 41:h=22,l++;break;case 43:Lr(d,t(l+1),t(l+2))?o():(h=9,l++);break;case 44:h=18,l++;break;case 45:Lr(d,t(l+1),t(l+2))?o():t(l+1)===45&&t(l+2)===62?(h=15,l=l+3):Ci(d,t(l+1),t(l+2))?r():(h=9,l++);break;case 46:Lr(d,t(l+1),t(l+2))?o():(h=9,l++);break;case 47:t(l+1)===42?(h=25,l=i.indexOf("*/",l+2),l=l===-1?i.length:l+2):(h=9,l++);break;case 58:h=16,l++;break;case 59:h=17,l++;break;case 60:t(l+1)===33&&t(l+2)===45&&t(l+3)===45?(h=14,l=l+4):(h=9,l++);break;case 64:Ci(t(l+1),t(l+2),t(l+3))?(h=3,l=so(i,l+1)):(h=9,l++);break;case 91:h=19,l++;break;case 92:Se(d,t(l+1))?r():(h=9,l++);break;case 93:h=20,l++;break;case 123:h=23,l++;break;case 125:h=24,l++;break;case ga:o();break;case Ar:r();break;default:h=9,l++}e(h,c,c=l)}}var zi=null,ue=class i{static createItem(e){return{prev:null,next:null,data:e}}constructor(){this.head=null,this.tail=null,this.cursor=null}createItem(e){return i.createItem(e)}allocateCursor(e,t){let o;return zi!==null?(o=zi,zi=zi.cursor,o.prev=e,o.next=t,o.cursor=this.cursor):o={prev:e,next:t,cursor:this.cursor},this.cursor=o,o}releaseCursor(){let{cursor:e}=this;this.cursor=e.cursor,e.prev=null,e.next=null,e.cursor=zi,zi=e}updateCursors(e,t,o,r){let{cursor:n}=this;for(;n!==null;)n.prev===e&&(n.prev=t),n.next===o&&(n.next=r),n=n.cursor}*[Symbol.iterator](){for(let e=this.head;e!==null;e=e.next)yield e.data}get size(){let e=0;for(let t=this.head;t!==null;t=t.next)e++;return e}get isEmpty(){return this.head===null}get first(){return this.head&&this.head.data}get last(){return this.tail&&this.tail.data}fromArray(e){let t=null;this.head=null;for(let o of e){let r=i.createItem(o);t!==null?t.next=r:this.head=r,r.prev=t,t=r}return this.tail=t,this}toArray(){return[...this]}toJSON(){return[...this]}forEach(e,t=this){let o=this.allocateCursor(null,this.head);for(;o.next!==null;){let r=o.next;o.next=r.next,e.call(t,r.data,r,this)}this.releaseCursor()}forEachRight(e,t=this){let o=this.allocateCursor(this.tail,null);for(;o.prev!==null;){let r=o.prev;o.prev=r.prev,e.call(t,r.data,r,this)}this.releaseCursor()}reduce(e,t,o=this){let r=this.allocateCursor(null,this.head),n=t,a;for(;r.next!==null;)a=r.next,r.next=a.next,n=e.call(o,n,a.data,a,this);return this.releaseCursor(),n}reduceRight(e,t,o=this){let r=this.allocateCursor(this.tail,null),n=t,a;for(;r.prev!==null;)a=r.prev,r.prev=a.prev,n=e.call(o,n,a.data,a,this);return this.releaseCursor(),n}some(e,t=this){for(let o=this.head;o!==null;o=o.next)if(e.call(t,o.data,o,this))return!0;return!1}map(e,t=this){let o=new i;for(let r=this.head;r!==null;r=r.next)o.appendData(e.call(t,r.data,r,this));return o}filter(e,t=this){let o=new i;for(let r=this.head;r!==null;r=r.next)e.call(t,r.data,r,this)&&o.appendData(r.data);return o}nextUntil(e,t,o=this){if(e===null)return;let r=this.allocateCursor(null,e);for(;r.next!==null;){let n=r.next;if(r.next=n.next,t.call(o,n.data,n,this))break}this.releaseCursor()}prevUntil(e,t,o=this){if(e===null)return;let r=this.allocateCursor(e,null);for(;r.prev!==null;){let n=r.prev;if(r.prev=n.prev,t.call(o,n.data,n,this))break}this.releaseCursor()}clear(){this.head=null,this.tail=null}copy(){let e=new i;for(let t of this)e.appendData(t);return e}prepend(e){return this.updateCursors(null,e,this.head,e),this.head!==null?(this.head.prev=e,e.next=this.head):this.tail=e,this.head=e,this}prependData(e){return this.prepend(i.createItem(e))}append(e){return this.insert(e)}appendData(e){return this.insert(i.createItem(e))}insert(e,t=null){if(t!==null)if(this.updateCursors(t.prev,e,t,e),t.prev===null){if(this.head!==t)throw new Error("before doesn't belong to list");this.head=e,t.prev=e,e.next=t,this.updateCursors(null,e)}else t.prev.next=e,e.prev=t.prev,t.prev=e,e.next=t;else this.updateCursors(this.tail,e,null,e),this.tail!==null?(this.tail.next=e,e.prev=this.tail):this.head=e,this.tail=e;return this}insertData(e,t){return this.insert(i.createItem(e),t)}remove(e){if(this.updateCursors(e,e.prev,e,e.next),e.prev!==null)e.prev.next=e.next;else{if(this.head!==e)throw new Error("item doesn't belong to list");this.head=e.next}if(e.next!==null)e.next.prev=e.prev;else{if(this.tail!==e)throw new Error("item doesn't belong to list");this.tail=e.prev}return e.prev=null,e.next=null,e}push(e){this.insert(i.createItem(e))}pop(){return this.tail!==null?this.remove(this.tail):null}unshift(e){this.prepend(i.createItem(e))}shift(){return this.head!==null?this.remove(this.head):null}prependList(e){return this.insertList(e,this.head)}appendList(e){return this.insertList(e)}insertList(e,t){return e.head===null?this:(t!=null?(this.updateCursors(t.prev,e.tail,t,e.head),t.prev!==null?(t.prev.next=e.head,e.head.prev=t.prev):this.head=e.head,t.prev=e.tail,e.tail.next=t):(this.updateCursors(this.tail,e.tail,null,e.head),this.tail!==null?(this.tail.next=e.head,e.head.prev=this.tail):this.head=e.head,this.tail=e.tail),e.head=null,e.tail=null,this)}replace(e,t){"head"in t?this.insertList(t,e):this.insert(t,e),this.remove(e)}};function Zt(i,e){let t=Object.create(SyntaxError.prototype),o=new Error;return Object.assign(t,{name:i,message:e,get stack(){return(o.stack||"").replace(/^(.+\n){1,3}/,`${i}: ${e}
`)}})}var Sa=100,Ud=60,Dd="    ";function Hd({source:i,line:e,column:t,baseLine:o,baseColumn:r},n){function a(p,g){return l.slice(p,g).map((f,y)=>String(p+y+1).padStart(u)+" |"+f).join(`
`)}let s=`
`.repeat(Math.max(o-1,0)),c=" ".repeat(Math.max(r-1,0)),l=(s+c+i).split(/\r\n?|\n|\f/),h=Math.max(1,e-n)-1,d=Math.min(e+n,l.length+1),u=Math.max(4,String(d).length)+1,m=0;t+=(Dd.length-1)*(l[e-1].substr(0,t-1).match(/\t/g)||[]).length,t>Sa&&(m=t-Ud+3,t=Ud-2);for(let p=h;p<=d;p++)p>=0&&p<l.length&&(l[p]=l[p].replace(/\t/g,Dd),l[p]=(m>0&&l[p].length>m?"\u2026":"")+l[p].substr(m,Sa-2)+(l[p].length>m+Sa-1?"\u2026":""));return[a(h,e),new Array(t+u+2).join("-")+"^",a(e,d)].filter(Boolean).join(`
`).replace(/^(\s+\d+\s+\|\n)+/,"").replace(/\n(\s+\d+\s+\|)+$/,"")}function ba(i,e,t,o,r,n=1,a=1){return Object.assign(Zt("SyntaxError",i),{source:e,offset:t,line:o,column:r,sourceFragment(c){return Hd({source:e,line:o,column:r,baseLine:n,baseColumn:a},isNaN(c)?0:c)},get formattedMessage(){return`Parse error: ${i}
`+Hd({source:e,line:o,column:r,baseLine:n,baseColumn:a},2)}})}function Wd(i){let e=this.createList(),t=!1,o={recognizer:i};for(;!this.eof;){switch(this.tokenType){case 25:this.next();continue;case 13:t=!0,this.next();continue}let r=i.getNode.call(this,o);if(r===void 0)break;t&&(i.onWhiteSpace&&i.onWhiteSpace.call(this,r,e,o),t=!1),e.push(r)}return t&&i.onWhiteSpace&&i.onWhiteSpace.call(this,null,e,o),e}var Li=()=>{},$g=33,qg=35,_a=59,Bd=123,jd=0,Jg={createList(){return[]},createSingleNodeList(i){return[i]},getFirstListNode(i){return i&&i[0]||null},getLastListNode(i){return i&&i.length>0?i[i.length-1]:null}},Zg={createList(){return new ue},createSingleNodeList(i){return new ue().appendData(i)},getFirstListNode(i){return i&&i.first},getLastListNode(i){return i&&i.last}};function Qg(i){return function(){return this[i]()}}function va(i){let e=Object.create(null);for(let t of Object.keys(i)){let o=i[t],r=o.parse||o;r&&(e[t]=r)}return e}function ef(i){let e={context:Object.create(null),features:Object.assign(Object.create(null),i.features),scope:Object.assign(Object.create(null),i.scope),atrule:va(i.atrule),pseudo:va(i.pseudo),node:va(i.node)};for(let[t,o]of Object.entries(i.parseContext))switch(typeof o){case"function":e.context[t]=o;break;case"string":e.context[t]=Qg(o);break}return{config:e,...e,...e.node}}function Gd(i){let e="",t="<unknown>",o=!1,r=Li,n=!1,a=new Ti,s=Object.assign(new Fi,ef(i||{}),{parseAtrulePrelude:!0,parseRulePrelude:!0,parseValue:!0,parseCustomProperty:!1,readSequence:Wd,consumeUntilBalanceEnd:()=>0,consumeUntilLeftCurlyBracket(h){return h===Bd?1:0},consumeUntilLeftCurlyBracketOrSemicolon(h){return h===Bd||h===_a?1:0},consumeUntilExclamationMarkOrSemicolon(h){return h===$g||h===_a?1:0},consumeUntilSemicolonIncluded(h){return h===_a?2:0},createList:Li,createSingleNodeList:Li,getFirstListNode:Li,getLastListNode:Li,parseWithFallback(h,d){let u=this.tokenIndex;try{return h.call(this)}catch(m){if(n)throw m;this.skip(u-this.tokenIndex);let p=d.call(this);return n=!0,r(m,p),n=!1,p}},lookupNonWSType(h){let d;do if(d=this.lookupType(h++),d!==13&&d!==25)return d;while(d!==jd);return jd},charCodeAt(h){return h>=0&&h<e.length?e.charCodeAt(h):0},substring(h,d){return e.substring(h,d)},substrToCursor(h){return this.source.substring(h,this.tokenStart)},cmpChar(h,d){return ct(e,h,d)},cmpStr(h,d,u){return ht(e,h,d,u)},consume(h){let d=this.tokenStart;return this.eat(h),this.substrToCursor(d)},consumeFunctionName(){let h=e.substring(this.tokenStart,this.tokenEnd-1);return this.eat(2),h},consumeNumber(h){let d=e.substring(this.tokenStart,Jt(e,this.tokenStart));return this.eat(h),d},eat(h){if(this.tokenType!==h){let d=Et[h].slice(0,-6).replace(/-/g," ").replace(/^./,p=>p.toUpperCase()),u=`${/[[\](){}]/.test(d)?`"${d}"`:d} is expected`,m=this.tokenStart;switch(h){case 1:this.tokenType===2||this.tokenType===7?(m=this.tokenEnd-1,u="Identifier is expected but function found"):u="Identifier is expected";break;case 4:this.isDelim(qg)&&(this.next(),m++,u="Name is expected");break;case 11:this.tokenType===10&&(m=this.tokenEnd,u="Percent sign is expected");break}this.error(u,m)}this.next()},eatIdent(h){(this.tokenType!==1||this.lookupValue(0,h)===!1)&&this.error(`Identifier "${h}" is expected`),this.next()},eatDelim(h){this.isDelim(h)||this.error(`Delim "${String.fromCharCode(h)}" is expected`),this.next()},getLocation(h,d){return o?a.getLocationRange(h,d,t):null},getLocationFromList(h){if(o){let d=this.getFirstListNode(h),u=this.getLastListNode(h);return a.getLocationRange(d!==null?d.loc.start.offset-a.startOffset:this.tokenStart,u!==null?u.loc.end.offset-a.startOffset:this.tokenStart,t)}return null},error(h,d){let u=typeof d<"u"&&d<e.length?a.getLocation(d):this.eof?a.getLocation(Od(e,e.length-1)):a.getLocation(this.tokenStart);throw new ba(h||"Unexpected input",e,u.offset,u.line,u.column,a.startLine,a.startColumn)}}),c=()=>({filename:t,source:e,tokenCount:s.tokenCount,getTokenType:h=>s.getTokenType(h),getTokenTypeName:h=>Et[s.getTokenType(h)],getTokenStart:h=>s.getTokenStart(h),getTokenEnd:h=>s.getTokenEnd(h),getTokenValue:h=>s.source.substring(s.getTokenStart(h),s.getTokenEnd(h)),substring:(h,d)=>s.source.substring(h,d),balance:s.balance.subarray(0,s.tokenCount+1),isBlockOpenerTokenType:s.isBlockOpenerTokenType,isBlockCloserTokenType:s.isBlockCloserTokenType,getBlockTokenPairIndex:h=>s.getBlockTokenPairIndex(h),getLocation:h=>a.getLocation(h,t),getRangeLocation:(h,d)=>a.getLocationRange(h,d,t)});return Object.assign(function(h,d){e=h,d=d||{},s.setSource(e,Tt),a.setSource(e,d.offset,d.line,d.column),t=d.filename||"<unknown>",o=!!d.positions,r=typeof d.onParseError=="function"?d.onParseError:Li,n=!1,s.parseAtrulePrelude="parseAtrulePrelude"in d?!!d.parseAtrulePrelude:!0,s.parseRulePrelude="parseRulePrelude"in d?!!d.parseRulePrelude:!0,s.parseValue="parseValue"in d?!!d.parseValue:!0,s.parseCustomProperty="parseCustomProperty"in d?!!d.parseCustomProperty:!1;let{context:u="default",list:m=!0,onComment:p,onToken:g}=d;if(!(u in s.context))throw new Error("Unknown context `"+u+"`");Object.assign(s,m?Zg:Jg),Array.isArray(g)?s.forEachToken((y,w,x)=>{g.push({type:y,start:w,end:x})}):typeof g=="function"&&s.forEachToken(g.bind(c())),typeof p=="function"&&s.forEachToken((y,w,x)=>{if(y===25){let k=s.getLocation(w,x),z=ht(e,x-2,x,"*/")?e.slice(w+2,x-2):e.slice(w+2,x);p(z,k)}});let f=s.context[u].call(s,d);return s.eof||s.error(),f},{SyntaxError:ba,config:s.config})}var du=Sp(cu(),1),hu=new Set(["Atrule","Selector","Declaration"]);function uu(i){let e=new du.SourceMapGenerator,t={line:1,column:0},o={line:0,column:0},r={line:1,column:0},n={generated:r},a=1,s=0,c=!1,l=i.node;i.node=function(u){if(u.loc&&u.loc.start&&hu.has(u.type)){let m=u.loc.start.line,p=u.loc.start.column-1;(o.line!==m||o.column!==p)&&(o.line=m,o.column=p,t.line=a,t.column=s,c&&(c=!1,(t.line!==r.line||t.column!==r.column)&&e.addMapping(n)),c=!0,e.addMapping({source:u.loc.source,original:o,generated:t}))}l.call(this,u),c&&hu.has(u.type)&&(r.line=a,r.column=s)};let h=i.emit;i.emit=function(u,m,p){for(let g=0;g<u.length;g++)u.charCodeAt(g)===10?(a++,s=0):s++;h(u,m,p)};let d=i.result;return i.result=function(){return c&&e.addMapping(n),{css:d(),map:e}},i}var Br={};_(Br,{safe:()=>Ta,spec:()=>xf});var _f=43,vf=45,Pa=(i,e)=>(i===9&&(i=e),typeof i=="string"&&(i=Math.min(i.charCodeAt(0),128)<<6),i<<1),mu=[[1,1],[1,2],[1,7],[1,8],[1,"-"],[1,10],[1,11],[1,12],[1,15],[1,21],[3,1],[3,2],[3,7],[3,8],[3,"-"],[3,10],[3,11],[3,12],[3,15],[4,1],[4,2],[4,7],[4,8],[4,"-"],[4,10],[4,11],[4,12],[4,15],[12,1],[12,2],[12,7],[12,8],[12,"-"],[12,10],[12,11],[12,12],[12,15],["#",1],["#",2],["#",7],["#",8],["#","-"],["#",10],["#",11],["#",12],["#",15],["-",1],["-",2],["-",7],["-",8],["-","-"],["-",10],["-",11],["-",12],["-",15],[10,1],[10,2],[10,7],[10,8],[10,10],[10,11],[10,12],[10,"%"],[10,15],["@",1],["@",2],["@",7],["@",8],["@","-"],["@",15],[".",10],[".",11],[".",12],["+",10],["+",11],["+",12],["/","*"]],wf=mu.concat([[1,4],[12,4],[4,4],[3,21],[3,5],[3,16],[11,11],[11,12],[11,2],[11,"-"],[22,1],[22,2],[22,11],[22,12],[22,4],[22,"-"]]);function pu(i){let e=new Set(i.map(([t,o])=>Pa(t)<<16|Pa(o)));return function(t,o,r){let n=Pa(o,r),a=r.charCodeAt(0),s=a===vf&&o!==1&&o!==2&&o!==15||a===_f?e.has((t&65534)<<16|a<<7):e.has((t&65534)<<16|n);return n|s}}var xf=pu(mu),Ta=pu(wf);var kf=92;function Cf(i,e){if(typeof e=="function"){let t=null;i.children.forEach(o=>{t!==null&&e.call(this,t),this.node(o),t=o});return}i.children.forEach(this.node,this)}function gu(i){let e=new Map;for(let[t,o]of Object.entries(i.node))typeof(o.generate||o)=="function"&&e.set(t,o.generate||o);return function(t,o){let r="",n=0,a={node(c){if(e.has(c.type))e.get(c.type).call(s,c);else throw new Error("Unknown node type: "+c.type)},tokenBefore:Ta,token(c,l,h){n=this.tokenBefore(n,c,l),!h&&n&1&&this.emit(" ",13,!0),this.emit(l,c,!1),c===9&&l.charCodeAt(0)===kf&&this.emit(`
`,13,!0)},emit(c){r+=c},result(){return r}};o&&(typeof o.decorator=="function"&&(a=o.decorator(a)),o.sourceMap&&(a=uu(a)),o.mode in Br&&(a.tokenBefore=Br[o.mode]));let s={node:c=>a.node(c),children:Cf,token:(c,l)=>a.token(c,l),tokenize:c=>Tt(c,(l,h,d)=>{a.token(l,c.slice(h,d),h!==0)})};return a.node(t),a.result()}}function fu(i){return{fromPlainObject(e){return i(e,{enter(t){t.children&&!(t.children instanceof ue)&&(t.children=new ue().fromArray(t.children))}}),e},toPlainObject(e){return i(e,{leave(t){t.children&&t.children instanceof ue&&(t.children=t.children.toArray())}}),e}}}var{hasOwnProperty:Fa}=Object.prototype,uo=function(){};function yu(i){return typeof i=="function"?i:uo}function Su(i,e){return function(t,o,r){t.type===e&&i.call(this,t,o,r)}}function Rf(i,e){let t=e.structure,o=[];for(let r in t){if(Fa.call(t,r)===!1)continue;let n=t[r],a={name:r,type:!1,nullable:!1};Array.isArray(n)||(n=[n]);for(let s of n)s===null?a.nullable=!0:typeof s=="string"?a.type="node":Array.isArray(s)&&(a.type="list");a.type&&o.push(a)}return o.length?{context:e.walkContext,fields:o}:null}function Ef(i){let e={};for(let t in i.node)if(Fa.call(i.node,t)){let o=i.node[t];if(!o.structure)throw new Error("Missed `structure` field in `"+t+"` node type definition");e[t]=Rf(t,o)}return e}function bu(i,e){let t=i.fields.slice(),o=i.context,r=typeof o=="string";return e&&t.reverse(),function(n,a,s,c){let l;r&&(l=a[o],a[o]=n);for(let h of t){let d=n[h.name];if(!h.nullable||d){if(h.type==="list"){if(e?d.reduceRight(c,!1):d.reduce(c,!1))return!0}else if(s(d))return!0}}r&&(a[o]=l)}}function _u({StyleSheet:i,Atrule:e,Rule:t,Block:o,DeclarationList:r}){return{Atrule:{StyleSheet:i,Atrule:e,Rule:t,Block:o},Rule:{StyleSheet:i,Atrule:e,Rule:t,Block:o},Declaration:{StyleSheet:i,Atrule:e,Rule:t,Block:o,DeclarationList:r}}}function vu(i){let e=Ef(i),t={},o={},r=Symbol("break-walk"),n=Symbol("skip-node");for(let l in e)Fa.call(e,l)&&e[l]!==null&&(t[l]=bu(e[l],!1),o[l]=bu(e[l],!0));let a=_u(t),s=_u(o),c=function(l,h){function d(y,w,x){let k=u.call(f,y,w,x);return k===r?!0:k===n?!1:!!(p.hasOwnProperty(y.type)&&p[y.type](y,f,d,g)||m.call(f,y,w,x)===r)}let u=uo,m=uo,p=t,g=(y,w,x,k)=>y||d(w,x,k),f={break:r,skip:n,root:l,stylesheet:null,atrule:null,atrulePrelude:null,rule:null,selector:null,block:null,declaration:null,function:null};if(typeof h=="function")u=h;else if(h&&(u=yu(h.enter),m=yu(h.leave),h.reverse&&(p=o),h.visit)){if(a.hasOwnProperty(h.visit))p=h.reverse?s[h.visit]:a[h.visit];else if(!e.hasOwnProperty(h.visit))throw new Error("Bad value `"+h.visit+"` for `visit` option (should be: "+Object.keys(e).sort().join(", ")+")");u=Su(u,h.visit),m=Su(m,h.visit)}if(u===uo&&m===uo)throw new Error("Neither `enter` nor `leave` walker handler is set or both aren't a function");d(l)};return c.break=r,c.skip=n,c.find=function(l,h){let d=null;return c(l,function(u,m,p){if(h.call(this,u,m,p))return d=u,r}),d},c.findLast=function(l,h){let d=null;return c(l,{reverse:!0,enter(u,m,p){if(h.call(this,u,m,p))return d=u,r}}),d},c.findAll=function(l,h){let d=[];return c(l,function(u,m,p){h.call(this,u,m,p)&&d.push(u)}),d},c}function Pf(i){return i}function Tf(i){let{min:e,max:t,comma:o}=i;return e===0&&t===0?o?"#?":"*":e===0&&t===1?"?":e===1&&t===0?o?"#":"+":e===1&&t===1?"":(o?"#":"")+(e===t?"{"+e+"}":"{"+e+","+(t!==0?t:"")+"}")}function Ff(i){if(i.type==="Range")return" ["+(i.min===null?"-\u221E":i.min)+","+(i.max===null?"\u221E":i.max)+"]";throw new Error("Unknown node type `"+i.type+"`")}function zf(i,e,t,o){let r=i.combinator===" "||o?i.combinator:" "+i.combinator+" ",n=i.terms.map(a=>jr(a,e,t,o)).join(r);return i.explicit||t?(o||n[0]===","?"[":"[ ")+n+(o?"]":" ]"):n}function jr(i,e,t,o){let r;switch(i.type){case"Group":r=zf(i,e,t,o)+(i.disallowEmpty?"!":"");break;case"Multiplier":return jr(i.term,e,t,o)+e(Tf(i),i);case"Boolean":r="<boolean-expr["+jr(i.term,e,t,o)+"]>";break;case"Type":r="<"+i.name+(i.opts?e(Ff(i.opts),i.opts):"")+">";break;case"Property":r="<'"+i.name+"'>";break;case"Keyword":r=i.name;break;case"AtKeyword":r="@"+i.name;break;case"Function":r=i.name+"(";break;case"String":case"Token":r=i.value;break;case"Comma":r=",";break;default:throw new Error("Unknown node type `"+i.type+"`")}return e(r,i)}function ei(i,e){let t=Pf,o=!1,r=!1;return typeof e=="function"?t=e:e&&(o=!!e.forceBraces,r=!!e.compact,typeof e.decorate=="function"&&(t=e.decorate)),jr(i,t,o,r)}var wu={offset:0,line:1,column:1};function Lf(i,e){let t=i.tokens,o=i.longestMatch,r=o<t.length&&t[o].node||null,n=r!==e?r:null,a=0,s=0,c=0,l="",h,d;for(let u=0;u<t.length;u++){let m=t[u].value;u===o&&(s=m.length,a=l.length),n!==null&&t[u].node===n&&(u<=o?c++:c=0),l+=m}return o===t.length||c>1?(h=Gr(n||e,"end")||mo(wu,l),d=mo(h)):(h=Gr(n,"start")||mo(Gr(e,"start")||wu,l.slice(0,a)),d=Gr(n,"end")||mo(h,l.substr(a,s))),{css:l,mismatchOffset:a,mismatchLength:s,start:h,end:d}}function Gr(i,e){let t=i&&i.loc&&i.loc[e];return t?"line"in t?mo(t):t:null}function mo({offset:i,line:e,column:t},o){let r={offset:i,line:e,column:t};if(o){let n=o.split(/\n|\r\n?|\f/);r.offset+=o.length,r.line+=n.length-1,r.column=n.length===1?r.column+o.length:n.pop().length+1}return r}var Ai=function(i,e){let t=Zt("SyntaxReferenceError",i+(e?" `"+e+"`":""));return t.reference=e,t},xu=function(i,e,t,o){let r=Zt("SyntaxMatchError",i),{css:n,mismatchOffset:a,mismatchLength:s,start:c,end:l}=Lf(o,t);return r.rawMessage=i,r.syntax=e?ei(e):"<generic>",r.css=n,r.mismatchOffset=a,r.mismatchLength=s,r.message=i+`
  syntax: `+r.syntax+`
   value: `+(n||"<empty string>")+`
  --------`+new Array(r.mismatchOffset+1).join("-")+"^",Object.assign(r,c),r.loc={source:t&&t.loc&&t.loc.source||"<unknown>",start:c,end:l},r};var Vr=new Map,Mi=new Map,Kr=45,po=Af,Yr=Mf,Of=za;function go(i,e){return e=e||0,i.length-e>=2&&i.charCodeAt(e)===Kr&&i.charCodeAt(e+1)===Kr}function za(i,e){if(e=e||0,i.length-e>=3&&i.charCodeAt(e)===Kr&&i.charCodeAt(e+1)!==Kr){let t=i.indexOf("-",e+2);if(t!==-1)return i.substring(e,t+1)}return""}function Af(i){if(Vr.has(i))return Vr.get(i);let e=i.toLowerCase(),t=Vr.get(e);if(t===void 0){let o=go(e,0),r=o?"":za(e,0);t=Object.freeze({basename:e.substr(r.length),name:e,prefix:r,vendor:r,custom:o})}return Vr.set(i,t),t}function Mf(i){if(Mi.has(i))return Mi.get(i);let e=i,t=i[0];t==="/"?t=i[1]==="/"?"//":"/":t!=="_"&&t!=="*"&&t!=="$"&&t!=="#"&&t!=="+"&&t!=="&"&&(t="");let o=go(e,t.length);if(!o&&(e=e.toLowerCase(),Mi.has(e))){let s=Mi.get(e);return Mi.set(i,s),s}let r=o?"":za(e,t.length),n=e.substr(0,t.length+r.length),a=Object.freeze({basename:e.substr(n.length),name:e.substr(t.length),hack:t,vendor:r,prefix:n,custom:o});return Mi.set(i,a),a}var Ni=["initial","inherit","unset","revert","revert-layer"];var yo=43,it=45,La=110,Ii=!0,If=!1;function Aa(i,e){return i!==null&&i.type===9&&i.value.charCodeAt(0)===e}function fo(i,e,t){for(;i!==null&&(i.type===13||i.type===25);)i=t(++e);return e}function Ft(i,e,t,o){if(!i)return 0;let r=i.value.charCodeAt(e);if(r===yo||r===it){if(t)return 0;e++}for(;e<i.value.length;e++)if(!Z(i.value.charCodeAt(e)))return 0;return o+1}function Oa(i,e,t){let o=!1,r=fo(i,e,t);if(i=t(r),i===null)return e;if(i.type!==10)if(Aa(i,yo)||Aa(i,it)){if(o=!0,r=fo(t(++r),r,t),i=t(r),i===null||i.type!==10)return 0}else return e;if(!o){let n=i.value.charCodeAt(0);if(n!==yo&&n!==it)return 0}return Ft(i,o?0:1,o,r)}function Ma(i,e){let t=0;if(!i)return 0;if(i.type===10)return Ft(i,0,If,t);if(i.type===1&&i.value.charCodeAt(0)===it){if(!ct(i.value,1,La))return 0;switch(i.value.length){case 2:return Oa(e(++t),t,e);case 3:return i.value.charCodeAt(2)!==it?0:(t=fo(e(++t),t,e),i=e(t),Ft(i,0,Ii,t));default:return i.value.charCodeAt(2)!==it?0:Ft(i,3,Ii,t)}}else if(i.type===1||Aa(i,yo)&&e(t+1).type===1){if(i.type!==1&&(i=e(++t)),i===null||!ct(i.value,0,La))return 0;switch(i.value.length){case 1:return Oa(e(++t),t,e);case 2:return i.value.charCodeAt(1)!==it?0:(t=fo(e(++t),t,e),i=e(t),Ft(i,0,Ii,t));default:return i.value.charCodeAt(1)!==it?0:Ft(i,2,Ii,t)}}else if(i.type===12){let o=i.value.charCodeAt(0),r=o===yo||o===it?1:0,n=r;for(;n<i.value.length&&Z(i.value.charCodeAt(n));n++);return n===r||!ct(i.value,n,La)?0:n+1===i.value.length?Oa(e(++t),t,e):i.value.charCodeAt(n+1)!==it?0:n+2===i.value.length?(t=fo(e(++t),t,e),i=e(t),Ft(i,0,Ii,t)):Ft(i,n+2,Ii,t)}return 0}var Uf=43,ku=45,Cu=63,Df=117;function Na(i,e){return i!==null&&i.type===9&&i.value.charCodeAt(0)===e}function Hf(i,e){return i.value.charCodeAt(0)===e}function So(i,e,t){let o=0;for(let r=e;r<i.value.length;r++){let n=i.value.charCodeAt(r);if(n===ku&&t&&o!==0)return So(i,e+o+1,!1),6;if(!Ae(n)||++o>6)return 0}return o}function Xr(i,e,t){if(!i)return 0;for(;Na(t(e),Cu);){if(++i>6)return 0;e++}return e}function Ia(i,e){let t=0;if(i===null||i.type!==1||!ct(i.value,0,Df)||(i=e(++t),i===null))return 0;if(Na(i,Uf))return i=e(++t),i===null?0:i.type===1?Xr(So(i,0,!0),++t,e):Na(i,Cu)?Xr(1,++t,e):0;if(i.type===10){let o=So(i,1,!0);return o===0?0:(i=e(++t),i===null?t:i.type===12||i.type===10?!Hf(i,ku)||!So(i,1,!1)?0:t+1:Xr(o,t,e))}return i.type===12?Xr(So(i,1,!0),++t,e):0}var Ua=["calc(","-moz-calc(","-webkit-calc("],Da=["min(","max(","clamp("],Ha=["round(","mod(","rem("],Wf=["sin(","cos(","tan("],Bf=["asin(","acos(","atan(","atan2("],jf=["pow(","sqrt(","log(","exp(","sign("],Wa=["hypot("],Ba=["abs("],Ru=[...Ua,...Da,...Ha,...Wf,...jf,...Wa,...Ba],Gf=[...Ua,...Da,...Ha,...Wa,...Ba],mt=[...Ua,...Da,...Ha,...Bf,...Wa,...Ba],ja=new Map([[2,22],[21,22],[19,20],[23,24]]);function qe(i,e){return e<i.length?i.charCodeAt(e):0}function Eu(i,e){return ht(i,0,i.length,e)}function Pu(i,e){for(let t=0;t<e.length;t++)if(Eu(i,e[t]))return!0;return!1}function Tu(i,e){return e!==i.length-2?!1:qe(i,e)===92&&Z(qe(i,e+1))}function $r(i,e,t){if(i&&i.type==="Range"){let o=Number(t!==void 0&&t!==e.length?e.substr(0,t):e);if(isNaN(o)||i.min!==null&&o<i.min&&typeof i.min!="string"||i.max!==null&&o>i.max&&typeof i.max!="string")return!0}return!1}function Vf(i,e){let t=0,o=[],r=0;e:do{switch(i.type){case 24:case 22:case 20:if(i.type!==t)break e;if(t=o.pop(),o.length===0){r++;break e}break;case 2:case 21:case 19:case 23:o.push(t),t=ja.get(i.type);break}r++}while(i=e(r));return r}function Ve(i,e){return function(t,o,r){return t===null?0:t.type===2&&Pu(t.value,e)?Vf(t,o):i(t,o,r)}}function B(i){return function(e){return e===null||e.type!==i?0:1}}function Kf(i){if(i===null||i.type!==1)return 0;let e=i.value.toLowerCase();return Pu(e,Ni)||Eu(e,"default")?0:1}function Fu(i){return i===null||i.type!==1||qe(i.value,0)!==45||qe(i.value,1)!==45?0:1}function Yf(i){return!Fu(i)||i.value==="--"?0:1}function Xf(i){if(i===null||i.type!==4)return 0;let e=i.value.length;if(e!==4&&e!==5&&e!==7&&e!==9)return 0;for(let t=1;t<e;t++)if(!Ae(qe(i.value,t)))return 0;return 1}function $f(i){return i===null||i.type!==4||!Ci(qe(i.value,1),qe(i.value,2),qe(i.value,3))?0:1}function qf(i,e){if(!i)return 0;let t=0,o=[],r=0;e:do{switch(i.type){case 6:case 8:break e;case 24:case 22:case 20:if(i.type!==t)break e;t=o.pop();break;case 17:if(t===0)break e;break;case 9:if(t===0&&i.value==="!")break e;break;case 2:case 21:case 19:case 23:o.push(t),t=ja.get(i.type);break}r++}while(i=e(r));return r}function Jf(i,e){if(!i)return 0;let t=0,o=[],r=0;e:do{switch(i.type){case 6:case 8:break e;case 24:case 22:case 20:if(i.type!==t)break e;t=o.pop();break;case 2:case 21:case 19:case 23:o.push(t),t=ja.get(i.type);break}r++}while(i=e(r));return r}function pt(i){return i&&(i=new Set(i)),function(e,t,o){if(e===null||e.type!==12)return 0;let r=Jt(e.value,0);if(i!==null){let n=e.value.indexOf("\\",r),a=n===-1||!Tu(e.value,n)?e.value.substr(r):e.value.substring(r,n);if(i.has(a.toLowerCase())===!1)return 0}return $r(o,e.value,r)?0:1}}function Zf(i,e,t){return i===null||i.type!==11||$r(t,i.value,i.value.length-1)?0:1}function zu(i){return typeof i!="function"&&(i=function(){return 0}),function(e,t,o){return e!==null&&e.type===10&&Number(e.value)===0?1:i(e,t,o)}}function Qf(i,e,t){if(i===null)return 0;let o=Jt(i.value,0);return!(o===i.value.length)&&!Tu(i.value,o)||$r(t,i.value,o)?0:1}function ey(i,e,t){if(i===null||i.type!==10)return 0;let o=qe(i.value,0)===43||qe(i.value,0)===45?1:0;for(;o<i.value.length;o++)if(!Z(qe(i.value,o)))return 0;return $r(t,i.value,o)?0:1}var ty={"ident-token":B(1),"function-token":B(2),"at-keyword-token":B(3),"hash-token":B(4),"string-token":B(5),"bad-string-token":B(6),"url-token":B(7),"bad-url-token":B(8),"delim-token":B(9),"number-token":B(10),"percentage-token":B(11),"dimension-token":B(12),"whitespace-token":B(13),"CDO-token":B(14),"CDC-token":B(15),"colon-token":B(16),"semicolon-token":B(17),"comma-token":B(18),"[-token":B(19),"]-token":B(20),"(-token":B(21),")-token":B(22),"{-token":B(23),"}-token":B(24)},iy={string:B(5),ident:B(1),percentage:Ve(Zf,Gf),zero:zu(),number:Ve(Qf,Ru),integer:Ve(ey,Ru),"custom-ident":Kf,"dashed-ident":Fu,"custom-property-name":Yf,"hex-color":Xf,"id-selector":$f,"an-plus-b":Ma,urange:Ia,"declaration-value":qf,"any-value":Jf},oy=["length","angle","time","frequency","resolution","flex","decibel","semitones"];function ry(i){let{angle:e,decibel:t,frequency:o,flex:r,length:n,resolution:a,semitones:s,time:c}=i||{};return{dimension:Ve(pt(null),mt),angle:Ve(pt(e),mt),decibel:Ve(pt(t),mt),frequency:Ve(pt(o),mt),flex:Ve(pt(r),mt),length:Ve(zu(pt(n)),mt),resolution:Ve(pt(a),mt),semitones:Ve(pt(s),mt),time:Ve(pt(c),mt)}}function ny(i){let e=new Set;for(let t of oy)if(Array.isArray(i[t]))for(let o of i[t])e.add(o.toLowerCase());return function(o){return o===null?0:o.type===9&&o.value==="%"||o.type===1&&e.has(o.value.toLowerCase())?1:0}}function Lu(i){return{...ty,...iy,...ry(i),"attr-unit":ny(i)}}var qr={};_(qr,{angle:()=>sy,decibel:()=>uy,flex:()=>dy,frequency:()=>cy,length:()=>ay,resolution:()=>hy,semitones:()=>my,time:()=>ly});var ay=["cm","mm","q","in","pt","pc","px","em","rem","ex","rex","cap","rcap","ch","rch","ic","ric","lh","rlh","vw","svw","lvw","dvw","vh","svh","lvh","dvh","vi","svi","lvi","dvi","vb","svb","lvb","dvb","vmin","svmin","lvmin","dvmin","vmax","svmax","lvmax","dvmax","cqw","cqh","cqi","cqb","cqmin","cqmax"],sy=["deg","grad","rad","turn"],ly=["s","ms"],cy=["hz","khz"],hy=["dpi","dpcm","dppx","x"],dy=["fr"],uy=["db"],my=["st"];var Ja={};_(Ja,{SyntaxError:()=>Jr,generate:()=>ei,parse:()=>Di,walk:()=>rn});function Jr(i,e,t){return Object.assign(Zt("SyntaxError",i),{input:e,offset:t,rawMessage:i,message:i+`
  `+e+`
--`+new Array((t||e.length)+1).join("-")+"^"})}var py=9,gy=10,fy=12,yy=13,Sy=32,Ou=new Uint8Array(128).map((i,e)=>/[a-zA-Z0-9\-]/.test(String.fromCharCode(e))?1:0),Zr=class{constructor(e){this.str=e,this.pos=0}charCodeAt(e){return e<this.str.length?this.str.charCodeAt(e):0}charCode(){return this.charCodeAt(this.pos)}isNameCharCode(e=this.charCode()){return e<128&&Ou[e]===1}nextCharCode(){return this.charCodeAt(this.pos+1)}nextNonWsCode(e){return this.charCodeAt(this.findWsEnd(e))}skipWs(){this.pos=this.findWsEnd(this.pos)}findWsEnd(e){for(;e<this.str.length;e++){let t=this.str.charCodeAt(e);if(t!==yy&&t!==gy&&t!==fy&&t!==Sy&&t!==py)break}return e}substringToPos(e){return this.str.substring(this.pos,this.pos=e)}eat(e){this.charCode()!==e&&this.error("Expect `"+String.fromCharCode(e)+"`"),this.pos++}peek(){return this.pos<this.str.length?this.str.charAt(this.pos++):""}error(e){throw new Jr(e,this.str,this.pos)}scanSpaces(){return this.substringToPos(this.findWsEnd(this.pos))}scanWord(){let e=this.pos;for(;e<this.str.length;e++){let t=this.str.charCodeAt(e);if(t>=128||Ou[t]===0)break}return this.pos===e&&this.error("Expect a keyword"),this.substringToPos(e)}scanNumber(){let e=this.pos;for(;e<this.str.length;e++){let t=this.str.charCodeAt(e);if(t<48||t>57)break}return this.pos===e&&this.error("Expect a number"),this.substringToPos(e)}scanString(){let e=this.str.indexOf("'",this.pos+1);return e===-1&&(this.pos=this.str.length,this.error("Expect an apostrophe")),this.substringToPos(e+1)}};var by=9,_y=10,vy=12,wy=13,xy=32,Hu=33,Ya=35,Au=38,Qr=39,Wu=40,Bu=41,ju=42,Xa=43,$a=44,Mu=45,qa=60,Va=62,en=63,ky=64,bo=91,Ui=93,tn=123,Nu=124,Ka=125,Iu=8734,Uu={" ":1,"&&":2,"||":3,"|":4};function Du(i){let e=null,t=null;return i.eat(tn),i.skipWs(),e=i.scanNumber(i),i.skipWs(),i.charCode()===$a?(i.pos++,i.skipWs(),i.charCode()!==Ka&&(t=i.scanNumber(i),i.skipWs())):t=e,i.eat(Ka),{min:Number(e),max:t?Number(t):0}}function Cy(i){let e=null,t=!1;switch(i.charCode()){case ju:i.pos++,e={min:0,max:0};break;case Xa:i.pos++,e={min:1,max:0};break;case en:i.pos++,e={min:0,max:1};break;case Ya:i.pos++,t=!0,i.charCode()===tn?e=Du(i):i.charCode()===en?(i.pos++,e={min:0,max:0}):e={min:1,max:0};break;case tn:e=Du(i);break;default:return null}return{type:"Multiplier",comma:t,min:e.min,max:e.max,term:null}}function gt(i,e){let t=Cy(i);return t!==null?(t.term=e,i.charCode()===Ya&&i.charCodeAt(i.pos-1)===Xa||i.charCode()===en&&i.charCodeAt(i.pos-1)===Ka?gt(i,t):t):e}function Ga(i){let e=i.peek();return e===""?null:gt(i,{type:"Token",value:e})}function Ry(i){let e;return i.eat(qa),i.eat(Qr),e=i.scanWord(),i.eat(Qr),i.eat(Va),gt(i,{type:"Property",name:e})}function Ey(i){let e=null,t=null,o=1;return i.eat(bo),i.charCode()===Mu&&(i.peek(),o=-1),o==-1&&i.charCode()===Iu?i.peek():(e=o*Number(i.scanNumber(i)),i.isNameCharCode()&&(e+=i.scanWord())),i.skipWs(),i.eat($a),i.skipWs(),i.charCode()===Iu?i.peek():(o=1,i.charCode()===Mu&&(i.peek(),o=-1),t=o*Number(i.scanNumber(i)),i.isNameCharCode()&&(t+=i.scanWord())),i.eat(Ui),{type:"Range",min:e,max:t}}function Py(i){let e,t=null;if(i.eat(qa),e=i.scanWord(),e==="boolean-expr"){i.eat(bo);let o=on(i,Ui);return i.eat(Ui),i.eat(Va),gt(i,{type:"Boolean",term:o.terms.length===1?o.terms[0]:o})}return i.charCode()===Wu&&i.nextCharCode()===Bu&&(i.pos+=2,e+="()"),i.charCodeAt(i.findWsEnd(i.pos))===bo&&(i.skipWs(),t=Ey(i)),i.eat(Va),gt(i,{type:"Type",name:e,opts:t})}function Ty(i){let e=i.scanWord();return i.charCode()===Wu?(i.pos++,{type:"Function",name:e}):gt(i,{type:"Keyword",name:e})}function Fy(i,e){function t(r,n){return{type:"Group",terms:r,combinator:n,disallowEmpty:!1,explicit:!1}}let o;for(e=Object.keys(e).sort((r,n)=>Uu[r]-Uu[n]);e.length>0;){o=e.shift();let r=0,n=0;for(;r<i.length;r++){let a=i[r];a.type==="Combinator"&&(a.value===o?(n===-1&&(n=r-1),i.splice(r,1),r--):(n!==-1&&r-n>1&&(i.splice(n,r-n,t(i.slice(n,r),o)),r=n+1),n=-1))}n!==-1&&e.length&&i.splice(n,r-n,t(i.slice(n,r),o))}return o}function on(i,e=-1){let t=Object.create(null),o=[],r=null,n=i.pos,a=!1;for(;i.charCode()!==e;){let s=a?on(i,Bu):Ly(i);if(!s)break;if(s.type!=="Spaces"){if(a){if(s.terms.length===0){a=!1;continue}if(s.combinator===" "){for(;s.terms.length>1;)t[" "]=!0,o.push({type:"Combinator",value:" "},s.terms.shift());s=s.terms[0]}}s.type==="Combinator"?((r===null||r.type==="Combinator")&&(i.pos=n,i.error("Unexpected combinator")),t[s.value]=!0):r!==null&&r.type!=="Combinator"&&(t[" "]=!0,o.push({type:"Combinator",value:" "})),o.push(s),r=s,n=i.pos,a=s.type==="Function"}}return r!==null&&r.type==="Combinator"&&(i.pos-=n,i.error("Unexpected combinator")),{type:"Group",terms:o,combinator:Fy(o,t)||" ",disallowEmpty:!1,explicit:!1}}function zy(i){let e;return i.eat(bo),e=on(i,Ui),i.eat(Ui),e.explicit=!0,i.charCode()===Hu&&(i.pos++,e.disallowEmpty=!0),e}function Ly(i){let e=i.charCode();switch(e){case Ui:break;case bo:return gt(i,zy(i));case qa:return i.nextCharCode()===Qr?Ry(i):Py(i);case Nu:return{type:"Combinator",value:i.substringToPos(i.pos+(i.nextCharCode()===Nu?2:1))};case Au:return i.pos++,i.eat(Au),{type:"Combinator",value:"&&"};case $a:return i.pos++,{type:"Comma"};case Qr:return gt(i,{type:"String",value:i.scanString()});case xy:case by:case _y:case wy:case vy:return{type:"Spaces",value:i.scanSpaces()};case ky:return e=i.nextCharCode(),i.isNameCharCode(e)?(i.pos++,{type:"AtKeyword",name:i.scanWord()}):Ga(i);case ju:case Xa:case en:case Ya:case Hu:break;case tn:if(e=i.nextCharCode(),e<48||e>57)return Ga(i);break;default:return i.isNameCharCode(e)?Ty(i):Ga(i)}}function Di(i){let e=new Zr(i),t=on(e);return e.pos!==i.length&&e.error("Unexpected input"),t.terms.length===1&&t.terms[0].type==="Group"?t.terms[0]:t}var _o=function(){};function Gu(i){return typeof i=="function"?i:_o}function rn(i,e,t){function o(a){switch(r.call(t,a),a.type){case"Group":a.terms.forEach(o);break;case"Multiplier":case"Boolean":o(a.term);break;case"Type":case"Property":case"Keyword":case"AtKeyword":case"Function":case"String":case"Token":case"Comma":break;default:throw new Error("Unknown type: "+a.type)}n.call(t,a)}let r=_o,n=_o;if(typeof e=="function"?r=e:e&&(r=Gu(e.enter),n=Gu(e.leave)),r===_o&&n===_o)throw new Error("Neither `enter` nor `leave` walker handler is set or both aren't a function");o(i,t)}var Oy={decorator(i){let e=[],t=null;return{...i,node(o){let r=t;t=o,i.node.call(this,o),t=r},emit(o,r,n){e.push({type:r,value:o,node:n?null:t})},result(){return e}}}};function Ay(i){let e=[];return Tt(i,(t,o,r)=>e.push({type:t,value:i.slice(o,r),node:null})),e}function Vu(i,e){return typeof i=="string"?Ay(i):e.generate(i,Oy)}var A={type:"Match"},U={type:"Mismatch"},nn={type:"DisallowEmpty"},My=40,Ny=41;function be(i,e,t){return e===A&&t===U||i===A&&e===A&&t===A?i:(i.type==="If"&&i.else===U&&e===A&&(e=i.then,i=i.match),{type:"If",match:i,then:e,else:t})}function Yu(i){return i.length>2&&i.charCodeAt(i.length-2)===My&&i.charCodeAt(i.length-1)===Ny}function Ku(i){return i.type==="Keyword"||i.type==="AtKeyword"||i.type==="Function"||i.type==="Type"&&Yu(i.name)}function zt(i,e=" ",t=!1){return{type:"Group",terms:i,combinator:e,disallowEmpty:!1,explicit:t}}function vo(i,e,t=new Set){if(!t.has(i))switch(t.add(i),i.type){case"If":i.match=vo(i.match,e,t),i.then=vo(i.then,e,t),i.else=vo(i.else,e,t);break;case"Type":return e[i.name]||i}return i}function Za(i,e,t){switch(i){case" ":{let o=A;for(let r=e.length-1;r>=0;r--){let n=e[r];o=be(n,o,U)}return o}case"|":{let o=U,r=null;for(let n=e.length-1;n>=0;n--){let a=e[n];if(Ku(a)&&(r===null&&n>0&&Ku(e[n-1])&&(r=Object.create(null),o=be({type:"Enum",map:r},A,o)),r!==null)){let s=(Yu(a.name)?a.name.slice(0,-1):a.name).toLowerCase();if(!(s in r)){r[s]=a;continue}}r=null,o=be(a,A,o)}return o}case"&&":{if(e.length>5)return{type:"MatchOnce",terms:e,all:!0};let o=U;for(let r=e.length-1;r>=0;r--){let n=e[r],a;e.length>1?a=Za(i,e.filter(function(s){return s!==n}),!1):a=A,o=be(n,a,o)}return o}case"||":{if(e.length>5)return{type:"MatchOnce",terms:e,all:!1};let o=t?A:U;for(let r=e.length-1;r>=0;r--){let n=e[r],a;e.length>1?a=Za(i,e.filter(function(s){return s!==n}),!0):a=A,o=be(n,a,o)}return o}}}function Iy(i){let e=A,t=Hi(i.term);if(i.max===0)t=be(t,nn,U),e=be(t,null,U),e.then=be(A,A,e),i.comma&&(e.then.else=be({type:"Comma",syntax:i},e,U));else for(let o=i.min||1;o<=i.max;o++)i.comma&&e!==A&&(e=be({type:"Comma",syntax:i},e,U)),e=be(t,be(A,A,e),U);if(i.min===0)e=be(A,A,e);else for(let o=0;o<i.min-1;o++)i.comma&&e!==A&&(e=be({type:"Comma",syntax:i},e,U)),e=be(t,e,U);return e}function Hi(i){if(typeof i=="function")return{type:"Generic",fn:i};switch(i.type){case"Group":{let e=Za(i.combinator,i.terms.map(Hi),!1);return i.disallowEmpty&&(e=be(e,nn,U)),e}case"Multiplier":return Iy(i);case"Boolean":{let e=Hi(i.term),t=Hi(zt([zt([{type:"Keyword",name:"not"},{type:"Type",name:"!boolean-group"}]),zt([{type:"Type",name:"!boolean-group"},zt([{type:"Multiplier",comma:!1,min:0,max:0,term:zt([{type:"Keyword",name:"and"},{type:"Type",name:"!boolean-group"}])},{type:"Multiplier",comma:!1,min:0,max:0,term:zt([{type:"Keyword",name:"or"},{type:"Type",name:"!boolean-group"}])}],"|")])],"|")),o=Hi(zt([{type:"Type",name:"!term"},zt([{type:"Token",value:"("},{type:"Type",name:"!self"},{type:"Token",value:")"}]),{type:"Type",name:"general-enclosed"}],"|"));return vo(o,{"!term":e,"!self":t}),vo(t,{"!boolean-group":o}),t}case"Type":case"Property":return{type:i.type,name:i.name,syntax:i};case"Keyword":return{type:i.type,name:i.name.toLowerCase(),syntax:i};case"AtKeyword":return{type:i.type,name:"@"+i.name.toLowerCase(),syntax:i};case"Function":return{type:i.type,name:i.name.toLowerCase()+"(",syntax:i};case"String":return i.value.length===3?{type:"Token",value:i.value.charAt(1),syntax:i}:{type:i.type,value:i.value.substr(1,i.value.length-2).replace(/\\'/g,"'"),syntax:i};case"Token":return{type:i.type,value:i.value,syntax:i};case"Comma":return{type:i.type,syntax:i};default:throw new Error("Unknown node type:",i.type)}}function wo(i,e){return typeof i=="string"&&(i=Di(i)),{type:"MatchGraph",match:Hi(i),syntax:e||null,source:i}}var{hasOwnProperty:Xu}=Object.prototype,Uy=0,Dy=1,es=2,Qu=3,$u="Match",Hy="Mismatch",Wy="Maximum iteration number exceeded (please fill an issue on https://github.com/csstree/csstree/issues)",qu=15e3,By=0;function jy(i){let e=null,t=null,o=i;for(;o!==null;)t=o.prev,o.prev=e,e=o,o=t;return e}function Qa(i,e){if(i.length!==e.length)return!1;for(let t=0;t<i.length;t++){let o=e.charCodeAt(t),r=i.charCodeAt(t);if(r>=65&&r<=90&&(r=r|32),r!==o)return!1}return!0}function Gy(i){return i.type!==9?!1:i.value!=="?"}function Ju(i){return i===null?!0:i.type===18||i.type===2||i.type===21||i.type===19||i.type===23||Gy(i)}function Zu(i){return i===null?!0:i.type===22||i.type===20||i.type===24||i.type===9&&i.value==="/"}function Vy(i,e,t){function o(){do w++,y=w<i.length?i[w]:null;while(y!==null&&(y.type===13||y.type===25))}function r(z){let ce=w+z;return ce<i.length?i[ce]:null}function n(z,ce){return{nextState:z,matchStack:k,syntaxStack:d,thenStack:u,tokenIndex:w,prev:ce}}function a(z){u={nextState:z,matchStack:k,syntaxStack:d,prev:u}}function s(z){m=n(z,m)}function c(){k={type:Dy,syntax:e.syntax,token:y,prev:k},o(),p=null,w>x&&(x=w)}function l(){d={syntax:e.syntax,opts:e.syntax.opts||d!==null&&d.opts||null,prev:d},k={type:es,syntax:e.syntax,token:k.token,prev:k}}function h(){k.type===es?k=k.prev:k={type:Qu,syntax:d.syntax,token:k.token,prev:k},d=d.prev}let d=null,u=null,m=null,p=null,g=0,f=null,y=null,w=-1,x=0,k={type:Uy,syntax:null,token:null,prev:null};for(o();f===null&&++g<qu;)switch(e.type){case"Match":if(u===null){if(y!==null&&(w!==i.length-1||y.value!=="\\0"&&y.value!=="\\9")){e=U;break}f=$u;break}if(e=u.nextState,e===nn)if(u.matchStack===k){e=U;break}else e=A;for(;u.syntaxStack!==d;)h();u=u.prev;break;case"Mismatch":if(p!==null&&p!==!1)(m===null||w>m.tokenIndex)&&(m=p,p=!1);else if(m===null){f=Hy;break}e=m.nextState,u=m.thenStack,d=m.syntaxStack,k=m.matchStack,w=m.tokenIndex,y=w<i.length?i[w]:null,m=m.prev;break;case"MatchGraph":e=e.match;break;case"If":e.else!==U&&s(e.else),e.then!==A&&a(e.then),e=e.match;break;case"MatchOnce":e={type:"MatchOnceBuffer",syntax:e,index:0,mask:0};break;case"MatchOnceBuffer":{let G=e.syntax.terms;if(e.index===G.length){if(e.mask===0||e.syntax.all){e=U;break}e=A;break}if(e.mask===(1<<G.length)-1){e=A;break}for(;e.index<G.length;e.index++){let V=1<<e.index;if((e.mask&V)===0){s(e),a({type:"AddMatchOnce",syntax:e.syntax,mask:e.mask|V}),e=G[e.index++];break}}break}case"AddMatchOnce":e={type:"MatchOnceBuffer",syntax:e.syntax,index:0,mask:e.mask};break;case"Enum":if(y!==null){let G=y.value.toLowerCase();if(G.indexOf("\\")!==-1&&(G=G.replace(/\\[09].*$/,"")),Xu.call(e.map,G)){e=e.map[G];break}}e=U;break;case"Generic":{let G=d!==null?d.opts:null,V=w+Math.floor(e.fn(y,r,G));if(!isNaN(V)&&V>w){for(;w<V;)c();e=A}else e=U;break}case"Type":case"Property":{let G=e.type==="Type"?"types":"properties",V=Xu.call(t,G)?t[G][e.name]:null;if(!V||!V.match)throw new Error("Bad syntax reference: "+(e.type==="Type"?"<"+e.name+">":"<'"+e.name+"'>"));if(p!==!1&&y!==null&&e.type==="Type"&&(e.name==="custom-ident"&&y.type===1||e.name==="length"&&y.value==="0")){p===null&&(p=n(e,m)),e=U;break}l(),e=V.matchRef||V.match;break}case"Keyword":{let G=e.name;if(y!==null){let V=y.value;if(V.indexOf("\\")!==-1&&(V=V.replace(/\\[09].*$/,"")),Qa(V,G)){c(),e=A;break}}e=U;break}case"AtKeyword":case"Function":if(y!==null&&Qa(y.value,e.name)){c(),e=A;break}e=U;break;case"Token":if(y!==null&&y.value===e.value){c(),e=A;break}e=U;break;case"Comma":y!==null&&y.type===18?Ju(k.token)?e=U:(c(),e=Zu(y)?U:A):e=Ju(k.token)||Zu(y)?A:U;break;case"String":let z="",ce=w;for(;ce<i.length&&z.length<e.value.length;ce++)z+=i[ce].value;if(Qa(z,e.value)){for(;w<ce;)c();e=A}else e=U;break;default:throw new Error("Unknown node type: "+e.type)}switch(By+=g,f){case null:console.warn("[csstree-match] BREAK after "+qu+" iterations"),f=Wy,k=null;break;case $u:for(;d!==null;)h();break;default:k=null}return{tokens:i,reason:f,iterations:g,match:k,longestMatch:x}}function ts(i,e,t){let o=Vy(i,e,t||{});if(o.match===null)return o;let r=o.match,n=o.match={syntax:e.syntax||null,match:[]},a=[n];for(r=jy(r).prev;r!==null;){switch(r.type){case es:n.match.push(n={syntax:r.syntax,match:[]}),a.push(n);break;case Qu:a.pop(),n=a[a.length-1];break;default:n.match.push({syntax:r.syntax||null,token:r.token.value,node:r.token.node})}r=r.prev}return o}var os={};_(os,{getTrace:()=>em,isKeyword:()=>Xy,isProperty:()=>Yy,isType:()=>Ky});function em(i){function e(r){return r===null?!1:r.type==="Type"||r.type==="Property"||r.type==="Keyword"}function t(r){if(Array.isArray(r.match)){for(let n=0;n<r.match.length;n++)if(t(r.match[n]))return e(r.syntax)&&o.unshift(r.syntax),!0}else if(r.node===i)return o=e(r.syntax)?[r.syntax]:[],!0;return!1}let o=null;return this.matched!==null&&t(this.matched),o}function Ky(i,e){return is(this,i,t=>t.type==="Type"&&t.name===e)}function Yy(i,e){return is(this,i,t=>t.type==="Property"&&t.name===e)}function Xy(i){return is(this,i,e=>e.type==="Keyword")}function is(i,e,t){let o=em.call(i,e);return o===null?!1:o.some(t)}function tm(i){return"node"in i?i.node:tm(i.match[0])}function im(i){return"node"in i?i.node:im(i.match[i.match.length-1])}function rs(i,e,t,o,r){function n(s){if(s.syntax!==null&&s.syntax.type===o&&s.syntax.name===r){let c=tm(s),l=im(s);i.syntax.walk(e,function(h,d,u){if(h===c){let m=new ue;do{if(m.appendData(d.data),d.data===l)break;d=d.next}while(d!==null);a.push({parent:u,nodes:m})}})}Array.isArray(s.match)&&s.match.forEach(n)}let a=[];return t.matched!==null&&n(t.matched),a}var{hasOwnProperty:xo}=Object.prototype;function ns(i){return typeof i=="number"&&isFinite(i)&&Math.floor(i)===i&&i>=0}function om(i){return!!i&&ns(i.offset)&&ns(i.line)&&ns(i.column)}function $y(i,e){return function(o,r){if(!o||o.constructor!==Object)return r(o,"Type of node should be an Object");for(let n in o){let a=!0;if(xo.call(o,n)!==!1){if(n==="type")o.type!==i&&r(o,"Wrong node type `"+o.type+"`, expected `"+i+"`");else if(n==="loc"){if(o.loc===null)continue;if(o.loc&&o.loc.constructor===Object)if(typeof o.loc.source!="string")n+=".source";else if(!om(o.loc.start))n+=".start";else if(!om(o.loc.end))n+=".end";else continue;a=!1}else if(e.hasOwnProperty(n)){a=!1;for(let s=0;!a&&s<e[n].length;s++){let c=e[n][s];switch(c){case String:a=typeof o[n]=="string";break;case Boolean:a=typeof o[n]=="boolean";break;case null:a=o[n]===null;break;default:typeof c=="string"?a=o[n]&&o[n].type===c:Array.isArray(c)&&(a=o[n]instanceof ue)}}}else r(o,"Unknown field `"+n+"` for "+i+" node type");a||r(o,"Bad value for `"+i+"."+n+"`")}}for(let n in e)xo.call(e,n)&&xo.call(o,n)===!1&&r(o,"Field `"+i+"."+n+"` is missed")}}function rm(i,e){let t=[];for(let o=0;o<i.length;o++){let r=i[o];if(r===String||r===Boolean)t.push(r.name.toLowerCase());else if(r===null)t.push("null");else if(typeof r=="string")t.push(r);else if(Array.isArray(r))t.push("List<"+(rm(r,e)||"any")+">");else throw new Error("Wrong value `"+r+"` in `"+e+"` structure definition")}return t.join(" | ")}function qy(i,e){let t=e.structure,o={type:String,loc:!0},r={type:'"'+i+'"'};for(let n in t){if(xo.call(t,n)===!1)continue;let a=o[n]=Array.isArray(t[n])?t[n].slice():[t[n]];r[n]=rm(a,i+"."+n)}return{docs:r,check:$y(i,o)}}function nm(i){let e={};if(i.node){for(let t in i.node)if(xo.call(i.node,t)){let o=i.node[t];if(o.structure)e[t]=qy(t,o);else throw new Error("Missed `structure` field in `"+t+"` node type definition")}}return e}function as(i,e,t){let o={};for(let r in i)i[r].syntax&&(o[r]=t?i[r].syntax:ei(i[r].syntax,{compact:e}));return o}function Jy(i,e,t){let o={};for(let[r,n]of Object.entries(i))o[r]={prelude:n.prelude&&(t?n.prelude.syntax:ei(n.prelude.syntax,{compact:e})),descriptors:n.descriptors&&as(n.descriptors,e,t)};return o}function Zy(i){for(let e=0;e<i.length;e++)if(i[e].value.toLowerCase()==="var(")return!0;return!1}function Qy(i){let e=i.terms[0];return i.explicit===!1&&i.terms.length===1&&e.type==="Multiplier"&&e.comma===!0}function Je(i,e,t){return{matched:i,iterations:t,error:e,...os}}function Wi(i,e,t,o){let r=Vu(t,i.syntax),n;return Zy(r)?Je(null,new Error("Matching for a tree with var() is not supported")):(o&&(n=ts(r,i.cssWideKeywordsSyntax,i)),(!o||!n.match)&&(n=ts(r,e.match,i),!n.match)?Je(null,new xu(n.reason,e.syntax,t,n),n.iterations):Je(n.match,null,n.iterations))}var ti=class{constructor(e,t,o){if(this.cssWideKeywords=Ni,this.syntax=t,this.generic=!1,this.units={...qr},this.atrules=Object.create(null),this.properties=Object.create(null),this.types=Object.create(null),this.structure=o||nm(e),e){if(e.cssWideKeywords&&(this.cssWideKeywords=e.cssWideKeywords),e.units)for(let r of Object.keys(qr))Array.isArray(e.units[r])&&(this.units[r]=e.units[r]);if(e.types)for(let[r,n]of Object.entries(e.types))this.addType_(r,n);if(e.generic){this.generic=!0;for(let[r,n]of Object.entries(Lu(this.units)))this.addType_(r,n)}if(e.atrules)for(let[r,n]of Object.entries(e.atrules))this.addAtrule_(r,n);if(e.properties)for(let[r,n]of Object.entries(e.properties))this.addProperty_(r,n)}this.cssWideKeywordsSyntax=wo(this.cssWideKeywords.join(" |  "))}checkStructure(e){function t(n,a){r.push({node:n,message:a})}let o=this.structure,r=[];return this.syntax.walk(e,function(n){o.hasOwnProperty(n.type)?o[n.type].check(n,t):t(n,"Unknown node type `"+n.type+"`")}),r.length?r:!1}createDescriptor(e,t,o,r=null){let n={type:t,name:o},a={type:t,name:o,parent:r,serializable:typeof e=="string"||e&&typeof e.type=="string",syntax:null,match:null,matchRef:null};return typeof e=="function"?a.match=wo(e,n):(typeof e=="string"?Object.defineProperty(a,"syntax",{get(){return Object.defineProperty(a,"syntax",{value:Di(e)}),a.syntax}}):a.syntax=e,Object.defineProperty(a,"match",{get(){return Object.defineProperty(a,"match",{value:wo(a.syntax,n)}),a.match}}),t==="Property"&&Object.defineProperty(a,"matchRef",{get(){let s=a.syntax,c=Qy(s)?wo({...s,terms:[s.terms[0].term]},n):null;return Object.defineProperty(a,"matchRef",{value:c}),c}})),a}addAtrule_(e,t){t&&(this.atrules[e]={type:"Atrule",name:e,prelude:t.prelude?this.createDescriptor(t.prelude,"AtrulePrelude",e):null,descriptors:t.descriptors?Object.keys(t.descriptors).reduce((o,r)=>(o[r]=this.createDescriptor(t.descriptors[r],"AtruleDescriptor",r,e),o),Object.create(null)):null})}addProperty_(e,t){t&&(this.properties[e]=this.createDescriptor(t,"Property",e))}addType_(e,t){t&&(this.types[e]=this.createDescriptor(t,"Type",e))}checkAtruleName(e){if(!this.getAtrule(e))return new Ai("Unknown at-rule","@"+e)}checkAtrulePrelude(e,t){let o=this.checkAtruleName(e);if(o)return o;let r=this.getAtrule(e);if(!r.prelude&&t)return new SyntaxError("At-rule `@"+e+"` should not contain a prelude");if(r.prelude&&!t&&!Wi(this,r.prelude,"",!1).matched)return new SyntaxError("At-rule `@"+e+"` should contain a prelude")}checkAtruleDescriptorName(e,t){let o=this.checkAtruleName(e);if(o)return o;let r=this.getAtrule(e),n=po(t);if(!r.descriptors)return new SyntaxError("At-rule `@"+e+"` has no known descriptors");if(!r.descriptors[n.name]&&!r.descriptors[n.basename])return new Ai("Unknown at-rule descriptor",t)}checkPropertyName(e){if(!this.getProperty(e))return new Ai("Unknown property",e)}matchAtrulePrelude(e,t){let o=this.checkAtrulePrelude(e,t);if(o)return Je(null,o);let r=this.getAtrule(e);return r.prelude?Wi(this,r.prelude,t||"",!1):Je(null,null)}matchAtruleDescriptor(e,t,o){let r=this.checkAtruleDescriptorName(e,t);if(r)return Je(null,r);let n=this.getAtrule(e),a=po(t);return Wi(this,n.descriptors[a.name]||n.descriptors[a.basename],o,!1)}matchDeclaration(e){return e.type!=="Declaration"?Je(null,new Error("Not a Declaration node")):this.matchProperty(e.property,e.value)}matchProperty(e,t){if(Yr(e).custom)return Je(null,new Error("Lexer matching doesn't applicable for custom properties"));let o=this.checkPropertyName(e);return o?Je(null,o):Wi(this,this.getProperty(e),t,!0)}matchType(e,t){let o=this.getType(e);return o?Wi(this,o,t,!1):Je(null,new Ai("Unknown type",e))}match(e,t){return typeof e!="string"&&(!e||!e.type)?Je(null,new Ai("Bad syntax")):((typeof e=="string"||!e.match)&&(e=this.createDescriptor(e,"Type","anonymous")),Wi(this,e,t,!1))}findValueFragments(e,t,o,r){return rs(this,t,this.matchProperty(e,t),o,r)}findDeclarationValueFragments(e,t,o){return rs(this,e.value,this.matchDeclaration(e),t,o)}findAllFragments(e,t,o){let r=[];return this.syntax.walk(e,{visit:"Declaration",enter:n=>{r.push.apply(r,this.findDeclarationValueFragments(n,t,o))}}),r}getAtrule(e,t=!0){let o=po(e);return(o.vendor&&t?this.atrules[o.name]||this.atrules[o.basename]:this.atrules[o.name])||null}getAtrulePrelude(e,t=!0){let o=this.getAtrule(e,t);return o&&o.prelude||null}getAtruleDescriptor(e,t){return this.atrules.hasOwnProperty(e)&&this.atrules.declarators&&this.atrules[e].declarators[t]||null}getProperty(e,t=!0){let o=Yr(e);return(o.vendor&&t?this.properties[o.name]||this.properties[o.basename]:this.properties[o.name])||null}getType(e){return hasOwnProperty.call(this.types,e)?this.types[e]:null}validate(){function e(c,l){return l?`<${c}>`:`<'${c}'>`}function t(c,l,h,d){if(h.has(l))return h.get(l);h.set(l,!1),d.syntax!==null&&rn(d.syntax,function(u){if(u.type!=="Type"&&u.type!=="Property")return;let m=u.type==="Type"?c.types:c.properties,p=u.type==="Type"?r:n;hasOwnProperty.call(m,u.name)?t(c,u.name,p,m[u.name])&&(o.push(`${e(l,h===r)} used broken syntax definition ${e(u.name,u.type==="Type")}`),h.set(l,!0)):(o.push(`${e(l,h===r)} used missed syntax definition ${e(u.name,u.type==="Type")}`),h.set(l,!0))},this)}let o=[],r=new Map,n=new Map;for(let c in this.types)t(this,c,r,this.types[c]);for(let c in this.properties)t(this,c,n,this.properties[c]);let a=[...r.keys()].filter(c=>r.get(c)),s=[...n.keys()].filter(c=>n.get(c));return a.length||s.length?{errors:o,types:a,properties:s}:null}dump(e,t){return{generic:this.generic,cssWideKeywords:this.cssWideKeywords,units:this.units,types:as(this.types,!t,e),properties:as(this.properties,!t,e),atrules:Jy(this.atrules,!t,e)}}toString(){return JSON.stringify(this.dump())}};function ss(i,e){return typeof e=="string"&&/^\s*\|/.test(e)?typeof i=="string"?i+e:e.replace(/^\s*\|\s*/,""):e||null}function eS(i,e){let t=Object.create(null);for(let o of Object.keys(i))e.includes(o)&&(t[o]=i[o]);return t}function ls(i,e,t){let o={...i};for(let[r,n]of Object.entries(e))o[r]={...o[r],...t?eS(n,t):n};return o}function ko(i,e){let t={...i};for(let[o,r]of Object.entries(e))switch(o){case"generic":t[o]=!!r;break;case"cssWideKeywords":t[o]=i[o]?[...i[o],...r]:r||[];break;case"units":t[o]={...i[o]};for(let[n,a]of Object.entries(r))t[o][n]=Array.isArray(a)?a:[];break;case"atrules":t[o]={...i[o]};for(let[n,a]of Object.entries(r)){let s=t[o][n]||{},c=t[o][n]={prelude:s.prelude||null,descriptors:{...s.descriptors}};if(a){c.prelude=a.prelude?ss(c.prelude,a.prelude):c.prelude||null;for(let[l,h]of Object.entries(a.descriptors||{}))c.descriptors[l]=h?ss(c.descriptors[l],h):null;Object.keys(c.descriptors).length||(c.descriptors=null)}}break;case"types":case"properties":t[o]={...i[o]};for(let[n,a]of Object.entries(r))t[o][n]=ss(t[o][n],a);break;case"parseContext":t[o]={...i[o],...r};break;case"scope":case"features":t[o]=ls(i[o],r);break;case"atrule":case"pseudo":t[o]=ls(i[o],r,["parse"]);break;case"node":t[o]=ls(i[o],r,["name","structure","parse","generate","walkContext"]);break}return t}function am(i){let e=Gd(i),t=vu(i),o=gu(i),{fromPlainObject:r,toPlainObject:n}=fu(t),a={lexer:null,createLexer:s=>new ti(s,a,a.lexer.structure),tokenize:Tt,parse:e,generate:o,walk:t,find:t.find,findLast:t.findLast,findAll:t.findAll,fromPlainObject:r,toPlainObject:n,fork(s){let c=ko({},i);return am(typeof s=="function"?s(c):ko(c,s))}};return a.lexer=new ti({generic:i.generic,cssWideKeywords:i.cssWideKeywords,units:i.units,types:i.types,atrules:i.atrules,properties:i.properties,node:i.node},a),a}var an=i=>am(ko({},i));var sm={generic:!0,cssWideKeywords:["initial","inherit","unset","revert","revert-layer"],units:{angle:["deg","grad","rad","turn"],decibel:["db"],flex:["fr"],frequency:["hz","khz"],length:["cm","mm","q","in","pt","pc","px","em","rem","ex","rex","cap","rcap","ch","rch","ic","ric","lh","rlh","vw","svw","lvw","dvw","vh","svh","lvh","dvh","vi","svi","lvi","dvi","vb","svb","lvb","dvb","vmin","svmin","lvmin","dvmin","vmax","svmax","lvmax","dvmax","cqw","cqh","cqi","cqb","cqmin","cqmax"],resolution:["dpi","dpcm","dppx","x"],semitones:["st"],time:["s","ms"]},types:{"abs()":"abs( <calc-sum> )","absolute-size":"xx-small|x-small|small|medium|large|x-large|xx-large|xxx-large","acos()":"acos( <calc-sum> )","alpha-value":"<number>|<percentage>","an+b":"odd|even|<integer>|<n-dimension>|'+'? \u2020 n|-n|<ndashdigit-dimension>|'+'? \u2020 <ndashdigit-ident>|<dashndashdigit-ident>|<n-dimension> <signed-integer>|'+'? \u2020 n <signed-integer>|-n <signed-integer>|<ndash-dimension> <signless-integer>|'+'? \u2020 n- <signless-integer>|-n- <signless-integer>|<n-dimension> ['+'|'-'] <signless-integer>|'+'? \u2020 n ['+'|'-'] <signless-integer>|-n ['+'|'-'] <signless-integer>","anchor()":"anchor( <anchor-name>?&&<anchor-side> , <length-percentage>? )","anchor-name":"<dashed-ident>","anchor-side":"inside|outside|top|left|right|bottom|start|end|self-start|self-end|<percentage>|center","anchor-size":"width|height|block|inline|self-block|self-inline","anchor-size()":"anchor-size( [<anchor-name>||<anchor-size>]? , <length-percentage>? )","angle-percentage":"<angle>|<percentage>","angular-color-hint":"<angle-percentage>|<zero>","angular-color-stop":"<color> <color-stop-angle>?","angular-color-stop-list":"<angular-color-stop> , [<angular-color-hint>? , <angular-color-stop>]#?","animateable-feature":"scroll-position|contents|<custom-ident>","animation-action":"none|play|play-once|play-forwards|play-backwards|pause|reset|replay","asin()":"asin( <calc-sum> )","atan()":"atan( <calc-sum> )","atan2()":"atan2( <calc-sum> , <calc-sum> )",attachment:"scroll|fixed|local","attr()":"attr( <attr-name> <attr-type>? , <declaration-value>? )","attr-matcher":"['~'|'|'|'^'|'$'|'*']? '='","attr-modifier":"i|s","attr-type":"type( <syntax> )|raw-string|number|<attr-unit>","attribute-selector":"'[' <wq-name> ']'|'[' <wq-name> <attr-matcher> [<string-token>|<ident-token>] <attr-modifier>? ']'","auto-repeat":"repeat( [auto-fill|auto-fit] , [<line-names>? <fixed-size>]+ <line-names>? )","auto-track-list":"[<line-names>? [<fixed-size>|<fixed-repeat>]]* <line-names>? <auto-repeat> [<line-names>? [<fixed-size>|<fixed-repeat>]]* <line-names>?",axis:"block|inline|x|y","baseline-position":"[first|last]? baseline","basic-shape":"<inset()>|<xywh()>|<rect()>|<circle()>|<ellipse()>|<polygon()>|<path()>","basic-shape-rect":"<inset()>|<rect()>|<xywh()>","bg-clip":"<visual-box>|border-area|text","bg-image":"<image>|none","bg-layer":"<bg-image>||<bg-position> [/ <bg-size>]?||<repeat-style>||<attachment>||<visual-box>||<visual-box>","bg-position":"[[left|center|right|top|bottom|<length-percentage>]|[left|center|right|<length-percentage>] [top|center|bottom|<length-percentage>]|[center|[left|right] <length-percentage>?]&&[center|[top|bottom] <length-percentage>?]]","bg-size":"[<length-percentage [0,\u221E]>|auto]{1,2}|cover|contain","blend-mode":"normal|multiply|screen|overlay|darken|lighten|color-dodge|color-burn|hard-light|soft-light|difference|exclusion|hue|saturation|color|luminosity","blur()":"blur( <length>? )","brightness()":"brightness( [<number>|<percentage>]? )","calc()":"calc( <calc-sum> )","calc-constant":"e|pi|infinity|-infinity|NaN","calc-product":"<calc-value> ['*' <calc-value>|'/' <number>]*","calc-size()":"calc-size( <calc-size-basis> , <calc-sum> )","calc-size-basis":"<intrinsic-size-keyword>|<calc-size()>|any|<calc-sum>","calc-sum":"<calc-product> [['+'|'-'] <calc-product>]*","calc-value":"<number>|<dimension>|<percentage>|<calc-constant>|( <calc-sum> )","cf-final-image":"<image>|<color>","cf-mixing-image":"<percentage>?&&<image>","circle()":"circle( <radial-size>? [at <position>]? )","clamp()":"clamp( <calc-sum>#{3} )","class-selector":"'.' <ident-token>","clip-source":"<url>",color:"<color-base>|currentColor|<system-color>|<device-cmyk()>|<light-dark()>|<-non-standard-color>","color()":"color( <colorspace-params> [/ [<alpha-value>|none]]? )","color-base":"<hex-color>|<color-function>|<named-color>|<color-mix()>|transparent","color-function":"<rgb()>|<rgba()>|<hsl()>|<hsla()>|<hwb()>|<lab()>|<lch()>|<oklab()>|<oklch()>|<color()>","color-interpolation-method":"in [<rectangular-color-space>|<polar-color-space> <hue-interpolation-method>?|<custom-color-space>]","color-mix()":"color-mix( <color-interpolation-method> , [<color>&&<percentage [0,100]>?]#{2} )","color-stop":"<color-stop-length>|<color-stop-angle>","color-stop-angle":"[<angle-percentage>|<zero>]{1,2}","color-stop-length":"<length-percentage>{1,2}","color-stop-list":"<linear-color-stop> , [<linear-color-hint>? , <linear-color-stop>]#?","colorspace-params":"[<predefined-rgb-params>|<xyz-params>]",combinator:"'>'|'+'|'~'|['|' '|']","common-lig-values":"[common-ligatures|no-common-ligatures]","compat-auto":"searchfield|textarea|checkbox|radio|menulist|listbox|meter|progress-bar|button","compat-special":"textfield|menulist-button","complex-selector":"<complex-selector-unit> [<combinator>? <complex-selector-unit>]*","complex-selector-list":"<complex-selector>#","composite-style":"clear|copy|source-over|source-in|source-out|source-atop|destination-over|destination-in|destination-out|destination-atop|xor","compositing-operator":"add|subtract|intersect|exclude","compound-selector":"[<type-selector>? <subclass-selector>*]!","compound-selector-list":"<compound-selector>#","conic-gradient()":"conic-gradient( [<conic-gradient-syntax>] )","conic-gradient-syntax":"[[[from [<angle>|<zero>]]? [at <position>]?]||<color-interpolation-method>]? , <angular-color-stop-list>","container-condition":"not <query-in-parens>|<query-in-parens> [[and <query-in-parens>]*|[or <query-in-parens>]*]","container-name":"<custom-ident>","container-query":"not <query-in-parens>|<query-in-parens> [[and <query-in-parens>]*|[or <query-in-parens>]*]","content-distribution":"space-between|space-around|space-evenly|stretch","content-list":"[<string>|contents|<image>|<counter>|<quote>|<target>|<leader()>|<attr()>]+","content-position":"center|start|end|flex-start|flex-end","content-replacement":"<image>","contextual-alt-values":"[contextual|no-contextual]","contrast()":"contrast( [<number>|<percentage>]? )","coord-box":"content-box|padding-box|border-box|fill-box|stroke-box|view-box","corner-shape-value":"round|scoop|bevel|notch|square|squircle|<superellipse()>","cos()":"cos( <calc-sum> )",counter:"<counter()>|<counters()>","counter()":"counter( <counter-name> , <counter-style>? )","counter-name":"<custom-ident>","counter-style":"<counter-style-name>|symbols( )","counter-style-name":"<custom-ident>","counters()":"counters( <counter-name> , <string> , <counter-style>? )","cross-fade()":"cross-fade( <cf-mixing-image> , <cf-final-image>? )","cubic-bezier()":"cubic-bezier( [<number [0,1]> , <number>]#{2} )","cubic-bezier-easing-function":"ease|ease-in|ease-out|ease-in-out|cubic-bezier( <number [0,1]> , <number> , <number [0,1]> , <number> )","cursor-predefined":"auto|default|none|context-menu|help|pointer|progress|wait|cell|crosshair|text|vertical-text|alias|copy|move|no-drop|not-allowed|e-resize|n-resize|ne-resize|nw-resize|s-resize|se-resize|sw-resize|w-resize|ew-resize|ns-resize|nesw-resize|nwse-resize|col-resize|row-resize|all-scroll|zoom-in|zoom-out|grab|grabbing","custom-color-space":"<dashed-ident>","custom-params":"<dashed-ident> [<number>|<percentage>|none]+",dasharray:"[[<length-percentage>|<number>]+]#","dashndashdigit-ident":"<ident-token>","deprecated-system-color":"ActiveBorder|ActiveCaption|AppWorkspace|Background|ButtonHighlight|ButtonShadow|CaptionText|InactiveBorder|InactiveCaption|InactiveCaptionText|InfoBackground|InfoText|Menu|MenuText|Scrollbar|ThreeDDarkShadow|ThreeDFace|ThreeDHighlight|ThreeDLightShadow|ThreeDShadow|Window|WindowFrame|WindowText","discretionary-lig-values":"[discretionary-ligatures|no-discretionary-ligatures]","display-box":"contents|none","display-inside":"flow|flow-root|table|flex|grid|ruby","display-internal":"table-row-group|table-header-group|table-footer-group|table-row|table-cell|table-column-group|table-column|table-caption|ruby-base|ruby-text|ruby-base-container|ruby-text-container","display-legacy":"inline-block|inline-list-item|inline-table|inline-flex|inline-grid","display-listitem":"<display-outside>?&&[flow|flow-root]?&&list-item","display-outside":"block|inline|run-in","drop-shadow()":"drop-shadow( [<color>?&&<length>{2,3}] )","dynamic-range-limit-mix()":"dynamic-range-limit-mix( [<'dynamic-range-limit'>&&<percentage [0,100]>]#{2,} )","easing-function":"<linear-easing-function>|<cubic-bezier-easing-function>|<step-easing-function>","east-asian-variant-values":"[jis78|jis83|jis90|jis04|simplified|traditional]","east-asian-width-values":"[full-width|proportional-width]","element()":"element( <custom-ident> , [first|start|last|first-except]? )|element( <id-selector> )","ellipse()":"ellipse( <radial-size>? [at <position>]? )","env()":"env( <custom-ident> , <declaration-value>? )","exp()":"exp( <calc-sum> )","explicit-track-list":"[<line-names>? <track-size>]+ <line-names>?","family-name":"<string>|<custom-ident>+","feature-tag-value":"<string> [<integer>|on|off]?","feature-type":"@stylistic|@historical-forms|@styleset|@character-variant|@swash|@ornaments|@annotation","feature-value-block":"<feature-type> '{' <feature-value-declaration-list> '}'","feature-value-block-list":"<feature-value-block>+","feature-value-declaration":"<custom-ident> : <integer>+ ;","feature-value-declaration-list":"<feature-value-declaration>","feature-value-name":"<custom-ident>","filter-function":"<blur()>|<brightness()>|<contrast()>|<drop-shadow()>|<grayscale()>|<hue-rotate()>|<invert()>|<opacity()>|<saturate()>|<sepia()>","filter-value-list":"[<filter-function>|<url>]+","final-bg-layer":"<bg-image>||<bg-position> [/ <bg-size>]?||<repeat-style>||<attachment>||<visual-box>||<visual-box>||<'background-color'>","fit-content()":"fit-content( <length-percentage [0,\u221E]> )","fixed-breadth":"<length-percentage>","fixed-repeat":"repeat( [<integer [1,\u221E]>] , [<line-names>? <fixed-size>]+ <line-names>? )","fixed-size":"<fixed-breadth>|minmax( <fixed-breadth> , <track-breadth> )|minmax( <inflexible-breadth> , <fixed-breadth> )","font-stretch-absolute":"normal|ultra-condensed|extra-condensed|condensed|semi-condensed|semi-expanded|expanded|extra-expanded|ultra-expanded|<percentage>","font-variant-css2":"normal|small-caps","font-weight-absolute":"normal|bold|<number [1,1000]>","font-width-css3":"normal|ultra-condensed|extra-condensed|condensed|semi-condensed|semi-expanded|expanded|extra-expanded|ultra-expanded","form-control-identifier":"select","frequency-percentage":"<frequency>|<percentage>","generic-complete":"serif|sans-serif|system-ui|cursive|fantasy|math|monospace","general-enclosed":"[<function-token> <any-value>? )]|[( <any-value>? )]","generic-family":"<generic-script-specific>|<generic-complete>|<generic-incomplete>|<-non-standard-generic-family>","generic-incomplete":"ui-serif|ui-sans-serif|ui-monospace|ui-rounded","geometry-box":"<shape-box>|fill-box|stroke-box|view-box",gradient:"<linear-gradient()>|<repeating-linear-gradient()>|<radial-gradient()>|<repeating-radial-gradient()>|<conic-gradient()>|<repeating-conic-gradient()>|<-legacy-gradient>","grayscale()":"grayscale( [<number>|<percentage>]? )","grid-line":"auto|<custom-ident>|[<integer>&&<custom-ident>?]|[span&&[<integer>||<custom-ident>]]","historical-lig-values":"[historical-ligatures|no-historical-ligatures]","hsl()":"hsl( <hue> , <percentage> , <percentage> , <alpha-value>? )|hsl( [<hue>|none] [<percentage>|<number>|none] [<percentage>|<number>|none] [/ [<alpha-value>|none]]? )","hsla()":"hsla( <hue> , <percentage> , <percentage> , <alpha-value>? )|hsla( [<hue>|none] [<percentage>|<number>|none] [<percentage>|<number>|none] [/ [<alpha-value>|none]]? )",hue:"<number>|<angle>","hue-interpolation-method":"[shorter|longer|increasing|decreasing] hue","hue-rotate()":"hue-rotate( [<angle>|<zero>]? )","hwb()":"hwb( [<hue>|none] [<percentage>|<number>|none] [<percentage>|<number>|none] [/ [<alpha-value>|none]]? )","hypot()":"hypot( <calc-sum># )",image:"<url>|<image()>|<image-set()>|<element()>|<paint()>|<cross-fade()>|<gradient>","image()":"image( <image-tags>? [<image-src>? , <color>?]! )","image-set()":"image-set( <image-set-option># )","image-set-option":"[<image>|<string>] [<resolution>||type( <string> )]","image-src":"<url>|<string>","image-tags":"ltr|rtl","inflexible-breadth":"<length-percentage>|min-content|max-content|auto","inset()":"inset( <length-percentage>{1,4} [round <'border-radius'>]? )","invert()":"invert( [<number>|<percentage>]? )","keyframe-block":"<keyframe-selector># { <declaration-list> }","keyframe-selector":"from|to|<percentage [0,100]>|<timeline-range-name> <percentage>","keyframes-name":"<custom-ident>|<string>","lab()":"lab( [<percentage>|<number>|none] [<percentage>|<number>|none] [<percentage>|<number>|none] [/ [<alpha-value>|none]]? )","layer()":"layer( <layer-name> )","layer-name":"<ident> ['.' <ident>]*","lch()":"lch( [<percentage>|<number>|none] [<percentage>|<number>|none] [<hue>|none] [/ [<alpha-value>|none]]? )","leader()":"leader( <leader-type> )","leader-type":"dotted|solid|space|<string>","length-percentage":"<length>|<percentage>","light-dark()":"light-dark( <color> , <color> )","line-name-list":"[<line-names>|<name-repeat>]+","line-names":"'[' <custom-ident>* ']'","line-style":"none|hidden|dotted|dashed|solid|double|groove|ridge|inset|outset","line-width":"<length>|thin|medium|thick","linear()":"linear( [<number>&&<percentage>{0,2}]# )","linear-color-hint":"<length-percentage>","linear-color-stop":"<color> <color-stop-length>?","linear-easing-function":"linear|<linear()>","linear-gradient()":"linear-gradient( [<linear-gradient-syntax>] )","linear-gradient-syntax":"[[<angle>|<zero>|to <side-or-corner>]||<color-interpolation-method>]? , <color-stop-list>","log()":"log( <calc-sum> , <calc-sum>? )","mask-layer":"<mask-reference>||<position> [/ <bg-size>]?||<repeat-style>||<geometry-box>||[<geometry-box>|no-clip]||<compositing-operator>||<masking-mode>","mask-position":"[<length-percentage>|left|center|right] [<length-percentage>|top|center|bottom]?","mask-reference":"none|<image>|<mask-source>","mask-source":"<url>","masking-mode":"alpha|luminance|match-source","matrix()":"matrix( <number>#{6} )","matrix3d()":"matrix3d( <number>#{16} )","max()":"max( <calc-sum># )","media-and":"<media-in-parens> [and <media-in-parens>]+","media-condition":"<media-not>|<media-and>|<media-or>|<media-in-parens>","media-condition-without-or":"<media-not>|<media-and>|<media-in-parens>","media-feature":"( [<mf-plain>|<mf-boolean>|<mf-range>] )","media-in-parens":"( <media-condition> )|<media-feature>|<general-enclosed>","media-not":"not <media-in-parens>","media-or":"<media-in-parens> [or <media-in-parens>]+","media-query":"<media-condition>|[not|only]? <media-type> [and <media-condition-without-or>]?","media-query-list":"<media-query>#","media-type":"<ident>","mf-boolean":"<mf-name>","mf-name":"<ident>","mf-plain":"<mf-name> : <mf-value>","mf-range":"<mf-name> ['<'|'>']? '='? <mf-value>|<mf-value> ['<'|'>']? '='? <mf-name>|<mf-value> '<' '='? <mf-name> '<' '='? <mf-value>|<mf-value> '>' '='? <mf-name> '>' '='? <mf-value>","mf-value":"<number>|<dimension>|<ident>|<ratio>","min()":"min( <calc-sum># )","minmax()":"minmax( [<length-percentage>|min-content|max-content|auto] , [<length-percentage>|<flex>|min-content|max-content|auto] )","mod()":"mod( <calc-sum> , <calc-sum> )","n-dimension":"<dimension-token>","name-repeat":"repeat( [<integer [1,\u221E]>|auto-fill] , <line-names>+ )","named-color":"aliceblue|antiquewhite|aqua|aquamarine|azure|beige|bisque|black|blanchedalmond|blue|blueviolet|brown|burlywood|cadetblue|chartreuse|chocolate|coral|cornflowerblue|cornsilk|crimson|cyan|darkblue|darkcyan|darkgoldenrod|darkgray|darkgreen|darkgrey|darkkhaki|darkmagenta|darkolivegreen|darkorange|darkorchid|darkred|darksalmon|darkseagreen|darkslateblue|darkslategray|darkslategrey|darkturquoise|darkviolet|deeppink|deepskyblue|dimgray|dimgrey|dodgerblue|firebrick|floralwhite|forestgreen|fuchsia|gainsboro|ghostwhite|gold|goldenrod|gray|green|greenyellow|grey|honeydew|hotpink|indianred|indigo|ivory|khaki|lavender|lavenderblush|lawngreen|lemonchiffon|lightblue|lightcoral|lightcyan|lightgoldenrodyellow|lightgray|lightgreen|lightgrey|lightpink|lightsalmon|lightseagreen|lightskyblue|lightslategray|lightslategrey|lightsteelblue|lightyellow|lime|limegreen|linen|magenta|maroon|mediumaquamarine|mediumblue|mediumorchid|mediumpurple|mediumseagreen|mediumslateblue|mediumspringgreen|mediumturquoise|mediumvioletred|midnightblue|mintcream|mistyrose|moccasin|navajowhite|navy|oldlace|olive|olivedrab|orange|orangered|orchid|palegoldenrod|palegreen|paleturquoise|palevioletred|papayawhip|peachpuff|peru|pink|plum|powderblue|purple|rebeccapurple|red|rosybrown|royalblue|saddlebrown|salmon|sandybrown|seagreen|seashell|sienna|silver|skyblue|slateblue|slategray|slategrey|snow|springgreen|steelblue|tan|teal|thistle|tomato|turquoise|violet|wheat|white|whitesmoke|yellow|yellowgreen","namespace-prefix":"<ident>","ndash-dimension":"<dimension-token>","ndashdigit-dimension":"<dimension-token>","ndashdigit-ident":"<ident-token>","ns-prefix":"[<ident-token>|'*']? '|'","number-percentage":"<number>|<percentage>","numeric-figure-values":"[lining-nums|oldstyle-nums]","numeric-fraction-values":"[diagonal-fractions|stacked-fractions]","numeric-spacing-values":"[proportional-nums|tabular-nums]","offset-path":"<ray()>|<url>|<basic-shape>","oklab()":"oklab( [<percentage>|<number>|none] [<percentage>|<number>|none] [<percentage>|<number>|none] [/ [<alpha-value>|none]]? )","oklch()":"oklch( [<percentage>|<number>|none] [<percentage>|<number>|none] [<hue>|none] [/ [<alpha-value>|none]]? )","opacity()":"opacity( [<number>|<percentage>]? )","opacity-value":"<number>|<percentage>","outline-line-style":"none|dotted|dashed|solid|double|groove|ridge|inset|outset","outline-radius":"<length>|<percentage>","overflow-position":"unsafe|safe","page-body":"<declaration>? [; <page-body>]?|<page-margin-box> <page-body>","page-margin-box":"<page-margin-box-type> '{' <declaration-list> '}'","page-margin-box-type":"@top-left-corner|@top-left|@top-center|@top-right|@top-right-corner|@bottom-left-corner|@bottom-left|@bottom-center|@bottom-right|@bottom-right-corner|@left-top|@left-middle|@left-bottom|@right-top|@right-middle|@right-bottom","page-selector":"<pseudo-page>+|<ident> <pseudo-page>*","page-selector-list":"[<page-selector>#]?","page-size":"A5|A4|A3|B5|B4|JIS-B5|JIS-B4|letter|legal|ledger",paint:"none|<color>|<url> [none|<color>]?|context-fill|context-stroke","paint()":"paint( <ident> , <declaration-value>? )","paint-box":"<visual-box>|fill-box|stroke-box","palette-identifier":"<dashed-ident>","palette-mix()":"palette-mix( <color-interpolation-method> , [[normal|light|dark|<palette-identifier>|<palette-mix()>]&&<percentage [0,100]>?]#{2} )","path()":"path( <'fill-rule'>? , <string> )","perspective()":"perspective( [<length [0,\u221E]>|none] )","polar-color-space":"hsl|hwb|lch|oklch","polygon()":"polygon( <'fill-rule'>? , [<length-percentage> <length-percentage>]# )",position:"[[left|center|right]||[top|center|bottom]|[left|center|right|<length-percentage>] [top|center|bottom|<length-percentage>]?|[[left|right] <length-percentage>]&&[[top|bottom] <length-percentage>]]","position-area":"[[left|center|right|span-left|span-right|x-start|x-end|span-x-start|span-x-end|x-self-start|x-self-end|span-x-self-start|span-x-self-end|span-all]||[top|center|bottom|span-top|span-bottom|y-start|y-end|span-y-start|span-y-end|y-self-start|y-self-end|span-y-self-start|span-y-self-end|span-all]|[block-start|center|block-end|span-block-start|span-block-end|span-all]||[inline-start|center|inline-end|span-inline-start|span-inline-end|span-all]|[self-block-start|center|self-block-end|span-self-block-start|span-self-block-end|span-all]||[self-inline-start|center|self-inline-end|span-self-inline-start|span-self-inline-end|span-all]|[start|center|end|span-start|span-end|span-all]{1,2}|[self-start|center|self-end|span-self-start|span-self-end|span-all]{1,2}]","pow()":"pow( <calc-sum> , <calc-sum> )","predefined-rgb":"srgb|srgb-linear|display-p3|display-p3-linear|a98-rgb|prophoto-rgb|rec2020","predefined-rgb-params":"<predefined-rgb> [<number>|<percentage>|none]{3}","pseudo-class-selector":"':' <ident-token>|':' <function-token> <any-value> ')'","pseudo-element-selector":"':' <pseudo-class-selector>|<legacy-pseudo-element-selector>","pseudo-page":": [left|right|first|blank]","query-in-parens":"( <container-condition> )|( <size-feature> )|style( <style-query> )|<general-enclosed>",quote:"open-quote|close-quote|no-open-quote|no-close-quote","radial-extent":"closest-corner|closest-side|farthest-corner|farthest-side","radial-gradient()":"radial-gradient( [<radial-gradient-syntax>] )","radial-gradient-syntax":"[[[<radial-shape>||<radial-size>]? [at <position>]?]||<color-interpolation-method>]? , <color-stop-list>","radial-shape":"circle|ellipse","radial-size":"<radial-extent>|<length [0,\u221E]>|<length-percentage [0,\u221E]>{2}",ratio:"<number [0,\u221E]> [/ <number [0,\u221E]>]?","ray()":"ray( <angle>&&<ray-size>?&&contain?&&[at <position>]? )","ray-size":"closest-side|closest-corner|farthest-side|farthest-corner|sides","rect()":"rect( [<length-percentage>|auto]{4} [round <'border-radius'>]? )","rectangular-color-space":"srgb|srgb-linear|display-p3|display-p3-linear|a98-rgb|prophoto-rgb|rec2020|lab|oklab|xyz|xyz-d50|xyz-d65","relative-selector":"<combinator>? <complex-selector>","relative-selector-list":"<relative-selector>#","relative-size":"larger|smaller","rem()":"rem( <calc-sum> , <calc-sum> )","repeat-style":"repeat-x|repeat-y|[repeat|space|round|no-repeat]{1,2}","repeating-conic-gradient()":"repeating-conic-gradient( [<conic-gradient-syntax>] )","repeating-linear-gradient()":"repeating-linear-gradient( [<linear-gradient-syntax>] )","repeating-radial-gradient()":"repeating-radial-gradient( [<radial-gradient-syntax>] )","reversed-counter-name":"reversed( <counter-name> )","rgb()":"rgb( <percentage>#{3} , <alpha-value>? )|rgb( <number>#{3} , <alpha-value>? )|rgb( [<number>|<percentage>|none]{3} [/ [<alpha-value>|none]]? )","rgba()":"rgba( <percentage>#{3} , <alpha-value>? )|rgba( <number>#{3} , <alpha-value>? )|rgba( [<number>|<percentage>|none]{3} [/ [<alpha-value>|none]]? )","rotate()":"rotate( [<angle>|<zero>] )","rotate3d()":"rotate3d( <number> , <number> , <number> , [<angle>|<zero>] )","rotateX()":"rotateX( [<angle>|<zero>] )","rotateY()":"rotateY( [<angle>|<zero>] )","rotateZ()":"rotateZ( [<angle>|<zero>] )","round()":"round( <rounding-strategy>? , <calc-sum> , <calc-sum> )","rounding-strategy":"nearest|up|down|to-zero","saturate()":"saturate( [<number>|<percentage>]? )","scale()":"scale( [<number>|<percentage>]#{1,2} )","scale3d()":"scale3d( [<number>|<percentage>]#{3} )","scaleX()":"scaleX( [<number>|<percentage>] )","scaleY()":"scaleY( [<number>|<percentage>] )","scaleZ()":"scaleZ( [<number>|<percentage>] )","scope-end":"<forgiving-selector-list>","scope-start":"<forgiving-selector-list>","scroll()":"scroll( [<scroller>||<axis>]? )",scroller:"root|nearest|self","scroll-state-feature":"<media-query-list>","scroll-state-in-parens":"( <scroll-state-query> )|( <scroll-state-feature> )|<general-enclosed>","scroll-state-query":"not <scroll-state-in-parens>|<scroll-state-in-parens> [[and <scroll-state-in-parens>]*|[or <scroll-state-in-parens>]*]|<scroll-state-feature>","selector-list":"<complex-selector-list>","self-position":"center|start|end|self-start|self-end|flex-start|flex-end","sepia()":"sepia( [<number>|<percentage>]? )",shadow:"inset?&&<length>{2,4}&&<color>?","shadow-t":"[<length>{2,3}&&<color>?]",shape:"rect( <top> , <right> , <bottom> , <left> )|rect( <top> <right> <bottom> <left> )","shape-box":"<visual-box>|margin-box","side-or-corner":"[left|right]||[top|bottom]","sign()":"sign( <calc-sum> )","signed-integer":"<number-token>","signless-integer":"<number-token>","sin()":"sin( <calc-sum> )","single-animation":"<'animation-duration'>||<easing-function>||<'animation-delay'>||<single-animation-iteration-count>||<single-animation-direction>||<single-animation-fill-mode>||<single-animation-play-state>||[none|<keyframes-name>]||<single-animation-timeline>","single-animation-composition":"replace|add|accumulate","single-animation-direction":"normal|reverse|alternate|alternate-reverse","single-animation-fill-mode":"none|forwards|backwards|both","single-animation-iteration-count":"infinite|<number>","single-animation-play-state":"running|paused","single-animation-timeline":"auto|none|<dashed-ident>|<scroll()>|<view()>","single-transition":"[none|<single-transition-property>]||<time>||<easing-function>||<time>||<transition-behavior-value>","single-transition-property":"all|<custom-ident>",size:"closest-side|farthest-side|closest-corner|farthest-corner|<length>|<length-percentage>{2}","size-feature":"<mf-plain>|<mf-boolean>|<mf-range>","skew()":"skew( [<angle>|<zero>] , [<angle>|<zero>]? )","skewX()":"skewX( [<angle>|<zero>] )","skewY()":"skewY( [<angle>|<zero>] )","sqrt()":"sqrt( <calc-sum> )","step-position":"jump-start|jump-end|jump-none|jump-both|start|end","step-easing-function":"step-start|step-end|<steps()>","steps()":"steps( <integer> , <step-position>? )","style-feature":"<declaration>","style-in-parens":"( <style-condition> )|( <style-feature> )|<general-enclosed>","style-query":"<style-condition>|<style-feature>","subclass-selector":"<id-selector>|<class-selector>|<attribute-selector>|<pseudo-class-selector>","superellipse()":"superellipse( [<number>|infinity|-infinity] )","supports-condition":"not <supports-in-parens>|<supports-in-parens> [and <supports-in-parens>]*|<supports-in-parens> [or <supports-in-parens>]*","supports-decl":"( <declaration> )","supports-feature":"<supports-decl>|<supports-selector-fn>","supports-in-parens":"( <supports-condition> )|<supports-feature>|<general-enclosed>","supports-selector-fn":"selector( <complex-selector> )",symbol:"<string>|<image>|<custom-ident>","symbols()":"symbols( <symbols-type>? [<string>|<image>]+ )","symbols-type":"cyclic|numeric|alphabetic|symbolic|fixed","system-color":"AccentColor|AccentColorText|ActiveText|ButtonBorder|ButtonFace|ButtonText|Canvas|CanvasText|Field|FieldText|GrayText|Highlight|HighlightText|LinkText|Mark|MarkText|SelectedItem|SelectedItemText|VisitedText","system-family-name":"caption|icon|menu|message-box|small-caption|status-bar","tan()":"tan( <calc-sum> )",target:"<target-counter()>|<target-counters()>|<target-text()>","target-counter()":"target-counter( [<string>|<url>] , <custom-ident> , <counter-style>? )","target-counters()":"target-counters( [<string>|<url>] , <custom-ident> , <string> , <counter-style>? )","target-text()":"target-text( [<string>|<url>] , [content|before|after|first-letter]? )","text-edge":"[text|cap|ex|ideographic|ideographic-ink] [text|alphabetic|ideographic|ideographic-ink]?","time-percentage":"<time>|<percentage>","timeline-range-name":"cover|contain|entry|exit|entry-crossing|exit-crossing","track-breadth":"<length-percentage>|<flex>|min-content|max-content|auto","track-list":"[<line-names>? [<track-size>|<track-repeat>]]+ <line-names>?","track-repeat":"repeat( [<integer [1,\u221E]>] , [<line-names>? <track-size>]+ <line-names>? )","track-size":"<track-breadth>|minmax( <inflexible-breadth> , <track-breadth> )|fit-content( <length-percentage> )","transform-function":"<matrix()>|<translate()>|<translateX()>|<translateY()>|<scale()>|<scaleX()>|<scaleY()>|<rotate()>|<skew()>|<skewX()>|<skewY()>|<matrix3d()>|<translate3d()>|<translateZ()>|<scale3d()>|<scaleZ()>|<rotate3d()>|<rotateX()>|<rotateY()>|<rotateZ()>|<perspective()>","transform-list":"<transform-function>+","transition-behavior-value":"normal|allow-discrete","translate()":"translate( <length-percentage> , <length-percentage>? )","translate3d()":"translate3d( <length-percentage> , <length-percentage> , <length> )","translateX()":"translateX( <length-percentage> )","translateY()":"translateY( <length-percentage> )","translateZ()":"translateZ( <length> )","try-size":"most-width|most-height|most-block-size|most-inline-size","try-tactic":"flip-block||flip-inline||flip-start","type-or-unit":"string|color|url|integer|number|length|angle|time|frequency|cap|ch|em|ex|ic|lh|rlh|rem|vb|vi|vw|vh|vmin|vmax|mm|Q|cm|in|pt|pc|px|deg|grad|rad|turn|ms|s|Hz|kHz|%","type-selector":"<wq-name>|<ns-prefix>? '*'","var()":"var( <custom-property-name> , <declaration-value>? )","view()":"view( [<axis>||<'view-timeline-inset'>]? )","viewport-length":"auto|<length-percentage>","visual-box":"content-box|padding-box|border-box","wq-name":"<ns-prefix>? <ident-token>","xywh()":"xywh( <length-percentage>{2} <length-percentage [0,\u221E]>{2} [round <'border-radius'>]? )",xyz:"xyz|xyz-d50|xyz-d65","xyz-params":"<xyz-space> [<number>|<percentage>|none]{3}","-legacy-gradient":"<-webkit-gradient()>|<-legacy-linear-gradient>|<-legacy-repeating-linear-gradient>|<-legacy-radial-gradient>|<-legacy-repeating-radial-gradient>","-legacy-linear-gradient":"-moz-linear-gradient( <-legacy-linear-gradient-arguments> )|-webkit-linear-gradient( <-legacy-linear-gradient-arguments> )|-o-linear-gradient( <-legacy-linear-gradient-arguments> )","-legacy-repeating-linear-gradient":"-moz-repeating-linear-gradient( <-legacy-linear-gradient-arguments> )|-webkit-repeating-linear-gradient( <-legacy-linear-gradient-arguments> )|-o-repeating-linear-gradient( <-legacy-linear-gradient-arguments> )","-legacy-linear-gradient-arguments":"[<angle>|<side-or-corner>]? , <color-stop-list>","-legacy-radial-gradient":"-moz-radial-gradient( <-legacy-radial-gradient-arguments> )|-webkit-radial-gradient( <-legacy-radial-gradient-arguments> )|-o-radial-gradient( <-legacy-radial-gradient-arguments> )","-legacy-repeating-radial-gradient":"-moz-repeating-radial-gradient( <-legacy-radial-gradient-arguments> )|-webkit-repeating-radial-gradient( <-legacy-radial-gradient-arguments> )|-o-repeating-radial-gradient( <-legacy-radial-gradient-arguments> )","-legacy-radial-gradient-arguments":"[<position> ,]? [[[<-legacy-radial-gradient-shape>||<-legacy-radial-gradient-size>]|[<length>|<percentage>]{2}] ,]? <color-stop-list>","-legacy-radial-gradient-size":"closest-side|closest-corner|farthest-side|farthest-corner|contain|cover","-legacy-radial-gradient-shape":"circle|ellipse","-non-standard-font":"-apple-system-body|-apple-system-headline|-apple-system-subheadline|-apple-system-caption1|-apple-system-caption2|-apple-system-footnote|-apple-system-short-body|-apple-system-short-headline|-apple-system-short-subheadline|-apple-system-short-caption1|-apple-system-short-footnote|-apple-system-tall-body","-non-standard-color":"-moz-ButtonDefault|-moz-ButtonHoverFace|-moz-ButtonHoverText|-moz-CellHighlight|-moz-CellHighlightText|-moz-Combobox|-moz-ComboboxText|-moz-Dialog|-moz-DialogText|-moz-dragtargetzone|-moz-EvenTreeRow|-moz-Field|-moz-FieldText|-moz-html-CellHighlight|-moz-html-CellHighlightText|-moz-mac-accentdarkestshadow|-moz-mac-accentdarkshadow|-moz-mac-accentface|-moz-mac-accentlightesthighlight|-moz-mac-accentlightshadow|-moz-mac-accentregularhighlight|-moz-mac-accentregularshadow|-moz-mac-chrome-active|-moz-mac-chrome-inactive|-moz-mac-focusring|-moz-mac-menuselect|-moz-mac-menushadow|-moz-mac-menutextselect|-moz-MenuHover|-moz-MenuHoverText|-moz-MenuBarText|-moz-MenuBarHoverText|-moz-nativehyperlinktext|-moz-OddTreeRow|-moz-win-communicationstext|-moz-win-mediatext|-moz-activehyperlinktext|-moz-default-background-color|-moz-default-color|-moz-hyperlinktext|-moz-visitedhyperlinktext|-webkit-activelink|-webkit-focus-ring-color|-webkit-link|-webkit-text","-non-standard-image-rendering":"optimize-contrast|-moz-crisp-edges|-o-crisp-edges|-webkit-optimize-contrast","-non-standard-overflow":"overlay|-moz-scrollbars-none|-moz-scrollbars-horizontal|-moz-scrollbars-vertical|-moz-hidden-unscrollable","-non-standard-size":"intrinsic|min-intrinsic|-webkit-fill-available|-webkit-fit-content|-webkit-min-content|-webkit-max-content|-moz-available|-moz-fit-content|-moz-min-content|-moz-max-content","-webkit-gradient()":"-webkit-gradient( <-webkit-gradient-type> , <-webkit-gradient-point> [, <-webkit-gradient-point>|, <-webkit-gradient-radius> , <-webkit-gradient-point>] [, <-webkit-gradient-radius>]? [, <-webkit-gradient-color-stop>]* )","-webkit-gradient-color-stop":"from( <color> )|color-stop( [<number-zero-one>|<percentage>] , <color> )|to( <color> )","-webkit-gradient-point":"[left|center|right|<length-percentage>] [top|center|bottom|<length-percentage>]","-webkit-gradient-radius":"<length>|<percentage>","-webkit-gradient-type":"linear|radial","-webkit-mask-box-repeat":"repeat|stretch|round","-ms-filter-function-list":"<-ms-filter-function>+","-ms-filter-function":"<-ms-filter-function-progid>|<-ms-filter-function-legacy>","-ms-filter-function-progid":"'progid:' [<ident-token> '.']* [<ident-token>|<function-token> <any-value>? )]","-ms-filter-function-legacy":"<ident-token>|<function-token> <any-value>? )",age:"child|young|old","attr-name":"<wq-name>","attr-fallback":"<any-value>",autospace:"no-autospace|[ideograph-alpha||ideograph-numeric||punctuation]||[insert|replace]",bottom:"<length>|auto","generic-voice":"[<age>? <gender> <integer>?]",gender:"male|female|neutral","generic-script-specific":"generic( kai )|generic( fangsong )|generic( nastaliq )","-non-standard-generic-family":"-apple-system|BlinkMacSystemFont","intrinsic-size-keyword":"min-content|max-content|fit-content",left:"<length>|auto","device-cmyk()":"<legacy-device-cmyk-syntax>|<modern-device-cmyk-syntax>","legacy-device-cmyk-syntax":"device-cmyk( <number>#{4} )","modern-device-cmyk-syntax":"device-cmyk( <cmyk-component>{4} [/ [<alpha-value>|none]]? )","cmyk-component":"<number>|<percentage>|none","color-space":"<rectangular-color-space>|<polar-color-space>|<custom-color-space>",right:"<length>|auto","forgiving-selector-list":"<complex-real-selector-list>","forgiving-relative-selector-list":"<relative-real-selector-list>","complex-real-selector-list":"<complex-real-selector>#","simple-selector-list":"<simple-selector>#","relative-real-selector-list":"<relative-real-selector>#","complex-selector-unit":"[<compound-selector>? <pseudo-compound-selector>*]!","complex-real-selector":"<compound-selector> [<combinator>? <compound-selector>]*","relative-real-selector":"<combinator>? <complex-real-selector>","pseudo-compound-selector":"<pseudo-element-selector> <pseudo-class-selector>*","simple-selector":"<type-selector>|<subclass-selector>","legacy-pseudo-element-selector":"':' [before|after|first-line|first-letter]","svg-length":"<percentage>|<length>|<number>","svg-writing-mode":"lr-tb|rl-tb|tb-rl|lr|rl|tb",top:"<length>|auto",x:"<number>",y:"<number>",declaration:"<ident-token> : <declaration-value>? ['!' important]?","declaration-list":"[<declaration>? ';']* <declaration>?",url:"url( <string> <url-modifier>* )|<url-token>","url-modifier":"<ident>|<function-token> <any-value> )","number-zero-one":"<number [0,1]>","number-one-or-greater":"<number [1,\u221E]>","xyz-space":"xyz|xyz-d50|xyz-d65","style-condition":"not <style-in-parens>|<style-in-parens> [[and <style-in-parens>]*|[or <style-in-parens>]*]","-non-standard-display":"-ms-inline-flexbox|-ms-grid|-ms-inline-grid|-webkit-flex|-webkit-inline-flex|-webkit-box|-webkit-inline-box|-moz-inline-stack|-moz-box|-moz-inline-box","inset-area":"[[left|center|right|span-left|span-right|x-start|x-end|span-x-start|span-x-end|x-self-start|x-self-end|span-x-self-start|span-x-self-end|span-all]||[top|center|bottom|span-top|span-bottom|y-start|y-end|span-y-start|span-y-end|y-self-start|y-self-end|span-y-self-start|span-y-self-end|span-all]|[block-start|center|block-end|span-block-start|span-block-end|span-all]||[inline-start|center|inline-end|span-inline-start|span-inline-end|span-all]|[self-block-start|self-block-end|span-self-block-start|span-self-block-end|span-all]||[self-inline-start|self-inline-end|span-self-inline-start|span-self-inline-end|span-all]|[start|center|end|span-start|span-end|span-all]{1,2}|[self-start|center|self-end|span-self-start|span-self-end|span-all]{1,2}]",syntax:"'*'|<syntax-component> [<syntax-combinator> <syntax-component>]*|<syntax-string>","syntax-component":"<syntax-single-component> <syntax-multiplier>?|'<' transform-list '>'","syntax-single-component":"'<' <syntax-type-name> '>'|<ident>","syntax-type-name":"angle|color|custom-ident|image|integer|length|length-percentage|number|percentage|resolution|string|time|url|transform-function","syntax-combinator":"'|'","syntax-multiplier":"'#'|'+'","syntax-string":"<string>"},properties:{"--*":"<declaration-value>","-ms-accelerator":"false|true","-ms-block-progression":"tb|rl|bt|lr","-ms-content-zoom-chaining":"none|chained","-ms-content-zoom-limit":"<'-ms-content-zoom-limit-min'> <'-ms-content-zoom-limit-max'>","-ms-content-zoom-limit-max":"<percentage>","-ms-content-zoom-limit-min":"<percentage>","-ms-content-zoom-snap":"<'-ms-content-zoom-snap-type'>||<'-ms-content-zoom-snap-points'>","-ms-content-zoom-snap-points":"snapInterval( <percentage> , <percentage> )|snapList( <percentage># )","-ms-content-zoom-snap-type":"none|proximity|mandatory","-ms-content-zooming":"none|zoom","-ms-filter":"<string>","-ms-flow-from":"[none|<custom-ident>]#","-ms-flow-into":"[none|<custom-ident>]#","-ms-grid-columns":"none|<track-list>|<auto-track-list>","-ms-grid-rows":"none|<track-list>|<auto-track-list>","-ms-high-contrast-adjust":"auto|none","-ms-hyphenate-limit-chars":"auto|<integer>{1,3}","-ms-hyphenate-limit-lines":"no-limit|<integer>","-ms-hyphenate-limit-zone":"<percentage>|<length>","-ms-ime-align":"auto|after","-ms-overflow-style":"auto|none|scrollbar|-ms-autohiding-scrollbar","-ms-scroll-chaining":"chained|none","-ms-scroll-limit":"<'-ms-scroll-limit-x-min'> <'-ms-scroll-limit-y-min'> <'-ms-scroll-limit-x-max'> <'-ms-scroll-limit-y-max'>","-ms-scroll-limit-x-max":"auto|<length>","-ms-scroll-limit-x-min":"<length>","-ms-scroll-limit-y-max":"auto|<length>","-ms-scroll-limit-y-min":"<length>","-ms-scroll-rails":"none|railed","-ms-scroll-snap-points-x":"snapInterval( <length-percentage> , <length-percentage> )|snapList( <length-percentage># )","-ms-scroll-snap-points-y":"snapInterval( <length-percentage> , <length-percentage> )|snapList( <length-percentage># )","-ms-scroll-snap-type":"none|proximity|mandatory","-ms-scroll-snap-x":"<'-ms-scroll-snap-type'> <'-ms-scroll-snap-points-x'>","-ms-scroll-snap-y":"<'-ms-scroll-snap-type'> <'-ms-scroll-snap-points-y'>","-ms-scroll-translation":"none|vertical-to-horizontal","-ms-scrollbar-3dlight-color":"<color>","-ms-scrollbar-arrow-color":"<color>","-ms-scrollbar-base-color":"<color>","-ms-scrollbar-darkshadow-color":"<color>","-ms-scrollbar-face-color":"<color>","-ms-scrollbar-highlight-color":"<color>","-ms-scrollbar-shadow-color":"<color>","-ms-scrollbar-track-color":"<color>","-ms-text-autospace":"none|ideograph-alpha|ideograph-numeric|ideograph-parenthesis|ideograph-space","-ms-touch-select":"grippers|none","-ms-user-select":"none|element|text","-ms-wrap-flow":"auto|both|start|end|maximum|clear","-ms-wrap-margin":"<length>","-ms-wrap-through":"wrap|none","-moz-appearance":"none|button|button-arrow-down|button-arrow-next|button-arrow-previous|button-arrow-up|button-bevel|button-focus|caret|checkbox|checkbox-container|checkbox-label|checkmenuitem|dualbutton|groupbox|listbox|listitem|menuarrow|menubar|menucheckbox|menuimage|menuitem|menuitemtext|menulist|menulist-button|menulist-text|menulist-textfield|menupopup|menuradio|menuseparator|meterbar|meterchunk|progressbar|progressbar-vertical|progresschunk|progresschunk-vertical|radio|radio-container|radio-label|radiomenuitem|range|range-thumb|resizer|resizerpanel|scale-horizontal|scalethumbend|scalethumb-horizontal|scalethumbstart|scalethumbtick|scalethumb-vertical|scale-vertical|scrollbarbutton-down|scrollbarbutton-left|scrollbarbutton-right|scrollbarbutton-up|scrollbarthumb-horizontal|scrollbarthumb-vertical|scrollbartrack-horizontal|scrollbartrack-vertical|searchfield|separator|sheet|spinner|spinner-downbutton|spinner-textfield|spinner-upbutton|splitter|statusbar|statusbarpanel|tab|tabpanel|tabpanels|tab-scroll-arrow-back|tab-scroll-arrow-forward|textfield|textfield-multiline|toolbar|toolbarbutton|toolbarbutton-dropdown|toolbargripper|toolbox|tooltip|treeheader|treeheadercell|treeheadersortarrow|treeitem|treeline|treetwisty|treetwistyopen|treeview|-moz-mac-unified-toolbar|-moz-win-borderless-glass|-moz-win-browsertabbar-toolbox|-moz-win-communicationstext|-moz-win-communications-toolbox|-moz-win-exclude-glass|-moz-win-glass|-moz-win-mediatext|-moz-win-media-toolbox|-moz-window-button-box|-moz-window-button-box-maximized|-moz-window-button-close|-moz-window-button-maximize|-moz-window-button-minimize|-moz-window-button-restore|-moz-window-frame-bottom|-moz-window-frame-left|-moz-window-frame-right|-moz-window-titlebar|-moz-window-titlebar-maximized","-moz-binding":"<url>|none","-moz-border-bottom-colors":"<color>+|none","-moz-border-left-colors":"<color>+|none","-moz-border-right-colors":"<color>+|none","-moz-border-top-colors":"<color>+|none","-moz-context-properties":"none|[fill|fill-opacity|stroke|stroke-opacity]#","-moz-float-edge":"border-box|content-box|margin-box|padding-box","-moz-force-broken-image-icon":"0|1","-moz-orient":"inline|block|horizontal|vertical","-moz-outline-radius":"<outline-radius>{1,4} [/ <outline-radius>{1,4}]?","-moz-outline-radius-bottomleft":"<outline-radius>","-moz-outline-radius-bottomright":"<outline-radius>","-moz-outline-radius-topleft":"<outline-radius>","-moz-outline-radius-topright":"<outline-radius>","-moz-stack-sizing":"ignore|stretch-to-fit","-moz-text-blink":"none|blink","-moz-user-focus":"ignore|normal|select-after|select-before|select-menu|select-same|select-all|none","-moz-user-input":"auto|none|enabled|disabled","-moz-user-modify":"read-only|read-write|write-only","-moz-window-dragging":"drag|no-drag","-moz-window-shadow":"default|menu|tooltip|sheet|none","-webkit-appearance":"none|button|button-bevel|caps-lock-indicator|caret|checkbox|default-button|inner-spin-button|listbox|listitem|media-controls-background|media-controls-fullscreen-background|media-current-time-display|media-enter-fullscreen-button|media-exit-fullscreen-button|media-fullscreen-button|media-mute-button|media-overlay-play-button|media-play-button|media-seek-back-button|media-seek-forward-button|media-slider|media-sliderthumb|media-time-remaining-display|media-toggle-closed-captions-button|media-volume-slider|media-volume-slider-container|media-volume-sliderthumb|menulist|menulist-button|menulist-text|menulist-textfield|meter|progress-bar|progress-bar-value|push-button|radio|scrollbarbutton-down|scrollbarbutton-left|scrollbarbutton-right|scrollbarbutton-up|scrollbargripper-horizontal|scrollbargripper-vertical|scrollbarthumb-horizontal|scrollbarthumb-vertical|scrollbartrack-horizontal|scrollbartrack-vertical|searchfield|searchfield-cancel-button|searchfield-decoration|searchfield-results-button|searchfield-results-decoration|slider-horizontal|slider-vertical|sliderthumb-horizontal|sliderthumb-vertical|square-button|textarea|textfield|-apple-pay-button","-webkit-border-before":"<'border-width'>||<'border-style'>||<color>","-webkit-border-before-color":"<color>","-webkit-border-before-style":"<'border-style'>","-webkit-border-before-width":"<'border-width'>","-webkit-box-reflect":"[above|below|right|left]? <length>? <image>?","-webkit-line-clamp":"none|<integer>","-webkit-mask":"[<mask-reference>||<position> [/ <bg-size>]?||<repeat-style>||[<visual-box>|border|padding|content|text]||[<visual-box>|border|padding|content]]#","-webkit-mask-attachment":"<attachment>#","-webkit-mask-clip":"[<coord-box>|no-clip|border|padding|content|text]#","-webkit-mask-composite":"<composite-style>#","-webkit-mask-image":"<mask-reference>#","-webkit-mask-origin":"[<coord-box>|border|padding|content]#","-webkit-mask-position":"<position>#","-webkit-mask-position-x":"[<length-percentage>|left|center|right]#","-webkit-mask-position-y":"[<length-percentage>|top|center|bottom]#","-webkit-mask-repeat":"<repeat-style>#","-webkit-mask-repeat-x":"repeat|no-repeat|space|round","-webkit-mask-repeat-y":"repeat|no-repeat|space|round","-webkit-mask-size":"<bg-size>#","-webkit-overflow-scrolling":"auto|touch","-webkit-tap-highlight-color":"<color>","-webkit-text-fill-color":"<color>","-webkit-text-stroke":"<length>||<color>","-webkit-text-stroke-color":"<color>","-webkit-text-stroke-width":"<length>","-webkit-touch-callout":"default|none","-webkit-user-modify":"read-only|read-write|read-write-plaintext-only","-webkit-user-select":"auto|none|text|all","accent-color":"auto|<color>","align-content":"normal|<baseline-position>|<content-distribution>|<overflow-position>? <content-position>","align-items":"normal|stretch|<baseline-position>|[<overflow-position>? <self-position>]|anchor-center","align-self":"auto|normal|stretch|<baseline-position>|<overflow-position>? <self-position>|anchor-center","align-tracks":"[normal|<baseline-position>|<content-distribution>|<overflow-position>? <content-position>]#","alignment-baseline":"auto|baseline|before-edge|text-before-edge|middle|central|after-edge|text-after-edge|ideographic|alphabetic|hanging|mathematical",all:"initial|inherit|unset|revert|revert-layer","anchor-name":"none|<dashed-ident>#","anchor-scope":"none|all|<dashed-ident>#",animation:"<single-animation>#","animation-composition":"<single-animation-composition>#","animation-delay":"<time>#","animation-direction":"<single-animation-direction>#","animation-duration":"[auto|<time [0s,\u221E]>]#","animation-fill-mode":"<single-animation-fill-mode>#","animation-iteration-count":"<single-animation-iteration-count>#","animation-name":"[none|<keyframes-name>]#","animation-play-state":"<single-animation-play-state>#","animation-range":"[<'animation-range-start'> <'animation-range-end'>?]#","animation-range-end":"[normal|<length-percentage>|<timeline-range-name> <length-percentage>?]#","animation-range-start":"[normal|<length-percentage>|<timeline-range-name> <length-percentage>?]#","animation-timeline":"<single-animation-timeline>#","animation-timing-function":"<easing-function>#","animation-trigger":"[none|[<dashed-ident> <animation-action>+]+]#",appearance:"none|auto|<compat-auto>|<compat-special>","aspect-ratio":"auto||<ratio>","backdrop-filter":"none|<filter-value-list>","backface-visibility":"visible|hidden",background:"<bg-layer>#? , <final-bg-layer>","background-attachment":"<attachment>#","background-blend-mode":"<blend-mode>#","background-clip":"<bg-clip>#","background-color":"<color>","background-image":"<bg-image>#","background-origin":"<visual-box>#","background-position":"<bg-position>#","background-position-x":"[center|[[left|right|x-start|x-end]? <length-percentage>?]!]#","background-position-y":"[center|[[top|bottom|y-start|y-end]? <length-percentage>?]!]#","background-repeat":"<repeat-style>#","background-size":"<bg-size>#","baseline-shift":"baseline|sub|super|<svg-length>","baseline-source":"auto|first|last","block-size":"<'width'>",border:"<line-width>||<line-style>||<color>","border-block":"<'border-block-start'>","border-block-color":"<'border-top-color'>{1,2}","border-block-end":"<'border-top-width'>||<'border-top-style'>||<color>","border-block-end-color":"<'border-top-color'>","border-block-end-style":"<'border-top-style'>","border-block-end-width":"<'border-top-width'>","border-block-start":"<'border-top-width'>||<'border-top-style'>||<color>","border-block-start-color":"<'border-top-color'>","border-block-start-style":"<'border-top-style'>","border-block-start-width":"<'border-top-width'>","border-block-style":"<'border-top-style'>{1,2}","border-block-width":"<'border-top-width'>{1,2}","border-bottom":"<line-width>||<line-style>||<color>","border-bottom-color":"<'border-top-color'>","border-bottom-left-radius":"<length-percentage [0,\u221E]>{1,2}","border-bottom-right-radius":"<length-percentage [0,\u221E]>{1,2}","border-bottom-style":"<line-style>","border-bottom-width":"<line-width>","border-collapse":"separate|collapse","border-color":"<color>{1,4}","border-end-end-radius":"<'border-top-left-radius'>","border-end-start-radius":"<'border-top-left-radius'>","border-image":"<'border-image-source'>||<'border-image-slice'> [/ <'border-image-width'>|/ <'border-image-width'>? / <'border-image-outset'>]?||<'border-image-repeat'>","border-image-outset":"[<length [0,\u221E]>|<number [0,\u221E]>]{1,4}","border-image-repeat":"[stretch|repeat|round|space]{1,2}","border-image-slice":"[<number [0,\u221E]>|<percentage [0,\u221E]>]{1,4}&&fill?","border-image-source":"none|<image>","border-image-width":"[<length-percentage [0,\u221E]>|<number [0,\u221E]>|auto]{1,4}","border-inline":"<'border-block-start'>","border-inline-color":"<'border-top-color'>{1,2}","border-inline-end":"<'border-top-width'>||<'border-top-style'>||<color>","border-inline-end-color":"<'border-top-color'>","border-inline-end-style":"<'border-top-style'>","border-inline-end-width":"<'border-top-width'>","border-inline-start":"<'border-top-width'>||<'border-top-style'>||<color>","border-inline-start-color":"<'border-top-color'>","border-inline-start-style":"<'border-top-style'>","border-inline-start-width":"<'border-top-width'>","border-inline-style":"<'border-top-style'>{1,2}","border-inline-width":"<'border-top-width'>{1,2}","border-left":"<line-width>||<line-style>||<color>","border-left-color":"<color>","border-left-style":"<line-style>","border-left-width":"<line-width>","border-radius":"<length-percentage [0,\u221E]>{1,4} [/ <length-percentage [0,\u221E]>{1,4}]?","border-right":"<line-width>||<line-style>||<color>","border-right-color":"<color>","border-right-style":"<line-style>","border-right-width":"<line-width>","border-spacing":"<length>{1,2}","border-start-end-radius":"<'border-top-left-radius'>","border-start-start-radius":"<'border-top-left-radius'>","border-style":"<line-style>{1,4}","border-top":"<line-width>||<line-style>||<color>","border-top-color":"<color>","border-top-left-radius":"<length-percentage [0,\u221E]>{1,2}","border-top-right-radius":"<length-percentage [0,\u221E]>{1,2}","border-top-style":"<line-style>","border-top-width":"<line-width>","border-width":"<line-width>{1,4}",bottom:"auto|<length-percentage>|<anchor()>|<anchor-size()>","box-align":"start|center|end|baseline|stretch","box-decoration-break":"slice|clone","box-direction":"normal|reverse|inherit","box-flex":"<number>","box-flex-group":"<integer>","box-lines":"single|multiple","box-ordinal-group":"<integer>","box-orient":"horizontal|vertical|inline-axis|block-axis|inherit","box-pack":"start|center|end|justify","box-shadow":"none|<shadow>#","box-sizing":"content-box|border-box","break-after":"auto|avoid|always|all|avoid-page|page|left|right|recto|verso|avoid-column|column|avoid-region|region","break-before":"auto|avoid|always|all|avoid-page|page|left|right|recto|verso|avoid-column|column|avoid-region|region","break-inside":"auto|avoid|avoid-page|avoid-column|avoid-region","caption-side":"top|bottom",caret:"<'caret-color'>||<'caret-animation'>||<'caret-shape'>","caret-animation":"auto|manual","caret-color":"auto|<color>","caret-shape":"auto|bar|block|underscore",clear:"none|left|right|both|inline-start|inline-end",clip:"<shape>|auto","clip-path":"<clip-source>|[<basic-shape>||<geometry-box>]|none","clip-rule":"nonzero|evenodd",color:"<color>","color-interpolation-filters":"auto|sRGB|linearRGB","color-scheme":"normal|[light|dark|<custom-ident>]+&&only?","column-count":"<integer>|auto","column-fill":"auto|balance","column-gap":"normal|<length-percentage>","column-height":"auto|<length [0,\u221E]>","column-rule":"<'column-rule-width'>||<'column-rule-style'>||<'column-rule-color'>","column-rule-color":"<color>","column-rule-style":"<'border-style'>","column-rule-width":"<'border-width'>","column-span":"none|all","column-width":"auto|<length [0,\u221E]>","column-wrap":"auto|nowrap|wrap",columns:"[<'column-width'>||<'column-count'>] [/ <'column-height'>]?",contain:"none|strict|content|[[size||inline-size]||layout||style||paint]","contain-intrinsic-block-size":"auto? [none|<length>]","contain-intrinsic-height":"auto? [none|<length>]","contain-intrinsic-inline-size":"auto? [none|<length>]","contain-intrinsic-size":"[auto? [none|<length>]]{1,2}","contain-intrinsic-width":"auto? [none|<length>]",container:"<'container-name'> [/ <'container-type'>]?","container-name":"none|<custom-ident>+","container-type":"normal||[size|inline-size]",content:"normal|none|[<content-replacement>|<content-list>] [/ [<string>|<counter>|<attr()>]+]?","content-visibility":"visible|auto|hidden","corner-block-end-shape":"<corner-shape-value>{1,2}","corner-block-start-shape":"<corner-shape-value>{1,2}","corner-bottom-shape":"<corner-shape-value>{1,2}","corner-bottom-left-shape":"<corner-shape-value>","corner-bottom-right-shape":"<corner-shape-value>","corner-end-end-shape":"<corner-shape-value>","corner-end-start-shape":"<corner-shape-value>","corner-inline-end-shape":"<corner-shape-value>{1,2}","corner-inline-start-shape":"<corner-shape-value>{1,2}","corner-left-shape":"<corner-shape-value>{1,2}","corner-right-shape":"<corner-shape-value>{1,2}","corner-shape":"<corner-shape-value>{1,4}","corner-start-start-shape":"<corner-shape-value>","corner-start-end-shape":"<corner-shape-value>","corner-top-shape":"<corner-shape-value>{1,2}","corner-top-left-shape":"<corner-shape-value>","corner-top-right-shape":"<corner-shape-value>","counter-increment":"[<counter-name> <integer>?]+|none","counter-reset":"[<counter-name> <integer>?|<reversed-counter-name> <integer>?]+|none","counter-set":"[<counter-name> <integer>?]+|none",cursor:"[[<url> [<x> <y>]? ,]* [auto|default|none|context-menu|help|pointer|progress|wait|cell|crosshair|text|vertical-text|alias|copy|move|no-drop|not-allowed|e-resize|n-resize|ne-resize|nw-resize|s-resize|se-resize|sw-resize|w-resize|ew-resize|ns-resize|nesw-resize|nwse-resize|col-resize|row-resize|all-scroll|zoom-in|zoom-out|grab|grabbing|hand|-webkit-grab|-webkit-grabbing|-webkit-zoom-in|-webkit-zoom-out|-moz-grab|-moz-grabbing|-moz-zoom-in|-moz-zoom-out]]",cx:"<length>|<percentage>",cy:"<length>|<percentage>",d:"none|path( <string> )",direction:"ltr|rtl",display:"[<display-outside>||<display-inside>]|<display-listitem>|<display-internal>|<display-box>|<display-legacy>|<-non-standard-display>","dominant-baseline":"auto|use-script|no-change|reset-size|ideographic|alphabetic|hanging|mathematical|central|middle|text-after-edge|text-before-edge","dynamic-range-limit":"standard|no-limit|constrained|<dynamic-range-limit-mix()>","empty-cells":"show|hide","field-sizing":"content|fixed",fill:"<paint>","fill-opacity":"<number-zero-one>|<percentage>","fill-rule":"nonzero|evenodd",filter:"none|<filter-value-list>|<-ms-filter-function-list>",flex:"none|[<'flex-grow'> <'flex-shrink'>?||<'flex-basis'>]","flex-basis":"content|<'width'>","flex-direction":"row|row-reverse|column|column-reverse","flex-flow":"<'flex-direction'>||<'flex-wrap'>","flex-grow":"<number>","flex-shrink":"<number>","flex-wrap":"nowrap|wrap|wrap-reverse",float:"left|right|none|inline-start|inline-end","flood-color":"<color>","flood-opacity":"<'opacity'>",font:"[[<'font-style'>||<font-variant-css2>||<'font-weight'>||<font-width-css3>]? <'font-size'> [/ <'line-height'>]? <'font-family'>#]|<system-family-name>|<-non-standard-font>","font-family":"[<family-name>|<generic-family>]#","font-feature-settings":"normal|<feature-tag-value>#","font-kerning":"auto|normal|none","font-language-override":"normal|<string>","font-optical-sizing":"auto|none","font-palette":"normal|light|dark|<palette-identifier>|<palette-mix()>","font-size":"<absolute-size>|<relative-size>|<length-percentage [0,\u221E]>|math","font-size-adjust":"none|[ex-height|cap-height|ch-width|ic-width|ic-height]? [from-font|<number>]","font-smooth":"auto|never|always|<absolute-size>|<length>","font-stretch":"<font-stretch-absolute>","font-style":"normal|italic|oblique <angle>?","font-synthesis":"none|[weight||style||small-caps||position]","font-synthesis-position":"auto|none","font-synthesis-small-caps":"auto|none","font-synthesis-style":"auto|none","font-synthesis-weight":"auto|none","font-variant":"normal|none|[<common-lig-values>||<discretionary-lig-values>||<historical-lig-values>||<contextual-alt-values>||stylistic( <feature-value-name> )||historical-forms||styleset( <feature-value-name># )||character-variant( <feature-value-name># )||swash( <feature-value-name> )||ornaments( <feature-value-name> )||annotation( <feature-value-name> )||[small-caps|all-small-caps|petite-caps|all-petite-caps|unicase|titling-caps]||<numeric-figure-values>||<numeric-spacing-values>||<numeric-fraction-values>||ordinal||slashed-zero||<east-asian-variant-values>||<east-asian-width-values>||ruby]","font-variant-alternates":"normal|[stylistic( <feature-value-name> )||historical-forms||styleset( <feature-value-name># )||character-variant( <feature-value-name># )||swash( <feature-value-name> )||ornaments( <feature-value-name> )||annotation( <feature-value-name> )]","font-variant-caps":"normal|small-caps|all-small-caps|petite-caps|all-petite-caps|unicase|titling-caps","font-variant-east-asian":"normal|[<east-asian-variant-values>||<east-asian-width-values>||ruby]","font-variant-emoji":"normal|text|emoji|unicode","font-variant-ligatures":"normal|none|[<common-lig-values>||<discretionary-lig-values>||<historical-lig-values>||<contextual-alt-values>]","font-variant-numeric":"normal|[<numeric-figure-values>||<numeric-spacing-values>||<numeric-fraction-values>||ordinal||slashed-zero]","font-variant-position":"normal|sub|super","font-variation-settings":"normal|[<string> <number>]#","font-weight":"<font-weight-absolute>|bolder|lighter","font-width":"normal|<percentage [0,\u221E]>|ultra-condensed|extra-condensed|condensed|semi-condensed|semi-expanded|expanded|extra-expanded|ultra-expanded","forced-color-adjust":"auto|none|preserve-parent-color",gap:"<'row-gap'> <'column-gap'>?",grid:"<'grid-template'>|<'grid-template-rows'> / [auto-flow&&dense?] <'grid-auto-columns'>?|[auto-flow&&dense?] <'grid-auto-rows'>? / <'grid-template-columns'>","grid-area":"<grid-line> [/ <grid-line>]{0,3}","grid-auto-columns":"<track-size>+","grid-auto-flow":"[row|column]||dense","grid-auto-rows":"<track-size>+","grid-column":"<grid-line> [/ <grid-line>]?","grid-column-end":"<grid-line>","grid-column-gap":"<length-percentage>","grid-column-start":"<grid-line>","grid-gap":"<'grid-row-gap'> <'grid-column-gap'>?","grid-row":"<grid-line> [/ <grid-line>]?","grid-row-end":"<grid-line>","grid-row-gap":"<length-percentage>","grid-row-start":"<grid-line>","grid-template":"none|[<'grid-template-rows'> / <'grid-template-columns'>]|[<line-names>? <string> <track-size>? <line-names>?]+ [/ <explicit-track-list>]?","grid-template-areas":"none|<string>+","grid-template-columns":"none|<track-list>|<auto-track-list>|subgrid <line-name-list>?","grid-template-rows":"none|<track-list>|<auto-track-list>|subgrid <line-name-list>?","hanging-punctuation":"none|[first||[force-end|allow-end]||last]",height:"auto|<length-percentage [0,\u221E]>|min-content|max-content|fit-content|fit-content( <length-percentage [0,\u221E]> )|<calc-size()>|<anchor-size()>|stretch|<-non-standard-size>","hyphenate-character":"auto|<string>","hyphenate-limit-chars":"[auto|<integer>]{1,3}",hyphens:"none|manual|auto","image-orientation":"from-image|<angle>|[<angle>? flip]","image-rendering":"auto|crisp-edges|pixelated|smooth|optimizeSpeed|optimizeQuality|<-non-standard-image-rendering>","image-resolution":"[from-image||<resolution>]&&snap?","ime-mode":"auto|normal|active|inactive|disabled","initial-letter":"normal|[<number> <integer>?]","initial-letter-align":"[auto|alphabetic|hanging|ideographic]","inline-size":"<'width'>",inset:"<'top'>{1,4}","inset-block":"<'top'>{1,2}","inset-block-end":"<'top'>","inset-block-start":"<'top'>","inset-inline":"<'top'>{1,2}","inset-inline-end":"<'top'>","inset-inline-start":"<'top'>","interpolate-size":"numeric-only|allow-keywords",isolation:"auto|isolate",interactivity:"auto|inert","interest-delay":"<'interest-delay-start'>{1,2}","interest-delay-end":"normal|<time>","interest-delay-start":"normal|<time>","justify-content":"normal|<content-distribution>|<overflow-position>? [<content-position>|left|right]","justify-items":"normal|stretch|<baseline-position>|<overflow-position>? [<self-position>|left|right]|legacy|legacy&&[left|right|center]|anchor-center","justify-self":"auto|normal|stretch|<baseline-position>|<overflow-position>? [<self-position>|left|right]|anchor-center","justify-tracks":"[normal|<content-distribution>|<overflow-position>? [<content-position>|left|right]]#",left:"auto|<length-percentage>|<anchor()>|<anchor-size()>","letter-spacing":"normal|<length-percentage>","lighting-color":"<color>","line-break":"auto|loose|normal|strict|anywhere","line-clamp":"none|<integer>","line-height":"normal|<number>|<length>|<percentage>","line-height-step":"<length>","list-style":"<'list-style-type'>||<'list-style-position'>||<'list-style-image'>","list-style-image":"<image>|none","list-style-position":"inside|outside","list-style-type":"<counter-style>|<string>|none",margin:"<'margin-top'>{1,4}","margin-block":"<'margin-top'>{1,2}","margin-block-end":"<'margin-top'>","margin-block-start":"<'margin-top'>","margin-bottom":"<length-percentage>|auto|<anchor-size()>","margin-inline":"<'margin-top'>{1,2}","margin-inline-end":"<'margin-top'>","margin-inline-start":"<'margin-top'>","margin-left":"<length-percentage>|auto|<anchor-size()>","margin-right":"<length-percentage>|auto|<anchor-size()>","margin-top":"<length-percentage>|auto|<anchor-size()>","margin-trim":"none|in-flow|all",marker:"none|<url>","marker-end":"none|<url>","marker-mid":"none|<url>","marker-start":"none|<url>",mask:"<mask-layer>#","mask-border":"<'mask-border-source'>||<'mask-border-slice'> [/ <'mask-border-width'>? [/ <'mask-border-outset'>]?]?||<'mask-border-repeat'>||<'mask-border-mode'>","mask-border-mode":"luminance|alpha","mask-border-outset":"[<length>|<number>]{1,4}","mask-border-repeat":"[stretch|repeat|round|space]{1,2}","mask-border-slice":"<number-percentage>{1,4} fill?","mask-border-source":"none|<image>","mask-border-width":"[<length-percentage>|<number>|auto]{1,4}","mask-clip":"[<coord-box>|no-clip]#","mask-composite":"<compositing-operator>#","mask-image":"<mask-reference>#","mask-mode":"<masking-mode>#","mask-origin":"<coord-box>#","mask-position":"<position>#","mask-repeat":"<repeat-style>#","mask-size":"<bg-size>#","mask-type":"luminance|alpha","masonry-auto-flow":"[pack|next]||[definite-first|ordered]","math-depth":"auto-add|add( <integer> )|<integer>","math-shift":"normal|compact","math-style":"normal|compact","max-block-size":"<'max-width'>","max-height":"none|<length-percentage [0,\u221E]>|min-content|max-content|fit-content|fit-content( <length-percentage [0,\u221E]> )|<calc-size()>|<anchor-size()>|stretch|<-non-standard-size>","max-inline-size":"<'max-width'>","max-lines":"none|<integer>","max-width":"none|<length-percentage [0,\u221E]>|min-content|max-content|fit-content|fit-content( <length-percentage [0,\u221E]> )|<calc-size()>|<anchor-size()>|stretch|<-non-standard-size>","min-block-size":"<'min-width'>","min-height":"auto|<length-percentage [0,\u221E]>|min-content|max-content|fit-content|fit-content( <length-percentage [0,\u221E]> )|<calc-size()>|<anchor-size()>|stretch|<-non-standard-size>","min-inline-size":"<'min-width'>","min-width":"auto|<length-percentage [0,\u221E]>|min-content|max-content|fit-content|fit-content( <length-percentage [0,\u221E]> )|<calc-size()>|<anchor-size()>|stretch|<-non-standard-size>","mix-blend-mode":"<blend-mode>|plus-darker|plus-lighter","object-fit":"fill|contain|cover|none|scale-down","object-position":"<position>","object-view-box":"none|<basic-shape-rect>",offset:"[<'offset-position'>? [<'offset-path'> [<'offset-distance'>||<'offset-rotate'>]?]?]! [/ <'offset-anchor'>]?","offset-anchor":"auto|<position>","offset-distance":"<length-percentage>","offset-path":"none|<offset-path>||<coord-box>","offset-position":"normal|auto|<position>","offset-rotate":"[auto|reverse]||<angle>",opacity:"<opacity-value>",order:"<integer>",orphans:"<integer>",outline:"<'outline-width'>||<'outline-style'>||<'outline-color'>","outline-color":"auto|<color>","outline-offset":"<length>","outline-style":"auto|<outline-line-style>","outline-width":"<line-width>",overflow:"[visible|hidden|clip|scroll|auto]{1,2}|<-non-standard-overflow>","overflow-anchor":"auto|none","overflow-block":"visible|hidden|clip|scroll|auto|<-non-standard-overflow>","overflow-clip-box":"padding-box|content-box","overflow-clip-margin":"<visual-box>||<length [0,\u221E]>","overflow-inline":"visible|hidden|clip|scroll|auto|<-non-standard-overflow>","overflow-wrap":"normal|break-word|anywhere","overflow-x":"visible|hidden|clip|scroll|auto|<-non-standard-overflow>","overflow-y":"visible|hidden|clip|scroll|auto|<-non-standard-overflow>",overlay:"none|auto","overscroll-behavior":"[contain|none|auto]{1,2}","overscroll-behavior-block":"contain|none|auto","overscroll-behavior-inline":"contain|none|auto","overscroll-behavior-x":"contain|none|auto","overscroll-behavior-y":"contain|none|auto",padding:"<'padding-top'>{1,4}","padding-block":"<'padding-top'>{1,2}","padding-block-end":"<'padding-top'>","padding-block-start":"<'padding-top'>","padding-bottom":"<length-percentage [0,\u221E]>","padding-inline":"<'padding-top'>{1,2}","padding-inline-end":"<'padding-top'>","padding-inline-start":"<'padding-top'>","padding-left":"<length-percentage [0,\u221E]>","padding-right":"<length-percentage [0,\u221E]>","padding-top":"<length-percentage [0,\u221E]>",page:"auto|<custom-ident>","page-break-after":"auto|always|avoid|left|right|recto|verso","page-break-before":"auto|always|avoid|left|right|recto|verso","page-break-inside":"auto|avoid","paint-order":"normal|[fill||stroke||markers]",perspective:"none|<length>","perspective-origin":"<position>","place-content":"<'align-content'> <'justify-content'>?","place-items":"<'align-items'> <'justify-items'>?","place-self":"<'align-self'> <'justify-self'>?","pointer-events":"auto|none|visiblePainted|visibleFill|visibleStroke|visible|painted|fill|stroke|all|inherit",position:"static|relative|absolute|sticky|fixed|-webkit-sticky","position-anchor":"auto|none|<anchor-name>","position-area":"none|<position-area>","position-try":"<'position-try-order'>? <'position-try-fallbacks'>","position-try-fallbacks":"none|[[<dashed-ident>||<try-tactic>]|<'position-area'>]#","position-try-order":"normal|<try-size>","position-visibility":"always|[anchors-valid||anchors-visible||no-overflow]","print-color-adjust":"economy|exact",quotes:"none|auto|[<string> <string>]+",r:"<length>|<percentage>","reading-flow":"normal|source-order|flex-visual|flex-flow|grid-rows|grid-columns|grid-order","reading-order":"<integer>",resize:"none|both|horizontal|vertical|block|inline",right:"auto|<length-percentage>|<anchor()>|<anchor-size()>",rotate:"none|<angle>|[x|y|z|<number>{3}]&&<angle>","row-gap":"normal|<length-percentage>","ruby-align":"start|center|space-between|space-around","ruby-merge":"separate|collapse|auto","ruby-overhang":"auto|none","ruby-position":"[alternate||[over|under]]|inter-character",rx:"<length>|<percentage>",ry:"<length>|<percentage>",scale:"none|[<number>|<percentage>]{1,3}","scroll-behavior":"auto|smooth","scroll-initial-target":"none|nearest","scroll-margin":"<length>{1,4}","scroll-margin-block":"<length>{1,2}","scroll-margin-block-end":"<length>","scroll-margin-block-start":"<length>","scroll-margin-bottom":"<length>","scroll-margin-inline":"<length>{1,2}","scroll-margin-inline-end":"<length>","scroll-margin-inline-start":"<length>","scroll-margin-left":"<length>","scroll-margin-right":"<length>","scroll-margin-top":"<length>","scroll-marker-group":"none|before|after","scroll-padding":"[auto|<length-percentage>]{1,4}","scroll-padding-block":"[auto|<length-percentage>]{1,2}","scroll-padding-block-end":"auto|<length-percentage>","scroll-padding-block-start":"auto|<length-percentage>","scroll-padding-bottom":"auto|<length-percentage>","scroll-padding-inline":"[auto|<length-percentage>]{1,2}","scroll-padding-inline-end":"auto|<length-percentage>","scroll-padding-inline-start":"auto|<length-percentage>","scroll-padding-left":"auto|<length-percentage>","scroll-padding-right":"auto|<length-percentage>","scroll-padding-top":"auto|<length-percentage>","scroll-snap-align":"[none|start|end|center]{1,2}","scroll-snap-coordinate":"none|<position>#","scroll-snap-destination":"<position>","scroll-snap-points-x":"none|repeat( <length-percentage> )","scroll-snap-points-y":"none|repeat( <length-percentage> )","scroll-snap-stop":"normal|always","scroll-snap-type":"none|[x|y|block|inline|both] [mandatory|proximity]?","scroll-snap-type-x":"none|mandatory|proximity","scroll-snap-type-y":"none|mandatory|proximity","scroll-target-group":"none|auto","scroll-timeline":"[<'scroll-timeline-name'> <'scroll-timeline-axis'>?]#","scroll-timeline-axis":"[block|inline|x|y]#","scroll-timeline-name":"[none|<dashed-ident>]#","scrollbar-color":"auto|<color>{2}","scrollbar-gutter":"auto|stable&&both-edges?","scrollbar-width":"auto|thin|none","shape-image-threshold":"<opacity-value>","shape-margin":"<length-percentage>","shape-outside":"none|[<shape-box>||<basic-shape>]|<image>","shape-rendering":"auto|optimizeSpeed|crispEdges|geometricPrecision","speak-as":"normal|spell-out||digits||[literal-punctuation|no-punctuation]","stop-color":"<'color'>","stop-opacity":"<'opacity'>",stroke:"<paint>","stroke-color":"<color>","stroke-dasharray":"none|[<svg-length>+]#","stroke-dashoffset":"<svg-length>","stroke-linecap":"butt|round|square","stroke-linejoin":"miter|round|bevel","stroke-miterlimit":"<number-one-or-greater>","stroke-opacity":"<'opacity'>","stroke-width":"<svg-length>","tab-size":"<integer>|<length>","table-layout":"auto|fixed","text-align":"start|end|left|right|center|justify|match-parent","text-align-last":"auto|start|end|left|right|center|justify","text-anchor":"start|middle|end","text-autospace":"normal|<autospace>|auto","text-box":"normal|<'text-box-trim'>||<'text-box-edge'>","text-box-edge":"auto|<text-edge>","text-box-trim":"none|trim-start|trim-end|trim-both","text-combine-upright":"none|all|[digits <integer>?]","text-decoration":"<'text-decoration-line'>||<'text-decoration-style'>||<'text-decoration-color'>||<'text-decoration-thickness'>","text-decoration-color":"<color>","text-decoration-inset":"<length>{1,2}|auto","text-decoration-line":"none|[underline||overline||line-through||blink]|spelling-error|grammar-error","text-decoration-skip":"none|[objects||[spaces|[leading-spaces||trailing-spaces]]||edges||box-decoration]","text-decoration-skip-ink":"auto|all|none","text-decoration-style":"solid|double|dotted|dashed|wavy","text-decoration-thickness":"auto|from-font|<length>|<percentage>","text-emphasis":"<'text-emphasis-style'>||<'text-emphasis-color'>","text-emphasis-color":"<color>","text-emphasis-position":"auto|[over|under]&&[right|left]?","text-emphasis-style":"none|[[filled|open]||[dot|circle|double-circle|triangle|sesame]]|<string>","text-indent":"<length-percentage>&&hanging?&&each-line?","text-justify":"auto|inter-character|inter-word|none","text-orientation":"mixed|upright|sideways","text-overflow":"[clip|ellipsis|<string>]{1,2}","text-rendering":"auto|optimizeSpeed|optimizeLegibility|geometricPrecision","text-shadow":"none|<shadow-t>#","text-size-adjust":"none|auto|<percentage>","text-spacing-trim":"space-all|normal|space-first|trim-start","text-transform":"none|[capitalize|uppercase|lowercase]||full-width||full-size-kana|math-auto","text-underline-offset":"auto|<length>|<percentage>","text-underline-position":"auto|from-font|[under||[left|right]]","text-wrap":"<'text-wrap-mode'>||<'text-wrap-style'>","text-wrap-mode":"wrap|nowrap","text-wrap-style":"auto|balance|stable|pretty","timeline-scope":"none|<dashed-ident>#","timeline-trigger":"none|[<'timeline-trigger-name'> <'timeline-trigger-source'> <'timeline-trigger-range'> ['/' <'timeline-trigger-exit-range'>]?]#","timeline-trigger-name":"none|<dashed-ident>#","timeline-trigger-exit-range":"[<'timeline-trigger-exit-range-start'> <'timeline-trigger-exit-range-end'>?]#","timeline-trigger-exit-range-end":"[auto|normal|<length-percentage>|<timeline-range-name> <length-percentage>?]#","timeline-trigger-exit-range-start":"[auto|normal|<length-percentage>|<timeline-range-name> <length-percentage>?]#","timeline-trigger-range":"[<'timeline-trigger-range-start'> <'timeline-trigger-range-end'>?]#","timeline-trigger-range-end":"[normal|<length-percentage>|<timeline-range-name> <length-percentage>?]#","timeline-trigger-range-start":"[normal|<length-percentage>|<timeline-range-name> <length-percentage>?]#","timeline-trigger-source":"<single-animation-timeline>#",top:"auto|<length-percentage>|<anchor()>|<anchor-size()>","touch-action":"auto|none|[[pan-x|pan-left|pan-right]||[pan-y|pan-up|pan-down]||pinch-zoom]|manipulation",transform:"none|<transform-list>","transform-box":"content-box|border-box|fill-box|stroke-box|view-box","transform-origin":"[<length-percentage>|left|center|right|top|bottom]|[[<length-percentage>|left|center|right]&&[<length-percentage>|top|center|bottom]] <length>?","transform-style":"flat|preserve-3d",transition:"<single-transition>#","transition-behavior":"<transition-behavior-value>#","transition-delay":"<time>#","transition-duration":"<time>#","transition-property":"none|<single-transition-property>#","transition-timing-function":"<easing-function>#",translate:"none|<length-percentage> [<length-percentage> <length>?]?","trigger-scope":"none|all|<dashed-ident>#","unicode-bidi":"normal|embed|isolate|bidi-override|isolate-override|plaintext|-moz-isolate|-moz-isolate-override|-moz-plaintext|-webkit-isolate|-webkit-isolate-override|-webkit-plaintext","user-select":"auto|text|none|all","vector-effect":"none|non-scaling-stroke|non-scaling-size|non-rotation|fixed-position","vertical-align":"baseline|sub|super|text-top|text-bottom|middle|top|bottom|<percentage>|<length>","view-timeline":"[<'view-timeline-name'> [<'view-timeline-axis'>||<'view-timeline-inset'>]?]#","view-timeline-axis":"[block|inline|x|y]#","view-timeline-inset":"[[auto|<length-percentage>]{1,2}]#","view-timeline-name":"[none|<dashed-ident>]#","view-transition-class":"none|<custom-ident>+","view-transition-name":"none|<custom-ident>|match-element",visibility:"visible|hidden|collapse","white-space":"normal|pre|pre-wrap|pre-line|<'white-space-collapse'>||<'text-wrap-mode'>","white-space-collapse":"collapse|preserve|preserve-breaks|preserve-spaces|break-spaces",widows:"<integer>",width:"auto|<length-percentage [0,\u221E]>|min-content|max-content|fit-content|fit-content( <length-percentage [0,\u221E]> )|<calc-size()>|<anchor-size()>|stretch|<-non-standard-size>","will-change":"auto|<animateable-feature>#","word-break":"normal|break-all|keep-all|break-word|auto-phrase","word-spacing":"normal|<length>","word-wrap":"normal|break-word","writing-mode":"horizontal-tb|vertical-rl|vertical-lr|sideways-rl|sideways-lr|<svg-writing-mode>",x:"<length>|<percentage>",y:"<length>|<percentage>","z-index":"auto|<integer>",zoom:"normal|reset|<number [0,\u221E]>||<percentage [0,\u221E]>","-moz-background-clip":"padding|border","-moz-border-radius-bottomleft":"<'border-bottom-left-radius'>","-moz-border-radius-bottomright":"<'border-bottom-right-radius'>","-moz-border-radius-topleft":"<'border-top-left-radius'>","-moz-border-radius-topright":"<'border-bottom-right-radius'>","-moz-control-character-visibility":"visible|hidden","-moz-osx-font-smoothing":"auto|grayscale","-moz-user-select":"none|text|all|-moz-none","-ms-flex-align":"start|end|center|baseline|stretch","-ms-flex-item-align":"auto|start|end|center|baseline|stretch","-ms-flex-line-pack":"start|end|center|justify|distribute|stretch","-ms-flex-negative":"<'flex-shrink'>","-ms-flex-pack":"start|end|center|justify|distribute","-ms-flex-order":"<integer>","-ms-flex-positive":"<'flex-grow'>","-ms-flex-preferred-size":"<'flex-basis'>","-ms-interpolation-mode":"nearest-neighbor|bicubic","-ms-grid-column-align":"start|end|center|stretch","-ms-grid-row-align":"start|end|center|stretch","-ms-hyphenate-limit-last":"none|always|column|page|spread","-webkit-background-clip":"[<visual-box>|border|padding|content|text]#","-webkit-column-break-after":"always|auto|avoid","-webkit-column-break-before":"always|auto|avoid","-webkit-column-break-inside":"always|auto|avoid","-webkit-font-smoothing":"auto|none|antialiased|subpixel-antialiased","-webkit-mask-box-image":"[<url>|<gradient>|none] [<length-percentage>{4} <-webkit-mask-box-repeat>{2}]?","-webkit-print-color-adjust":"economy|exact","-webkit-text-security":"none|circle|disc|square","-webkit-user-drag":"none|element|auto",behavior:"<url>+",cue:"<'cue-before'> <'cue-after'>?","cue-after":"<url> <decibel>?|none","cue-before":"<url> <decibel>?|none","glyph-orientation-horizontal":"<angle>","glyph-orientation-vertical":"<angle>",kerning:"auto|<svg-length>",pause:"<'pause-before'> <'pause-after'>?","pause-after":"<time>|none|x-weak|weak|medium|strong|x-strong","pause-before":"<time>|none|x-weak|weak|medium|strong|x-strong","position-try-options":"<'position-try-fallbacks'>",rest:"<'rest-before'> <'rest-after'>?","rest-after":"<time>|none|x-weak|weak|medium|strong|x-strong","rest-before":"<time>|none|x-weak|weak|medium|strong|x-strong",speak:"auto|never|always","voice-balance":"<number>|left|center|right|leftwards|rightwards","voice-duration":"auto|<time>","voice-family":"[[<family-name>|<generic-voice>] ,]* [<family-name>|<generic-voice>]|preserve","voice-pitch":"<frequency>&&absolute|[[x-low|low|medium|high|x-high]||[<frequency>|<semitones>|<percentage>]]","voice-range":"<frequency>&&absolute|[[x-low|low|medium|high|x-high]||[<frequency>|<semitones>|<percentage>]]","voice-rate":"[normal|x-slow|slow|medium|fast|x-fast]||<percentage>","voice-stress":"normal|strong|moderate|none|reduced","voice-volume":"silent|[[x-soft|soft|medium|loud|x-loud]||<decibel>]","white-space-trim":"none|discard-before||discard-after||discard-inner"},atrules:{charset:{prelude:"<string>",descriptors:null},"counter-style":{prelude:"<counter-style-name>",descriptors:{"additive-symbols":"[<integer [0,\u221E]>&&<symbol>]#",fallback:"<counter-style-name>",negative:"<symbol> <symbol>?",pad:"<integer [0,\u221E]>&&<symbol>",prefix:"<symbol>",range:"[[<integer>|infinite]{2}]#|auto","speak-as":"auto|bullets|numbers|words|spell-out|<counter-style-name>",suffix:"<symbol>",symbols:"<symbol>+",system:"cyclic|numeric|alphabetic|symbolic|additive|[fixed <integer>?]|[extends <counter-style-name>]"}},container:{prelude:"[<container-name>]? <container-condition>",descriptors:null},document:{prelude:"[<url>|url-prefix( <string> )|domain( <string> )|media-document( <string> )|regexp( <string> )]#",descriptors:null},"font-face":{prelude:null,descriptors:{"ascent-override":"normal|<percentage>","descent-override":"normal|<percentage>","font-display":"auto|block|swap|fallback|optional","font-family":"<family-name>","font-feature-settings":"normal|<feature-tag-value>#","font-stretch":"<font-stretch-absolute>{1,2}","font-style":"normal|italic|oblique <angle>{0,2}","font-variation-settings":"normal|[<string> <number>]#","font-weight":"<font-weight-absolute>{1,2}","line-gap-override":"normal|<percentage>","size-adjust":"<percentage>",src:"[<url> [format( <string># )]?|local( <family-name> )]#","unicode-range":"<urange>#"}},"font-feature-values":{prelude:"<family-name>#",descriptors:null},"font-palette-values":{prelude:"<dashed-ident>",descriptors:{"base-palette":"light|dark|<integer [0,\u221E]>","font-family":"<family-name>#","override-colors":"[<integer [0,\u221E]> <color>]#"}},import:{prelude:"[<string>|<url>] [layer|layer( <layer-name> )]? [supports( [<supports-condition>|<declaration>] )]? <media-query-list>?",descriptors:null},keyframes:{prelude:"<keyframes-name>",descriptors:null},layer:{prelude:"[<layer-name>#|<layer-name>?]",descriptors:null},media:{prelude:"<media-query-list>",descriptors:null},namespace:{prelude:"<namespace-prefix>? [<string>|<url>]",descriptors:null},page:{prelude:"<page-selector-list>",descriptors:{bleed:"auto|<length>",marks:"none|[crop||cross]","page-orientation":"upright|rotate-left|rotate-right",size:"<length [0,\u221E]>{1,2}|auto|[<page-size>||[portrait|landscape]]"}},"position-try":{prelude:"<dashed-ident>",descriptors:{top:"<'top'>",left:"<'left'>",bottom:"<'bottom'>",right:"<'right'>","inset-block-start":"<'inset-block-start'>","inset-block-end":"<'inset-block-end'>","inset-inline-start":"<'inset-inline-start'>","inset-inline-end":"<'inset-inline-end'>","inset-block":"<'inset-block'>","inset-inline":"<'inset-inline'>",inset:"<'inset'>","margin-top":"<'margin-top'>","margin-left":"<'margin-left'>","margin-bottom":"<'margin-bottom'>","margin-right":"<'margin-right'>","margin-block-start":"<'margin-block-start'>","margin-block-end":"<'margin-block-end'>","margin-inline-start":"<'margin-inline-start'>","margin-inline-end":"<'margin-inline-end'>",margin:"<'margin'>","margin-block":"<'margin-block'>","margin-inline":"<'margin-inline'>",width:"<'width'>",height:"<'height'>","min-width":"<'min-width'>","min-height":"<'min-height'>","max-width":"<'max-width'>","max-height":"<'max-height'>","block-size":"<'block-size'>","inline-size":"<'inline-size'>","min-block-size":"<'min-block-size'>","min-inline-size":"<'min-inline-size'>","max-block-size":"<'max-block-size'>","max-inline-size":"<'max-inline-size'>","align-self":"<'align-self'>|anchor-center","justify-self":"<'justify-self'>|anchor-center"}},property:{prelude:"<custom-property-name>",descriptors:{inherits:"true|false","initial-value":"<declaration-value>?",syntax:"<string>"}},scope:{prelude:"[( <scope-start> )]? [to ( <scope-end> )]?",descriptors:null},"starting-style":{prelude:null,descriptors:null},supports:{prelude:"<supports-condition>",descriptors:null},"view-transition":{prelude:null,descriptors:{navigation:"auto|none",types:"none|<custom-ident>+"}},"font-features-values":{prelude:"[<string>|<custom-ident>]+",descriptors:{"font-display":"auto|block|swap|fallback|optional"}}}};var Eo={};_(Eo,{AnPlusB:()=>ds,Atrule:()=>ms,AtrulePrelude:()=>gs,AttributeSelector:()=>Ss,Block:()=>_s,Brackets:()=>ws,CDC:()=>ks,CDO:()=>Rs,ClassSelector:()=>Ps,Combinator:()=>Fs,Comment:()=>Ls,Condition:()=>As,Declaration:()=>Ns,DeclarationList:()=>Ds,Dimension:()=>Ws,Feature:()=>js,FeatureFunction:()=>Vs,FeatureRange:()=>Xs,Function:()=>qs,GeneralEnclosed:()=>Zs,Hash:()=>el,IdSelector:()=>rl,Identifier:()=>il,Layer:()=>al,LayerList:()=>ll,MediaQuery:()=>hl,MediaQueryList:()=>ul,NestingSelector:()=>pl,Nth:()=>fl,Number:()=>Sl,Operator:()=>_l,Parentheses:()=>wl,Percentage:()=>kl,PseudoClassSelector:()=>Rl,PseudoElementSelector:()=>Pl,Ratio:()=>Fl,Raw:()=>Ll,Rule:()=>Al,Scope:()=>Nl,Selector:()=>Ul,SelectorList:()=>Hl,String:()=>Gl,StyleSheet:()=>Kl,SupportsDeclaration:()=>Xl,TypeSelector:()=>Jl,UnicodeRange:()=>ec,Url:()=>nc,Value:()=>sc,WhiteSpace:()=>cc});var ds={};_(ds,{generate:()=>rS,name:()=>iS,parse:()=>hs,structure:()=>oS});var ot=43,De=45,sn=110,ii=!0,tS=!1;function ln(i,e){let t=this.tokenStart+i,o=this.charCodeAt(t);for((o===ot||o===De)&&(e&&this.error("Number sign is not allowed"),t++);t<this.tokenEnd;t++)Z(this.charCodeAt(t))||this.error("Integer is expected",t)}function Bi(i){return ln.call(this,0,i)}function Lt(i,e){if(!this.cmpChar(this.tokenStart+i,e)){let t="";switch(e){case sn:t="N is expected";break;case De:t="HyphenMinus is expected";break}this.error(t,this.tokenStart+i)}}function cs(){let i=0,e=0,t=this.tokenType;for(;t===13||t===25;)t=this.lookupType(++i);if(t!==10)if(this.isDelim(ot,i)||this.isDelim(De,i)){e=this.isDelim(ot,i)?ot:De;do t=this.lookupType(++i);while(t===13||t===25);t!==10&&(this.skip(i),Bi.call(this,ii))}else return null;return i>0&&this.skip(i),e===0&&(t=this.charCodeAt(this.tokenStart),t!==ot&&t!==De&&this.error("Number sign is expected")),Bi.call(this,e!==0),e===De?"-"+this.consume(10):this.consume(10)}var iS="AnPlusB",oS={a:[String,null],b:[String,null]};function hs(){let i=this.tokenStart,e=null,t=null;if(this.tokenType===10)Bi.call(this,tS),t=this.consume(10);else if(this.tokenType===1&&this.cmpChar(this.tokenStart,De))switch(e="-1",Lt.call(this,1,sn),this.tokenEnd-this.tokenStart){case 2:this.next(),t=cs.call(this);break;case 3:Lt.call(this,2,De),this.next(),this.skipSC(),Bi.call(this,ii),t="-"+this.consume(10);break;default:Lt.call(this,2,De),ln.call(this,3,ii),this.next(),t=this.substrToCursor(i+2)}else if(this.tokenType===1||this.isDelim(ot)&&this.lookupType(1)===1){let o=0;switch(e="1",this.isDelim(ot)&&(o=1,this.next()),Lt.call(this,0,sn),this.tokenEnd-this.tokenStart){case 1:this.next(),t=cs.call(this);break;case 2:Lt.call(this,1,De),this.next(),this.skipSC(),Bi.call(this,ii),t="-"+this.consume(10);break;default:Lt.call(this,1,De),ln.call(this,2,ii),this.next(),t=this.substrToCursor(i+o+1)}}else if(this.tokenType===12){let o=this.charCodeAt(this.tokenStart),r=o===ot||o===De,n=this.tokenStart+r;for(;n<this.tokenEnd&&Z(this.charCodeAt(n));n++);n===this.tokenStart+r&&this.error("Integer is expected",this.tokenStart+r),Lt.call(this,n-this.tokenStart,sn),e=this.substring(i,n),n+1===this.tokenEnd?(this.next(),t=cs.call(this)):(Lt.call(this,n-this.tokenStart+1,De),n+2===this.tokenEnd?(this.next(),this.skipSC(),Bi.call(this,ii),t="-"+this.consume(10)):(ln.call(this,n-this.tokenStart+2,ii),this.next(),t=this.substrToCursor(n+1)))}else this.error();return e!==null&&e.charCodeAt(0)===ot&&(e=e.substr(1)),t!==null&&t.charCodeAt(0)===ot&&(t=t.substr(1)),{type:"AnPlusB",loc:this.getLocation(i,this.tokenStart),a:e,b:t}}function rS(i){if(i.a){let e=i.a==="+1"&&"n"||i.a==="1"&&"n"||i.a==="-1"&&"-n"||i.a+"n";if(i.b){let t=i.b[0]==="-"||i.b[0]==="+"?i.b:"+"+i.b;this.tokenize(e+t)}else this.tokenize(e)}else this.tokenize(i.b)}var ms={};_(ms,{generate:()=>cS,name:()=>aS,parse:()=>us,structure:()=>lS,walkContext:()=>sS});function lm(){return this.Raw(this.consumeUntilLeftCurlyBracketOrSemicolon,!0)}function nS(){for(let i=1,e;e=this.lookupType(i);i++){if(e===24)return!0;if(e===23||e===3)return!1}return!1}var aS="Atrule",sS="atrule",lS={name:String,prelude:["AtrulePrelude","Raw",null],block:["Block",null]};function us(i=!1){let e=this.tokenStart,t,o,r=null,n=null;switch(this.eat(3),t=this.substrToCursor(e+1),o=t.toLowerCase(),this.skipSC(),this.eof===!1&&this.tokenType!==23&&this.tokenType!==17&&(this.parseAtrulePrelude?r=this.parseWithFallback(this.AtrulePrelude.bind(this,t,i),lm):r=lm.call(this,this.tokenIndex),this.skipSC()),this.tokenType){case 17:this.next();break;case 23:hasOwnProperty.call(this.atrule,o)&&typeof this.atrule[o].block=="function"?n=this.atrule[o].block.call(this,i):n=this.Block(nS.call(this));break}return{type:"Atrule",loc:this.getLocation(e,this.tokenStart),name:t,prelude:r,block:n}}function cS(i){this.token(3,"@"+i.name),i.prelude!==null&&this.node(i.prelude),i.block?this.node(i.block):this.token(17,";")}var gs={};_(gs,{generate:()=>mS,name:()=>hS,parse:()=>ps,structure:()=>uS,walkContext:()=>dS});var hS="AtrulePrelude",dS="atrulePrelude",uS={children:[[]]};function ps(i){let e=null;return i!==null&&(i=i.toLowerCase()),this.skipSC(),hasOwnProperty.call(this.atrule,i)&&typeof this.atrule[i].prelude=="function"?e=this.atrule[i].prelude.call(this):e=this.readSequence(this.scope.AtrulePrelude),this.skipSC(),this.eof!==!0&&this.tokenType!==23&&this.tokenType!==17&&this.error("Semicolon or block is expected"),{type:"AtrulePrelude",loc:this.getLocationFromList(e),children:e}}function mS(i){this.children(i)}var Ss={};_(Ss,{generate:()=>vS,name:()=>bS,parse:()=>ys,structure:()=>_S});var pS=36,cm=42,cn=61,gS=94,fs=124,fS=126;function yS(){this.eof&&this.error("Unexpected end of input");let i=this.tokenStart,e=!1;return this.isDelim(cm)?(e=!0,this.next()):this.isDelim(fs)||this.eat(1),this.isDelim(fs)?this.charCodeAt(this.tokenStart+1)!==cn?(this.next(),this.eat(1)):e&&this.error("Identifier is expected",this.tokenEnd):e&&this.error("Vertical line is expected"),{type:"Identifier",loc:this.getLocation(i,this.tokenStart),name:this.substrToCursor(i)}}function SS(){let i=this.tokenStart,e=this.charCodeAt(i);return e!==cn&&e!==fS&&e!==gS&&e!==pS&&e!==cm&&e!==fs&&this.error("Attribute selector (=, ~=, ^=, $=, *=, |=) is expected"),this.next(),e!==cn&&(this.isDelim(cn)||this.error("Equal sign is expected"),this.next()),this.substrToCursor(i)}var bS="AttributeSelector",_S={name:"Identifier",matcher:[String,null],value:["String","Identifier",null],flags:[String,null]};function ys(){let i=this.tokenStart,e,t=null,o=null,r=null;return this.eat(19),this.skipSC(),e=yS.call(this),this.skipSC(),this.tokenType!==20&&(this.tokenType!==1&&(t=SS.call(this),this.skipSC(),o=this.tokenType===5?this.String():this.Identifier(),this.skipSC()),this.tokenType===1&&(r=this.consume(1),this.skipSC())),this.eat(20),{type:"AttributeSelector",loc:this.getLocation(i,this.tokenStart),name:e,matcher:t,value:o,flags:r}}function vS(i){this.token(9,"["),this.node(i.name),i.matcher!==null&&(this.tokenize(i.matcher),this.node(i.value)),i.flags!==null&&this.token(1,i.flags),this.token(9,"]")}var _s={};_(_s,{generate:()=>ES,name:()=>kS,parse:()=>bs,structure:()=>RS,walkContext:()=>CS});var wS=38;function um(){return this.Raw(null,!0)}function hm(){return this.parseWithFallback(this.Rule,um)}function dm(){return this.Raw(this.consumeUntilSemicolonIncluded,!0)}function xS(){if(this.tokenType===17)return dm.call(this,this.tokenIndex);let i=this.parseWithFallback(this.Declaration,dm);return this.tokenType===17&&this.next(),i}var kS="Block",CS="block",RS={children:[["Atrule","Rule","Declaration"]]};function bs(i){let e=i?xS:hm,t=this.tokenStart,o=this.createList();this.eat(23);e:for(;!this.eof;)switch(this.tokenType){case 24:break e;case 13:case 25:this.next();break;case 3:o.push(this.parseWithFallback(this.Atrule.bind(this,i),um));break;default:i&&this.isDelim(wS)?o.push(hm.call(this)):o.push(e.call(this))}return this.eof||this.eat(24),{type:"Block",loc:this.getLocation(t,this.tokenStart),children:o}}function ES(i){this.token(23,"{"),this.children(i,e=>{e.type==="Declaration"&&this.token(17,";")}),this.token(24,"}")}var ws={};_(ws,{generate:()=>FS,name:()=>PS,parse:()=>vs,structure:()=>TS});var PS="Brackets",TS={children:[[]]};function vs(i,e){let t=this.tokenStart,o=null;return this.eat(19),o=i.call(this,e),this.eof||this.eat(20),{type:"Brackets",loc:this.getLocation(t,this.tokenStart),children:o}}function FS(i){this.token(9,"["),this.children(i),this.token(9,"]")}var ks={};_(ks,{generate:()=>OS,name:()=>zS,parse:()=>xs,structure:()=>LS});var zS="CDC",LS=[];function xs(){let i=this.tokenStart;return this.eat(15),{type:"CDC",loc:this.getLocation(i,this.tokenStart)}}function OS(){this.token(15,"-->")}var Rs={};_(Rs,{generate:()=>NS,name:()=>AS,parse:()=>Cs,structure:()=>MS});var AS="CDO",MS=[];function Cs(){let i=this.tokenStart;return this.eat(14),{type:"CDO",loc:this.getLocation(i,this.tokenStart)}}function NS(){this.token(14,"<!--")}var Ps={};_(Ps,{generate:()=>HS,name:()=>US,parse:()=>Es,structure:()=>DS});var IS=46,US="ClassSelector",DS={name:String};function Es(){return this.eatDelim(IS),{type:"ClassSelector",loc:this.getLocation(this.tokenStart-1,this.tokenEnd),name:this.consume(1)}}function HS(i){this.token(9,"."),this.token(1,i.name)}var Fs={};_(Fs,{generate:()=>KS,name:()=>GS,parse:()=>Ts,structure:()=>VS});var WS=43,mm=47,BS=62,jS=126,GS="Combinator",VS={name:String};function Ts(){let i=this.tokenStart,e;switch(this.tokenType){case 13:e=" ";break;case 9:switch(this.charCodeAt(this.tokenStart)){case BS:case WS:case jS:this.next();break;case mm:this.next(),this.eatIdent("deep"),this.eatDelim(mm);break;default:this.error("Combinator is expected")}e=this.substrToCursor(i);break}return{type:"Combinator",loc:this.getLocation(i,this.tokenStart),name:e}}function KS(i){this.tokenize(i.name)}var Ls={};_(Ls,{generate:()=>JS,name:()=>$S,parse:()=>zs,structure:()=>qS});var YS=42,XS=47,$S="Comment",qS={value:String};function zs(){let i=this.tokenStart,e=this.tokenEnd;return this.eat(25),e-i+2>=2&&this.charCodeAt(e-2)===YS&&this.charCodeAt(e-1)===XS&&(e-=2),{type:"Comment",loc:this.getLocation(i,this.tokenStart),value:this.substring(i+2,e)}}function JS(i){this.token(25,"/*"+i.value+"*/")}var As={};_(As,{generate:()=>ib,name:()=>QS,parse:()=>Os,structure:()=>eb});var ZS=new Set([16,22,0]),QS="Condition",eb={kind:String,children:[["Identifier","Feature","FeatureFunction","FeatureRange","SupportsDeclaration"]]};function pm(i){return this.lookupTypeNonSC(1)===1&&ZS.has(this.lookupTypeNonSC(2))?this.Feature(i):this.FeatureRange(i)}var tb={media:pm,container:pm,supports(){return this.SupportsDeclaration()}};function Os(i="media"){let e=this.createList();e:for(;!this.eof;)switch(this.tokenType){case 25:case 13:this.next();continue;case 1:e.push(this.Identifier());break;case 21:{let t=this.parseWithFallback(()=>tb[i].call(this,i),()=>null);t||(t=this.parseWithFallback(()=>{this.eat(21);let o=this.Condition(i);return this.eat(22),o},()=>this.GeneralEnclosed(i))),e.push(t);break}case 2:{let t=this.parseWithFallback(()=>this.FeatureFunction(i),()=>null);t||(t=this.GeneralEnclosed(i)),e.push(t);break}default:break e}return e.isEmpty&&this.error("Condition is expected"),{type:"Condition",loc:this.getLocationFromList(e),kind:i,children:e}}function ib(i){i.children.forEach(e=>{e.type==="Condition"?(this.token(21,"("),this.node(e),this.token(22,")")):this.node(e)})}var Ns={};_(Ns,{generate:()=>pb,name:()=>db,parse:()=>Ms,structure:()=>mb,walkContext:()=>ub});var fm=33,ob=35,rb=36,nb=38,ab=42,sb=43,gm=47;function lb(){return this.Raw(this.consumeUntilExclamationMarkOrSemicolon,!0)}function cb(){return this.Raw(this.consumeUntilExclamationMarkOrSemicolon,!1)}function hb(){let i=this.tokenIndex,e=this.Value();return e.type!=="Raw"&&this.eof===!1&&this.tokenType!==17&&this.isDelim(fm)===!1&&this.isBalanceEdge(i)===!1&&this.error(),e}var db="Declaration",ub="declaration",mb={important:[Boolean,String],property:String,value:["Value","Raw"]};function Ms(){let i=this.tokenStart,e=this.tokenIndex,t=gb.call(this),o=go(t),r=o?this.parseCustomProperty:this.parseValue,n=o?cb:lb,a=!1,s;this.skipSC(),this.eat(16);let c=this.tokenIndex;if(o||this.skipSC(),r?s=this.parseWithFallback(hb,n):s=n.call(this,this.tokenIndex),o&&s.type==="Value"&&s.children.isEmpty){for(let l=c-this.tokenIndex;l<=0;l++)if(this.lookupType(l)===13){s.children.appendData({type:"WhiteSpace",loc:null,value:" "});break}}return this.isDelim(fm)&&(a=fb.call(this),this.skipSC()),this.eof===!1&&this.tokenType!==17&&this.isBalanceEdge(e)===!1&&this.error(),{type:"Declaration",loc:this.getLocation(i,this.tokenStart),important:a,property:t,value:s}}function pb(i){this.token(1,i.property),this.token(16,":"),this.node(i.value),i.important&&(this.token(9,"!"),this.token(1,i.important===!0?"important":i.important))}function gb(){let i=this.tokenStart;if(this.tokenType===9)switch(this.charCodeAt(this.tokenStart)){case ab:case rb:case sb:case ob:case nb:this.next();break;case gm:this.next(),this.isDelim(gm)&&this.next();break}return this.tokenType===4?this.eat(4):this.eat(1),this.substrToCursor(i)}function fb(){this.eat(9),this.skipSC();let i=this.consume(1);return i==="important"?!0:i}var Ds={};_(Ds,{generate:()=>_b,name:()=>Sb,parse:()=>Us,structure:()=>bb});var yb=38;function Is(){return this.Raw(this.consumeUntilSemicolonIncluded,!0)}var Sb="DeclarationList",bb={children:[["Declaration","Atrule","Rule"]]};function Us(){let i=this.createList();for(;!this.eof;)switch(this.tokenType){case 13:case 25:case 17:this.next();break;case 3:i.push(this.parseWithFallback(this.Atrule.bind(this,!0),Is));break;default:this.isDelim(yb)?i.push(this.parseWithFallback(this.Rule,Is)):i.push(this.parseWithFallback(this.Declaration,Is))}return{type:"DeclarationList",loc:this.getLocationFromList(i),children:i}}function _b(i){this.children(i,e=>{e.type==="Declaration"&&this.token(17,";")})}var Ws={};_(Ws,{generate:()=>xb,name:()=>vb,parse:()=>Hs,structure:()=>wb});var vb="Dimension",wb={value:String,unit:String};function Hs(){let i=this.tokenStart,e=this.consumeNumber(12);return{type:"Dimension",loc:this.getLocation(i,this.tokenStart),value:e,unit:this.substring(i+e.length,this.tokenStart)}}function xb(i){this.token(12,i.value+i.unit)}var js={};_(js,{generate:()=>Eb,name:()=>Cb,parse:()=>Bs,structure:()=>Rb});var kb=47,Cb="Feature",Rb={kind:String,name:String,value:["Identifier","Number","Dimension","Ratio","Function",null]};function Bs(i){let e=this.tokenStart,t,o=null;if(this.eat(21),this.skipSC(),t=this.consume(1),this.skipSC(),this.tokenType!==22){switch(this.eat(16),this.skipSC(),this.tokenType){case 10:this.lookupNonWSType(1)===9?o=this.Ratio():o=this.Number();break;case 12:o=this.Dimension();break;case 1:o=this.Identifier();break;case 2:o=this.parseWithFallback(()=>{let r=this.Function(this.readSequence,this.scope.Value);return this.skipSC(),this.isDelim(kb)&&this.error(),r},()=>this.Ratio());break;default:this.error("Number, dimension, ratio or identifier is expected")}this.skipSC()}return this.eof||this.eat(22),{type:"Feature",loc:this.getLocation(e,this.tokenStart),kind:i,name:t,value:o}}function Eb(i){this.token(21,"("),this.token(1,i.name),i.value!==null&&(this.token(16,":"),this.node(i.value)),this.token(22,")")}var Vs={};_(Vs,{generate:()=>zb,name:()=>Pb,parse:()=>Gs,structure:()=>Tb});var Pb="FeatureFunction",Tb={kind:String,feature:String,value:["Declaration","Selector"]};function Fb(i,e){let o=(this.features[i]||{})[e];return typeof o!="function"&&this.error(`Unknown feature ${e}()`),o}function Gs(i="unknown"){let e=this.tokenStart,t=this.consumeFunctionName(),o=Fb.call(this,i,t.toLowerCase());this.skipSC();let r=this.parseWithFallback(()=>{let n=this.tokenIndex,a=o.call(this);return this.eof===!1&&this.isBalanceEdge(n)===!1&&this.error(),a},()=>this.Raw(null,!1));return this.eof||this.eat(22),{type:"FeatureFunction",loc:this.getLocation(e,this.tokenStart),kind:i,feature:t,value:r}}function zb(i){this.token(2,i.feature+"("),this.node(i.value),this.token(22,")")}var Xs={};_(Xs,{generate:()=>Nb,name:()=>Ab,parse:()=>Ys,structure:()=>Mb});var ym=47,Lb=60,Sm=61,Ob=62,Ab="FeatureRange",Mb={kind:String,left:["Identifier","Number","Dimension","Ratio","Function"],leftComparison:String,middle:["Identifier","Number","Dimension","Ratio","Function"],rightComparison:[String,null],right:["Identifier","Number","Dimension","Ratio","Function",null]};function Ks(){switch(this.skipSC(),this.tokenType){case 10:return this.isDelim(ym,this.lookupOffsetNonSC(1))?this.Ratio():this.Number();case 12:return this.Dimension();case 1:return this.Identifier();case 2:return this.parseWithFallback(()=>{let i=this.Function(this.readSequence,this.scope.Value);return this.skipSC(),this.isDelim(ym)&&this.error(),i},()=>this.Ratio());default:this.error("Number, dimension, ratio or identifier is expected")}}function bm(i){if(this.skipSC(),this.isDelim(Lb)||this.isDelim(Ob)){let e=this.source[this.tokenStart];return this.next(),this.isDelim(Sm)?(this.next(),e+"="):e}if(this.isDelim(Sm))return"=";this.error(`Expected ${i?'":", ':""}"<", ">", "=" or ")"`)}function Ys(i="unknown"){let e=this.tokenStart;this.skipSC(),this.eat(21);let t=Ks.call(this),o=bm.call(this,t.type==="Identifier"),r=Ks.call(this),n=null,a=null;return this.lookupNonWSType(0)!==22&&(n=bm.call(this),a=Ks.call(this)),this.skipSC(),this.eat(22),{type:"FeatureRange",loc:this.getLocation(e,this.tokenStart),kind:i,left:t,leftComparison:o,middle:r,rightComparison:n,right:a}}function Nb(i){this.token(21,"("),this.node(i.left),this.tokenize(i.leftComparison),this.node(i.middle),i.right&&(this.tokenize(i.rightComparison),this.node(i.right)),this.token(22,")")}var qs={};_(qs,{generate:()=>Hb,name:()=>Ib,parse:()=>$s,structure:()=>Db,walkContext:()=>Ub});var Ib="Function",Ub="function",Db={name:String,children:[[]]};function $s(i,e){let t=this.tokenStart,o=this.consumeFunctionName(),r=o.toLowerCase(),n;return n=e.hasOwnProperty(r)?e[r].call(this,e):i.call(this,e),this.eof||this.eat(22),{type:"Function",loc:this.getLocation(t,this.tokenStart),name:o,children:n}}function Hb(i){this.token(2,i.name+"("),this.children(i),this.token(22,")")}var Zs={};_(Zs,{generate:()=>jb,name:()=>Wb,parse:()=>Js,structure:()=>Bb});var Wb="GeneralEnclosed",Bb={kind:String,function:[String,null],children:[[]]};function Js(i){let e=this.tokenStart,t=null;this.tokenType===2?t=this.consumeFunctionName():this.eat(21);let o=this.parseWithFallback(()=>{let r=this.tokenIndex,n=this.readSequence(this.scope.Value);return this.eof===!1&&this.isBalanceEdge(r)===!1&&this.error(),n},()=>this.createSingleNodeList(this.Raw(null,!1)));return this.eof||this.eat(22),{type:"GeneralEnclosed",loc:this.getLocation(e,this.tokenStart),kind:i,function:t,children:o}}function jb(i){i.function?this.token(2,i.function+"("):this.token(21,"("),this.children(i),this.token(22,")")}var el={};_(el,{generate:()=>Yb,name:()=>Vb,parse:()=>Qs,structure:()=>Kb,xxx:()=>Gb});var Gb="XXX",Vb="Hash",Kb={value:String};function Qs(){let i=this.tokenStart;return this.eat(4),{type:"Hash",loc:this.getLocation(i,this.tokenStart),value:this.substrToCursor(i+1)}}function Yb(i){this.token(4,"#"+i.value)}var il={};_(il,{generate:()=>qb,name:()=>Xb,parse:()=>tl,structure:()=>$b});var Xb="Identifier",$b={name:String};function tl(){return{type:"Identifier",loc:this.getLocation(this.tokenStart,this.tokenEnd),name:this.consume(1)}}function qb(i){this.token(1,i.name)}var rl={};_(rl,{generate:()=>Qb,name:()=>Jb,parse:()=>ol,structure:()=>Zb});var Jb="IdSelector",Zb={name:String};function ol(){let i=this.tokenStart;return this.eat(4),{type:"IdSelector",loc:this.getLocation(i,this.tokenStart),name:this.substrToCursor(i+1)}}function Qb(i){this.token(9,"#"+i.name)}var al={};_(al,{generate:()=>o_,name:()=>t_,parse:()=>nl,structure:()=>i_});var e_=46,t_="Layer",i_={name:String};function nl(){let i=this.tokenStart,e=this.consume(1);for(;this.isDelim(e_);)this.eat(9),e+="."+this.consume(1);return{type:"Layer",loc:this.getLocation(i,this.tokenStart),name:e}}function o_(i){this.tokenize(i.name)}var ll={};_(ll,{generate:()=>a_,name:()=>r_,parse:()=>sl,structure:()=>n_});var r_="LayerList",n_={children:[["Layer"]]};function sl(){let i=this.createList();for(this.skipSC();!this.eof&&(i.push(this.Layer()),this.lookupTypeNonSC(0)===18);)this.skipSC(),this.next(),this.skipSC();return{type:"LayerList",loc:this.getLocationFromList(i),children:i}}function a_(i){this.children(i,()=>this.token(18,","))}var hl={};_(hl,{generate:()=>c_,name:()=>s_,parse:()=>cl,structure:()=>l_});var s_="MediaQuery",l_={modifier:[String,null],mediaType:[String,null],condition:["Condition",null]};function cl(){let i=this.tokenStart,e=null,t=null,o=null;if(this.skipSC(),this.tokenType===1&&this.lookupTypeNonSC(1)!==21){let r=this.consume(1),n=r.toLowerCase();switch(n==="not"||n==="only"?(this.skipSC(),e=n,t=this.consume(1)):t=r,this.lookupTypeNonSC(0)){case 1:{this.skipSC(),this.eatIdent("and"),o=this.Condition("media");break}case 23:case 17:case 18:case 0:break;default:this.error("Identifier or parenthesis is expected")}}else switch(this.tokenType){case 1:case 21:case 2:{o=this.Condition("media");break}case 23:case 17:case 0:break;default:this.error("Identifier or parenthesis is expected")}return{type:"MediaQuery",loc:this.getLocation(i,this.tokenStart),modifier:e,mediaType:t,condition:o}}function c_(i){i.mediaType?(i.modifier&&this.token(1,i.modifier),this.token(1,i.mediaType),i.condition&&(this.token(1,"and"),this.node(i.condition))):i.condition&&this.node(i.condition)}var ul={};_(ul,{generate:()=>u_,name:()=>h_,parse:()=>dl,structure:()=>d_});var h_="MediaQueryList",d_={children:[["MediaQuery"]]};function dl(){let i=this.createList();for(this.skipSC();!this.eof&&(i.push(this.MediaQuery()),this.tokenType===18);)this.next();return{type:"MediaQueryList",loc:this.getLocationFromList(i),children:i}}function u_(i){this.children(i,()=>this.token(18,","))}var pl={};_(pl,{generate:()=>f_,name:()=>p_,parse:()=>ml,structure:()=>g_});var m_=38,p_="NestingSelector",g_={};function ml(){let i=this.tokenStart;return this.eatDelim(m_),{type:"NestingSelector",loc:this.getLocation(i,this.tokenStart)}}function f_(){this.token(9,"&")}var fl={};_(fl,{generate:()=>b_,name:()=>y_,parse:()=>gl,structure:()=>S_});var y_="Nth",S_={nth:["AnPlusB","Identifier"],selector:["SelectorList",null]};function gl(){this.skipSC();let i=this.tokenStart,e=i,t=null,o;return this.lookupValue(0,"odd")||this.lookupValue(0,"even")?o=this.Identifier():o=this.AnPlusB(),e=this.tokenStart,this.skipSC(),this.lookupValue(0,"of")&&(this.next(),t=this.SelectorList(),e=this.tokenStart),{type:"Nth",loc:this.getLocation(i,e),nth:o,selector:t}}function b_(i){this.node(i.nth),i.selector!==null&&(this.token(1,"of"),this.node(i.selector))}var Sl={};_(Sl,{generate:()=>w_,name:()=>__,parse:()=>yl,structure:()=>v_});var __="Number",v_={value:String};function yl(){return{type:"Number",loc:this.getLocation(this.tokenStart,this.tokenEnd),value:this.consume(10)}}function w_(i){this.token(10,i.value)}var _l={};_(_l,{generate:()=>C_,name:()=>x_,parse:()=>bl,structure:()=>k_});var x_="Operator",k_={value:String};function bl(){let i=this.tokenStart;return this.next(),{type:"Operator",loc:this.getLocation(i,this.tokenStart),value:this.substrToCursor(i)}}function C_(i){this.tokenize(i.value)}var wl={};_(wl,{generate:()=>P_,name:()=>R_,parse:()=>vl,structure:()=>E_});var R_="Parentheses",E_={children:[[]]};function vl(i,e){let t=this.tokenStart,o=null;return this.eat(21),o=i.call(this,e),this.eof||this.eat(22),{type:"Parentheses",loc:this.getLocation(t,this.tokenStart),children:o}}function P_(i){this.token(21,"("),this.children(i),this.token(22,")")}var kl={};_(kl,{generate:()=>z_,name:()=>T_,parse:()=>xl,structure:()=>F_});var T_="Percentage",F_={value:String};function xl(){return{type:"Percentage",loc:this.getLocation(this.tokenStart,this.tokenEnd),value:this.consumeNumber(11)}}function z_(i){this.token(11,i.value+"%")}var Rl={};_(Rl,{generate:()=>M_,name:()=>L_,parse:()=>Cl,structure:()=>A_,walkContext:()=>O_});var L_="PseudoClassSelector",O_="function",A_={name:String,children:[["Raw"],null]};function Cl(){let i=this.tokenStart,e=null,t,o;return this.eat(16),this.tokenType===2?(t=this.consumeFunctionName(),o=t.toLowerCase(),this.lookupNonWSType(0)==22?e=this.createList():hasOwnProperty.call(this.pseudo,o)?(this.skipSC(),e=this.pseudo[o].call(this),this.skipSC()):(e=this.createList(),e.push(this.Raw(null,!1))),this.eat(22)):t=this.consume(1),{type:"PseudoClassSelector",loc:this.getLocation(i,this.tokenStart),name:t,children:e}}function M_(i){this.token(16,":"),i.children===null?this.token(1,i.name):(this.token(2,i.name+"("),this.children(i),this.token(22,")"))}var Pl={};_(Pl,{generate:()=>D_,name:()=>N_,parse:()=>El,structure:()=>U_,walkContext:()=>I_});var N_="PseudoElementSelector",I_="function",U_={name:String,children:[["Raw"],null]};function El(){let i=this.tokenStart,e=null,t,o;return this.eat(16),this.eat(16),this.tokenType===2?(t=this.consumeFunctionName(),o=t.toLowerCase(),this.lookupNonWSType(0)==22?e=this.createList():hasOwnProperty.call(this.pseudo,o)?(this.skipSC(),e=this.pseudo[o].call(this),this.skipSC()):(e=this.createList(),e.push(this.Raw(null,!1))),this.eat(22)):t=this.consume(1),{type:"PseudoElementSelector",loc:this.getLocation(i,this.tokenStart),name:t,children:e}}function D_(i){this.token(16,":"),this.token(16,":"),i.children===null?this.token(1,i.name):(this.token(2,i.name+"("),this.children(i),this.token(22,")"))}var Fl={};_(Fl,{generate:()=>B_,name:()=>H_,parse:()=>Tl,structure:()=>W_});var _m=47;function vm(){switch(this.skipSC(),this.tokenType){case 10:return this.Number();case 2:return this.Function(this.readSequence,this.scope.Value);default:this.error("Number of function is expected")}}var H_="Ratio",W_={left:["Number","Function"],right:["Number","Function",null]};function Tl(){let i=this.tokenStart,e=vm.call(this),t=null;return this.skipSC(),this.isDelim(_m)&&(this.eatDelim(_m),t=vm.call(this)),{type:"Ratio",loc:this.getLocation(i,this.tokenStart),left:e,right:t}}function B_(i){this.node(i.left),this.token(9,"/"),i.right?this.node(i.right):this.node(10,1)}var Ll={};_(Ll,{generate:()=>K_,name:()=>G_,parse:()=>zl,structure:()=>V_});function j_(){return this.tokenIndex>0&&this.lookupType(-1)===13?this.tokenIndex>1?this.getTokenStart(this.tokenIndex-1):this.firstCharOffset:this.tokenStart}var G_="Raw",V_={value:String};function zl(i,e){let t=this.getTokenStart(this.tokenIndex),o;return this.skipUntilBalanced(this.tokenIndex,i||this.consumeUntilBalanceEnd),e&&this.tokenStart>t?o=j_.call(this):o=this.tokenStart,{type:"Raw",loc:this.getLocation(t,o),value:this.substring(t,o)}}function K_(i){this.tokenize(i.value)}var Al={};_(Al,{generate:()=>J_,name:()=>X_,parse:()=>Ol,structure:()=>q_,walkContext:()=>$_});function wm(){return this.Raw(this.consumeUntilLeftCurlyBracket,!0)}function Y_(){let i=this.SelectorList();return i.type!=="Raw"&&this.eof===!1&&this.tokenType!==23&&this.error(),i}var X_="Rule",$_="rule",q_={prelude:["SelectorList","Raw"],block:["Block"]};function Ol(){let i=this.tokenIndex,e=this.tokenStart,t,o;return this.parseRulePrelude?t=this.parseWithFallback(Y_,wm):t=wm.call(this,i),o=this.Block(!0),{type:"Rule",loc:this.getLocation(e,this.tokenStart),prelude:t,block:o}}function J_(i){this.node(i.prelude),this.node(i.block)}var Nl={};_(Nl,{generate:()=>ev,name:()=>Z_,parse:()=>Ml,structure:()=>Q_});var Z_="Scope",Q_={root:["SelectorList","Raw",null],limit:["SelectorList","Raw",null]};function Ml(){let i=null,e=null;this.skipSC();let t=this.tokenStart;return this.tokenType===21&&(this.next(),this.skipSC(),i=this.parseWithFallback(this.SelectorList,()=>this.Raw(!1,!0)),this.skipSC(),this.eat(22)),this.lookupNonWSType(0)===1&&(this.skipSC(),this.eatIdent("to"),this.skipSC(),this.eat(21),this.skipSC(),e=this.parseWithFallback(this.SelectorList,()=>this.Raw(!1,!0)),this.skipSC(),this.eat(22)),{type:"Scope",loc:this.getLocation(t,this.tokenStart),root:i,limit:e}}function ev(i){i.root&&(this.token(21,"("),this.node(i.root),this.token(22,")")),i.limit&&(this.token(1,"to"),this.token(21,"("),this.node(i.limit),this.token(22,")"))}var Ul={};_(Ul,{generate:()=>ov,name:()=>tv,parse:()=>Il,structure:()=>iv});var tv="Selector",iv={children:[["TypeSelector","IdSelector","ClassSelector","AttributeSelector","PseudoClassSelector","PseudoElementSelector","Combinator"]]};function Il(){let i=this.readSequence(this.scope.Selector);return this.getFirstListNode(i)===null&&this.error("Selector is expected"),{type:"Selector",loc:this.getLocationFromList(i),children:i}}function ov(i){this.children(i)}var Hl={};_(Hl,{generate:()=>sv,name:()=>rv,parse:()=>Dl,structure:()=>av,walkContext:()=>nv});var rv="SelectorList",nv="selector",av={children:[["Selector","Raw"]]};function Dl(){let i=this.createList();for(;!this.eof;){if(i.push(this.Selector()),this.tokenType===18){this.next();continue}break}return{type:"SelectorList",loc:this.getLocationFromList(i),children:i}}function sv(i){this.children(i,()=>this.token(18,","))}var Gl={};_(Gl,{generate:()=>hv,name:()=>lv,parse:()=>jl,structure:()=>cv});var hn={};_(hn,{decode:()=>Co,encode:()=>Bl});var Wl=92,xm=34,km=39;function Co(i){let e=i.length,t=i.charCodeAt(0),o=t===xm||t===km?1:0,r=o===1&&e>1&&i.charCodeAt(e-1)===t?e-2:e-1,n="";for(let a=o;a<=r;a++){let s=i.charCodeAt(a);if(s===Wl){if(a===r){a!==e-1&&(n=i.substr(a+1));break}if(s=i.charCodeAt(++a),Se(Wl,s)){let c=a-1,l=Ke(i,c);a=l-1,n+=Ei(i.substring(c+1,l))}else s===13&&i.charCodeAt(a+1)===10&&a++}else n+=i[a]}return n}function Bl(i,e){let t=e?"'":'"',o=e?km:xm,r="",n=!1;for(let a=0;a<i.length;a++){let s=i.charCodeAt(a);if(s===0){r+="\uFFFD";continue}if(s<=31||s===127){r+="\\"+s.toString(16),n=!0;continue}s===o||s===Wl?(r+="\\"+i.charAt(a),n=!1):(n&&(Ae(s)||tt(s))&&(r+=" "),r+=i.charAt(a),n=!1)}return t+r+t}var lv="String",cv={value:String};function jl(){return{type:"String",loc:this.getLocation(this.tokenStart,this.tokenEnd),value:Co(this.consume(5))}}function hv(i){this.token(5,Bl(i.value))}var Kl={};_(Kl,{generate:()=>gv,name:()=>uv,parse:()=>Vl,structure:()=>pv,walkContext:()=>mv});var dv=33;function Cm(){return this.Raw(null,!1)}var uv="StyleSheet",mv="stylesheet",pv={children:[["Comment","CDO","CDC","Atrule","Rule","Raw"]]};function Vl(){let i=this.tokenStart,e=this.createList(),t;for(;!this.eof;){switch(this.tokenType){case 13:this.next();continue;case 25:if(this.charCodeAt(this.tokenStart+2)!==dv){this.next();continue}t=this.Comment();break;case 14:t=this.CDO();break;case 15:t=this.CDC();break;case 3:t=this.parseWithFallback(this.Atrule,Cm);break;default:t=this.parseWithFallback(this.Rule,Cm)}e.push(t)}return{type:"StyleSheet",loc:this.getLocation(i,this.tokenStart),children:e}}function gv(i){this.children(i)}var Xl={};_(Xl,{generate:()=>Sv,name:()=>fv,parse:()=>Yl,structure:()=>yv});var fv="SupportsDeclaration",yv={declaration:"Declaration"};function Yl(){let i=this.tokenStart;this.eat(21),this.skipSC();let e=this.Declaration();return this.eof||this.eat(22),{type:"SupportsDeclaration",loc:this.getLocation(i,this.tokenStart),declaration:e}}function Sv(i){this.token(21,"("),this.node(i.declaration),this.token(22,")")}var Jl={};_(Jl,{generate:()=>wv,name:()=>_v,parse:()=>ql,structure:()=>vv});var bv=42,Rm=124;function $l(){this.tokenType!==1&&this.isDelim(bv)===!1&&this.error("Identifier or asterisk is expected"),this.next()}var _v="TypeSelector",vv={name:String};function ql(){let i=this.tokenStart;return this.isDelim(Rm)?(this.next(),$l.call(this)):($l.call(this),this.isDelim(Rm)&&(this.next(),$l.call(this))),{type:"TypeSelector",loc:this.getLocation(i,this.tokenStart),name:this.substrToCursor(i)}}function wv(i){this.tokenize(i.name)}var ec={};_(ec,{generate:()=>Ev,name:()=>Cv,parse:()=>Ql,structure:()=>Rv});var Em=43,Pm=45,Zl=63;function Ro(i,e){let t=0;for(let o=this.tokenStart+i;o<this.tokenEnd;o++){let r=this.charCodeAt(o);if(r===Pm&&e&&t!==0)return Ro.call(this,i+t+1,!1),-1;Ae(r)||this.error(e&&t!==0?"Hyphen minus"+(t<6?" or hex digit":"")+" is expected":t<6?"Hex digit is expected":"Unexpected input",o),++t>6&&this.error("Too many hex digits",o)}return this.next(),t}function dn(i){let e=0;for(;this.isDelim(Zl);)++e>i&&this.error("Too many question marks"),this.next()}function xv(i){this.charCodeAt(this.tokenStart)!==i&&this.error((i===Em?"Plus sign":"Hyphen minus")+" is expected")}function kv(){let i=0;switch(this.tokenType){case 10:if(i=Ro.call(this,1,!0),this.isDelim(Zl)){dn.call(this,6-i);break}if(this.tokenType===12||this.tokenType===10){xv.call(this,Pm),Ro.call(this,1,!1);break}break;case 12:i=Ro.call(this,1,!0),i>0&&dn.call(this,6-i);break;default:if(this.eatDelim(Em),this.tokenType===1){i=Ro.call(this,0,!0),i>0&&dn.call(this,6-i);break}if(this.isDelim(Zl)){this.next(),dn.call(this,5);break}this.error("Hex digit or question mark is expected")}}var Cv="UnicodeRange",Rv={value:String};function Ql(){let i=this.tokenStart;return this.eatIdent("u"),kv.call(this),{type:"UnicodeRange",loc:this.getLocation(i,this.tokenStart),value:this.substrToCursor(i)}}function Ev(i){this.tokenize(i.value)}var nc={};_(nc,{generate:()=>Av,name:()=>Lv,parse:()=>rc,structure:()=>Ov});var un={};_(un,{decode:()=>ic,encode:()=>oc});var Pv=32,tc=92,Tv=34,Fv=39,zv=40,Tm=41;function ic(i){let e=i.length,t=4,o=i.charCodeAt(e-1)===Tm?e-2:e-1,r="";for(;t<o&&tt(i.charCodeAt(t));)t++;for(;t<o&&tt(i.charCodeAt(o));)o--;for(let n=t;n<=o;n++){let a=i.charCodeAt(n);if(a===tc){if(n===o){n!==e-1&&(r=i.substr(n+1));break}if(a=i.charCodeAt(++n),Se(tc,a)){let s=n-1,c=Ke(i,s);n=c-1,r+=Ei(i.substring(s+1,c))}else a===13&&i.charCodeAt(n+1)===10&&n++}else r+=i[n]}return r}function oc(i){let e="",t=!1;for(let o=0;o<i.length;o++){let r=i.charCodeAt(o);if(r===0){e+="\uFFFD";continue}if(r<=31||r===127){e+="\\"+r.toString(16),t=!0;continue}r===Pv||r===tc||r===Tv||r===Fv||r===zv||r===Tm?(e+="\\"+i.charAt(o),t=!1):(t&&Ae(r)&&(e+=" "),e+=i.charAt(o),t=!1)}return"url("+e+")"}var Lv="Url",Ov={value:String};function rc(){let i=this.tokenStart,e;switch(this.tokenType){case 7:e=ic(this.consume(7));break;case 2:this.cmpStr(this.tokenStart,this.tokenEnd,"url(")||this.error("Function name must be `url`"),this.eat(2),this.skipSC(),e=Co(this.consume(5)),this.skipSC(),this.eof||this.eat(22);break;default:this.error("Url or Function is expected")}return{type:"Url",loc:this.getLocation(i,this.tokenStart),value:e}}function Av(i){this.token(7,oc(i.value))}var sc={};_(sc,{generate:()=>Iv,name:()=>Mv,parse:()=>ac,structure:()=>Nv});var Mv="Value",Nv={children:[[]]};function ac(){let i=this.tokenStart,e=this.readSequence(this.scope.Value);return{type:"Value",loc:this.getLocation(i,this.tokenStart),children:e}}function Iv(i){this.children(i)}var cc={};_(cc,{generate:()=>Wv,name:()=>Dv,parse:()=>lc,structure:()=>Hv});var Uv=Object.freeze({type:"WhiteSpace",loc:null,value:" "}),Dv="WhiteSpace",Hv={value:String};function lc(){return this.eat(13),Uv}function Wv(i){this.token(13,i.value)}var Fm={generic:!0,cssWideKeywords:Ni,...sm,node:Eo};var hc={};_(hc,{AtrulePrelude:()=>Lm,Selector:()=>Am,Value:()=>Um});var Bv=35,jv=42,zm=43,Gv=45,Vv=47,Kv=117;function Po(i){switch(this.tokenType){case 4:return this.Hash();case 18:return this.Operator();case 21:return this.Parentheses(this.readSequence,i.recognizer);case 19:return this.Brackets(this.readSequence,i.recognizer);case 5:return this.String();case 12:return this.Dimension();case 11:return this.Percentage();case 10:return this.Number();case 2:return this.cmpStr(this.tokenStart,this.tokenEnd,"url(")?this.Url():this.Function(this.readSequence,i.recognizer);case 7:return this.Url();case 1:return this.cmpChar(this.tokenStart,Kv)&&this.cmpChar(this.tokenStart+1,zm)?this.UnicodeRange():this.Identifier();case 9:{let e=this.charCodeAt(this.tokenStart);if(e===Vv||e===jv||e===zm||e===Gv)return this.Operator();e===Bv&&this.error("Hex or identifier is expected",this.tokenStart+1);break}}}var Lm={getNode:Po};var Yv=35,Xv=38,$v=42,qv=43,Jv=47,Om=46,Zv=62,Qv=124,ew=126;function tw(i,e){e.last!==null&&e.last.type!=="Combinator"&&i!==null&&i.type!=="Combinator"&&e.push({type:"Combinator",loc:null,name:" "})}function iw(){switch(this.tokenType){case 19:return this.AttributeSelector();case 4:return this.IdSelector();case 16:return this.lookupType(1)===16?this.PseudoElementSelector():this.PseudoClassSelector();case 1:return this.TypeSelector();case 10:case 11:return this.Percentage();case 12:this.charCodeAt(this.tokenStart)===Om&&this.error("Identifier is expected",this.tokenStart+1);break;case 9:{switch(this.charCodeAt(this.tokenStart)){case qv:case Zv:case ew:case Jv:return this.Combinator();case Om:return this.ClassSelector();case $v:case Qv:return this.TypeSelector();case Yv:return this.IdSelector();case Xv:return this.NestingSelector()}break}}}var Am={onWhiteSpace:tw,getNode:iw};function Mm(){return this.createSingleNodeList(this.Raw(null,!1))}function Nm(){let i=this.createList();if(this.skipSC(),i.push(this.Identifier()),this.skipSC(),this.tokenType===18){i.push(this.Operator());let e=this.tokenIndex,t=this.parseCustomProperty?this.Value(null):this.Raw(this.consumeUntilExclamationMarkOrSemicolon,!1);if(t.type==="Value"&&t.children.isEmpty){for(let o=e-this.tokenIndex;o<=0;o++)if(this.lookupType(o)===13){t.children.appendData({type:"WhiteSpace",loc:null,value:" "});break}}i.push(t)}return i}function Im(i){return i!==null&&i.type==="Operator"&&(i.value[i.value.length-1]==="-"||i.value[i.value.length-1]==="+")}var Um={getNode:Po,onWhiteSpace(i,e){Im(i)&&(i.value=" "+i.value),Im(e.last)&&(e.last.value+=" ")},expression:Mm,var:Nm};var ow=new Set(["none","and","not","or"]),Dm={parse:{prelude(){let i=this.createList();if(this.tokenType===1){let e=this.substring(this.tokenStart,this.tokenEnd);ow.has(e.toLowerCase())||i.push(this.Identifier())}return i.push(this.Condition("container")),i},block(i=!1){return this.Block(i)}}};var Hm={parse:{prelude:null,block(){return this.Block(!0)}}};function dc(i,e){return this.parseWithFallback(()=>{try{return i.call(this)}finally{this.skipSC(),this.lookupNonWSType(0)!==22&&this.error()}},e||(()=>this.Raw(null,!0)))}var Wm={layer(){this.skipSC();let i=this.createList(),e=dc.call(this,this.Layer);return(e.type!=="Raw"||e.value!=="")&&i.push(e),i},supports(){this.skipSC();let i=this.createList(),e=dc.call(this,this.Declaration,()=>dc.call(this,()=>this.Condition("supports")));return(e.type!=="Raw"||e.value!=="")&&i.push(e),i}},Bm={parse:{prelude(){let i=this.createList();switch(this.tokenType){case 5:i.push(this.String());break;case 7:case 2:i.push(this.Url());break;default:this.error("String or url() is expected")}return this.skipSC(),this.tokenType===1&&this.cmpStr(this.tokenStart,this.tokenEnd,"layer")?i.push(this.Identifier()):this.tokenType===2&&this.cmpStr(this.tokenStart,this.tokenEnd,"layer(")&&i.push(this.Function(null,Wm)),this.skipSC(),this.tokenType===2&&this.cmpStr(this.tokenStart,this.tokenEnd,"supports(")&&i.push(this.Function(null,Wm)),(this.lookupNonWSType(0)===1||this.lookupNonWSType(0)===21)&&i.push(this.MediaQueryList()),i},block:null}};var jm={parse:{prelude(){return this.createSingleNodeList(this.LayerList())},block(){return this.Block(!1)}}};var Gm={parse:{prelude(){return this.createSingleNodeList(this.MediaQueryList())},block(i=!1){return this.Block(i)}}};var Vm={parse:{prelude(){return this.createSingleNodeList(this.SelectorList())},block(){return this.Block(!0)}}};var Km={parse:{prelude(){return this.createSingleNodeList(this.SelectorList())},block(){return this.Block(!0)}}};var Ym={parse:{prelude(){return this.createSingleNodeList(this.Scope())},block(i=!1){return this.Block(i)}}};var Xm={parse:{prelude:null,block(i=!1){return this.Block(i)}}};var $m={parse:{prelude(){return this.createSingleNodeList(this.Condition("supports"))},block(i=!1){return this.Block(i)}}};var qm={container:Dm,"font-face":Hm,import:Bm,layer:jm,media:Gm,nest:Vm,page:Km,scope:Ym,"starting-style":Xm,supports:$m};function Jm(){let i=this.createList();this.skipSC();e:for(;!this.eof;){switch(this.tokenType){case 1:i.push(this.Identifier());break;case 5:i.push(this.String());break;case 18:i.push(this.Operator());break;case 22:break e;default:this.error("Identifier, string or comma is expected")}this.skipSC()}return i}var oi={parse(){return this.createSingleNodeList(this.SelectorList())}},uc={parse(){return this.createSingleNodeList(this.Selector())}},rw={parse(){return this.createSingleNodeList(this.Identifier())}},nw={parse:Jm},mn={parse(){return this.createSingleNodeList(this.Nth())}},Zm={dir:rw,has:oi,lang:nw,matches:oi,is:oi,"-moz-any":oi,"-webkit-any":oi,where:oi,not:oi,"nth-child":mn,"nth-last-child":mn,"nth-last-of-type":mn,"nth-of-type":mn,slotted:uc,host:uc,"host-context":uc};var mc={};_(mc,{AnPlusB:()=>hs,Atrule:()=>us,AtrulePrelude:()=>ps,AttributeSelector:()=>ys,Block:()=>bs,Brackets:()=>vs,CDC:()=>xs,CDO:()=>Cs,ClassSelector:()=>Es,Combinator:()=>Ts,Comment:()=>zs,Condition:()=>Os,Declaration:()=>Ms,DeclarationList:()=>Us,Dimension:()=>Hs,Feature:()=>Bs,FeatureFunction:()=>Gs,FeatureRange:()=>Ys,Function:()=>$s,GeneralEnclosed:()=>Js,Hash:()=>Qs,IdSelector:()=>ol,Identifier:()=>tl,Layer:()=>nl,LayerList:()=>sl,MediaQuery:()=>cl,MediaQueryList:()=>dl,NestingSelector:()=>ml,Nth:()=>gl,Number:()=>yl,Operator:()=>bl,Parentheses:()=>vl,Percentage:()=>xl,PseudoClassSelector:()=>Cl,PseudoElementSelector:()=>El,Ratio:()=>Tl,Raw:()=>zl,Rule:()=>Ol,Scope:()=>Ml,Selector:()=>Il,SelectorList:()=>Dl,String:()=>jl,StyleSheet:()=>Vl,SupportsDeclaration:()=>Yl,TypeSelector:()=>ql,UnicodeRange:()=>Ql,Url:()=>rc,Value:()=>ac,WhiteSpace:()=>lc});var Qm={parseContext:{default:"StyleSheet",stylesheet:"StyleSheet",atrule:"Atrule",atrulePrelude(i){return this.AtrulePrelude(i.atrule?String(i.atrule):null)},mediaQueryList:"MediaQueryList",mediaQuery:"MediaQuery",condition(i){return this.Condition(i.kind)},rule:"Rule",selectorList:"SelectorList",selector:"Selector",block(){return this.Block(!0)},declarationList:"DeclarationList",declaration:"Declaration",value:"Value"},features:{supports:{selector(){return this.Selector()}},container:{style(){return this.Declaration()}}},scope:hc,atrule:qm,pseudo:Zm,node:mc};var ep={node:Eo};var tp=an({...Fm,...Qm,...ep});var aw="3.2.1";function pn(i){let e={};for(let t of Object.keys(i)){let o=i[t];o&&(Array.isArray(o)||o instanceof ue?o=o.map(pn):o.constructor===Object&&(o=pn(o))),e[t]=o}return e}var pc={};_(pc,{decode:()=>sw,encode:()=>lw});var ip=92;function sw(i){let e=i.length-1,t="";for(let o=0;o<i.length;o++){let r=i.charCodeAt(o);if(r===ip){if(o===e)break;if(r=i.charCodeAt(++o),Se(ip,r)){let n=o-1,a=Ke(i,n);o=a-1,t+=Ei(i.substring(n+1,a))}else r===13&&i.charCodeAt(o+1)===10&&o++}else t+=i[o]}return t}function lw(i){let e="";if(i.length===1&&i.charCodeAt(0)===45)return"\\-";for(let t=0;t<i.length;t++){let o=i.charCodeAt(t);if(o===0){e+="\uFFFD";continue}if(o<=31||o===127||o>=48&&o<=57&&(t===0||t===1&&i.charCodeAt(0)===45)){e+="\\"+o.toString(16)+" ";continue}ki(o)?e+=i.charAt(t):e+="\\"+i.charAt(t)}return e}var{tokenize:cw,parse:hw,generate:dw,lexer:uw,createLexer:mw,walk:pw,find:gw,findLast:fw,findAll:yw,toPlainObject:Sw,fromPlainObject:bw,fork:_w}=tp;export{Pr as EpubNavigator,Ie as Link,H as Locator,No as Manifest,Uo as Publication,Lo as Resource,op as css,ng as setScriptNonce};
