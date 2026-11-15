// A pretend host for previewing the shell in a normal browser
// (`npx vite --config vite.shell.config.ts`, then shell.html?surface=dock).
// Only loaded by bridge.ts when the real host's message handler is missing.
// All names here are made up; nothing is read from the machine.
import type { Bridge } from './bridge';
import type { AppInfo, AudioState, BrightnessState, GreeterReply, NetworkInfo, NotifyItem, WindowInfo } from './protocol';

const TILE: Record<string, [string, string]> = {
  files: ['#3b82f6', 'M4 7h6l2 2h8v9H4z'],
  web: ['#0ea5e9', 'M12 4a8 8 0 1 0 0 16 8 8 0 0 0 0-16zM4 12h16M12 4c2.5 2.5 2.5 13.5 0 16M12 4c-2.5 2.5-2.5 13.5 0 16'],
  terminal: ['#1f2937', 'M6 8l4 4-4 4M12 16h6'],
  editor: ['#f59e0b', 'M6 4h9l3 3v13H6zM9 10h6M9 14h6'],
  calc: ['#64748b', 'M7 4h10v16H7zM9 7h6M9 11h2M13 11h2M9 15h2M13 15h2'],
  settings: ['#6b7280', 'M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6zM12 3v3M12 18v3M3 12h3M18 12h3'],
  music: ['#ec4899', 'M9 17V6l10-2v11M9 17a2 2 0 1 1-4 0 2 2 0 0 1 4 0zM19 15a2 2 0 1 1-4 0 2 2 0 0 1 4 0z'],
  video: ['#ef4444', 'M5 6h14v12H5zM10 9v6l5-3z'],
  photos: ['#10b981', 'M4 6h16v12H4zM7 15l3-4 3 3 2-2 3 3'],
  writer: ['#2563eb', 'M6 4h12v16H6zM9 8h6M9 12h6M9 16h4'],
  sheet: ['#16a34a', 'M5 5h14v14H5zM5 10h14M5 15h14M10 5v14'],
  slides: ['#ea580c', 'M4 5h16v11H4zM12 16v4M8 20h8'],
  mail: ['#0284c7', 'M4 6h16v12H4zM4 7l8 6 8-6'],
  chat: ['#8b5cf6', 'M5 5h14v10H9l-4 4z'],
  chess: ['#78350f', 'M9 20h6M10 17h4l1-6h-6zM12 4v4M10 6h4'],
  mines: ['#475569', 'M12 7a5 5 0 1 0 0 10 5 5 0 0 0 0-10zM12 3v3M12 18v3M3 12h3M18 12h3'],
  cards: ['#be123c', 'M7 4h9v14H7zM10 9l2-2 2 2-2 4z'],
  monitor: ['#0f766e', 'M3 12h4l3-7 4 14 3-7h4'],
  disks: ['#7c3aed', 'M4 6h16v12H4zM4 14h16M8 17h.01'],
  archive: ['#a16207', 'M4 5h16v4H4zM5 9h14v10H5zM10 13h4'],
  shot: ['#0891b2', 'M4 8h4l2-2h4l2 2h4v10H4zM12 10a3 3 0 1 0 0 6 3 3 0 0 0 0-6z'],
  camera: ['#334155', 'M4 8h12v8H4zM16 11l4-2v6l-4-2'],
  clock: ['#111827', 'M12 4a8 8 0 1 0 0 16 8 8 0 0 0 0-16zM12 8v4l3 2'],
  weather: ['#38bdf8', 'M7 17h10a4 4 0 0 0 0-8 5 5 0 0 0-9.6 1.5A3.5 3.5 0 0 0 7 17z'],
  maps: ['#22c55e', 'M9 4l6 2 5-2v14l-5 2-6-2-5 2V6zM9 4v14M15 6v14'],
  notes: ['#eab308', 'M6 4h12v16H6zM9 9h6M9 13h6'],
  paint: ['#db2777', 'M12 4a8 8 0 1 0 0 16c1 0 1-1 1-2s1-2 2-2h2a3 3 0 0 0 3-3 8 8 0 0 0-8-9z'],
  store: ['#e5383b', 'M5 8h14l-1 12H6zM9 8V6a3 3 0 0 1 6 0v2'],
};

function icon(kind: string): string {
  const [bg, d] = TILE[kind] ?? ['#555', 'M6 6h12v12H6z'];
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48"><defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#fff" stop-opacity=".28"/><stop offset=".5" stop-color="#fff" stop-opacity="0"/></linearGradient></defs><rect width="48" height="48" rx="11" fill="${bg}"/><rect width="48" height="48" rx="11" fill="url(#g)"/><g transform="translate(8 8) scale(1.333)" fill="none" stroke="#fff" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="${d}"/></g></svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

const app = (id: string, name: string, kind: string, categories: string[], comment = '', keywords: string[] = []): AppInfo => ({
  id: `${id}.desktop`,
  name,
  comment,
  icon: kind === 'none' ? '' : icon(kind),
  categories,
  keywords,
});

const APPS: AppInfo[] = [
  app('files', 'Files', 'files', ['Utility', 'FileManager'], 'Browse your files', ['folder', 'explorer']),
  app('web', 'Web Browser', 'web', ['Network', 'WebBrowser'], 'Browse the web', ['internet', 'www']),
  app('terminal', 'Terminal', 'terminal', ['System', 'TerminalEmulator'], 'Run commands', ['shell', 'console', 'cli']),
  app('editor', 'Text Editor', 'editor', ['Utility', 'TextEditor'], 'Edit text files', ['notepad', 'txt']),
  app('calc', 'Calculator', 'calc', ['Utility', 'Calculator'], 'Do the maths'),
  app('settings', 'System Settings', 'settings', ['Settings', 'System'], 'Hardware and desktop settings', ['preferences']),
  app('music', 'Music', 'music', ['AudioVideo', 'Audio', 'Player'], 'Play your music', ['songs', 'mp3']),
  app('video', 'Videos', 'video', ['AudioVideo', 'Video', 'Player'], 'Watch videos', ['movie', 'mp4']),
  app('photos', 'Image Viewer', 'photos', ['Graphics', 'Viewer'], 'Look at pictures', ['png', 'jpg']),
  app('writer', 'Writer', 'writer', ['Office', 'WordProcessor'], 'Write documents', ['doc', 'word']),
  app('sheet', 'Sheets', 'sheet', ['Office', 'Spreadsheet'], 'Number crunching', ['xls', 'table']),
  app('slides', 'Slides', 'slides', ['Office', 'Presentation'], 'Make presentations', ['deck']),
  app('mail', 'Mail', 'mail', ['Network', 'Email'], 'Read and send email', ['inbox']),
  app('chat', 'Chat', 'chat', ['Network', 'InstantMessaging'], 'Talk to people', ['messages']),
  app('chess', 'Chess', 'chess', ['Game', 'BoardGame'], 'Play chess'),
  app('mines', 'Mines', 'mines', ['Game', 'LogicGame'], 'Clear the field'),
  app('cards', 'Solitaire', 'cards', ['Game', 'CardGame'], 'A game of patience'),
  app('monitor', 'System Monitor', 'monitor', ['System', 'Monitor'], 'Processes and resources', ['task', 'cpu']),
  app('disks', 'Disks', 'disks', ['System', 'Utility'], 'Manage drives', ['partition']),
  app('archive', 'Archives', 'archive', ['Utility', 'Archiving'], 'Open zip files', ['zip', 'tar']),
  app('shot', 'Screenshot', 'shot', ['Utility', 'Graphics'], 'Capture the screen', ['capture']),
  app('camera', 'Camera', 'camera', ['AudioVideo', 'Recorder'], 'Take photos and videos', ['webcam']),
  app('clock', 'Clocks', 'clock', ['Clock'], 'Alarms and timers', ['alarm', 'timer']),
  app('weather', 'Weather', 'weather', [], 'The forecast'),
  app('maps', 'Maps', 'maps', ['Maps'], 'Find places'),
  app('notes', 'Notes', 'notes', [], 'Jot things down', ['memo']),
  app('paint', 'Paint', 'paint', ['Graphics', '2DGraphics'], 'Draw pictures', ['draw']),
  app('store', 'Software', 'store', ['System', 'PackageManager'], 'Add apps', ['install']),
  app('readme', 'Read Me', 'none', [], 'A file without an icon'),
];

export function startMock(bridge: Bridge): (json: string) => void {
  const surface = new URLSearchParams(location.search).get('surface') ?? 'bar';
  let pinned = ['files.desktop', 'web.desktop', 'terminal.desktop', 'editor.desktop', 'music.desktop', 'settings.desktop'];
  let windows: WindowInfo[] = [
    { id: 1, appId: 'web', title: 'Start page — Web Browser', active: true, minimized: false, maximized: true },
    { id: 2, appId: 'files', title: 'Home', active: false, minimized: false, maximized: false },
    { id: 3, appId: 'terminal', title: 'alex: ~', active: false, minimized: true, maximized: false },
    { id: 4, appId: 'calc', title: 'Calculator', active: false, minimized: false, maximized: false },
    { id: 5, appId: 'unknown-tool', title: 'Some Tool', active: false, minimized: false, maximized: false },
  ];
  let audio: AudioState = { available: true, volume: 62, muted: false };
  let brightness: BrightnessState = { available: true, percent: 80 };
  const network: NetworkInfo = { state: 'online', kind: 'wifi', name: 'Home Wi-Fi', strength: 72 };
  const now = Date.now();
  let notes: NotifyItem[] = [
    { id: 'n1', source: 'nocapos', app: 'NoCapOS', icon: '', title: 'Backup finished', body: 'Your files were backed up to the storage pool.', time: now - 4 * 60_000, urgency: 1, actions: [{ key: 'default', label: 'Open' }], read: false },
    { id: 'n2', source: 'app', app: 'Mail', icon: icon('mail'), title: 'Sam Lee', body: 'Lunch tomorrow? I found a new place near the station.', time: now - 25 * 60_000, urgency: 1, actions: [{ key: 'reply', label: 'Reply' }, { key: 'archive', label: 'Archive' }], read: false },
    { id: 'n3', source: 'app', app: 'Mail', icon: icon('mail'), title: 'Weekly digest', body: 'Five new posts from the people you follow.', time: now - 3 * 3600_000, urgency: 0, actions: [], read: true },
    { id: 'n4', source: 'nocapos', app: 'NoCapOS', icon: '', title: 'Update available', body: 'NoCapOS 0.9.1 is ready to install.', time: now - 26 * 3600_000, urgency: 1, actions: [{ key: 'install', label: 'Install' }], read: true },
    { id: 'n5', source: 'app', app: 'Clocks', icon: icon('clock'), title: 'Timer done', body: 'Tea is ready.', time: now - 50 * 60_000, urgency: 2, actions: [], read: false },
  ];
  let nextNote = 10;

  const emit = (name: string, payload: unknown, ms = 0) => setTimeout(() => bridge.event(name, payload), ms);
  const unread = () => notes.filter((n) => !n.read).length;
  const winChanged = () => emit('windows.changed', windows.map((w) => ({ ...w })));
  const log = (...a: unknown[]) => console.info('[mock host]', ...a);

  const handle = (method: string, p: Record<string, unknown>): unknown => {
    switch (method) {
      case 'shell.info':
        return { surface, user: 'alex', realName: 'Alex Rivera', voice: new URLSearchParams(location.search).get('voice') ?? 'genz', theme: new URLSearchParams(location.search).get('theme') ?? 'dark', version: '0.9.0-dev' };
      case 'surface.show':
      case 'surface.hide':
      case 'surface.toggle':
      case 'surface.size':
        log(method, p);
        return null;
      case 'apps.list':
        return APPS;
      case 'apps.launch': {
        const a = APPS.find((x) => x.id === p.id);
        if (!a) throw new Error('unknown app');
        const id = Math.max(0, ...windows.map((w) => Number(w.id))) + 1;
        windows = windows.map((w) => ({ ...w, active: false }));
        windows.push({ id, appId: a.id.replace(/\.desktop$/, ''), title: a.name, active: true, minimized: false, maximized: false });
        winChanged();
        return null;
      }
      case 'dock.get':
        return { pinned };
      case 'dock.set':
        pinned = (p.pinned as string[]).slice(0, 24);
        return null;
      case 'windows.list':
        return windows;
      case 'windows.focus':
        windows = windows.map((w) => ({ ...w, active: w.id === p.id, minimized: w.id === p.id ? false : w.minimized }));
        winChanged();
        return null;
      case 'windows.minimize':
        windows = windows.map((w) => (w.id === p.id ? { ...w, minimized: true, active: false } : w));
        winChanged();
        return null;
      case 'windows.close':
        windows = windows.filter((w) => w.id !== p.id);
        winChanged();
        return null;
      case 'windows.maximize':
        windows = windows.map((w) => (w.id === p.id ? { ...w, maximized: !w.maximized } : w));
        winChanged();
        return null;
      case 'audio.get':
        return audio;
      case 'audio.set':
        audio = { ...audio, ...(typeof p.volume === 'number' ? { volume: p.volume } : {}), ...(typeof p.muted === 'boolean' ? { muted: p.muted } : {}) };
        emit('audio.changed', audio);
        return audio;
      case 'brightness.get':
        return brightness;
      case 'brightness.set':
        brightness = { ...brightness, percent: Number(p.percent) };
        emit('brightness.changed', brightness);
        return brightness;
      case 'network.get':
        return network;
      case 'power.action':
      case 'nocapos.open':
        log(method, p);
        return null;
      case 'notify.list':
        return notes;
      case 'notify.dismiss':
        notes = notes.filter((n) => n.id !== p.id);
        emit('notify.closed', { id: p.id });
        emit('notify.unread', { count: unread() });
        return null;
      case 'notify.invoke':
        log('invoke', p);
        notes = notes.filter((n) => n.id !== p.id);
        emit('notify.closed', { id: p.id });
        emit('notify.unread', { count: unread() });
        return null;
      case 'notify.clear':
        for (const n of notes) emit('notify.closed', { id: n.id });
        notes = [];
        emit('notify.unread', { count: 0 });
        return null;
      case 'notify.read':
        notes = notes.map((n) => ({ ...n, read: true }));
        emit('notify.unread', { count: 0 });
        return null;
      case 'polkit.respond':
        emit('polkit.done', { cookie: p.cookie, ok: p.password === 'nocap' }, 600);
        if (p.password === 'nocap') emit('polkit.request', polkit(), 2500);
        return null;
      case 'polkit.cancel':
        emit('polkit.done', { cookie: p.cookie, ok: false });
        emit('polkit.request', polkit(), 2500);
        return null;
      case 'greeter.users':
        return [
          { name: 'alex', realName: 'Alex Rivera' },
          { name: 'sam', realName: 'Sam Lee' },
          { name: 'guest', realName: '' },
        ];
      case 'greeter.login':
        return { state: 'secret', message: 'Password' } satisfies GreeterReply;
      case 'greeter.respond':
        return p.response === 'nocap' ? { state: 'done', message: '' } : { state: 'failed', message: 'Authentication failed' };
      case 'greeter.cancel':
        return null;
      default:
        throw new Error(`unknown method ${method}`);
    }
  };

  let cookie = 0;
  const polkit = () => ({
    cookie: `c${++cookie}`,
    message: 'Authentication is needed to change the system time zone.',
    icon: '',
    user: 'alex',
  });

  // A little life for the preview.
  const fresh = (): NotifyItem => {
    const k = nextNote++;
    const urgent = k % 4 === 0;
    return {
      id: `n${k}`,
      source: k % 3 === 0 ? 'nocapos' : 'app',
      app: k % 3 === 0 ? 'NoCapOS' : 'Chat',
      icon: k % 3 === 0 ? '' : icon('chat'),
      title: urgent ? 'Disk almost full' : k % 3 === 0 ? 'Storage check passed' : 'Sam Lee',
      body: urgent ? 'Only 2 GB left on the system drive.' : k % 3 === 0 ? 'All drives look healthy.' : `Message number ${k}: are you around later?`,
      time: Date.now(),
      urgency: urgent ? 2 : 1,
      actions: k % 3 === 0 ? [] : [{ key: 'default', label: 'Open' }, { key: 'reply', label: 'Reply' }],
      read: false,
    };
  };
  if (surface === 'banners' || surface === 'center' || surface === 'bar') {
    setInterval(() => {
      const n = fresh();
      notes = [n, ...notes];
      bridge.event('notify.added', n);
      bridge.event('notify.unread', { count: unread() });
    }, surface === 'banners' ? 3500 : 15000);
    if (surface === 'banners') emit('notify.added', fresh(), 300);
  }
  if (surface === 'osd') {
    let up = true;
    setInterval(() => {
      const kind = Math.random() > 0.5 ? 'volume' : 'brightness';
      const value = kind === 'volume' ? audio.volume : brightness.percent;
      const v = Math.max(0, Math.min(100, value + (up ? 10 : -10)));
      if (v === 100 || v === 0) up = !up;
      if (kind === 'volume') audio = { ...audio, volume: v };
      else brightness = { ...brightness, percent: v };
      bridge.event('osd.show', { kind, value: v, muted: kind === 'volume' && v === 0 });
    }, 1600);
  }
  if (surface === 'polkit') emit('polkit.request', polkit(), 200);
  if (surface === 'launcher' || surface === 'center') emit('surface.visible', { name: surface, visible: true }, 100);

  return (json: string) => {
    const msg = JSON.parse(json) as { id: number; method: string; params?: Record<string, unknown> };
    setTimeout(() => {
      try {
        bridge.reply(msg.id, true, handle(msg.method, msg.params ?? {}));
      } catch (e) {
        bridge.reply(msg.id, false, { error: e instanceof Error ? e.message : String(e) });
      }
    }, 15);
  };
}
