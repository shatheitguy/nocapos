import { Icon } from './Icon';

/**
 * The NoCapOS close control for dialogs, sheets and panels: the same button
 * windows have in their title bar, so it follows the theme and the
 * left/right button style exactly like window controls do.
 */
export function WinClose({ onClick, label, className = '' }: { onClick: () => void; label: string; className?: string }) {
  return (
    <div className={`win-buttons win-close-solo ${className}`}>
      <button type="button" className="close" aria-label={label} title={label} onClick={onClick}>
        <Icon name="close" size={14} />
      </button>
    </div>
  );
}
