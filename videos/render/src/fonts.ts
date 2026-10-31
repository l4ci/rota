import {continueRender, delayRender, staticFile} from 'remotion';

// Bundled static fonts: a render must never fall back to a system font.
const FACES: [string, string][] = [
  ['Martian Mono', 'fonts/martian-mono-normal-latin.woff2'],
  ['Atkinson Hyperlegible Next', 'fonts/atkinson-hyperlegible-next-normal-latin.woff2'],
];

let started = false;
export function loadFonts() {
  if (started) return;
  started = true;
  const handle = delayRender('fonts');
  Promise.all(
    FACES.map(async ([family, file]) => {
      const face = new FontFace(family, `url(${staticFile(file)}) format("woff2")`, {weight: '100 800'});
      await face.load();
      (document.fonts as unknown as {add(f: FontFace): void}).add(face);
    }),
  )
    .then(() => continueRender(handle))
    .catch((e) => {
      throw e;
    });
}
