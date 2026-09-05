'use client';

/**
 * Draw-your-signature canvas (SPEC §5.5, optional step of the renter signing
 * flow). Pointer events cover finger, stylus and mouse in one path; the
 * backing store is sized to the device pixel ratio so a phone signature
 * doesn't print as a blurred smear.
 *
 * The image is evidence, not data: it is exported as a transparent PNG and
 * PUT straight at the `signatures` bucket with a presigned URL.
 */

import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react';

export interface SignaturePadHandle {
  /** Transparent PNG of the strokes, or null when nothing was drawn. */
  toBlob: () => Promise<Blob | null>;
  clear: () => void;
  isEmpty: () => boolean;
}

const HEIGHT = 180;
const INK = '#1c2b5a'; /* --ink; canvas cannot read a CSS variable */

export const SignaturePad = forwardRef<SignaturePadHandle, { onChange?: (empty: boolean) => void }>(
  function SignaturePad({ onChange }, ref) {
    const canvasRef = useRef<HTMLCanvasElement | null>(null);
    const drawing = useRef(false);
    const empty = useRef(true);
    const [isEmpty, setIsEmpty] = useState(true);

    const markDirty = useCallback(() => {
      if (empty.current) {
        empty.current = false;
        setIsEmpty(false);
        onChange?.(false);
      }
    }, [onChange]);

    /* Size the backing store to the CSS box × DPR, once laid out. Redrawing
       after a resize would need stroke history, so the canvas is simply
       cleared — a rotation mid-signature starts a fresh one. */
    const resize = useCallback(() => {
      const canvas = canvasRef.current;
      if (!canvas) return;
      const dpr = window.devicePixelRatio || 1;
      const width = canvas.clientWidth || 320;
      canvas.width = Math.round(width * dpr);
      canvas.height = Math.round(HEIGHT * dpr);
      const ctx = canvas.getContext('2d');
      if (!ctx) return;
      ctx.scale(dpr, dpr);
      ctx.lineWidth = 2.5;
      ctx.lineCap = 'round';
      ctx.lineJoin = 'round';
      ctx.strokeStyle = INK;
    }, []);

    useEffect(() => {
      resize();
      window.addEventListener('resize', resize);
      return () => window.removeEventListener('resize', resize);
    }, [resize]);

    const point = (e: React.PointerEvent<HTMLCanvasElement>) => {
      const rect = e.currentTarget.getBoundingClientRect();
      return { x: e.clientX - rect.left, y: e.clientY - rect.top };
    };

    function start(e: React.PointerEvent<HTMLCanvasElement>) {
      const ctx = canvasRef.current?.getContext('2d');
      if (!ctx) return;
      e.currentTarget.setPointerCapture(e.pointerId);
      drawing.current = true;
      const p = point(e);
      ctx.beginPath();
      ctx.moveTo(p.x, p.y);
      // A tap alone should leave a dot, not nothing.
      ctx.lineTo(p.x + 0.01, p.y);
      ctx.stroke();
      markDirty();
    }

    function move(e: React.PointerEvent<HTMLCanvasElement>) {
      if (!drawing.current) return;
      const ctx = canvasRef.current?.getContext('2d');
      if (!ctx) return;
      const p = point(e);
      ctx.lineTo(p.x, p.y);
      ctx.stroke();
    }

    function end() {
      drawing.current = false;
    }

    const clear = useCallback(() => {
      const canvas = canvasRef.current;
      const ctx = canvas?.getContext('2d');
      if (!canvas || !ctx) return;
      ctx.save();
      ctx.setTransform(1, 0, 0, 1, 0, 0);
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      ctx.restore();
      empty.current = true;
      setIsEmpty(true);
      onChange?.(true);
    }, [onChange]);

    useImperativeHandle(
      ref,
      () => ({
        clear,
        isEmpty: () => empty.current,
        toBlob: () =>
          new Promise<Blob | null>((resolve) => {
            const canvas = canvasRef.current;
            if (!canvas || empty.current) {
              resolve(null);
              return;
            }
            canvas.toBlob((blob) => resolve(blob), 'image/png');
          }),
      }),
      [clear],
    );

    return (
      <div style={{ display: 'grid', gap: 'var(--sp-2)' }}>
        <canvas
          ref={canvasRef}
          aria-label="Sign with your finger"
          onPointerDown={start}
          onPointerMove={move}
          onPointerUp={end}
          onPointerCancel={end}
          onPointerLeave={end}
          style={{
            width: '100%',
            height: HEIGHT,
            // A signature is dark ink on paper wherever it is later shown
            // (the contract sheet, a print-out), and the exported PNG is
            // transparent — so the signing surface stays light even under a
            // dark theme, or the strokes would be invisible while drawing.
            background: '#ffffff',
            colorScheme: 'light',
            border: '1px solid var(--rule)',
            borderRadius: 'var(--radius-sm)',
            // The page must not pan while a finger is drawing on it.
            touchAction: 'none',
            cursor: 'crosshair',
          }}
        />
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <span className="pencil">
            {isEmpty ? 'Sign above with your finger' : 'Looks good?'}
          </span>
          <button
            type="button"
            className="btn btn-quiet"
            onClick={clear}
            disabled={isEmpty}
            style={{ width: 'auto' }}
          >
            Clear
          </button>
        </div>
      </div>
    );
  },
);
