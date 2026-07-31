'use client';

import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { ScrollArea } from '@/components/ui/scroll-area';
import { cn } from '@/lib/utils';
import {
  buildLineDiff,
  diffStats,
  formatJSON,
  previewAnnotationsPatch,
  type CompatDisplay,
  type DiffLine,
} from '../utils/client-compat';

interface ResponseCompatDiffProps {
  responseBody: unknown;
  compat: CompatDisplay;
  /** unified = code-review style (default for all surfaces); height only differs by compact */
  mode?: 'side-by-side' | 'unified';
  className?: string;
  /** Smaller height for drawer preview */
  compact?: boolean;
}

function lineRowClass(type: DiffLine['type']): string {
  switch (type) {
    case 'add':
      return 'bg-emerald-500/10 text-emerald-900 dark:bg-emerald-500/[0.12] dark:text-emerald-200';
    case 'del':
      return 'bg-red-500/10 text-red-900 dark:bg-red-500/[0.12] dark:text-red-200';
    case 'meta':
      return 'bg-muted/40 text-muted-foreground italic';
    default:
      return 'text-foreground/85';
  }
}

function markerClass(type: DiffLine['type']): string {
  switch (type) {
    case 'add':
      return 'text-emerald-600 dark:text-emerald-400';
    case 'del':
      return 'text-red-600 dark:text-red-400';
    default:
      return 'text-muted-foreground/50';
  }
}

function gutterBg(type: DiffLine['type']): string {
  switch (type) {
    case 'add':
      return 'bg-emerald-500/[0.08] dark:bg-emerald-500/[0.1]';
    case 'del':
      return 'bg-red-500/[0.08] dark:bg-red-500/[0.1]';
    case 'meta':
      return 'bg-muted/30';
    default:
      return 'bg-muted/15';
  }
}

function DiffMarker({ type }: { type: DiffLine['type'] }) {
  const ch = type === 'add' ? '+' : type === 'del' ? '-' : type === 'meta' ? ' ' : ' ';
  return (
    <span
      className={cn(
        'inline-block w-4 shrink-0 select-none text-center font-medium',
        markerClass(type)
      )}
      aria-hidden
    >
      {ch}
    </span>
  );
}

function LineNumber({ n, type }: { n?: number; type: DiffLine['type'] }) {
  return (
    <span
      className={cn(
        'inline-block w-10 shrink-0 select-none pr-2 text-right tabular-nums text-[11px] leading-5',
        type === 'meta' ? 'text-transparent' : 'text-muted-foreground/55'
      )}
    >
      {n ?? ''}
    </span>
  );
}

export function ResponseCompatDiff({
  responseBody,
  compat,
  mode: _mode = 'unified',
  className,
  compact,
}: ResponseCompatDiffProps) {
  const { t } = useTranslation();
  // Both preview and detail use the same code-review unified diff.
  // `mode` is kept for call-site compatibility; side-by-side full JSON was retired.
  void _mode;

  const { original, patched, changed } = useMemo(
    () => previewAnnotationsPatch(responseBody),
    [responseBody]
  );

  const beforeText = useMemo(() => formatJSON(original), [original]);
  const afterText = useMemo(() => formatJSON(patched), [patched]);
  const lines = useMemo(
    () => buildLineDiff(beforeText, afterText, { context: 4, collapseThreshold: 10 }),
    [beforeText, afterText]
  );
  const stats = useMemo(() => diffStats(lines), [lines]);

  if (!responseBody) {
    return (
      <div className='text-muted-foreground flex h-32 items-center justify-center text-sm'>
        {t('requests.detail.noResponse')}
      </div>
    );
  }

  if (!compat.applied && !changed) {
    return null;
  }

  const heightClass = compact ? 'h-[min(50vh,360px)]' : 'h-[min(60vh,520px)]';

  return (
    <div className={cn('min-w-0', className)}>
      <div className='mb-2 flex flex-wrap items-center gap-2'>
        <Badge
          variant='secondary'
          className={
            compat.applied
              ? 'border-red-200 bg-red-50 text-red-700 dark:border-red-800 dark:bg-red-950/40 dark:text-red-300'
              : 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300'
          }
        >
          {compat.applied
            ? t('requests.client.compat.applied')
            : t('requests.client.compat.notApplied')}
        </Badge>
        {changed ? (
          <span className='font-mono text-xs tabular-nums'>
            <span className='text-emerald-600 dark:text-emerald-400'>+{stats.additions}</span>
            <span className='text-muted-foreground mx-1.5'>/</span>
            <span className='text-red-600 dark:text-red-400'>-{stats.deletions}</span>
          </span>
        ) : (
          <span className='text-muted-foreground text-xs'>{t('requests.client.compat.noBodyDelta')}</span>
        )}
        <span className='text-muted-foreground text-xs'>{t('requests.client.compat.diffHint')}</span>
      </div>

      <div className='bg-muted/20 overflow-hidden rounded-lg border'>
        <div className='bg-muted/40 text-muted-foreground flex items-center justify-between gap-2 border-b px-3 py-1.5 text-[11px]'>
          <span className='truncate font-medium tracking-wide'>
            {t('requests.client.compat.diffHeader')}
          </span>
          <span className='shrink-0 font-mono opacity-80'>
            {t('requests.client.compat.beforeShort')}
            <span className='mx-1 opacity-50'>→</span>
            {t('requests.client.compat.afterShort')}
          </span>
        </div>

        <ScrollArea className={cn('w-full', heightClass)}>
          <div
            role='table'
            aria-label={t('requests.client.compat.responseDiffTitle')}
            className='font-mono text-[12px] leading-5'
          >
            {lines.map((line, idx) => (
              <div
                key={idx}
                role='row'
                className={cn(
                  'flex min-w-0 border-b border-transparent last:border-0',
                  lineRowClass(line.type)
                )}
              >
                <div
                  className={cn(
                    'sticky left-0 z-[1] flex shrink-0 select-none border-r border-border/40',
                    gutterBg(line.type)
                  )}
                >
                  <LineNumber n={line.oldLine} type={line.type} />
                  <LineNumber n={line.newLine} type={line.type} />
                  <span className='flex w-5 items-center justify-center border-l border-border/30'>
                    <DiffMarker type={line.type} />
                  </span>
                </div>
                <pre className='min-w-0 flex-1 overflow-x-auto whitespace-pre-wrap break-all px-2 py-0'>
                  {line.text || ' '}
                </pre>
              </div>
            ))}
          </div>
        </ScrollArea>
      </div>
    </div>
  );
}
