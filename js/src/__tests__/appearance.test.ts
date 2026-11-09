import {expect,it} from 'vitest';
import {defaultDisplayOptions,displayOptions,NORD_THEME,terminalPreferences} from '../../packages/terminal-core/appearance';
it('uses the same Nord palette and Source Code Pro metrics in both clients',()=>{
 const options=defaultDisplayOptions();expect(options.fontSize).toBe(14);expect(options.fontFamily).toBe("'Source Code Pro', monospace");expect(options.theme).toEqual(NORD_THEME);expect(options.scrollback).toBe(500);
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
