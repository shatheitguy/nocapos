import { useEffect, useState } from 'react';
import type { ApiResult } from '../api/client';
import { toast } from '../state/toasts';

/** Same rule as the server: an http(s) URL with a host, no user:password. */
export function validWebAddress(s: string): boolean {
  const v = s.trim();
  if (!v) return true;
  try {
    const u = new URL(v);
    return (u.protocol === 'http:' || u.protocol === 'https:') && !!u.hostname && !u.username && !u.password && !/\s/.test(v);
  } catch {
    return false;
  }
}

/**
 * The address "Open" uses for an app or container, set by an admin (e.g.
 * https://jellyfin.home.lan behind a reverse proxy, or http://10.0.0.50:8096).
 * Empty = automatic: the container's own IP on macvlan/ipvlan, else this
 * server and the published port.
 */
export function WebAddressField({ value, automatic, onSave, onSaved }: {
  value: string;
  /** What Open uses when no address is set (shown as the placeholder). */
  automatic?: string | null;
  onSave: (url: string) => Promise<ApiResult<{ url: string }>>;
  onSaved?: (url: string) => void;
}) {
  const [text, setText] = useState(value);
  const [busy, setBusy] = useState(false);
  useEffect(() => setText(value), [value]);
  const valid = validWebAddress(text);
  const dirty = text.trim() !== value;

  const save = async (url: string) => {
    setBusy(true);
    const r = await onSave(url.trim());
    setBusy(false);
    if (!r.ok) return toast('error', 'Couldn’t save the web address', r.error);
    toast('success', r.data.url ? 'Open now goes to ' + r.data.url : 'Open uses the automatic address again');
    setText(r.data.url);
    onSaved?.(r.data.url);
  };

  return (
    <form
      className="web-address"
      onSubmit={(e) => {
        e.preventDefault();
        if (valid && dirty) void save(text);
      }}
    >
      <label>
        <span className="section-label">Web address</span>
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={automatic || 'https://app.home.lan or http://10.0.0.50:8096'}
          spellCheck={false}
          autoComplete="off"
          aria-invalid={!valid}
        />
      </label>
      <div className="web-address-row">
        <span className={`small ${valid ? 'muted' : 'error'}`}>
          {valid ? (text.trim() ? 'Open goes here everywhere: App Store, home screen and Containers.' : 'Automatic' + (automatic ? `: ${automatic}` : '')) : 'Use a full address starting with http:// or https://'}
        </span>
        <span className="spacer" />
        {value && (
          <button type="button" className="ghost small" disabled={busy} onClick={() => void save('')}>
            Use automatic
          </button>
        )}
        <button type="submit" className="small" disabled={busy || !valid || !dirty}>
          {busy ? 'Saving…' : 'Save'}
        </button>
      </div>
    </form>
  );
}
