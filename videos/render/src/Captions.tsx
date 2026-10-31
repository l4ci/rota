import React from 'react';
import {useCurrentFrame, useVideoConfig} from 'remotion';
import {captionAt, captionChunks} from './timeline';
import {C, MONO, RADIUS, SANS} from './theme';

// Skill names (/rota-work) and issue IDs (#12) in a caption render in Martian Mono.
const CODE_RE = /(\/rota-[a-z-]+|#\d+)/g;

/** Burned-in caption band, ink on paper at 85% opacity. Shown for the shot's voiceover. */
export const Captions: React.FC<{text: string; voFrames: number; style?: React.CSSProperties}> = ({text, voFrames, style}) => {
  const frame = useCurrentFrame();
  const {width, height} = useVideoConfig();
  const portrait = height > width;
  const chunk = captionAt(captionChunks(text), voFrames, frame);
  if (!chunk) return null;
  return (
    <div data-testid="caption" style={{...style, background: 'rgba(13,17,23,0.85)', borderRadius: RADIUS, padding: '16px 28px', fontFamily: SANS, fontWeight: 400, fontSize: portrait ? 52 : 44, lineHeight: 1.25, color: C.ink, textAlign: 'center', maxWidth: portrait ? 952 : 1500}}>
      {chunk.split(CODE_RE).map((part, i) => (i % 2 ? <span key={i} style={{fontFamily: MONO, fontSize: '0.88em'}}>{part}</span> : part))}
    </div>
  );
};
