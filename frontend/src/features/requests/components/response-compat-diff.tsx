'use client';

import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { ScrollArea } from '@/components/ui/scroll-area';
import {
  buildLineDiff,
  formatJSON,
  previewAnnotationsPatch,
  type CompatDisplay,
} from '../utils/client-compat';

interface ResponseCompatDiffProps {
  responseBody: unknown;
  compat: CompatDisplay;
  /** side-by-side (detail) vs unified line diff (drawer preview) */
  mode?: 'side-by-side' | 'unified';
  className?: string;
}

export function ResponseCompatDiff({
  responseBody,
  compat,
  mode = 'side-by-side',
  className,
}: ResponseCompatDiffProps) {
  const { t } = useTranslation();

  const { original, patched, changed } = useMemo(
    () => previewAnnotationsPatch(responseBody),
    [responseBody]
  );

  const beforeText = useMemo(() => formatJSON(original), [original]);
  const afterText = useMemo(() => formatJSON(patched), [patched]);
  const lines = useMemo(() => buildLineDiff(beforeText, afterText), [beforeText, afterText]);

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

  return (
    <div className={className}>
      <div className='mb-3 flex flex-wrap items-center gap-2'>
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
        <span className='text-muted-foreground text-xs'>
          {t('requests.client.compat.diffHint')}
        </span>
        {!changed && compat.applied && (
          <span className='text-muted-foreground text-xs'>{t('requests.client.compat.noBodyDelta')}</span>
        )}
      </div>

      {mode === 'side-by-side' ? (
        <div className='grid gap-3 lg:grid-cols-2'>
          <div className='min-w-0 rounded-lg border'>
            <div className='bg-muted/40 border-b px-3 py-2 text-xs font-medium'>
              {t('requests.client.compat.before')}
            </div>
            <ScrollArea className='h-[420px] w-full p-3'>
              <pre className='font-mono text-xs whitespace-pre-wrap break-all'>{beforeText}</pre>
            </ScrollArea>
          </div>
          <div className='min-w-0 rounded-lg border border-red-200/60 dark:border-red-900/50'>
            <div className='border-b border-red-200/60 bg-red-50/50 px-3 py-2 text-xs font-medium text-red-800 dark:border-red-900/50 dark:bg-red-950/30 dark:text-red-200'>
              {t('requests.client.compat.after')}
            </div>
            <ScrollArea className='h-[420px] w-full p-3'>
              <pre className='font-mono text-xs whitespace-pre-wrap break-all'>{afterText}</pre>
            </ScrollArea>
          </div>
        </div>
      ) : (
        <ScrollArea className='bg-muted/20 h-[360px] w-full rounded-lg border p-3'>
          <pre className='font-mono text-xs leading-5'>
            {lines.map((line, idx) => (
              <div
                key={idx}
                className={
                  line.type === 'add'
                    ? 'bg-emerald-100/80 text-emerald-900 dark:bg-emerald-950/50 dark:text-emerald-200'
                    : line.type === 'del'
                      ? 'bg-red-100/80 text-red-900 dark:bg-red-950/50 dark:text-red-200'
                      : 'text-foreground/80'
                }
              >
                <span className='inline-block w-4 select-none opacity-60'>
                  {line.type === 'add' ? '+' : line.type === 'del' ? '-' : ' '}
                </span>
                {line.text}
              </div>
            ))}
          </pre>
        </ScrollArea>
      )}
    </div>
  );
}
