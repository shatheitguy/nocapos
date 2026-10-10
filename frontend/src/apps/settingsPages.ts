// Settings' page catalogue, kept apart from the Settings app so universal
// search can list the pages without loading the whole app.
import type { IconName } from '../components/Icon';

export type Cat = 'network' | 'general' | 'appearance' | 'wallpaper' | 'desktop' | 'windows' | 'notifications' | 'sound' | 'lock' | 'security' | 'users' | 'account';
export type Sub =
  | 'about' | 'update' | 'storage' | 'datetime' | 'language' | 'backup' | 'troubleshoot' | 'power'
  | 'appearance' | 'wallpaper' | 'desktop' | 'windows'
  | 'notifications' | 'sound'
  | 'security' | 'lock'
  | 'account' | 'users'
  | 'wifi' | `iface:${string}`;

export interface CatInfo {
  id: Cat;
  label: string;
  icon: IconName;
  color: string;
  admin?: boolean;
  /** Sub-pages, in groups (each group is one card on the category page). */
  groups: Sub[][];
}

export const CATS: CatInfo[] = [
  { id: 'network', label: 'Network', icon: 'wifi', color: '#0a84ff', admin: true, groups: [] },
  { id: 'general', label: 'General', icon: 'settings', color: '#8e8e93',
    groups: [['about', 'update'], ['storage'], ['datetime', 'language'], ['backup', 'troubleshoot', 'power']] },
  { id: 'appearance', label: 'Appearance', icon: 'palette', color: '#5e5ce6', groups: [] },
  { id: 'wallpaper', label: 'Wallpaper', icon: 'image', color: '#32ade6', groups: [] },
  { id: 'desktop', label: 'Desktop & Dock', icon: 'launcher', color: '#3a3a3c', groups: [] },
  { id: 'windows', label: 'Windows', icon: 'maximize', color: '#3a3a3c', groups: [] },
  { id: 'notifications', label: 'Notifications', icon: 'bell', color: '#ff3b30', groups: [] },
  { id: 'sound', label: 'Sound', icon: 'sound', color: '#ff2d55', groups: [] },
  { id: 'lock', label: 'Lock Screen', icon: 'lock', color: '#48484a', groups: [] },
  { id: 'security', label: 'Privacy & Security', icon: 'shield', color: '#30a0ff', groups: [] },
  { id: 'users', label: 'Users & Groups', icon: 'users', color: '#007aff', admin: true, groups: [] },
  { id: 'account', label: 'Your Account', icon: 'user', color: '#007aff', groups: [] },
];

export const SUBS: Record<string, { label: string; icon: IconName; color: string; admin?: boolean; desc?: string }> = {
  about: { label: 'About', icon: 'info', color: '#8e8e93' },
  update: { label: 'Software Update', icon: 'download', color: '#8e8e93', admin: true },
  storage: { label: 'Storage', icon: 'disk', color: '#8e8e93' },
  datetime: { label: 'Date & Time', icon: 'clock', color: '#0a84ff' },
  language: { label: 'Language & Region', icon: 'globe', color: '#0a84ff' },
  backup: { label: 'Export & Restore', icon: 'save', color: '#8e8e93', admin: true },
  troubleshoot: { label: 'Troubleshoot', icon: 'wrench', color: '#8e8e93', admin: true },
  power: { label: 'Restart & Shut Down', icon: 'power', color: '#ff453a' },
  appearance: { label: 'Appearance', icon: 'palette', color: '#5e5ce6', desc: 'Theme, accent color, brightness and effects' },
  wallpaper: { label: 'Wallpaper', icon: 'image', color: '#32ade6', desc: 'Photos, gradients or your own picture' },
  desktop: { label: 'Desktop & Dock', icon: 'launcher', color: '#3a3a3c', desc: 'Dock, widgets and desktop icons' },
  windows: { label: 'Windows', icon: 'maximize', color: '#3a3a3c', desc: 'Title bar buttons and double-click' },
  notifications: { label: 'Notifications', icon: 'bell', color: '#ff3b30', desc: 'Banner style, what to be told about, Focus and sounds' },
  sound: { label: 'Sound', icon: 'sound', color: '#ff2d55', desc: 'Sound effects and volume' },
  security: { label: 'Privacy & Security', icon: 'shield', color: '#30a0ff', desc: 'Change your password and ask for a code when you sign in' },
  lock: { label: 'Lock Screen', icon: 'lock', color: '#48484a', desc: 'Auto-lock and screen saver' },
  account: { label: 'Your Account', icon: 'user', color: '#007aff', desc: 'Photo, name and sign out' },
  users: { label: 'Users', icon: 'users', color: '#007aff', admin: true, desc: 'Add people and choose who is an administrator' },
  wifi: { label: 'Wi-Fi', icon: 'wifi', color: '#0a84ff' },
};

/** Everything searchable: deep-link ids (also used by universal search and older links). */
export interface Entry {
  id: string;
  label: string;
  icon: IconName;
  desc: string;
  cat: Cat;
  sub?: Sub;
  admin?: boolean;
  keywords?: string;
  options?: string[];
}

export const ENTRIES: Entry[] = [
  { id: 'network', cat: 'network', admin: true, label: 'Network', icon: 'wifi', desc: 'Wi-Fi, Ethernet and IP addresses', keywords: 'internet lan connection', options: ['Networking'] },
  { id: 'wifi', cat: 'network', sub: 'wifi', admin: true, label: 'Wi-Fi', icon: 'wifi', desc: 'Join a wireless network', keywords: 'wireless wlan ssid hotspot' },
  { id: 'ethernet', cat: 'network', admin: true, label: 'Ethernet & IP address', icon: 'ethernet', desc: 'Wired adapters, static IP, DNS and gateway', keywords: 'ip address dhcp static dns gateway router ipv4 lan cable' },
  { id: 'general', cat: 'general', label: 'General', icon: 'settings', desc: 'Device, storage, updates, date and language' },
  { id: 'about', cat: 'general', sub: 'about', label: 'About', icon: 'info', desc: 'Device name, hardware, Docker and versions', keywords: 'version device hardware docker gpu npu model processor memory serial ip' },
  { id: 'update', cat: 'general', sub: 'update', admin: true, label: 'Software Update', icon: 'download', desc: 'Check for a newer NoCapOS', keywords: 'upgrade version release new' },
  { id: 'storage', cat: 'general', sub: 'storage', label: 'Storage', icon: 'disk', desc: 'Disks and how full they are', keywords: 'disk drive space capacity usage' },
  { id: 'datetime', cat: 'general', sub: 'datetime', label: 'Date & Time', icon: 'clock', desc: 'Time zone, 24-hour clock and calendar', keywords: 'clock time zone calendar week',
    options: ['24-hour time', 'Show seconds', 'Show the date in the Dock', 'Set time zone automatically', 'Time zone', 'First day of the week'] },
  { id: 'language', cat: 'general', sub: 'language', label: 'Language & Region', icon: 'globe', desc: 'Language and region formats', keywords: 'locale currency number format', options: ['NoCapOS language', 'Region formats'] },
  { id: 'backup', cat: 'general', sub: 'backup', admin: true, label: 'Export & Restore', icon: 'save', desc: 'Download a backup of NoCapOS itself', keywords: 'backup export database restore download', options: ['Download a backup', 'Restore'] },
  { id: 'troubleshoot', cat: 'general', sub: 'troubleshoot', admin: true, label: 'Troubleshoot', icon: 'wrench', desc: 'NoCapOS logs', keywords: 'logs debug journal problems errors support' },
  { id: 'power', cat: 'general', sub: 'power', label: 'Restart & Shut Down', icon: 'power', desc: 'Restart or shut down this machine, or sign out', keywords: 'reboot shutdown power off sign out', options: ['Restart', 'Shut down', 'Sign out'] },
  { id: 'appearance', cat: 'appearance', label: 'Appearance', icon: 'palette', desc: 'Theme, accent color, brightness and effects', keywords: 'theme glass classic cyber deck dark light accent color colour',
    options: ['Light or dark', 'Accent color', 'Match wallpaper', 'Brightness', 'Reduce transparency', 'Reduce motion', 'Square corners', 'Reset personalization'] },
  { id: 'wallpaper', cat: 'wallpaper', label: 'Wallpaper', icon: 'image', desc: 'Photos, gradients or your own picture', keywords: 'background photo picture', options: ['Your photo', 'Dim wallpaper'] },
  { id: 'desktop', cat: 'desktop', label: 'Desktop & Dock', icon: 'launcher', desc: 'Dock, widgets and desktop icons', keywords: 'dock taskbar widgets icons grid desktop',
    options: ['Position on screen', 'Icon size', 'Magnify on hover', 'Automatically hide the Dock', 'Show recent apps', 'Show widgets on the desktop', 'Snap icons to grid'] },
  { id: 'windows', cat: 'windows', label: 'Windows', icon: 'maximize', desc: 'Title bar buttons and double-click', keywords: 'title bar macos menu bar', options: ['Window buttons', 'Double-click a title bar to'] },
  { id: 'notifications', cat: 'notifications', label: 'Notifications', icon: 'bell', desc: 'Banner style, what to be told about, Focus and sounds', keywords: 'do not disturb dnd silence alerts banners toasts macos windows notification center updates', options: ['Banner style', 'Send a test notification', 'Notify me about', 'Focus', 'Play a sound for notifications'] },
  { id: 'sound', cat: 'sound', label: 'Sound', icon: 'sound', desc: 'Sound effects and volume', keywords: 'volume mute clicks effects',
    options: ['Play sound effects', 'Volume', 'Clicking buttons and icons', 'Opening and closing windows', 'Locking and unlocking'] },
  { id: 'security', cat: 'security', label: 'Password & Two-Factor', icon: 'key', desc: 'Change your password and ask for a code when you sign in', keywords: '2fa totp otp authenticator security password',
    options: ['Two-factor authentication', 'Change password'] },
  { id: 'lock', cat: 'lock', label: 'Lock Screen', icon: 'lock', desc: 'Auto-lock and screen saver', keywords: 'screensaver screen saver idle auto-lock', options: ['Lock the screen after', 'Screen saver', 'Start after'] },
  { id: 'account', cat: 'account', label: 'Your Account', icon: 'user', desc: 'Your photo, name and sign out', keywords: 'profile avatar picture sign out log out', options: ['Profile photo', 'Close other windows', 'Sign out'] },
  { id: 'users', cat: 'users', admin: true, label: 'Users & Groups', icon: 'users', desc: 'Add people and choose who is an administrator', keywords: 'people roles admin linux accounts', options: ['Add user', 'Reset password', 'Delete user', 'Role'] },
];

/** Every Settings page, for universal search. */
export const SETTINGS_PAGES: readonly { id: string; label: string; icon: IconName; admin?: boolean; keywords?: string }[] = ENTRIES.map((e) => ({
  id: e.id, label: e.label, icon: e.icon, admin: e.admin, keywords: `${e.desc} ${e.keywords ?? ''} ${(e.options ?? []).join(' ')}`,
}));
