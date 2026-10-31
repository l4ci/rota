import React from 'react';
import {AbsoluteFill, useCurrentFrame, useVideoConfig} from 'remotion';
import {C, MONO, RADIUS, SANS} from './theme';
import {Anim, Item, activeIndex, appearFrame, enter, fillAt, parseItems} from './sceneTiming';

// Reusable animated scenes for motion shots (STYLE.md, Motion graphics). A shot picks one
// with `anim: <name> | key=value` in its script. Each scene gets the shot's voiceover
// length and builds in step with it; on screen text adds to the voice, it does not repeat it.

type Tone = 'rule' | 'duty' | 'merged' | 'ask' | 'danger';
const BORDER: Record<Tone, string> = {rule: C.rule, duty: C.duty, merged: C.merged, ask: C.ask, danger: C.danger};
const WASH: Record<Tone, string> = {rule: 'transparent', duty: C.dutyWash, merged: C.mergedWash, ask: C.askWash, danger: 'transparent'};
const tone = (v: string | undefined, fallback: Tone): Tone => (v && v in BORDER ? (v as Tone) : fallback);

type SceneProps = {anim: Anim; voFrames: number};

const Stage: React.FC<{children: React.ReactNode}> = ({children}) => {
  const {width, height} = useVideoConfig();
  const portrait = height > width;
  return (
    <AbsoluteFill style={{justifyContent: 'center', alignItems: 'flex-start', padding: portrait ? '96px 64px 360px' : '96px 96px 220px'}}>
      {children}
    </AbsoluteFill>
  );
};

const useLayout = () => {
  const {width, height} = useVideoConfig();
  const portrait = height > width;
  return {portrait, label: portrait ? 44 : 48, detail: portrait ? 32 : 34, title: portrait ? 56 : 64};
};

const Card: React.FC<{item: Item; mono?: boolean; tone: Tone; at: number; grow?: boolean}> = ({item, mono, tone: t, at, grow}) => {
  const frame = useCurrentFrame();
  const L = useLayout();
  const e = enter(frame, at);
  return (
    <div
      style={{
        opacity: e.opacity,
        transform: `translateY(${e.dy}px)`,
        border: `2px solid ${BORDER[t]}`,
        background: WASH[t],
        borderRadius: RADIUS,
        padding: '24px 32px',
        flex: grow ? 1 : undefined,
        minWidth: 0,
      }}
    >
      <div style={{fontFamily: mono ? MONO : SANS, fontWeight: mono ? 500 : 600, fontSize: mono ? L.label - 8 : L.label, color: C.ink, lineHeight: 1.2}}>{item.label}</div>
      {item.detail ? <div style={{fontFamily: SANS, fontSize: L.detail, color: C.muted, marginTop: 8, lineHeight: 1.25}}>{item.detail}</div> : null}
    </div>
  );
};

const Arrow: React.FC<{at: number; active: boolean; vertical: boolean}> = ({at, active, vertical}) => {
  const frame = useCurrentFrame();
  const e = enter(frame, at);
  const color = active ? C.duty : C.muted;
  return vertical ? (
    <div style={{opacity: e.opacity, display: 'flex', flexDirection: 'column', alignItems: 'center', height: 56}}>
      <div style={{width: 2, flex: 1, background: color}} />
      <div style={{width: 0, height: 0, borderLeft: '8px solid transparent', borderRight: '8px solid transparent', borderTop: `12px solid ${color}`}} />
    </div>
  ) : (
    <div style={{opacity: e.opacity, display: 'flex', alignItems: 'center', width: 72, flexShrink: 0}}>
      <div style={{height: 2, flex: 1, background: color}} />
      <div style={{width: 0, height: 0, borderTop: '8px solid transparent', borderBottom: '8px solid transparent', borderLeft: `12px solid ${color}`}} />
    </div>
  );
};

/** Before/after style split: two titled panels, the left builds first, the right second. */
export const Split: React.FC<SceneProps> = ({anim: {params: p}, voFrames}) => {
  const L = useLayout();
  const left = parseItems(p['left-items']);
  const right = parseItems(p['right-items']);
  const rt = tone(p['right-tone'], 'ask');
  const panel = (title: string, items: Item[], t: Tone, from: number, to: number) => (
    <div style={{flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 20}}>
      <div style={{fontFamily: SANS, fontWeight: 600, fontSize: L.title, color: t === 'duty' ? C.duty : C.ink, opacity: enter(useCurrentFrame(), appearFrame(0, 1, voFrames, from)).opacity}}>{title}</div>
      {items.map((it, i) => (
        <Card key={it.label} item={it} tone={t} at={appearFrame(i, items.length, voFrames, from + 0.04, to)} />
      ))}
    </div>
  );
  return (
    <Stage>
      <div style={{display: 'flex', flexDirection: L.portrait ? 'column' : 'row', gap: L.portrait ? 40 : 64, width: '100%'}}>
        {panel(p.left ?? '', left, 'duty', 0, 0.3)}
        {panel(p.right ?? '', right, rt, 0.4, 0.8)}
      </div>
    </Stage>
  );
};

/** Keyword cards that build up with the voice; `frame=repo` sets them inside a repo outline. */
export const Cards: React.FC<SceneProps> = ({anim: {params: p}, voFrames}) => {
  const L = useLayout();
  const frame = useCurrentFrame();
  const items = parseItems(p.items);
  const mono = p.mono === 'true';
  const last = tone(p['last-tone'], 'rule');
  const inner = (
    <div style={{display: 'flex', flexDirection: L.portrait ? 'column' : 'row', gap: 24, width: '100%'}}>
      {items.map((it, i) => (
        <Card key={it.label} item={it} mono={mono} grow tone={i === items.length - 1 && last !== 'rule' ? last : 'rule'} at={appearFrame(i, items.length, voFrames)} />
      ))}
    </div>
  );
  return (
    <Stage>
      {p.frame ? (
        <div style={{width: '100%', border: `2px solid ${C.rule}`, borderRadius: RADIUS, padding: 32, opacity: enter(frame, 0).opacity}}>
          <div style={{fontFamily: MONO, fontSize: 32, color: C.muted, marginBottom: 24}}>{p.frame}</div>
          {inner}
        </div>
      ) : (
        inner
      )}
    </Stage>
  );
};

/** Steps joined by arrows. The active step turns `duty` as the voice reaches it. With `bounce=<step>`
 *  the last step is `merged` (a gate flow) and a failing token is sent back at that step. */
export const Flow: React.FC<SceneProps> = ({anim: {params: p}, voFrames}) => {
  const L = useLayout();
  const frame = useCurrentFrame();
  const steps = parseItems(p.steps);
  const mono = p.mono === 'true';
  const n = steps.length;
  const act = activeIndex(frame, n, voFrames, 0, 0.7);
  const bounce = p.bounce ? steps.findIndex((s) => s.label === p.bounce) : -1;
  const bounceAt = Math.round(voFrames * 0.82);
  const vertical = L.portrait;
  return (
    <Stage>
      <div style={{display: 'flex', flexDirection: vertical ? 'column' : 'row', alignItems: vertical ? 'stretch' : 'flex-start', width: '100%'}}>
        {steps.map((s, i) => {
          const at = appearFrame(i, n, voFrames, 0, 0.7);
          const isLast = i === n - 1;
          const t: Tone = i === act ? (isLast && bounce >= 0 ? 'merged' : 'duty') : 'rule';
          return (
            <React.Fragment key={s.label}>
              {i > 0 ? <Arrow at={at} active={i <= act} vertical={vertical} /> : null}
              <div style={{flex: vertical ? undefined : 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 12}}>
                <Card item={s} mono={mono} tone={t} at={at} />
                {i === bounce ? (
                  <div style={{opacity: enter(frame, bounceAt).opacity, transform: `translateY(${enter(frame, bounceAt).dy}px)`, border: `2px solid ${C.danger}`, borderRadius: RADIUS, padding: '12px 20px', fontFamily: SANS, fontSize: L.detail, color: C.danger}}>
                    second PR: sent back
                  </div>
                ) : null}
              </div>
            </React.Fragment>
          );
        })}
      </div>
    </Stage>
  );
};

/** Parallel worktrees: each lane fills in its own time, turns `merged`, and all feed one target. */
export const Lanes: React.FC<SceneProps> = ({anim: {params: p}, voFrames}) => {
  const L = useLayout();
  const frame = useCurrentFrame();
  const lanes = parseItems(p.lanes);
  const n = lanes.length;
  const end = p.end ?? 'main';
  const doneAt = (i: number) => Math.round(voFrames * (0.5 + 0.12 * i));
  const allDone = lanes.length > 0 && frame >= doneAt(n - 1);
  const endIn = enter(frame, doneAt(n - 1));
  return (
    <Stage>
      <div style={{display: 'flex', flexDirection: 'column', gap: 28, width: '100%'}}>
        {lanes.map((lane, i) => {
          const at = appearFrame(i, n, voFrames, 0, 0.25);
          const e = enter(frame, at);
          const f = fillAt(frame, at + 6, doneAt(i));
          const done = f >= 1;
          return (
            <div key={lane.label} style={{opacity: e.opacity, transform: `translateY(${e.dy}px)`, display: 'flex', alignItems: 'center', gap: 24}}>
              <div style={{fontFamily: MONO, fontWeight: 500, fontSize: L.label - 8, color: C.ink, width: L.portrait ? 200 : 260}}>{lane.label}</div>
              <div style={{flex: 1, height: 40, border: `2px solid ${done ? C.merged : C.rule}`, borderRadius: RADIUS}}>
                <div style={{width: `${f * 100}%`, height: '100%', background: done ? C.merged : C.duty}} />
              </div>
              <div style={{fontFamily: SANS, fontSize: L.detail, color: done ? C.merged : C.muted, width: L.portrait ? 0 : 180, overflow: 'hidden', whiteSpace: 'nowrap'}}>{done ? 'verified' : 'building'}</div>
            </div>
          );
        })}
        <div style={{opacity: endIn.opacity, transform: `translateY(${endIn.dy}px)`, alignSelf: 'flex-start', border: `2px solid ${allDone ? C.merged : C.rule}`, background: allDone ? C.mergedWash : 'transparent', borderRadius: RADIUS, padding: '16px 32px', fontFamily: MONO, fontSize: L.label - 8, color: C.ink}}>
          {end}
        </div>
      </div>
    </Stage>
  );
};

export const SCENES: Record<string, React.FC<SceneProps>> = {split: Split, cards: Cards, flow: Flow, lanes: Lanes};
