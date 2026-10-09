// Stroke icon set (24px grid, currentColor).
const paths: Record<string, string> = {
  launcher: 'M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z',
  monitor: 'M3 12h4l3-7 4 14 3-7h4',
  containers: 'M3 7.5 12 3l9 4.5v9L12 21l-9-4.5zM3 7.5l9 4.5 9-4.5M12 12v9',
  settings:
    'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z',
  store: 'M5 8h14l-1 12H6zM9 8V6a3 3 0 0 1 6 0v2',
  brave: 'M12 3l6 2 1 3-1 7-6 5-6-5-1-7 1-3zM12 8l-2.5 3.5L12 14l2.5-2.5z',
  desktop: 'M3 5h18v11H3zM8 20h8M12 16v4',
  info: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 11v6M12 7.5v.01',
  power: 'M12 3v9M6.3 6.3a8 8 0 1 0 11.4 0',
  lock: 'M6 11h12v10H6zM8.5 11V7.5a3.5 3.5 0 0 1 7 0V11',
  moon: 'M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z',
  sun: 'M12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4',
  auto: 'M12 21a9 9 0 1 0 0-18v18z M12 3a9 9 0 0 1 0 18',
  close: 'M6 6l12 12M18 6 6 18',
  minimize: 'M6 12h12',
  maximize: 'M5 5h14v14H5z',
  restore: 'M8 8h11v11H8zM5 16V5h11',
  search: 'M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM20 20l-4-4',
  play: 'M7 5v14l12-7z',
  stop: 'M6 6h12v12H6z',
  restart: 'M4 12a8 8 0 1 0 2.3-5.7M4 4v4h4',
  logs: 'M5 4h14v16H5zM8 8h8M8 12h8M8 16h5',
  cpu: 'M7 7h10v10H7zM10 10h4v4h-4zM9 3v4M15 3v4M9 17v4M15 17v4M3 9h4M3 15h4M17 9h4M17 15h4',
  memory: 'M3 8h18v8H3zM7 8v8M11 8v8M15 8v8M6 16v3M18 16v3',
  disk: 'M4 5h16v14H4zM4 14h16M8 17h.01',
  gpu: 'M3 7h18v10H3zM7 11a1.5 1.5 0 1 0 3 0 1.5 1.5 0 0 0-3 0M14 11a1.5 1.5 0 1 0 3 0 1.5 1.5 0 0 0-3 0M6 17v3',
  network: 'M4 8a12 12 0 0 1 16 0M7 11.5a7.5 7.5 0 0 1 10 0M10 15a3 3 0 0 1 4 0M12 18.5v.01',
  user: 'M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM4 21a8 8 0 0 1 16 0',
  check: 'M5 12.5 10 17 19 7',
  alert: 'M12 9v4M12 17v.01M10.3 3.9 2.4 18a2 2 0 0 0 1.7 3h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z',
  arrowDown: 'M12 5v14M6 13l6 6 6-6',
  arrowUp: 'M12 19V5M6 11l6-6 6 6',
  palette: 'M12 21a9 9 0 1 1 9-9c0 2-1.5 3-3.5 3H16a2 2 0 0 0-1.4 3.4A1.6 1.6 0 0 1 13.5 21zM7.5 11.5h.01M10 7.5h.01M15 7.5h.01',
  image: 'M4 5h16v14H4zM4 16l5-5 4 4 2-2 5 5M15.5 9.5h.01',
  pin: 'M9 4h6l-1 6 3 3H7l3-3zM12 13v7',
  trash: 'M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13',
  terminal: 'M4 5h16v14H4zM7 9l3 3-3 3M12 15h5',
  files: 'M3 6h6l2 2h10v11H3z',
  folder: 'M3 6.5A1.5 1.5 0 0 1 4.5 5H9l2 2h8.5A1.5 1.5 0 0 1 21 8.5v9a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 17.5z',
  folderPlus: 'M3 6.5A1.5 1.5 0 0 1 4.5 5H9l2 2h8.5A1.5 1.5 0 0 1 21 8.5v9a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 17.5zM12 10.5v5M9.5 13h5',
  file: 'M6 3h8l4 4v14H6zM14 3v4h4',
  fileText: 'M6 3h8l4 4v14H6zM14 3v4h4M9 12h6M9 16h6',
  fileCode: 'M6 3h8l4 4v14H6zM14 3v4h4M10 12l-2 2 2 2M14 12l2 2-2 2',
  fileImage: 'M6 3h8l4 4v14H6zM14 3v4h4M8 18l3-3 2 2 1.5-1.5L17 18M10 11.5h.01',
  fileVideo: 'M6 3h8l4 4v14H6zM14 3v4h4M10.5 11.5v5l4-2.5z',
  fileAudio: 'M6 3h8l4 4v14H6zM14 3v4h4M11 17.5a1.5 1.5 0 1 1-1.5-1.5H11v-5l3.5-1',
  fileArchive: 'M6 3h8l4 4v14H6zM14 3v4h4M11 6h2M11 9h2M11 12h2M10.5 15h3v3h-3z',
  filePdf: 'M6 3h8l4 4v14H6zM14 3v4h4M8.5 17v-4h1.5a1.3 1.3 0 0 1 0 2.6H8.5M13 13v4h1a2 2 0 0 0 0-4z',
  upload: 'M12 16V4M7 9l5-5 5 5M4 16v4h16v-4',
  download: 'M12 4v12M7 11l5 5 5-5M4 20h16',
  grid: 'M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z',
  list: 'M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01',
  columns: 'M4 4h16v16H4zM12 4v16',
  sidebarRight: 'M4 4h16v16H4zM15 4v16',
  swap: 'M7 7h13l-4-4M17 17H4l4 4',
  chevronLeft: 'M15 5l-7 7 7 7',
  chevronRight: 'M9 5l7 7-7 7',
  chevronUp: 'M5 15l7-7 7 7',
  chevronDown: 'M5 9l7 7 7-7',
  clock: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 7v5l3 2',
  sound: 'M4 9v6h4l5 4V5L8 9zM16 9a4 4 0 0 1 0 6M18.5 6.5a8 8 0 0 1 0 11',
  wifi: 'M2.5 9a14 14 0 0 1 19 0M5.5 12.5a9.5 9.5 0 0 1 13 0M8.6 16a5 5 0 0 1 6.8 0M12 19.5h.01',
  ethernet: 'M5 4h14v10H5zM9 14v3M12 14v3M15 14v3M8 20h8M8 8h2M14 8h2',
  users: 'M9 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM2.5 20a6.5 6.5 0 0 1 13 0M16 4.5a3.5 3.5 0 0 1 0 6.5M18 14a6 6 0 0 1 3.5 6',
  logout: 'M15 4h4v16h-4M10 8l-4 4 4 4M6 12h10',
  key: 'M8 15a4 4 0 1 1 0-8 4 4 0 0 1 0 8zM11.5 11H21M18 11v3M15 11v2',
  sliders: 'M4 7h9M17 7h3M15 5v4M4 17h3M11 17h9M9 15v4',
  globe: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18',
  shield: 'M12 3l7 3v5c0 4.5-3 8.3-7 10-4-1.7-7-5.5-7-10V6z',
  scissors: 'M6 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM6 21a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM8.1 7.9 20 20M8.1 16.1 20 4',
  copy: 'M9 9h11v11H9zM5 15V4h11',
  clipboard: 'M8 4h8v3H8zM8 5.5H5V21h14V5.5h-3',
  edit: 'M4 20h4L19 9l-4-4L4 16zM13.5 6.5l4 4',
  eye: 'M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z',
  home: 'M4 11 12 4l8 7v9h-5v-6H9v6H4z',
  drive: 'M3 14h18v6H3zM5 14l2.5-9h9L19 14M7 17h.01',
  save: 'M5 4h11l3 3v13H5zM8 4v5h7V4M8 20v-6h8v6',
  external: 'M14 4h6v6M20 4l-9 9M18 14v6H4V6h6',
  sparkles: 'M12 3l1.8 4.9L19 9.7l-5.2 1.8L12 16l-1.8-4.5L5 9.7l5.2-1.8zM18 14l.9 2.3 2.3.9-2.3.9L18 20l-.9-2.3-2.3-.9 2.3-.9z',
  send: 'M4 12l16-8-6 16-3-6-7-2z',
  plus: 'M12 5v14M5 12h14',
  brain: 'M9 4a3 3 0 0 0-3 3 3 3 0 0 0-1 5 3 3 0 0 0 2 4 2.5 2.5 0 0 0 5 .5V5a2 2 0 0 0-3-1zM15 4a3 3 0 0 1 3 3 3 3 0 0 1 1 5 3 3 0 0 1-2 4 2.5 2.5 0 0 1-5 .5',
  pencil: 'M4 20h4L19 9l-4-4L4 16zM13.5 6.5l4 4',
  menu: 'M4 6h16M4 12h16M4 18h16',
  heart: 'M12 20s-7.5-4.6-7.5-10.2A4.3 4.3 0 0 1 12 7a4.3 4.3 0 0 1 7.5 2.8C19.5 15.4 12 20 12 20z',
  albums: 'M4 9h16v11H4zM6 6h12M8 3h8',
};

export type IconName = keyof typeof paths;

export function Icon({ name, size = 18, className }: { name: IconName; size?: number; className?: string }) {
  return (
    <svg
      className={className}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d={paths[name]} />
    </svg>
  );
}
