import { useState } from 'react';
import { fileKind } from '../api/files';
import { Icon } from '../components/Icon';
import { useT } from '../i18n';
import { extOf, type HandlerId } from '../lib/openWith';
import { appsFor, defaultAppFor, HANDLER_ICON, handlerHint, handlerName, setDefaultApp } from './openWithApps';

/**
 * "Open with": the apps that can open a file, with "Always use this app"
 * for its type (saved per person, so it follows them to other devices).
 */
export function OpenWithDialog({ name, onOpen, onClose }: { name: string; onOpen: (id: HandlerId) => void; onClose: () => void }) {
  const t = useT();
  const apps = appsFor(name);
  const current = defaultAppFor(name);
  const [pick, setPick] = useState<HandlerId | null>(current ?? apps[0] ?? null);
  const [always, setAlways] = useState(!!current);
  const ext = extOf(name);
  const kind = fileKind(name);

  const open = () => {
    if (!pick) return;
    if (always) setDefaultApp(name, pick);
    else if (current) setDefaultApp(name, null);
    onClose();
    onOpen(pick);
  };

  return (
    <div className="ow-scrim" role="presentation" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        className="ow-sheet"
        role="dialog"
        aria-modal="true"
        aria-label={t('openwith.title')}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.stopPropagation();
            onClose();
          }
          if (e.key === 'Enter' && pick) {
            e.preventDefault();
            open();
          }
        }}
      >
        <header className="ow-head">
          <span className="ow-file">
            <Icon name="file" size={18} />
          </span>
          <div>
            <h2>{t('openwith.title')}</h2>
            <p className="ellipsis">{name}</p>
          </div>
        </header>
        {apps.length ? (
          <div className="ow-list" role="radiogroup" aria-label={t('openwith.apps')}>
            {apps.map((id, i) => (
              <button
                key={id}
                type="button"
                role="radio"
                aria-checked={pick === id}
                autoFocus={pick === id || (!pick && i === 0)}
                className={`ow-app${pick === id ? ' on' : ''}`}
                onClick={() => setPick(id)}
                onDoubleClick={() => {
                  setPick(id);
                  onClose();
                  if (always) setDefaultApp(name, id);
                  onOpen(id);
                }}
              >
                <span className={`ow-icon ow-${id}`}>
                  <Icon name={HANDLER_ICON[id]} size={20} />
                </span>
                <span className="ow-text">
                  <strong>
                    {handlerName(id)}
                    {current === id && <em>{t('openwith.isDefault')}</em>}
                  </strong>
                  <small>{handlerHint(id)}</small>
                </span>
              </button>
            ))}
          </div>
        ) : (
          <p className="ow-none">{t('openwith.none')}</p>
        )}
        <label className="ow-always">
          <input type="checkbox" checked={always} disabled={!pick} onChange={(e) => setAlways(e.target.checked)} />
          <span>{ext ? t('openwith.always', { ext: `.${ext}` }) : t('openwith.alwaysKind', { kind: t(`openwith.kind.${kind}`) })}</span>
        </label>
        <footer className="ow-foot">
          <button type="button" className="ghost" onClick={onClose}>
            {t('common.cancel')}
          </button>
          <button type="button" disabled={!pick} onClick={open}>
            {t('openwith.open')}
          </button>
        </footer>
      </div>
    </div>
  );
}
