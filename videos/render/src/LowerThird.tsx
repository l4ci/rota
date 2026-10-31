import React from 'react';
import {useCurrentFrame, useVideoConfig} from 'remotion';
import {lowerThirdOpacity, parseLowerThird} from './timeline';
import {C, MONO, SANS} from './theme';

/** Verb or skill in Martian Mono over a one-line gloss: duty bar left, duty-wash band. */
export const LowerThird: React.FC<{text: string; style?: React.CSSProperties}> = ({text, style}) => {
  const frame = useCurrentFrame();
  const {width, height} = useVideoConfig();
  const portrait = height > width;
  const {mono, gloss} = parseLowerThird(text);
  const o = lowerThirdOpacity(frame);
  if (o === 0) return null;
  return (
    <div style={{...style, opacity: o, background: C.dutyWash, borderLeft: `3px solid ${C.duty}`, padding: '12px 24px', display: 'flex', flexDirection: 'column', gap: 4}}>
      <span style={{fontFamily: MONO, fontSize: portrait ? 40 : 36, color: C.ink}}>{mono}</span>
      <span style={{fontFamily: SANS, fontSize: portrait ? 34 : 30, color: C.muted}}>{gloss}</span>
    </div>
  );
};
