import React from 'react';
import {Composition} from 'remotion';
import {Episode, EpisodeProps, totalFrames} from './Episode';
import {FPS} from './timeline';

const defaults: EpisodeProps = {
  script: {slug: 'empty', shots: []},
  captures: [],
  clips: [],
  outroClip: {n: 'outro', file: 'outro.wav', seconds: 3},
};

// Duration comes from the inputs (voiceover lengths), never from the clock.
const meta = ({props}: {props: EpisodeProps}) => ({durationInFrames: props.script.shots.length ? totalFrames(props) : 150});

export const Root: React.FC = () => (
  <>
    <Composition id="Landscape" component={Episode} width={1920} height={1080} fps={FPS} durationInFrames={150} defaultProps={defaults} calculateMetadata={meta} />
    <Composition id="Portrait" component={Episode} width={1080} height={1920} fps={FPS} durationInFrames={150} defaultProps={defaults} calculateMetadata={meta} />
  </>
);
