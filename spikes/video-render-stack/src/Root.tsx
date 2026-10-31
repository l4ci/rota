import React from 'react';
import {Composition} from 'remotion';
import {Clip} from './Clip';
const FPS = 30;
const DUR = Math.ceil(4.255 * FPS);
export const Root: React.FC = () => (
  <>
    <Composition id="Landscape" component={Clip} durationInFrames={DUR} fps={FPS} width={1920} height={1080} defaultProps={{portrait: false}} />
    <Composition id="Portrait" component={Clip} durationInFrames={DUR} fps={FPS} width={1080} height={1920} defaultProps={{portrait: true}} />
  </>
);
