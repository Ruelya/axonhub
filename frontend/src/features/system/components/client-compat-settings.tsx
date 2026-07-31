'use client';

import { useEffect, useMemo, useState } from 'react';
import { Loader2, Save } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import {
  useClientCompatSettings,
  useUpdateClientCompatSettings,
  type ClientProfileView,
} from '../data/system';

export function ClientCompatSettings() {
  const { t } = useTranslation();
  const { data, isLoading } = useClientCompatSettings();
  const updateSettings = useUpdateClientCompatSettings();

  const [enabled, setEnabled] = useState(false);
  const [profiles, setProfiles] = useState<ClientProfileView[]>([]);

  useEffect(() => {
    if (!data) return;
    setEnabled(data.enabled);
    setProfiles(data.profiles || []);
  }, [data]);

  const hasChanges = useMemo(() => {
    if (!data) return false;
    if (enabled !== data.enabled) return true;
    for (const p of profiles) {
      const orig = data.profiles.find((x) => x.id === p.id);
      if (!orig) continue;
      if (p.enabled !== orig.enabled) return true;
      if (p.patches.ensureOutputTextAnnotations !== orig.patches.ensureOutputTextAnnotations) return true;
    }
    return false;
  }, [data, enabled, profiles]);

  const updateProfile = (id: string, patch: Partial<ClientProfileView> & { ensureAnnotations?: boolean }) => {
    setProfiles((prev) =>
      prev.map((p) => {
        if (p.id !== id) return p;
        const next = { ...p, ...patch };
        if (patch.ensureAnnotations !== undefined) {
          next.patches = {
            ...p.patches,
            ensureOutputTextAnnotations: patch.ensureAnnotations,
          };
        }
        return next;
      })
    );
  };

  const handleSave = async () => {
    await updateSettings.mutateAsync({
      enabled,
      profiles: profiles.map((p) => ({
        id: p.id,
        enabled: p.enabled,
        templateId: p.templateId,
        ensureOutputTextAnnotations: p.patches.ensureOutputTextAnnotations,
      })),
    });
  };

  if (isLoading) {
    return (
      <div className='flex h-32 items-center justify-center'>
        <Loader2 className='h-6 w-6 animate-spin' />
        <span className='text-muted-foreground ml-2'>{t('common.loading')}</span>
      </div>
    );
  }

  const grokProfile = profiles.find((p) => p.id === 'grok_build');

  return (
    <div className='space-y-6'>
      <Card>
        <CardHeader>
          <CardTitle>{t('system.clientCompat.title')}</CardTitle>
          <CardDescription>{t('system.clientCompat.description')}</CardDescription>
        </CardHeader>
        <CardContent className='space-y-6'>
          <div className='flex items-center justify-between gap-4'>
            <div className='space-y-1'>
              <Label>{t('system.clientCompat.enabled.label')}</Label>
              <p className='text-muted-foreground text-sm'>{t('system.clientCompat.enabled.help')}</p>
            </div>
            <Switch checked={enabled} onCheckedChange={setEnabled} disabled={updateSettings.isPending} />
          </div>

          <div className='bg-muted/40 space-y-1 rounded-lg border p-3 text-sm'>
            <p className='font-medium'>{t('system.clientCompat.detection.headers')}</p>
            <p className='text-muted-foreground font-mono text-xs'>
              {data?.detection.explicitHeader || 'X-AxonHub-Client'} ·{' '}
              {data?.detection.explicitVersionHeader || 'X-AxonHub-Client-Version'}
            </p>
            <p className='text-muted-foreground text-xs'>{t('system.clientCompat.detection.priority')}</p>
          </div>

          <div className='flex justify-end'>
            <Button onClick={handleSave} disabled={!hasChanges || updateSettings.isPending} className='min-w-[100px]'>
              {updateSettings.isPending ? (
                <>
                  <Loader2 className='mr-2 h-4 w-4 animate-spin' />
                  {t('system.buttons.saving')}
                </>
              ) : (
                <>
                  <Save className='mr-2 h-4 w-4' />
                  {t('system.buttons.save')}
                </>
              )}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('system.clientCompat.profiles.title')}</CardTitle>
          <CardDescription>{t('system.clientCompat.profiles.description')}</CardDescription>
        </CardHeader>
        <CardContent className='space-y-4'>
          {grokProfile && (
            <div className='rounded-xl border p-4'>
              <div className='flex flex-wrap items-center justify-between gap-3'>
                <div>
                  <div className='flex items-center gap-2'>
                    <span className='font-medium'>{grokProfile.displayName}</span>
                    <Badge variant='secondary'>grok_build</Badge>
                  </div>
                  <p className='text-muted-foreground mt-1 text-sm'>{t('system.clientCompat.profiles.grokHelp')}</p>
                </div>
                <div className='flex items-center gap-2'>
                  <Label className='text-sm'>{t('system.clientCompat.profiles.enablePatch')}</Label>
                  <Switch
                    checked={grokProfile.enabled}
                    onCheckedChange={(v) => updateProfile('grok_build', { enabled: v })}
                    disabled={updateSettings.isPending}
                  />
                </div>
              </div>
              <div className='mt-4 flex items-center justify-between gap-4 border-t pt-4'>
                <div>
                  <Label>{t('system.clientCompat.patches.annotations')}</Label>
                  <p className='text-muted-foreground text-xs'>{t('system.clientCompat.patches.annotationsHelp')}</p>
                </div>
                <Switch
                  checked={grokProfile.patches.ensureOutputTextAnnotations}
                  onCheckedChange={(v) => updateProfile('grok_build', { ensureAnnotations: v })}
                  disabled={updateSettings.isPending || !grokProfile.enabled}
                />
              </div>
              <p className='text-muted-foreground mt-3 text-xs'>
                {t('system.clientCompat.profiles.template')}: <span className='font-mono'>{grokProfile.templateId}</span>
              </p>
            </div>
          )}

          <div className='grid gap-2 sm:grid-cols-2 lg:grid-cols-3'>
            {profiles
              .filter((p) => p.id !== 'grok_build' && p.id !== 'unknown')
              .map((p) => (
                <div key={p.id} className='bg-muted/20 rounded-lg border px-3 py-2'>
                  <div className='flex items-center justify-between gap-2'>
                    <span className='text-sm font-medium'>{p.displayName}</span>
                    <Badge variant='outline' className='text-[10px]'>
                      {t('system.clientCompat.profiles.detectOnly')}
                    </Badge>
                  </div>
                  <p className='text-muted-foreground mt-1 font-mono text-[11px]'>{p.id}</p>
                </div>
              ))}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
