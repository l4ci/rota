import React from 'react';
import {AbsoluteFill, Audio, staticFile, useCurrentFrame, useVideoConfig} from 'remotion';
import capture from './capture.json';

const CAPTION = 'Coding agents write fast. rota makes sure what they merge works.';
const FONT = '"DejaVu Sans Mono", "Liberation Mono", Menlo, monospace'; // Martian Mono not bundled in spike

export const Clip: React.FC<{portrait: boolean}> = ({portrait}) => {
  const frame = useCurrentFrame();
  const {fps, durationInFrames} = useVideoConfig();
  const text: string = capture.stdout;
  // type across first 80% of the clip, deterministic function of frame
  const typeFrames = durationInFrames * 0.8;
  const n = Math.min(text.length, Math.floor((frame / typeFrames) * text.length));
  const shown = text.slice(0, n);
  const lines = shown.split('\n');
  const maxLines = portrait ? 46 : 26;
  const visible = lines.slice(-maxLines);
  const cursorOn = Math.floor(frame / (fps / 2)) % 2 === 0;
  const fs = portrait ? 34 : 30;
  return (
    <AbsoluteFill style={{background: '#0D1117', color: '#F0F6FC', fontFamily: FONT, padding: portrait ? 60 : 80}}>
      <div style={{flex: 1, border: '2px solid #30363D', borderRadius: 16, background: '#010409', overflow: 'hidden', display: 'flex', flexDirection: 'column'}}>
        <div style={{height: 56, background: '#161B22', display: 'flex', alignItems: 'center', gap: 12, paddingLeft: 24}}>
          {['#FF5F56', '#FFBD2E', '#27C93F'].map((c) => (
            <div key={c} style={{width: 18, height: 18, borderRadius: 9, background: c}} />
          ))}
        </div>
        <pre style={{margin: 0, padding: 28, fontSize: fs, lineHeight: 1.35, whiteSpace: 'pre-wrap'}}>
          {visible.join('\n')}
          <span style={{opacity: cursorOn ? 1 : 0, background: '#F0F6FC'}}>{' '}</span>
        </pre>
      </div>
      <div style={{marginTop: 40, fontSize: portrait ? 52 : 48, fontFamily: 'sans-serif', fontWeight: 600, textAlign: 'center', minHeight: 130}}>{CAPTION}</div>
      <Audio src={staticFile('vo.wav')} />
    </AbsoluteFill>
  );
};
