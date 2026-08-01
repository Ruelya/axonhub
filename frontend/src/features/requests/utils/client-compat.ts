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

function hasNonEmptyString(obj: Record<string, unknown>, key: string): boolean {
  const v = obj[key];
  return typeof v === 'string' && v.trim() !== '';
}

function firstOutputText(msg: Record<string, unknown>): string {
  const content = msg.content;
  if (!Array.isArray(content)) return '';
  for (const c of content) {
    if (c && typeof c === 'object') {
      const m = c as Record<string, unknown>;
      if (m.type === 'output_text' && typeof m.text === 'string') {
        return m.text.slice(0, 64);
      }
    }
  }
  return '';
}

/** Stable synthetic id (preview only; backend may backfill from stream hints). */
function syntheticMessageId(index: number, msg: Record<string, unknown>): string {
  const role = typeof msg.role === 'string' ? msg.role : '';
  const text = firstOutputText(msg);
  // Lightweight non-crypto hash for UI preview stability.
  let h = 0;
  const s = `${index}|${role}|${text}`;
  for (let i = 0; i < s.length; i++) h = (Math.imul(31, h) + s.charCodeAt(i)) | 0;
  return `msg_${(h >>> 0).toString(16).padStart(8, '0')}`;
}

function shouldStripConversation(conv: unknown): boolean {
  if (!conv || typeof conv !== 'object' || Array.isArray(conv)) return true;
  return !hasNonEmptyString(conv as Record<string, unknown>, 'id');
}

/**
 * Mirror backend NormalizeResponsesJSON for dry-run diffs:
 * annotations, message id/status/role, strip empty conversation.
 */
function normalizeResponsesValue(value: unknown, msgIndex: { n: number }): boolean {
  if (value === null || value === undefined) return false;
  if (Array.isArray(value)) {
    let changed = false;
    for (const item of value) {
      if (normalizeResponsesValue(item, msgIndex)) changed = true;
    }
    return changed;
  }
  if (typeof value === 'object') {
    const obj = value as Record<string, unknown>;
    let changed = false;

    if ('conversation' in obj && shouldStripConversation(obj.conversation)) {
      delete obj.conversation;
      changed = true;
    }

    if (obj.type === 'output_text') {
      if (!('annotations' in obj) || obj.annotations === null || obj.annotations === undefined) {
        obj.annotations = [];
        changed = true;
      }
    }

    if (obj.type === 'message') {
      const idx = msgIndex.n++;
      if (!hasNonEmptyString(obj, 'id')) {
        obj.id = syntheticMessageId(idx, obj);
        changed = true;
      }
      if (!hasNonEmptyString(obj, 'status')) {
        obj.status = 'completed';
        changed = true;
      }
      if (!hasNonEmptyString(obj, 'role')) {
        obj.role = 'assistant';
        changed = true;
      }
    }

    for (const child of Object.values(obj)) {
      if (normalizeResponsesValue(child, msgIndex)) changed = true;
    }
    return changed;
  }
  return false;
}

/**
 * Apply the same wire patch as backend NormalizeResponsesJSON (Grok-strict suite).
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
  const changed = normalizeResponsesValue(patched, { n: 0 });
  return { original, patched, changed };
}

/**
 * Build a before/after pair for request-body compat display.
 * Prefer real outbound body from request execution when it differs from inbound.
 * Fall back to dry-run prompt_cache_key inject from session header.
 */
export function previewRequestBodyPatch(args: {
  inboundBody: unknown;
  outboundBody?: unknown | null;
  requestHeaders?: unknown;
}): { original: unknown; patched: unknown; changed: boolean; source: 'execution' | 'preview' | 'none' } {
  const { inboundBody, outboundBody, requestHeaders } = args;
  if (inboundBody === null || inboundBody === undefined) {
    return { original: inboundBody, patched: inboundBody, changed: false, source: 'none' };
  }

  // Prefer real outbound execution body when it differs.
  if (outboundBody !== null && outboundBody !== undefined) {
    try {
      const a = JSON.stringify(inboundBody);
      const b = JSON.stringify(outboundBody);
      if (a !== b) {
        return { original: inboundBody, patched: outboundBody, changed: true, source: 'execution' };
      }
    } catch {
      // fall through to dry-run
    }
  }

  // Dry-run: inject prompt_cache_key from X-Grok-Session-Id when missing.
  const sessionId = extractGrokSessionId(requestHeaders);
  if (!sessionId || typeof inboundBody !== 'object' || Array.isArray(inboundBody)) {
    return { original: inboundBody, patched: inboundBody, changed: false, source: 'none' };
  }
  const obj = inboundBody as Record<string, unknown>;
  const existing = obj.prompt_cache_key;
  if (typeof existing === 'string' && existing.trim() !== '') {
    return { original: inboundBody, patched: inboundBody, changed: false, source: 'none' };
  }
  const patched = cloneJSON(inboundBody) as Record<string, unknown>;
  patched.prompt_cache_key = sessionId;
  return { original: inboundBody, patched, changed: true, source: 'preview' };
}

function extractGrokSessionId(headers: unknown): string {
  if (!headers || typeof headers !== 'object') return '';
  const h = headers as Record<string, unknown>;
  const get = (...keys: string[]): string => {
    for (const k of keys) {
      const v = h[k] ?? h[k.toLowerCase()] ?? h[k.toUpperCase()];
      if (typeof v === 'string' && v.trim()) return v.trim();
      if (Array.isArray(v) && typeof v[0] === 'string' && v[0].trim()) return v[0].trim();
    }
    return '';
  };
  for (const id of [
    get('X-Grok-Session-Id', 'x-grok-session-id'),
    get('X-Grok-Conv-Id', 'x-grok-conv-id'),
    get('Session-Id', 'session-id', 'Session_id'),
  ]) {
    if (!id) continue;
    if (id.toLowerCase().startsWith('recap-')) continue;
    return id;
  }
  return '';
}

/** Generic before/after pair for unified diff display. */
export function bodyPairDiff(
  original: unknown,
  patched: unknown
): { beforeText: string; afterText: string; changed: boolean } {
  const beforeText = formatJSON(original);
  const afterText = formatJSON(patched);
  return { beforeText, afterText, changed: beforeText !== afterText };
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

export type DiffLineType = 'same' | 'add' | 'del' | 'meta';

export type DiffLine = {
  type: DiffLineType;
  text: string;
  /** 1-based line number in the "before" document (del / same). */
  oldLine?: number;
  /** 1-based line number in the "after" document (add / same). */
  newLine?: number;
};

/**
 * Line-oriented LCS diff for code-review style display.
 * Full file is returned (no collapse) so the viewer can scroll freely.
 */
export function buildLineDiff(before: string, after: string): DiffLine[] {
  const a = before.length ? before.split('\n') : [''];
  const b = after.length ? after.split('\n') : [''];

  // Drop trailing empty line from JSON.stringify (common when body ends with }\n)
  if (a.length > 1 && a[a.length - 1] === '') a.pop();
  if (b.length > 1 && b[b.length - 1] === '') b.pop();

  const n = a.length;
  const m = b.length;

  // DP LCS length table (fine for response bodies of a few thousand lines)
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      if (a[i] === b[j]) dp[i][j] = dp[i + 1][j + 1] + 1;
      else dp[i][j] = Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }

  const result: DiffLine[] = [];
  let i = 0;
  let j = 0;
  let oldLine = 1;
  let newLine = 1;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      result.push({ type: 'same', text: a[i], oldLine, newLine });
      i++;
      j++;
      oldLine++;
      newLine++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      result.push({ type: 'del', text: a[i], oldLine });
      i++;
      oldLine++;
    } else {
      result.push({ type: 'add', text: b[j], newLine });
      j++;
      newLine++;
    }
  }
  while (i < n) {
    result.push({ type: 'del', text: a[i], oldLine });
    i++;
    oldLine++;
  }
  while (j < m) {
    result.push({ type: 'add', text: b[j], newLine });
    j++;
    newLine++;
  }

  return result;
}

/** Stats for header badges. */
export function diffStats(lines: DiffLine[]): { additions: number; deletions: number } {
  let additions = 0;
  let deletions = 0;
  for (const line of lines) {
    if (line.type === 'add') additions++;
    else if (line.type === 'del') deletions++;
  }
  return { additions, deletions };
}
