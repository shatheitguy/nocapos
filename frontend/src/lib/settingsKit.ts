// Small helpers every Settings page shares: telling "this server has no such
// feature" apart from a real failure, and copying with feedback.
import type { ApiResult } from '../api/client';
import { copyText } from '../apps/assistant/markdown';
import { tr } from '../i18n';
import { toast } from '../state/toasts';

/**
 * Whether an answer means this server doesn't offer the feature at all: an
 * older server without the endpoint, or one that says it isn't supported
 * here. Pages show "Not available on this system" instead of an error.
 */
export function missing(r: ApiResult<unknown>): boolean {
  if (r.status === 404 || r.status === 405 || r.status === 501) return true;
  // An older server answers unknown pages with the app itself (not JSON).
  return r.ok && (r.data == null || typeof r.data !== 'object');
}

/** Copy text and say whether it worked. */
export async function copyWithToast(text: string, done: string) {
  if (await copyText(text)) toast('success', done);
  else toast('error', tr('set.copyFail'));
}
