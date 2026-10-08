import { Icon } from '../components/Icon';
import { usePrefs } from '../state/prefs';
import { useToasts } from '../state/toasts';

export function Toasts() {
  const all = useToasts((s) => s.toasts);
  const focus = usePrefs((s) => s.focusMode);
  // Focus hides routine notifications; errors still get through.
  const toasts = focus ? all.filter((t) => t.kind === 'error') : all;
  const dismiss = useToasts((s) => s.dismiss);
  return (
    <div className="toasts" aria-live="polite">
      {toasts.map((t) => (
        <div key={t.id} className={`toast ${t.kind}`} onClick={() => dismiss(t.id)}>
          <Icon name={t.kind === 'error' ? 'alert' : t.kind === 'success' ? 'check' : 'info'} size={18} />
          <div>
            <b>{t.title}</b>
            {t.body && <div className="small muted">{t.body}</div>}
          </div>
        </div>
      ))}
    </div>
  );
}
