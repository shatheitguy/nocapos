// One notification, as a banner or a card in the control center.
import type { MouseEvent } from 'react';
import { Icon } from '../components/Icon';
import { useT } from './i18n';
import { age, toMs } from './logic';
import type { NotifyItem } from './protocol';
import { AppIcon, LogoMark } from './ui';

export function NoteTime({ time, now }: { time: number; now: number }) {
  const t = useT();
  const ms = toMs(time);
  const a = age(ms, now);
  const text = !a
    ? new Date(ms).toLocaleDateString([], { month: 'short', day: 'numeric' })
    : a.unit === 'now'
      ? t('nsh.note.now')
      : a.unit === 'min'
        ? t('nsh.note.min', { n: a.n })
        : t('nsh.note.hour', { n: a.n });
  return <span className="nsh-note-time">{text}</span>;
}

/** The icon for a notification: NoCapOS's own carry the NoCap mark. */
export function NoteIcon({ item, size = 34 }: { item: Pick<NotifyItem, 'source' | 'icon' | 'app'>; size?: number }) {
  if (item.source === 'nocapos' && !item.icon) {
    return (
      <span className="nsh-note-mark" style={{ width: size, height: size }}>
        <LogoMark size={Math.round(size * 0.8)} />
      </span>
    );
  }
  return <AppIcon icon={item.icon} name={item.app || '?'} size={size} nocap={item.source === 'nocapos'} />;
}

export function Note({
  item,
  now,
  banner = false,
  onOpen,
  onDismiss,
  onAction,
}: {
  item: NotifyItem;
  now: number;
  banner?: boolean;
  onOpen: () => void;
  onDismiss: () => void;
  onAction: (key: string) => void;
}) {
  const t = useT();
  const buttons = item.actions.filter((a) => a.key !== 'default' && a.label);
  const stop = (fn: () => void) => (e: MouseEvent) => {
    e.stopPropagation();
    fn();
  };
  return (
    <div
      className={`nsh-note ${banner ? 'banner' : ''} ${item.urgency === 2 ? 'urgent' : ''} ${item.read ? '' : 'unread'}`}
      role={banner ? 'alert' : 'listitem'}
      tabIndex={0}
      onClick={onOpen}
      onKeyDown={(e) => {
        if (e.key === 'Enter') onOpen();
        if (e.key === 'Delete') onDismiss();
      }}
    >
      <NoteIcon item={item} />
      <div className="nsh-note-text">
        <div className="nsh-note-top">
          <b className="nsh-note-title">{item.title || item.app}</b>
          <NoteTime time={item.time} now={now} />
        </div>
        {banner && item.app && item.title && <span className="nsh-note-app">{item.source === 'nocapos' ? 'NoCapOS' : item.app}</span>}
        {item.body && <div className="nsh-note-body">{item.body}</div>}
        {buttons.length > 0 && (
          <div className="nsh-note-actions">
            {buttons.slice(0, 3).map((a) => (
              <button key={a.key} type="button" className="nsh-note-btn" onClick={stop(() => onAction(a.key))}>
                {a.label}
              </button>
            ))}
          </div>
        )}
      </div>
      <button type="button" className="nsh-note-x" aria-label={t('nsh.note.dismiss')} title={t('nsh.note.dismiss')} onClick={stop(onDismiss)}>
        <Icon name="close" size={12} />
      </button>
    </div>
  );
}
