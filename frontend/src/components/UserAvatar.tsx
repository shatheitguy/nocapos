import { useAvatar } from '../lib/avatar';

/** The signed-in user's photo, or their initial. `src` overrides (login screen). */
export function UserAvatar({ name, size = '', src }: { name: string; size?: '' | 'lg' | 'xl' | 'xxl'; src?: string | null }) {
  const mine = useAvatar((s) => s.url);
  const url = src === undefined ? mine : src;
  return (
    <span className={`avatar ${size} ${url ? 'has-photo' : ''}`}>
      {url ? <img src={url} alt="" draggable={false} /> : (name || '?').slice(0, 1).toUpperCase()}
    </span>
  );
}
