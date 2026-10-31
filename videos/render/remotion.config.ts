import {Config} from '@remotion/cli/config';

// PNG frames keep flat-colour text crisp and the encode deterministic; mp4 (h264/aac) is the output.
Config.setVideoImageFormat('png');
Config.setCodec('h264');
Config.setOverwriteOutput(true);
