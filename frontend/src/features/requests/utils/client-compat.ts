/**
 * Client-compat helpers for request log UI (detect display, wire patch preview, schema compare).
 * Mirrors backend ensureOutputTextAnnotations for dry-run diffs against stored (pre-patch) bodies.
 */

import { detectClientFromHeaders, type ClientDetectResult } from '@/features/system/data/client-detect';

export type CompatDisplay = {
  profileId: string;
  displayName: string;
  source: string;
  applied: boolean;
};

const PROFILE_NAMES: Record<string, string> = {
  grok_build: 'Grok Build',
  codex: 'Codex',
  claude_code: 'Claude Code',
  cursor: 'Cursor',
  windsurf: 'Windsurf',
  cline: 'Cline',
  aider: 'Aider',
  continue: 'Continue',
  opencode: 'OpenCode',
  copilot: 'GitHub Copilot',
  roo_code: 'Roo Code',
  zed: 'Zed',
  generic_openai_sdk: 'OpenAI SDK',
  unknown: 'Unknown',
};

export function profileDisplayName(profileId?: string | null): string {
  if (!profileId) return PROFILE_NAMES.unknown;
  return PROFILE_NAMES[profileId] || profileId;
}

/** Resolve display info from stored fields, falling back to header detection. */
export function resolveCompatDisplay(request: {
  clientProfile?: string | null;
  clientDetectSource?: string | null;
  clientCompatApplied?: boolean | null;
  requestHeaders?: unknown;
}): CompatDisplay {
  const storedProfile = (request.clientProfile || '').trim();
  if (storedProfile) {
    return {
      profileId: storedProfile,
      displayName: profileDisplayName(storedProfile),
      source: request.clientDetectSource || 'none',
      applied: !!request.clientCompatApplied,
    };
  }

  const detected: ClientDetectResult = detectClientFromHeaders(request.requestHeaders);
  return {
    profileId: detected.profileId,
    displayName: detected.displayName,
    source: detected.source,
    applied: !!request.clientCompatApplied,
  };
}

/** Deep-clone JSON-compatible value via structured clone when possible. */
function cloneJSON<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function ensureAnnotationsInValue(value: unknown): boolean {
  if (value === null || value === undefined) return false;
  if (Array.isArray(value)) {
    let changed = false;
    for (const item of value) {
      if (ensureAnnotationsInValue(item)) changed = true;
    }
    return changed;
  }
  if (typeof value === 'object') {
    const obj = value as Record<string, unknown>;
    let changed = false;
    if (obj.type === 'output_text') {
      if (!('annotations' in obj) || obj.annotations === null || obj.annotations === undefined) {
        obj.annotations = [];
        changed = true;
      }
    }
    for (const child of Object.values(obj)) {
      if (ensureAnnotationsInValue(child)) changed = true;
    }
    return changed;
  }
  return false;
}

/**
 * Apply the same wire patch as backend EnsureOutputTextAnnotations.
 * Returns { original, patched, changed }.
 */
export function previewAnnotationsPatch(responseBody: unknown): {
  original: unknown;
  patched: unknown;
  changed: boolean;
} {
  if (responseBody === null || responseBody === undefined) {
    return { original: responseBody, patched: responseBody, changed: false };
  }
  const original = responseBody;
  const patched = cloneJSON(responseBody);
  const changed = ensureAnnotationsInValue(patched);
  return { original, patched, changed };
}

export type SchemaDiff = {
  templateId: string;
  templateName: string;
  missingPaths: string[];
  extraPaths: string[];
  absentVsEmptyArrays: string[];
};

/** Built-in template selection by client profile + API format. */
export function resolveTemplateId(profileId: string, format?: string | null): string {
  const fmt = (format || '').toLowerCase();
  const isResponses =
    fmt.includes('response') || fmt.includes('responses') || fmt === 'openai/response' || fmt === 'openai/responses';

  if (!isResponses && !fmt.includes('chat')) {
    // Prefer responses template for grok_build even if format string is odd.
    if (profileId === 'grok_build') return 'grok_build_responses_v1';
  }
  if (profileId === 'grok_build' && (isResponses || !fmt)) {
    return 'grok_build_responses_v1';
  }
  if (isResponses) {
    return profileId === 'grok_build' ? 'grok_build_responses_v1' : 'generic_openai_responses';
  }
  // Chat completions: no responses template fields to compare meaningfully.
  return 'generic_openai_responses';
}

function walkOutputText(
  value: unknown,
  path: string,
  fn: (path: string, obj: Record<string, unknown>) => void
) {
  if (Array.isArray(value)) {
    value.forEach((item, i) => {
      const p = path ? `${path}[${i}]` : `[${i}]`;
      walkOutputText(item, p, fn);
    });
    return;
  }
  if (value && typeof value === 'object') {
    const obj = value as Record<string, unknown>;
    if (obj.type === 'output_text') {
      fn(path || 'output_text', obj);
    }
    for (const [k, v] of Object.entries(obj)) {
      const p = path ? `${path}.${k}` : k;
      walkOutputText(v, p, fn);
    }
  }
}

/**
 * Lightweight schema compare against built-in Grok Build / generic Responses rules.
 * Focuses on output_text.annotations (the main Grok Build strict field).
 */
export function compareAgainstBuiltinTemplate(
  responseBody: unknown,
  profileId: string,
  format?: string | null
): SchemaDiff {
  const templateId = resolveTemplateId(profileId, format);
  const templateName =
    templateId === 'grok_build_responses_v1' ? 'Grok Build Responses v1' : 'OpenAI Responses (generic)';

  const missingPaths: string[] = [];
  const extraPaths: string[] = [];
  const absentVsEmptyArrays: string[] = [];

  if (!responseBody || typeof responseBody !== 'object') {
    return { templateId, templateName, missingPaths: ['(empty response body)'], extraPaths, absentVsEmptyArrays };
  }

  const body = responseBody as Record<string, unknown>;
  // Responses shape may be bare response object or wrapped { response: ... }
  const root = body.object === 'response' || Array.isArray(body.output) ? body : body.response;
  if (!root || typeof root !== 'object') {
    if (templateId.startsWith('grok') || templateId.includes('responses')) {
      // chat completions etc.
      return { templateId, templateName, missingPaths: [], extraPaths: [], absentVsEmptyArrays: [] };
    }
  }

  const requireAnnotations = templateId === 'grok_build_responses_v1';
  if (requireAnnotations) {
    walkOutputText(responseBody, '', (path, obj) => {
      const full = `${path}.annotations`;
      if (!('annotations' in obj)) {
        missingPaths.push(full);
        absentVsEmptyArrays.push(`${full} (key absent)`);
      } else if (obj.annotations === null) {
        missingPaths.push(`${full} (null)`);
        absentVsEmptyArrays.push(`${full} (null)`);
      } else if (!Array.isArray(obj.annotations)) {
        missingPaths.push(`${full} (not array)`);
      }
      const known = new Set(['type', 'text', 'annotations', 'logprobs']);
      for (const k of Object.keys(obj)) {
        if (!known.has(k)) extraPaths.push(`${path}.${k}`);
      }
    });
  }

  missingPaths.sort();
  extraPaths.sort();
  absentVsEmptyArrays.sort();
  return { templateId, templateName, missingPaths, extraPaths, absentVsEmptyArrays };
}

/** Pretty JSON string for side-by-side panels. */
export function formatJSON(data: unknown): string {
  if (data === null || data === undefined) return '';
  try {
    return JSON.stringify(data, null, 2);
  } catch {
    return String(data);
  }
}

/** Build a simple line-oriented diff for preview (added/removed lines). */
export function buildLineDiff(
  before: string,
  after: string
): Array<{ type: 'same' | 'add' | 'del'; text: string }> {
  const a = before.split('\n');
  const b = after.split('\n');
  // LCS-free simple scan for UI (good enough for annotations fills).
  const result: Array<{ type: 'same' | 'add' | 'del'; text: string }> = [];
  let i = 0;
  let j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) {
      result.push({ type: 'same', text: a[i] });
      i++;
      j++;
      continue;
    }
    // Prefer showing deletes then adds when lines diverge
    if (i < a.length && (j >= b.length || !b.slice(j).includes(a[i]))) {
      result.push({ type: 'del', text: a[i] });
      i++;
      continue;
    }
    if (j < b.length) {
      result.push({ type: 'add', text: b[j] });
      j++;
      continue;
    }
    break;
  }
  return result;
}
