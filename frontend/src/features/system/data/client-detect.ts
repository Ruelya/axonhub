/**
 * Client detection mirror of backend biz.DetectClient defaults.
 * Used for request-detail badges without an extra GraphQL round-trip.
 */

export type ClientDetectSource = 'explicit' | 'user_agent' | 'none';

export interface ClientDetectResult {
  profileId: string;
  displayName: string;
  source: ClientDetectSource;
  confidence: string;
  userAgent?: string;
  clientHeader?: string;
  clientVersion?: string;
  matchedRules?: string[];
  rawProfileId?: string;
}

interface UARule {
  id: string;
  pattern: string;
  profileId: string;
  priority: number;
}

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

const DEFAULT_UA_RULES: UARule[] = [
  { id: 'grok_pager', pattern: 'grok-pager/', profileId: 'grok_build', priority: 100 },
  { id: 'grok_shell', pattern: 'grok-shell/', profileId: 'grok_build', priority: 100 },
  { id: 'grok_build', pattern: 'grok-build', profileId: 'grok_build', priority: 90 },
  { id: 'codex_tui', pattern: 'codex-tui/', profileId: 'codex', priority: 100 },
  { id: 'codex_cli', pattern: 'codex-cli/', profileId: 'codex', priority: 100 },
  { id: 'codex', pattern: 'codex/', profileId: 'codex', priority: 80 },
  { id: 'claude_cli', pattern: 'claude-cli/', profileId: 'claude_code', priority: 100 },
  { id: 'claude_code', pattern: 'claude-code', profileId: 'claude_code', priority: 90 },
  { id: 'anthropic_claude_code', pattern: '@anthropic-ai/claude-code', profileId: 'claude_code', priority: 90 },
  { id: 'cursor_slash', pattern: 'Cursor/', profileId: 'cursor', priority: 100 },
  { id: 'cursor_dash', pattern: 'cursor-', profileId: 'cursor', priority: 80 },
  { id: 'windsurf_slash', pattern: 'Windsurf/', profileId: 'windsurf', priority: 100 },
  { id: 'windsurf', pattern: 'windsurf', profileId: 'windsurf', priority: 70 },
  { id: 'cline_slash', pattern: 'Cline/', profileId: 'cline', priority: 100 },
  { id: 'cline', pattern: 'cline', profileId: 'cline', priority: 70 },
  { id: 'aider_slash', pattern: 'aider/', profileId: 'aider', priority: 100 },
  { id: 'aider', pattern: 'Aider', profileId: 'aider', priority: 80 },
  { id: 'continue_slash', pattern: 'Continue/', profileId: 'continue', priority: 100 },
  { id: 'continue_dev', pattern: 'continue.dev', profileId: 'continue', priority: 90 },
  { id: 'opencode', pattern: 'opencode', profileId: 'opencode', priority: 80 },
  { id: 'opencode_title', pattern: 'OpenCode', profileId: 'opencode', priority: 80 },
  { id: 'copilot_chat', pattern: 'GitHubCopilotChat/', profileId: 'copilot', priority: 100 },
  { id: 'copilot', pattern: 'copilot', profileId: 'copilot', priority: 60 },
  { id: 'roo_code', pattern: 'Roo-Code', profileId: 'roo_code', priority: 100 },
  { id: 'roo_code_dash', pattern: 'roo-code', profileId: 'roo_code', priority: 90 },
  { id: 'zed_slash', pattern: 'zed/', profileId: 'zed', priority: 100 },
  { id: 'zed', pattern: 'Zed', profileId: 'zed', priority: 80 },
  { id: 'openai_node', pattern: 'openai-node', profileId: 'generic_openai_sdk', priority: 40 },
  { id: 'openai_python', pattern: 'openai-python', profileId: 'generic_openai_sdk', priority: 40 },
  { id: 'openai_slash', pattern: 'OpenAI/', profileId: 'generic_openai_sdk', priority: 30 },
];

const EXPLICIT_HEADER = 'x-axonhub-client';
const EXPLICIT_VERSION_HEADER = 'x-axonhub-client-version';

function normalizeProfileId(id: string): string {
  let v = id.trim().toLowerCase().replace(/-/g, '_').replace(/ /g, '_');
  if (v === 'grokbuild' || v === 'grok') return 'grok_build';
  if (v === 'claude' || v === 'claudecode') return 'claude_code';
  if (v === 'roo' || v === 'roocode') return 'roo_code';
  if (v === 'github_copilot' || v === 'githubcopilot') return 'copilot';
  if (v === 'openai' || v === 'openai_sdk') return 'generic_openai_sdk';
  return v;
}

/** Extract a header value from stored requestHeaders JSON (object or multi-value). */
export function getHeaderValue(headers: unknown, name: string): string {
  if (!headers || typeof headers !== 'object') return '';
  const target = name.toLowerCase();
  for (const [k, v] of Object.entries(headers as Record<string, unknown>)) {
    if (k.toLowerCase() !== target) continue;
    if (typeof v === 'string') return v;
    if (Array.isArray(v) && v.length > 0) return String(v[0]);
  }
  return '';
}

export function detectClientFromHeaders(headers: unknown): ClientDetectResult {
  const ua = getHeaderValue(headers, 'User-Agent');
  const clientHeader = getHeaderValue(headers, EXPLICIT_HEADER) || getHeaderValue(headers, 'X-AxonHub-Client');
  const clientVersion =
    getHeaderValue(headers, EXPLICIT_VERSION_HEADER) || getHeaderValue(headers, 'X-AxonHub-Client-Version');

  if (clientHeader) {
    const normalized = normalizeProfileId(clientHeader);
    const known = PROFILE_NAMES[normalized] && normalized !== 'unknown';
    return {
      profileId: known ? normalized : 'unknown',
      displayName: known ? PROFILE_NAMES[normalized] : PROFILE_NAMES.unknown,
      source: 'explicit',
      confidence: known ? 'high' : 'medium',
      userAgent: ua || undefined,
      clientHeader,
      clientVersion: clientVersion || undefined,
      rawProfileId: clientHeader,
    };
  }

  if (!ua) {
    return {
      profileId: 'unknown',
      displayName: PROFILE_NAMES.unknown,
      source: 'none',
      confidence: 'none',
    };
  }

  const uaLower = ua.toLowerCase();
  const matches = DEFAULT_UA_RULES.filter((r) => uaLower.includes(r.pattern.toLowerCase())).sort(
    (a, b) => b.priority - a.priority
  );

  if (matches.length === 0) {
    return {
      profileId: 'unknown',
      displayName: PROFILE_NAMES.unknown,
      source: 'none',
      confidence: 'none',
      userAgent: ua,
    };
  }

  const best = matches[0];
  return {
    profileId: best.profileId,
    displayName: PROFILE_NAMES[best.profileId] || best.profileId,
    source: 'user_agent',
    confidence: 'high',
    userAgent: ua,
    matchedRules: matches.map((m) => m.id),
  };
}

export function clientSourceLabel(source: ClientDetectSource): string {
  switch (source) {
    case 'explicit':
      return 'X-AxonHub-Client';
    case 'user_agent':
      return 'User-Agent';
    default:
      return '';
  }
}
