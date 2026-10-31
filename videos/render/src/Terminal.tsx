import React from 'react';
import {useCurrentFrame, useVideoConfig} from 'remotion';
import {FPS, Row, linesAt, visibleWindow, wrapLines} from './timeline';
import {C, MONO, RADIUS} from './theme';

const ADVANCE = 0.7; // Martian Mono advance width in em
const BAR = 56;
const PAD = 28;

/** Bottom-anchored terminal: new lines appear at the bottom, older lines scroll up. */
export const Terminal: React.FC<{rows: Row[]; boxStyle: React.CSSProperties; boxWidth: number; boxHeight: number}> = ({rows, boxStyle, boxWidth, boxHeight}) => {
  const frame = useCurrentFrame();
  const {width, height} = useVideoConfig();
  const portrait = height > width;
  const fs = portrait ? 34 : 30;
  const lh = fs * 1.45;
  const cols = Math.max(20, Math.floor((boxWidth - 2 * PAD) / (fs * ADVANCE)));
  const fit = Math.max(1, Math.floor((boxHeight - BAR - 2 * PAD) / lh));
  const lines = visibleWindow(wrapLines(linesAt(rows, frame), cols), fit);
  const waiting = rows.length > 0 && frame >= rows[rows.length - 1].appear + 1;
  const cursorOn = waiting && Math.floor(frame / (FPS * 0.53)) % 2 === 0;
  return (
    <div style={{...boxStyle, width: boxWidth, height: boxHeight, border: `1px solid ${C.rule}`, borderRadius: RADIUS, background: C.paper, display: 'flex', flexDirection: 'column', overflow: 'hidden'}}>
      <div style={{height: BAR, borderBottom: `1px solid ${C.rule}`, display: 'flex', alignItems: 'center', gap: 12, paddingLeft: 24, flex: 'none'}}>
        {[0, 1, 2].map((i) => (
          <div key={i} style={{width: 16, height: 16, borderRadius: 8, background: C.rule}} />
        ))}
      </div>
      {/* justify-content: flex-end pins the transcript to the bottom of the box */}
      <div data-testid="terminal-body" style={{flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'flex-end', padding: PAD, fontFamily: MONO, fontSize: fs, lineHeight: `${lh}px`, color: C.ink, whiteSpace: 'pre'}}>
        {lines.map((l, i) => (
          <div key={i}>
            <span style={{color: C.muted}}>{l.startsWith('$ ') ? '$ ' : ''}</span>
            {l.startsWith('$ ') ? l.slice(2) : l}
            {i === lines.length - 1 && cursorOn ? <span style={{background: C.ink}}>{' '}</span> : null}
          </div>
        ))}
      </div>
    </div>
  );
};
