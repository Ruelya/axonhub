'use client';

import { useMemo, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Copy } from 'lucide-react';
import { toast } from 'sonner';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { ScrollArea } from '@/components/ui/scroll-area';
import { cn } from '@/lib/utils';
import {
  bodyPairDiff,
  buildLineDiff,
  diffStats,
  type CompatDisplay,
  type DiffLine,
} from '../utils/client-compat';

interface BodyCompatDiffProps {
  original: unknown;
  patched: unknown;
  changed: boolean;
  compat: CompatDisplay;
  /** Label override for title area badges */
  sourceLabel?: string;
  className?: string;
  compact?: boolean;
  copyLabel?: string;
  copySuccessLabel?: string;
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
  const ch = type === 'add' ? '+' : type === 'del' ? '-' : ' ';
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

/**
 * Unified code-review style diff for client-compat request/response body patches.
 */
export function BodyCompatDiff({
  original,
  patched,
  changed,
  compat,
  sourceLabel,
  className,
  compact,
  copyLabel,
  copySuccessLabel,
}: BodyCompatDiffProps) {
  const { t } = useTranslation();
  const { beforeText, afterText } = useMemo(() => bodyPairDiff(original, patched), [original, patched]);
  const lines = useMemo(() => buildLineDiff(beforeText, afterText), [beforeText, afterText]);
  const stats = useMemo(() => diffStats(lines), [lines]);

  const copyPatched = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(afterText);
      toast.success(copySuccessLabel || t('requests.client.compat.copyPatchedSuccess'));
    } catch {
      toast.error(t('common.error.copyFailed', { defaultValue: 'Copy failed' }));
    }
  }, [afterText, copySuccessLabel, t]);

  if (original === null || original === undefined) {
    return (
      <div className='text-muted-foreground flex h-32 items-center justify-center text-sm'>
        {t('requests.detail.noRequest', { defaultValue: 'No request body' })}
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
            changed || compat.applied
              ? 'border-red-200 bg-red-50 text-red-700 dark:border-red-800 dark:bg-red-950/40 dark:text-red-300'
              : 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300'
          }
        >
          {changed || compat.applied
            ? t('requests.client.compat.applied')
            : t('requests.client.compat.notApplied')}
        </Badge>
        {sourceLabel ? (
          <span className='text-muted-foreground text-[10px]'>{sourceLabel}</span>
        ) : null}
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
        <div className='ml-auto'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='h-7 gap-1.5 px-2 text-xs'
            onClick={copyPatched}
            disabled={!afterText}
          >
            <Copy className='h-3.5 w-3.5' />
            {copyLabel || t('requests.client.compat.copyPatched')}
          </Button>
        </div>
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
          <div role='table' className='min-w-full font-mono text-[12px] leading-5'>
            {lines.map((line, idx) => (
              <div
                key={idx}
                role='row'
                className={cn('flex min-w-full border-b border-transparent', lineRowClass(line.type))}
              >
                <div className={cn('flex shrink-0 select-none', gutterBg(line.type))}>
                  <LineNumber n={line.oldLine} type={line.type} />
                  <LineNumber n={line.newLine} type={line.type} />
                  <DiffMarker type={line.type} />
                </div>
                <pre className='m-0 min-w-0 flex-1 whitespace-pre-wrap break-all px-2 py-0'>{line.text}</pre>
              </div>
            ))}
          </div>
        </ScrollArea>
      </div>
    </div>
  );
}
