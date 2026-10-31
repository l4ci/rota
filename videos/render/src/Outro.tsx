import React from 'react';
import {AbsoluteFill, Easing, Img, interpolate, staticFile, useCurrentFrame, useVideoConfig} from 'remotion';
import {C, MONO, RADIUS, SANS} from './theme';

export const REPO_LINK = 'github.com/l4ci/rota';

/** 4 s outro card: tagline, then the repo link on a duty-wash band. Two-line link in 9:16. */
export const Outro: React.FC = () => {
  const frame = useCurrentFrame();
  const {width, height} = useVideoConfig();
  const portrait = height > width;
  const fade = interpolate(frame, [0, 9], [0, 1], {easing: Easing.out(Easing.cubic), extrapolateRight: 'clamp'});
  const [host, repo] = [REPO_LINK.slice(0, REPO_LINK.indexOf('/') + 1), REPO_LINK.slice(REPO_LINK.indexOf('/') + 1)];
  return (
    <AbsoluteFill style={{background: C.paper, opacity: fade, padding: portrait ? '96px 64px' : 96, justifyContent: 'center', gap: 36}}>
      <Img src={staticFile('brand/rota-icon.svg')} style={{width: 64, height: 64}} />
      <div style={{fontFamily: SANS, fontWeight: 600, fontSize: portrait ? 72 : 80, lineHeight: 1.15, color: C.ink}}>Coding agents write fast.</div>
      <div style={{fontFamily: SANS, fontWeight: 600, fontSize: portrait ? 72 : 80, lineHeight: 1.15, color: C.duty}}>rota makes sure what they merge works.</div>
      <div
        data-testid="repo-link"
        style={{marginTop: 24, alignSelf: 'flex-start', background: C.dutyWash, borderRadius: RADIUS, padding: '24px 40px', fontFamily: MONO, fontSize: portrait ? 64 : 72, color: C.ink, lineHeight: 1.25}}
      >
        {portrait ? (
          <>
            {host}
            <br />
            {repo}
          </>
        ) : (
          REPO_LINK
        )}
      </div>
    </AbsoluteFill>
  );
};
