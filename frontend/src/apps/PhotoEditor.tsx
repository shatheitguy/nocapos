import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type PointerEvent as RPointerEvent } from 'react';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import {
  aspectCrop,
  autoLevels,
  clampCrop,
  colorMatrix,
  FILTER_IDS,
  histogram,
  isUnchanged,
  NEW_EDIT,
  NO_ADJUST,
  record,
  redo,
  saveType,
  startHistory,
  straightenScale,
  turnedSize,
  undo,
  applyMatrix,
  type Adjust,
  type Aspect,
  type Crop,
  type Edit,
  type FilterId,
} from '../lib/photoEdit';
import { confirmDialog } from '../state/confirm';
import { toast } from '../state/toasts';

type Tab = 'adjust' | 'crop' | 'filters';
const SLIDERS: (keyof Adjust)[] = ['brightness', 'contrast', 'saturation', 'warmth'];
const ASPECTS: Aspect[] = ['free', '1:1', '4:3', '16:9'];

/** Draws the edit onto a canvas at full size and returns it as a file of the picture's type. */
export async function renderEdit(src: string, name: string, e: Edit): Promise<Blob> {
  const blob = await (await fetch(src)).blob();
  // Decoded off the main thread.
  const bmp = await createImageBitmap(blob);
  const t = turnedSize(bmp.width, bmp.height, e.turns);
  const out = { w: Math.max(1, Math.round(t.w * e.crop.w)), h: Math.max(1, Math.round(t.h * e.crop.h)) };
  const canvas = document.createElement('canvas');
  canvas.width = out.w;
  canvas.height = out.h;
  const ctx = canvas.getContext('2d', { willReadFrequently: true })!;
  ctx.imageSmoothingQuality = 'high';
  ctx.translate(-e.crop.x * t.w + t.w / 2, -e.crop.y * t.h + t.h / 2);
  ctx.rotate(((e.turns * 90 + e.straighten) * Math.PI) / 180);
  const s = straightenScale(t.w, t.h, e.straighten);
  ctx.scale(s, s);
  ctx.drawImage(bmp, -bmp.width / 2, -bmp.height / 2);
  bmp.close();
  const m = colorMatrix(e);
  if (m.some((v, i) => v !== [1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 1, 0][i])) {
    // In bands, so a big picture never needs two full copies at once.
    const band = Math.max(1, Math.floor(4_000_000 / out.w));
    for (let y = 0; y < out.h; y += band) {
      const h = Math.min(band, out.h - y);
      const img = ctx.getImageData(0, y, out.w, h);
      applyMatrix(img.data, m);
      ctx.putImageData(img, 0, y);
    }
  }
  const type = saveType(name);
  const file = await new Promise<Blob | null>((res) => canvas.toBlob(res, type, 0.92));
  if (!file) throw new Error('encode');
  return file;
}

/**
 * Simple edits on a picture: light and colour, crop and straighten, a few
 * NoCap looks. The preview uses the same colour matrix as the saved file.
 * Saved as a copy by default; replacing the original asks first.
 */
export function PhotoEditor({
  src,
  name,
  canReplace = true,
  onCancel,
  onSave,
}: {
  src: string;
  name: string;
  /** Whether the original can be written back in its own format (else only copies). */
  canReplace?: boolean;
  onCancel: () => void;
  onSave: (file: Blob, replace: boolean) => Promise<boolean>;
}) {
  const t = useT();
  const fid = useId().replace(/:/g, '');
  const [hist, setHist] = useState(() => startHistory<Edit>(NEW_EDIT));
  const [draft, setDraft] = useState<Edit | null>(null);
  const edit = draft ?? hist.present;
  const [tab, setTab] = useState<Tab>('adjust');
  const [nat, setNat] = useState<{ w: number; h: number } | null>(null);
  const [stage, setStage] = useState({ w: 0, h: 0 });
  const [busy, setBusy] = useState(false);
  const stageRef = useRef<HTMLDivElement>(null);
  const imgRef = useRef<HTMLImageElement>(null);

  const commit = (next: Edit) => {
    setDraft(null);
    setHist((h) => record(h, next));
  };
  const live = (next: Edit) => setDraft(next);
  const settle = () => draft && commit(draft);

  useLayoutEffect(() => {
    const el = stageRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setStage({ w: el.clientWidth, h: el.clientHeight }));
    ro.observe(el);
    setStage({ w: el.clientWidth, h: el.clientHeight });
    return () => ro.disconnect();
  }, []);

  // Keys: undo / redo, Escape cancels.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'z') {
        e.preventDefault();
        e.stopPropagation();
        setHist((h) => (e.shiftKey ? redo(h) : undo(h)));
      } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'y') {
        e.preventDefault();
        e.stopPropagation();
        setHist((h) => redo(h));
      } else if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        onCancel();
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [onCancel]);

  // The picture, turned, fitted into the stage.
  const turned = nat ? turnedSize(nat.w, nat.h, edit.turns) : { w: 4, h: 3 };
  const pad = 28;
  const fit = stage.w && stage.h ? Math.min((stage.w - pad * 2) / turned.w, (stage.h - pad * 2) / turned.h) : 0;
  const frame = { w: turned.w * fit, h: turned.h * fit };
  const imgBox = nat ? { w: nat.w * fit, h: nat.h * fit } : { w: 0, h: 0 };
  const scale = straightenScale(turned.w, turned.h, edit.straighten);
  const matrix = useMemo(() => colorMatrix(edit).map((v) => +v.toFixed(5)).join(' '), [edit]);

  const autoEnhance = () => {
    const img = imgRef.current;
    if (!img || !img.naturalWidth) return;
    if (edit.levels) return commit({ ...edit, levels: null });
    // A small copy is enough to read the tones.
    const c = document.createElement('canvas');
    const k = Math.min(1, 256 / Math.max(img.naturalWidth, img.naturalHeight));
    c.width = Math.max(1, Math.round(img.naturalWidth * k));
    c.height = Math.max(1, Math.round(img.naturalHeight * k));
    const ctx = c.getContext('2d', { willReadFrequently: true })!;
    ctx.drawImage(img, 0, 0, c.width, c.height);
    const lv = autoLevels(histogram(ctx.getImageData(0, 0, c.width, c.height).data));
    commit({ ...edit, levels: lv ?? { lo: 0, hi: 255 }, adjust: { ...edit.adjust, saturation: edit.adjust.saturation || 8 } });
  };

  // ---------- crop box dragging ----------
  const drag = useRef<{ mode: 'move' | 'nw' | 'ne' | 'sw' | 'se'; x: number; y: number; from: Crop } | null>(null);
  const onCropDown = (mode: 'move' | 'nw' | 'ne' | 'sw' | 'se') => (e: RPointerEvent) => {
    e.stopPropagation();
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { mode, x: e.clientX, y: e.clientY, from: edit.crop };
  };
  const onCropMove = (e: RPointerEvent) => {
    const d = drag.current;
    if (!d || !frame.w) return;
    const dx = (e.clientX - d.x) / frame.w;
    const dy = (e.clientY - d.y) / frame.h;
    const f = d.from;
    let c: Crop;
    if (d.mode === 'move') c = { ...f, x: f.x + dx, y: f.y + dy };
    else {
      const left = d.mode === 'nw' || d.mode === 'sw';
      const top = d.mode === 'nw' || d.mode === 'ne';
      const w = Math.max(0.05, f.w + (left ? -dx : dx));
      const h = Math.max(0.05, f.h + (top ? -dy : dy));
      c = clampCrop({ x: left ? f.x + f.w - w : f.x, y: top ? f.y + f.h - h : f.y, w, h }, edit.aspect, turned.w, turned.h);
      // A fixed ratio trims the height: keep the dragged edge where the finger is.
      if (top) c = { ...c, y: Math.max(0, f.y + f.h - c.h) };
    }
    live({ ...edit, crop: clampCrop(c, edit.aspect, turned.w, turned.h) });
  };
  const onCropUp = () => {
    drag.current = null;
    settle();
  };

  const setAspect = (aspect: Aspect) => commit({ ...edit, aspect, crop: aspectCrop(aspect, turned.w, turned.h) });
  const turn = (d: number) => {
    const next = { ...edit, turns: edit.turns + d };
    const tw = nat ? turnedSize(nat.w, nat.h, next.turns) : turned;
    commit({ ...next, crop: edit.aspect === 'free' ? NEW_EDIT.crop : aspectCrop(edit.aspect, tw.w, tw.h) });
  };

  const save = async (replace: boolean) => {
    if (replace && !(await confirmDialog({ title: t('pv.edit.replaceAsk'), message: t('pv.edit.replaceBody'), confirmLabel: t('pv.edit.replace'), danger: true }))) return;
    setBusy(true);
    try {
      const file = await renderEdit(src, name, edit);
      if (await onSave(file, replace)) return;
    } catch {
      toast('error', t('pv.edit.saveFail'));
    }
    setBusy(false);
  };

  const c = edit.crop;
  const cropStyle: CSSProperties = { left: c.x * frame.w, top: c.y * frame.h, width: c.w * frame.w, height: c.h * frame.h };

  return (
    <div className="pe" role="dialog" aria-label={t('pv.edit.title')} onPointerDown={(e) => e.stopPropagation()}>
      <svg width="0" height="0" aria-hidden="true" style={{ position: 'absolute' }}>
        <filter id={`pe-${fid}`} colorInterpolationFilters="sRGB">
          <feColorMatrix type="matrix" values={matrix} />
        </filter>
        {FILTER_IDS.map((f) => (
          <filter key={f} id={`pe-${fid}-${f}`} colorInterpolationFilters="sRGB">
            <feColorMatrix type="matrix" values={colorMatrix({ ...edit, filter: f }).map((v) => +v.toFixed(5)).join(' ')} />
          </filter>
        ))}
      </svg>
      <div className="pe-stage" ref={stageRef}>
        <div className="pe-frame" style={{ width: frame.w, height: frame.h }} onPointerMove={onCropMove} onPointerUp={onCropUp} onPointerCancel={onCropUp}>
          <img
            ref={imgRef}
            src={src}
            alt=""
            draggable={false}
            onLoad={(e) => setNat({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })}
            style={{
              width: imgBox.w,
              height: imgBox.h,
              filter: `url(#pe-${fid})`,
              transform: `translate(-50%, -50%) rotate(${edit.turns * 90 + edit.straighten}deg) scale(${scale})`,
            }}
          />
          <div className={`pe-crop${tab === 'crop' ? ' on' : ''}`} style={cropStyle} onPointerDown={tab === 'crop' ? onCropDown('move') : undefined}>
            {tab === 'crop' && (
              <>
                <i className="pe-grid" />
                {(['nw', 'ne', 'sw', 'se'] as const).map((h) => (
                  <span key={h} className={`pe-handle ${h}`} onPointerDown={onCropDown(h)} />
                ))}
              </>
            )}
          </div>
        </div>
      </div>
      <aside className="pe-panel">
        <div className="pe-tabs" role="tablist">
          {(['adjust', 'crop', 'filters'] as Tab[]).map((k) => (
            <button key={k} type="button" role="tab" aria-selected={tab === k} className={tab === k ? 'on' : ''} onClick={() => setTab(k)}>
              {t(`pv.edit.tab.${k}`)}
            </button>
          ))}
        </div>
        <div className="pe-body">
          {tab === 'adjust' && (
            <>
              <button type="button" className={`pe-auto${edit.levels ? ' on' : ''}`} aria-pressed={!!edit.levels} onClick={autoEnhance}>
                <Icon name="sparkles" size={15} /> {t('pv.edit.auto')}
              </button>
              {SLIDERS.map((k) => (
                <label key={k} className="pe-slider">
                  <span>
                    {t(`pv.edit.${k}`)}
                    <b>{edit.adjust[k] > 0 ? `+${edit.adjust[k]}` : edit.adjust[k]}</b>
                  </span>
                  <input
                    type="range"
                    min={-100}
                    max={100}
                    value={edit.adjust[k]}
                    onChange={(e) => live({ ...edit, adjust: { ...edit.adjust, [k]: Number(e.target.value) } })}
                    onPointerUp={settle}
                    onKeyUp={settle}
                    onBlur={settle}
                    onDoubleClick={() => commit({ ...edit, adjust: { ...edit.adjust, [k]: 0 } })}
                  />
                </label>
              ))}
              <button type="button" className="ghost small" disabled={!edit.levels && SLIDERS.every((k) => !edit.adjust[k])} onClick={() => commit({ ...edit, adjust: NO_ADJUST, levels: null })}>
                {t('pv.edit.resetLight')}
              </button>
            </>
          )}
          {tab === 'crop' && (
            <>
              <div className="pe-chips" role="radiogroup" aria-label={t('pv.edit.aspect')}>
                {ASPECTS.map((a) => (
                  <button key={a} type="button" role="radio" aria-checked={edit.aspect === a} className={edit.aspect === a ? 'on' : ''} onClick={() => setAspect(a)}>
                    {a === 'free' ? t('pv.edit.free') : a}
                  </button>
                ))}
              </div>
              <label className="pe-slider">
                <span>
                  {t('pv.edit.straighten')}
                  <b>{edit.straighten.toFixed(1)}°</b>
                </span>
                <input
                  type="range"
                  min={-45}
                  max={45}
                  step={0.5}
                  value={edit.straighten}
                  onChange={(e) => live({ ...edit, straighten: Number(e.target.value) })}
                  onPointerUp={settle}
                  onKeyUp={settle}
                  onBlur={settle}
                  onDoubleClick={() => commit({ ...edit, straighten: 0 })}
                />
              </label>
              <div className="pe-row">
                <button type="button" className="ghost small" onClick={() => turn(-1)}>
                  <Icon name="rotate" size={15} className="flip-x" /> {t('pv.rotateLeft')}
                </button>
                <button type="button" className="ghost small" onClick={() => turn(1)}>
                  <Icon name="rotate" size={15} /> {t('pv.rotateRight')}
                </button>
              </div>
              <button type="button" className="ghost small" onClick={() => commit({ ...edit, aspect: 'free', crop: NEW_EDIT.crop, straighten: 0, turns: 0 })}>
                {t('pv.edit.resetCrop')}
              </button>
            </>
          )}
          {tab === 'filters' && (
            <div className="pe-filters">
              {FILTER_IDS.map((f: FilterId) => (
                <button key={f} type="button" className={edit.filter === f ? 'on' : ''} aria-pressed={edit.filter === f} onClick={() => commit({ ...edit, filter: f })}>
                  <img src={src} alt="" draggable={false} style={{ filter: `url(#pe-${fid}-${f})` }} />
                  <span>{t(`pv.edit.filter.${f}`)}</span>
                </button>
              ))}
            </div>
          )}
        </div>
        <footer className="pe-foot">
          <button type="button" className="ghost icon-btn" aria-label={t('pv.edit.undo')} title={`${t('pv.edit.undo')} (Ctrl+Z)`} disabled={!hist.past.length} onClick={() => setHist(undo)}>
            <Icon name="undo" size={16} />
          </button>
          <button type="button" className="ghost icon-btn" aria-label={t('pv.edit.redo')} title={`${t('pv.edit.redo')} (Ctrl+Shift+Z)`} disabled={!hist.future.length} onClick={() => setHist(redo)}>
            <Icon name="undo" size={16} className="flip-x" />
          </button>
          <span className="pe-spacer" />
          <button type="button" className="ghost" onClick={onCancel} disabled={busy}>
            {t('common.cancel')}
          </button>
          {canReplace && (
            <button type="button" className="ghost" disabled={busy || isUnchanged(edit)} onClick={() => void save(true)}>
              {t('pv.edit.replace')}
            </button>
          )}
          <button type="button" className="pe-save" disabled={busy || isUnchanged(edit)} onClick={() => void save(false)}>
            {busy ? t('pv.edit.saving') : t('pv.edit.saveCopy')}
          </button>
        </footer>
      </aside>
    </div>
  );
}
