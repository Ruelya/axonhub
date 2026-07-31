'use client';

import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { compareAgainstBuiltinTemplate, type CompatDisplay } from '../utils/client-compat';

interface SchemaComparePanelProps {
  responseBody: unknown;
  compat: CompatDisplay;
  format?: string | null;
}

function PathList({ title, items, empty }: { title: string; items: string[]; empty: string }) {
  return (
    <div className='rounded-lg border p-3'>
      <p className='mb-2 text-sm font-medium'>
        {title} <span className='text-muted-foreground font-normal'>({items.length})</span>
      </p>
      {items.length === 0 ? (
        <p className='text-muted-foreground text-xs'>{empty}</p>
      ) : (
        <ul className='max-h-40 space-y-1 overflow-auto font-mono text-xs text-amber-800 dark:text-amber-300'>
          {items.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function SchemaComparePanel({ responseBody, compat, format }: SchemaComparePanelProps) {
  const { t } = useTranslation();
  const diff = useMemo(
    () => compareAgainstBuiltinTemplate(responseBody, compat.profileId, format),
    [responseBody, compat.profileId, format]
  );

  if (!responseBody) return null;

  const hasIssues =
    diff.missingPaths.length > 0 || diff.extraPaths.length > 0 || diff.absentVsEmptyArrays.length > 0;

  return (
    <Card>
      <CardHeader className='pb-3'>
        <div className='flex flex-wrap items-center gap-2'>
          <CardTitle className='text-base'>{t('requests.client.schema.title')}</CardTitle>
          <Badge variant='outline' className='font-mono text-[11px]'>
            {diff.templateName}
          </Badge>
          <Badge variant='secondary' className='font-mono text-[11px]'>
            {compat.displayName}
          </Badge>
          {format && (
            <Badge variant='outline' className='font-mono text-[11px]'>
              {format}
            </Badge>
          )}
        </div>
        <CardDescription>{t('requests.client.schema.description')}</CardDescription>
      </CardHeader>
      <CardContent className='space-y-3'>
        {!hasIssues ? (
          <p className='text-muted-foreground text-sm'>{t('requests.client.schema.match')}</p>
        ) : (
          <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-3'>
            <PathList
              title={t('requests.client.schema.missing')}
              items={diff.missingPaths}
              empty={t('requests.client.schema.none')}
            />
            <PathList
              title={t('requests.client.schema.absentEmpty')}
              items={diff.absentVsEmptyArrays}
              empty={t('requests.client.schema.none')}
            />
            <PathList
              title={t('requests.client.schema.extra')}
              items={diff.extraPaths}
              empty={t('requests.client.schema.none')}
            />
          </div>
        )}
      </CardContent>
    </Card>
  );
}
