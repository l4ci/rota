import React from 'react';
import {AbsoluteFill, Easing, interpolate, useCurrentFrame, useVideoConfig} from 'remotion';
import {C, SANS} from './theme';

/**
 * Generic motion shot: the voiceover line as a one-idea title card. The script's
 * scene: prose is a brief for a designed scene; until a shot gets its own
 * component this card carries the idea so the episode still renders end to end.
 */
export const Motion: React.FC<{text: string}> = ({text}) => {
  const frame = useCurrentFrame();
  const {width, height} = useVideoConfig();
  const portrait = height > width;
  const t = interpolate(frame, [0, 8], [0, 1], {easing: Easing.out(Easing.cubic), extrapolateRight: 'clamp'});
  return (
    <AbsoluteFill style={{justifyContent: 'center', padding: portrait ? '96px 64px 360px' : '96px 96px 220px'}}>
      <div style={{fontFamily: SANS, fontWeight: 600, fontSize: portrait ? 88 : 96, lineHeight: 1.12, color: C.ink, opacity: t, transform: `translateY(${(1 - t) * 16}px)`, maxWidth: portrait ? 952 : 1500}}>{text}</div>
    </AbsoluteFill>
  );
};
