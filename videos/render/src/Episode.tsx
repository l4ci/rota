import React from 'react';
import {AbsoluteFill, Audio, Sequence, staticFile, useVideoConfig} from 'remotion';
import {Captions} from './Captions';
import {Intro} from './Intro';
import {LowerThird} from './LowerThird';
import {Motion} from './Motion';
import {Outro} from './Outro';
import {Terminal} from './Terminal';
import {Capture, Clip, OUTRO_FRAMES, OUTRO_VO, Shot, buildTimeline} from './timeline';
import {loadFonts} from './fonts';
import {C} from './theme';

export type EpisodeProps = {script: {slug: string; shots: Shot[]}; captures: Capture[]; clips: Clip[]; outroClip: Clip};

const SAFE = 64;
const SAFE_BOTTOM_PORTRAIT = 96;

export const Episode: React.FC<EpisodeProps> = ({script, captures, clips, outroClip}) => {
  loadFonts();
  const {width, height} = useVideoConfig();
  const portrait = height > width;
  const tl = buildTimeline(script.shots, captures, clips);
  const side = portrait ? SAFE : 96;
  const boxW = width - 2 * side;
  const captionH = portrait ? 260 : 200;
  const top = portrait ? 96 : 80;
  const bottom = portrait ? SAFE_BOTTOM_PORTRAIT : 64;
  const boxH = height - top - bottom - captionH - 24;
  return (
    <AbsoluteFill style={{background: C.paper}}>
      <Sequence from={0} durationInFrames={45}>
        <Intro />
      </Sequence>
      {tl.shots.map((s) => (
        <Sequence key={s.n} from={s.start} durationInFrames={s.frames}>
          {s.kind === 'terminal' ? (
            <Terminal rows={s.rows} boxWidth={boxW} boxHeight={boxH} boxStyle={{position: 'absolute', left: side, top}} />
          ) : (
            <Motion text={s.caption} />
          )}
          {s.lower_third ? <LowerThird text={s.lower_third} style={{position: 'absolute', left: side, top: top + 72}} /> : null}
          <Audio src={staticFile('audio/' + s.audio)} />
          <Captions text={s.caption} voFrames={s.voFrames} style={{position: 'absolute', left: '50%', transform: 'translateX(-50%)', bottom: bottom + (portrait ? 0 : 16)}} />
        </Sequence>
      ))}
      <Sequence from={tl.outroStart} durationInFrames={OUTRO_FRAMES}>
        <Outro />
        <Audio src={staticFile('audio/' + outroClip.file)} />
      </Sequence>
    </AbsoluteFill>
  );
};

export const totalFrames = (p: EpisodeProps) => buildTimeline(p.script.shots, p.captures, p.clips).total;
export {OUTRO_VO};
