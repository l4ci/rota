import React from 'react';
import {AbsoluteFill, Easing, Img, interpolate, staticFile, useCurrentFrame, useVideoConfig} from 'remotion';
import {C} from './theme';

const ease = Easing.out(Easing.cubic);

/** 1.5 s logo sting: the icon fades and scales in (no rotation), the wordmark fades in beneath it. */
export const Intro: React.FC = () => {
  const frame = useCurrentFrame();
  const {width, height} = useVideoConfig();
  const portrait = height > width;
  const iconIn = interpolate(frame, [0, 18], [0, 1], {easing: ease, extrapolateRight: 'clamp'});
  const scale = interpolate(frame, [0, 18], [0.88, 1], {easing: ease, extrapolateRight: 'clamp'});
  const wordIn = interpolate(frame, [27, 36], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});
  const icon = portrait ? 220 : 240;
  return (
    <AbsoluteFill style={{background: C.paper, alignItems: 'center', justifyContent: portrait ? 'flex-start' : 'center', paddingTop: portrait ? height * 0.4 - icon : 0}}>
      <div style={{display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 48}}>
        <Img src={staticFile('brand/rota-icon.svg')} style={{width: icon, height: icon, opacity: iconIn, transform: `scale(${scale})`}} />
        <Img src={staticFile('brand/rota-logo.svg')} style={{width: portrait ? 420 : 360, opacity: wordIn}} />
      </div>
    </AbsoluteFill>
  );
};
