import {expect,it} from 'vitest';
import {BUILTIN_THEMES,defaultDisplayOptions,displayOptions,NORD_THEME,terminalPreferences} from '../../packages/terminal-core/appearance';
it('uses the same Nord palette and explicit DejaVu rendering baseline in both clients',()=>{
 const options=defaultDisplayOptions();expect(options).toMatchObject({fontSize:14,fontFamily:"'DejaVu Sans Mono', monospace",fontWeight:400,fontWeightBold:700,lineHeight:1,letterSpacing:0});expect(options.theme).toEqual(NORD_THEME);expect(options.scrollback).toBe(500);
});
it('keeps remote font size, scrollback and cursor settings while applying picker choices',()=>{
 const prefs={'font-size':20,'scrollback-lines':900,'cursor-blink':true,'cursor-style':'bar'};
 const value=displayOptions(prefs,{nord:NORD_THEME,light:{background:'white',foreground:'black'}},{theme:'light',fontFamily:'Menlo, monospace'});
 expect(value).toMatchObject({fontSize:20,scrollback:900,cursorBlink:true,cursorStyle:'bar',fontFamily:'Menlo, monospace',theme:{background:'white'}});
 expect(displayOptions(prefs,{}, {fontSize:24}).fontSize).toBe(24);
});
it('translates legacy theme selection and discards invalid preference values',()=>{
 expect(terminalPreferences({'font-size':NaN,'cursor-style':'invalid','scrollback-lines':-1})).toEqual({});
 expect(terminalPreferences({theme:{selection:'#123456'}}).theme?.selectionBackground).toBe('#123456');
});
it('keeps Paper text, ANSI colors and the cursor readable on its light surfaces',()=>{
 const paper=BUILTIN_THEMES.paper;
 const luminance=(hex:string)=>{
  const linear=[1,3,5].map(index=>parseInt(hex.slice(index,index+2),16)/255).map(value=>value<=0.04045?value/12.92:((value+0.055)/1.055)**2.4);
  return linear[0]*0.2126+linear[1]*0.7152+linear[2]*0.0722;
 };
 const contrast=(a:string,b:string)=>(Math.max(luminance(a),luminance(b))+0.05)/(Math.min(luminance(a),luminance(b))+0.05);
 for(const name of ['foreground','black','red','green','yellow','blue','magenta','cyan','white','brightBlack','brightRed','brightGreen','brightYellow','brightBlue','brightMagenta','brightCyan','brightWhite']) {
  expect(contrast(paper[name],paper.background),name).toBeGreaterThanOrEqual(4.5);
 }
 expect(contrast(paper.foreground,paper.selection)).toBeGreaterThanOrEqual(4.5);
 expect(contrast(paper.cursor,paper.background)).toBeGreaterThanOrEqual(3);
 expect(contrast(paper.cursorAccent,paper.cursor)).toBeGreaterThanOrEqual(4.5);
});
