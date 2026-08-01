'use client';

import { useMemo } from 'react';
import { previewAnnotationsPatch, type CompatDisplay } from '../utils/client-compat';
import { BodyCompatDiff } from './body-compat-diff';

interface ResponseCompatDiffProps {
  responseBody: unknown;
  compat: CompatDisplay;
  /** unified = code-review style (default for all surfaces); height only differs by compact */
  mode?: 'side-by-side' | 'unified';
  className?: string;
  /** Smaller height for drawer preview */
  compact?: boolean;
}

export function ResponseCompatDiff({
  responseBody,
  compat,
  mode: _mode = 'unified',
  className,
  compact,
}: ResponseCompatDiffProps) {
  // `mode` kept for call-site compatibility; side-by-side full JSON was retired.
  void _mode;

  const { original, patched, changed } = useMemo(
    () => previewAnnotationsPatch(responseBody),
    [responseBody]
  );

  return (
    <BodyCompatDiff
      original={original}
      patched={patched}
      changed={changed}
      compat={compat}
      className={className}
      compact={compact}
    />
  );
}

export { BodyCompatDiff };
