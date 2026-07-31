'use client';

import { useEffect, useMemo, useState } from 'react';
import { Loader2, Save, FlaskConical, GitCompareArrows } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import {
  useClientCompatSettings,
  useUpdateClientCompatSettings,
  useTestClientDetect,
  useCompareClientSchema,
  usePreviewClientCompatPatch,
  type ClientProfileView,
  type ClientSchemaCompareResult,
  type ClientDetectResultGQL,
} from '../data/system';

export function ClientCompatSettings() {
  const { t } = useTranslation();
  const { data, isLoading } = useClientCompatSettings();
  const updateSettings = useUpdateClientCompatSettings();
  const testDetect = useTestClientDetect();
  const compareSchema = useCompareClientSchema();
  const previewPatch = usePreviewClientCompatPatch();

  const [enabled, setEnabled] = useState(false);
  const [profiles, setProfiles] = useState<ClientProfileView[]>([]);
  const [testUA, setTestUA] = useState('grok-pager/1.0.0 grok-shell/0.2.117');
  const [testHeader, setTestHeader] = useState('');
  const [detectResult, setDetectResult] = useState<ClientDetectResultGQL | null>(null);

  const [compareTemplateId, setCompareTemplateId] = useState('grok_build_responses_v1');
  const [compareJSON, setCompareJSON] = useState(
    '{\n  "response": {\n    "output": [{\n      "type": "message",\n      "content": [{\n        "type": "output_text",\n        "text": "hello"\n      }]\n    }]\n  }\n}'
  );
  const [compareResult, setCompareResult] = useState<ClientSchemaCompareResult | null>(null);
  const [previewResult, setPreviewResult] = useState<{
    changed: boolean;
    patchedJson: string;
    compare?: ClientSchemaCompareResult | null;
  } | null>(null);
  const [applyAnnotationsOnPreview, setApplyAnnotationsOnPreview] = useState(true);

  useEffect(() => {
    if (!data) return;
    setEnabled(data.enabled);
    setProfiles(data.profiles || []);
    if (data.templates?.length) {
      const preferred = data.templates.find((tpl) => tpl.id === 'grok_build_responses_v1') || data.templates[0];
      setCompareTemplateId(preferred.id);
    }
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

  const handleTestDetect = async () => {
    const result = await testDetect.mutateAsync({
      userAgent: testUA || undefined,
      clientHeader: testHeader || undefined,
    });
    setDetectResult(result);
  };

  const handleCompare = async () => {
    const result = await compareSchema.mutateAsync({
      templateId: compareTemplateId,
      document: compareJSON,
    });
    setCompareResult(result);
    setPreviewResult(null);
  };

  const handlePreviewPatch = async () => {
    const result = await previewPatch.mutateAsync({
      document: compareJSON,
      ensureOutputTextAnnotations: applyAnnotationsOnPreview,
      templateId: compareTemplateId,
    });
    setPreviewResult(result);
    if (result.compare) {
      setCompareResult(result.compare);
    }
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

          <div className='bg-muted/40 rounded-lg border p-3 text-sm'>
            <p className='font-medium'>{t('system.clientCompat.detection.headers')}</p>
            <p className='text-muted-foreground mt-1 font-mono text-xs'>
              {data?.detection.explicitHeader || 'X-AxonHub-Client'} /{' '}
              {data?.detection.explicitVersionHeader || 'X-AxonHub-Client-Version'}
            </p>
            <p className='text-muted-foreground mt-2 text-xs'>{t('system.clientCompat.detection.priority')}</p>
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
                    <Badge variant='outline'>{t('system.clientCompat.profiles.phase1')}</Badge>
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

      <Card>
        <CardHeader>
          <CardTitle>{t('system.clientCompat.detectTest.title')}</CardTitle>
          <CardDescription>{t('system.clientCompat.detectTest.description')}</CardDescription>
        </CardHeader>
        <CardContent className='space-y-4'>
          <div className='space-y-2'>
            <Label htmlFor='test-ua'>User-Agent</Label>
            <Input id='test-ua' value={testUA} onChange={(e) => setTestUA(e.target.value)} className='font-mono text-sm' />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='test-header'>X-AxonHub-Client ({t('system.clientCompat.detectTest.optional')})</Label>
            <Input
              id='test-header'
              value={testHeader}
              onChange={(e) => setTestHeader(e.target.value)}
              placeholder='grok-build'
              className='font-mono text-sm'
            />
          </div>
          <Button onClick={handleTestDetect} disabled={testDetect.isPending} variant='secondary'>
            {testDetect.isPending ? <Loader2 className='mr-2 h-4 w-4 animate-spin' /> : <FlaskConical className='mr-2 h-4 w-4' />}
            {t('system.clientCompat.detectTest.run')}
          </Button>
          {detectResult && (
            <div className='bg-muted/30 rounded-lg border p-3 text-sm'>
              <div className='flex flex-wrap items-center gap-2'>
                <Badge>{detectResult.displayName}</Badge>
                <span className='text-muted-foreground font-mono text-xs'>{detectResult.profileId}</span>
                <Badge variant='outline'>{detectResult.source}</Badge>
                <Badge variant='secondary'>{detectResult.confidence}</Badge>
              </div>
              {detectResult.matchedRules && detectResult.matchedRules.length > 0 && (
                <p className='text-muted-foreground mt-2 text-xs'>
                  rules: {detectResult.matchedRules.join(', ')}
                </p>
              )}
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('system.clientCompat.compare.title')}</CardTitle>
          <CardDescription>{t('system.clientCompat.compare.description')}</CardDescription>
        </CardHeader>
        <CardContent className='space-y-4'>
          <div className='space-y-2'>
            <Label>{t('system.clientCompat.compare.template')}</Label>
            <Select value={compareTemplateId} onValueChange={setCompareTemplateId}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(data?.templates || []).map((tpl) => (
                  <SelectItem key={tpl.id} value={tpl.id}>
                    {tpl.displayName}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {data?.templates?.find((x) => x.id === compareTemplateId)?.description && (
              <p className='text-muted-foreground text-xs'>
                {data.templates.find((x) => x.id === compareTemplateId)?.description}
              </p>
            )}
          </div>
          <div className='space-y-2'>
            <Label htmlFor='compare-json'>{t('system.clientCompat.compare.json')}</Label>
            <Textarea
              id='compare-json'
              value={compareJSON}
              onChange={(e) => setCompareJSON(e.target.value)}
              className='min-h-[180px] font-mono text-xs'
            />
          </div>
          <div className='flex flex-wrap items-center gap-3'>
            <Button onClick={handleCompare} disabled={compareSchema.isPending} variant='secondary'>
              {compareSchema.isPending ? (
                <Loader2 className='mr-2 h-4 w-4 animate-spin' />
              ) : (
                <GitCompareArrows className='mr-2 h-4 w-4' />
              )}
              {t('system.clientCompat.compare.run')}
            </Button>
            <div className='flex items-center gap-2'>
              <Switch checked={applyAnnotationsOnPreview} onCheckedChange={setApplyAnnotationsOnPreview} />
              <Label className='text-sm'>{t('system.clientCompat.compare.fillAnnotations')}</Label>
            </div>
            <Button onClick={handlePreviewPatch} disabled={previewPatch.isPending} variant='outline'>
              {previewPatch.isPending ? <Loader2 className='mr-2 h-4 w-4 animate-spin' /> : null}
              {t('system.clientCompat.compare.previewPatch')}
            </Button>
          </div>

          {compareResult && (
            <div className='grid gap-3 sm:grid-cols-2'>
              <DiffList title={t('system.clientCompat.compare.missing')} items={compareResult.missingPaths} tone='warn' />
              <DiffList title={t('system.clientCompat.compare.absentEmpty')} items={compareResult.absentVsEmptyArrays} tone='warn' />
              <DiffList title={t('system.clientCompat.compare.extra')} items={compareResult.extraPaths} tone='muted' />
              <DiffList title={t('system.clientCompat.compare.typeMismatch')} items={compareResult.typeMismatches} tone='warn' />
            </div>
          )}

          {previewResult && (
            <div className='space-y-2'>
              <div className='flex items-center gap-2'>
                <Label>{t('system.clientCompat.compare.patched')}</Label>
                <Badge variant={previewResult.changed ? 'default' : 'secondary'}>
                  {previewResult.changed
                    ? t('system.clientCompat.compare.changed')
                    : t('system.clientCompat.compare.unchanged')}
                </Badge>
              </div>
              <pre className='bg-muted/40 max-h-64 overflow-auto rounded-lg border p-3 font-mono text-xs whitespace-pre-wrap'>
                {previewResult.patchedJson}
              </pre>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function DiffList({ title, items, tone }: { title: string; items: string[]; tone: 'warn' | 'muted' }) {
  return (
    <div className='rounded-lg border p-3'>
      <p className='mb-2 text-sm font-medium'>
        {title}{' '}
        <span className='text-muted-foreground font-normal'>({items?.length || 0})</span>
      </p>
      {items && items.length > 0 ? (
        <ul className={`space-y-1 font-mono text-xs ${tone === 'warn' ? 'text-amber-700 dark:text-amber-400' : 'text-muted-foreground'}`}>
          {items.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
      ) : (
        <p className='text-muted-foreground text-xs'>—</p>
      )}
    </div>
  );
}
